/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"bytes"
	"crypto/sha1" // #nosec G505: not used for security purposes
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"

	apierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
)

var log = &logger.Log

var (
	HttpCommonError   = errors.New("received HTTP Error")
	HttpNotFoundError = errors.New("received HTTP Not Found Error")
	HttpBadRequest    = errors.New("received HTTP Bad Request Error")
)

// ErrorDetail is error detail which info extracted from http.Response.Body.
type ErrorDetail struct {
	StatusCode         int
	ErrorCode          int
	RelatedErrorCodes  []int
	RelatedStatusCodes []string
	Details            string
}

// PortAddress is used when named port is specified.
type PortAddress struct {
	// Port is the port number.
	Port int `json:"port"`
	// IPs is a list of IPs associated to port number.
	IPs []string `json:"ips"`
}

func (e *ErrorDetail) Error() string {
	msg := fmt.Sprintf("StatusCode is %d,", e.StatusCode)
	if e.ErrorCode > 0 {
		msg += fmt.Sprintf("ErrorCode is %d,", e.ErrorCode)
	}
	if len(e.RelatedErrorCodes) > 0 {
		msg += fmt.Sprintf("RelatedErrorCodes is %v,", e.RelatedErrorCodes)
	}
	if len(e.RelatedStatusCodes) > 0 {
		msg += fmt.Sprintf("RelatedStatusCodes is %v,", e.RelatedStatusCodes)
	}
	if len(e.Details) > 0 {
		msg += fmt.Sprintf("Detail is %s", e.Details)
	}
	return msg
}

type responseBody struct {
	ErrorCode  int             `json:"error_code"`
	RelatedErr []relatedErrors `json:"related_errors"`
	ErrorMsg   string          `json:"error_message"`
}

type relatedErrors struct {
	HTTPStatus   string `json:"httpStatus"`
	ErrorCode    int    `json:"error_code"`
	ErrorMessage string `json:"error_message"`
}

var (
	groundTriggers     = []string{"ConnectionError", "Timeout"}
	retriables         = []string{"APITransactionAborted", "CannotConnectToServer", "ServerBusy"}
	regenerateTriggers = []string{"InvalidCredentials", "ClientCertificateNotTrusted", "BadXSRFToken"}
	categoryTable      = map[string][]string{"groundTriggers": groundTriggers, "retriables": retriables, "regenerateTriggers": regenerateTriggers}
)

func category(err error, cate string) bool {
	if err == nil {
		return false
	}
	table := categoryTable[cate]
	for _, e := range table {
		fn := strings.Split(reflect.TypeOf(err).String(), ".")
		var name string
		if len(fn) == 1 {
			name = fn[0]
		} else {
			name = fn[1]
		}
		if e == name {
			return true
		}
	}
	return false
}

// ShouldGroundPoint checks if it's a error which grounds an endpoint.
func ShouldGroundPoint(err error) bool {
	return category(err, "groundTriggers")
}

// ShouldRetry checks if it's a retriable error.
func ShouldRetry(err error) bool {
	return category(err, "retriables")
}

// ShouldRegenerate check if it's a error should regenerate pool.
func ShouldRegenerate(err error) bool {
	return category(err, "regenerateTriggers")
}

// InitErrorFromResponse returns error based on http.Response
func InitErrorFromResponse(host string, statusCode int, body []byte) NsxError {
	detail, err := extractHTTPDetailFromBody(host, statusCode, body)
	if err != nil {
		return CreateGeneralManagerError(host, "decode body", err.Error())
	}
	if detail.ErrorCode == 0 || detail.StatusCode == http.StatusOK {
		return nil
	}
	return httpErrortoNSXError(&detail)
}

func extractHTTPDetailFromBody(host string, statusCode int, body []byte) (ErrorDetail, error) {
	log.V(2).Info("HTTP response", "status code", statusCode, "body", string(body))
	ec := ErrorDetail{StatusCode: statusCode}
	if len(body) == 0 {
		log.V(1).Info("body length is 0")
		return ec, nil
	}
	var res responseBody
	if err := json.Unmarshal(body, &res); err != nil {
		log.Error(err, "Failed to decode response body for extracting HTTP detail")
		return ec, CreateGeneralManagerError(host, "decode body", err.Error())
	}

	ec.ErrorCode = res.ErrorCode
	msg := []string{res.ErrorMsg}
	for _, a := range res.RelatedErr {
		ec.RelatedErrorCodes = append(ec.RelatedErrorCodes, a.ErrorCode)
		ec.RelatedStatusCodes = append(ec.RelatedStatusCodes, a.HTTPStatus)
		msg = append(msg, a.ErrorMessage)
	}
	ec.Details = res.ErrorMsg
	ec.Details += strings.Join(msg, " ")
	return ec, nil
}

type errmap map[string]NsxError

var (
	errorTable = map[string]errmap{
		"404": // http.StatusNotFound
		{
			"202":     &BackendResourceNotFound{},
			"500090":  &StaleRevision{},
			"default": &ResourceNotFound{},
		},
		"400": // http.StatusBadRequest
		{
			"60508":  &NsxIndexingInProgress{},
			"60514":  &NsxSearchTimeout{},
			"60515":  &NsxSearchOutOfSync{},
			"8327":   &NsxOverlapVlan{},
			"500045": &NsxPendingDelete{},
			"500030": &ResourceInUse{},
			"500087": &StaleRevision{},
			"500105": &NsxOverlapAddresses{},
			"500232": &StaleRevision{},
			"503040": &NsxSegmentWithVM{},
			"100148": &StaleRevision{},
		},
		"500": // http.StatusInternalServerError
		{
			"98":  &CannotConnectToServer{},
			"99":  &ClientCertificateNotTrusted{},
			"607": &APITransactionAborted{},
		},
		"403": // http.StatusForbidden
		{
			"98":  &BadXSRFToken{},
			"403": &InvalidCredentials{},
			"505": &InvalidLicense{},
		},
	}

	errorTable1 = map[string]NsxError{
		"409":// http.StatusConflict
		&StaleRevision{},
		"412":// http.StatusPreconditionFailed
		&StaleRevision{},
		"429":// http.statusTooManyRequests
		&TooManyRequests{},
		"503":// http.StatusServiceUnavailable
		&ServiceUnavailable{},
	}
)

func httpErrortoNSXError(detail *ErrorDetail) NsxError {
	statusCode := detail.StatusCode
	errorCode := detail.ErrorCode
	relatedErrorCode := detail.RelatedErrorCodes

	if e, ok := errorTable[strconv.Itoa(statusCode)]; ok {
		if errorCode > 0 {
			if e1, ok := e[strconv.Itoa(errorCode)]; ok {
				e1.setDetail(detail)
				return e1
			}
		}
		if len(relatedErrorCode) > 0 {
			for _, i := range relatedErrorCode {
				if e1, ok := e[strconv.Itoa(i)]; ok {
					e1.setDetail(detail)
					return e1
				}
			}
		}
		if e1, ok := e["default"]; ok {
			e1.setDetail(detail)
			return e1
		}
	}
	if e, ok := errorTable1[strconv.Itoa(statusCode)]; ok {
		e.setDetail(detail)
		return e
	}
	err := &GeneralManagerError{}
	err.setDetail(detail)
	return err
}

func HandleHTTPResponse(response *http.Response, result interface{}, debug bool) (error, []byte) {
	body, err := io.ReadAll(response.Body)
	defer response.Body.Close()
	if !(response.StatusCode == http.StatusOK || response.StatusCode == http.StatusAccepted || response.StatusCode == http.StatusCreated) {
		err := HttpCommonError
		if response.StatusCode == http.StatusNotFound {
			err = HttpNotFoundError
		}
		if response.StatusCode == http.StatusBadRequest {
			err = HttpBadRequest
		}
		log.Error(err, "Handle HTTP response", "status", response.StatusCode, "request URL", response.Request.URL, "response body", string(body))
		return err, nil
	}
	if err != nil || body == nil {
		return err, body
	}
	if result == nil {
		return nil, nil
	}

	if debug {
		log.V(2).Info("Received HTTP response", "response", string(body))
	}
	if err := json.Unmarshal(body, result); err != nil {
		log.Error(err, "Failed to convert HTTP response to result", "result type", result)
		return err, body
	}
	return nil, body
}

func MergeAddressByPort(portAddressOriginal []PortAddress) []PortAddress {
	var portAddress []PortAddress
	var sortKeys []int
	mappedPorts := make(map[int][]string)
	for _, pa := range portAddressOriginal {
		if _, ok := mappedPorts[pa.Port]; !ok {
			sortKeys = append(sortKeys, pa.Port)
			mappedPorts[pa.Port] = pa.IPs
		} else {
			mappedPorts[pa.Port] = append(mappedPorts[pa.Port], pa.IPs...)
		}
	}
	sort.Ints(sortKeys)
	for _, key := range sortKeys {
		portAddress = append(portAddress, PortAddress{Port: key, IPs: mappedPorts[key]})
	}
	return portAddress
}

func DumpHttpRequest(request *http.Request) {
	var body []byte
	var err error
	if request == nil {
		return
	}
	if request.Body == nil {
		return
	}
	if request != nil {
		body, err = io.ReadAll(request.Body)
		if err != nil {
			return
		}
	}
	request.Body.Close()
	request.Body = io.NopCloser(bytes.NewReader(body))
	log.V(2).Info("HTTP request", "url", request.URL, "body", string(body), "head", request.Header)
}

type NSXApiError struct {
	*model.ApiError
	apierrors.ErrorTypeEnum
}

func NewNSXApiError(apiError *model.ApiError, errorType apierrors.ErrorTypeEnum) *NSXApiError {
	return &NSXApiError{
		ApiError:      apiError,
		ErrorTypeEnum: errorType,
	}
}

func (e *NSXApiError) Error() string {
	if e.ApiError != nil {
		apierror := e.ApiError
		return fmt.Sprintf("nsx error code: %d, message: %s, details: %s, related error: %s",
			safeInt(apierror.ErrorCode), safeString(apierror.ErrorMessage), safeString(apierror.Details),
			relatedErrorsToString(apierror.RelatedErrors))
	}
	return "SDKError: unknown error"
}

func (e *NSXApiError) Type() apierrors.ErrorTypeEnum {
	return e.ErrorTypeEnum
}

// TransNSXApiError processes an error and returns a formatted NSX API error message if applicable.
// If the processed API error is nil, return the original error
func TransNSXApiError(err error) error {
	if err == nil {
		return err
	}
	apierror, errorType := DumpAPIError(err)
	if apierror == nil {
		return err
	}
	return NewNSXApiError(apierror, *errorType)
}

func relatedErrorToString(err *model.RelatedApiError) string {
	if err == nil {
		return "nil"
	}

	return fmt.Sprintf(
		"{Details: %s, ErrorCode: %d,  ErrorMessage: %s, ModuleName: %s}",
		safeString(err.Details),
		safeInt(err.ErrorCode),
		safeString(err.ErrorMessage),
		safeString(err.ModuleName),
	)
}

func relatedErrorsToString(errors []model.RelatedApiError) string {
	if errors == nil {
		return "nil"
	}

	var errorStrings []string
	for i := 0; i < len(errors); i++ {
		currentErr := errors[i]
		errorStrings = append(errorStrings, relatedErrorToString(&currentErr))
	}

	return fmt.Sprintf("[%s]", strings.Join(errorStrings, ", "))
}

func safeString(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return *ptr
}

func safeInt(ptr *int64) int64 {
	if ptr == nil {
		return 0
	}
	return *ptr
}

// if ApiError is nil, check ErrorTypeEnum, such as ServiceUnavailable
// if both return value are nil, the error is not on the list
// there is no httpstatus, ApiError does't include it
func DumpAPIError(err error) (*model.ApiError, *apierrors.ErrorTypeEnum) {
	switch i := err.(type) {
	case apierrors.AlreadyExists:
		return castApiError(i.Data), i.ErrorType
	case apierrors.AlreadyInDesiredState:
		return castApiError(i.Data), i.ErrorType
	case apierrors.Canceled:
		return castApiError(i.Data), i.ErrorType
	case apierrors.ConcurrentChange:
		return castApiError(i.Data), i.ErrorType
	case apierrors.Error:
		return castApiError(i.Data), i.ErrorType
	case apierrors.FeatureInUse:
		return castApiError(i.Data), i.ErrorType
	case apierrors.InternalServerError:
		return castApiError(i.Data), i.ErrorType
	case apierrors.InvalidRequest:
		return castApiError(i.Data), i.ErrorType
	case apierrors.InvalidArgument:
		return castApiError(i.Data), i.ErrorType
	case apierrors.InvalidElementConfiguration:
		return castApiError(i.Data), i.ErrorType
	case apierrors.InvalidElementType:
		return castApiError(i.Data), i.ErrorType
	case apierrors.NotAllowedInCurrentState:
		return castApiError(i.Data), i.ErrorType
	case apierrors.NotFound:
		return castApiError(i.Data), i.ErrorType
	case apierrors.OperationNotFound:
		return castApiError(i.Data), i.ErrorType
	case apierrors.ResourceBusy:
		return castApiError(i.Data), i.ErrorType
	case apierrors.ResourceInUse:
		return castApiError(i.Data), i.ErrorType
	case apierrors.ResourceInaccessible:
		return castApiError(i.Data), i.ErrorType
	case apierrors.ServiceUnavailable:
		return castApiError(i.Data), i.ErrorType
	case apierrors.TimedOut:
		return castApiError(i.Data), i.ErrorType
	case apierrors.UnableToAllocateResource:
		return castApiError(i.Data), i.ErrorType
	case apierrors.Unauthenticated:
		return castApiError(i.Data), i.ErrorType
	case apierrors.Unauthorized:
		return castApiError(i.Data), i.ErrorType
	case apierrors.UnexpectedInput:
		return castApiError(i.Data), i.ErrorType
	case apierrors.Unsupported:
		return castApiError(i.Data), i.ErrorType
	case apierrors.UnverifiedPeer:
		return castApiError(i.Data), i.ErrorType
	default:
		log.Info("Dump api error", "error not supported", err)
		return nil, nil
	}
}

func castApiError(apiErrorDataValue *data.StructValue) *model.ApiError {
	info := "Dump api error"
	if apiErrorDataValue == nil {
		log.Info(info, "no extra error info", apiErrorDataValue)
		return nil
	}
	typeConverter := bindings.NewTypeConverter()
	data, err := typeConverter.ConvertToGolang(apiErrorDataValue, model.ApiErrorBindingType())
	if err != nil && isEmptyAPIError(data.(model.ApiError)) {
		log.Error(err[0], info)
		return nil
	}
	apiError, ok := data.(model.ApiError)
	if !ok {
		log.Info(info, "error raw data", data)
		return nil
	}
	return &apiError
}

func isEmptyAPIError(apiError model.ApiError) bool {
	return (apiError.ErrorCode == nil && apiError.ErrorMessage == nil)
}

func VerifyNsxCertWithThumbprint(der []byte, thumbprint string) error {
	tbRaw := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(thumbprint, ":", "")))
	var tbFromCert string
	if len(tbRaw) == 40 {
		// SHA-1
		digest := sha1.Sum(der) // #nosec G401: not used
		tbFromCert = hex.EncodeToString(digest[:])
	} else if len(tbRaw) == 64 {
		// SHA-256
		digest := sha256.Sum256(der)
		tbFromCert = hex.EncodeToString(digest[:])
	} else {
		err := errors.New("invalid thumbprint format")
		log.Error(err, "Unknown thumbprint length", "thumbprint", tbRaw)
		return err
	}

	if strings.Compare(tbRaw, tbFromCert) == 0 {
		return nil
	}

	err := errors.New("server certificate didn't match trusted fingerprint")
	log.Error(err, "Verify thumbprint", "server", tbFromCert, "local", tbRaw)
	return err
}

// GetTLSConfigForCert returns TLS config based on given pemCerts.
// If CA cert is passed, TLS config will do native cert check for connection.
// Otherwise, exact byte-to-byte check will be performed.
// Error is returned if pem invalid or not a certificate.
func GetTLSConfigForCert(pemCerts []byte) (*tls.Config, error) {
	block, _ := pem.Decode(pemCerts)
	if block == nil {
		err := errors.New("decode ca file fail")
		log.Error(err, "Failed to decode cert", "pem", pemCerts)
		return nil, err
	}
	if block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
		err := errors.New("pem not certificate or header not found")
		log.Error(err, "Failed to decode cert", "pem", pemCerts)
		return nil, err
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		log.Error(err, "Failed to decode cert", "pem", pemCerts)
		return nil, err
	}

	// Native cert verification in case of CA cert
	if cert.IsCA {
		log.Info("Configured CA cert", "subject", cert.Subject)
		certPool := x509.NewCertPool()
		certPool.AddCert(cert)
		// #nosec G402: ignore insecure options
		config := &tls.Config{
			RootCAs: certPool,
		}
		return config, nil
	}

	// Exact pem matching for leaf certs (certificate pinning)
	// #nosec G402: ignore insecure options
	config := &tls.Config{
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if cs.PeerCertificates == nil || cs.PeerCertificates[0] == nil {
				err := errors.New("server didn't present cert")
				log.Error(err, "Verify cert")
				return err
			}
			if !bytes.Equal(cs.PeerCertificates[0].Raw, cert.Raw) {
				err := errors.New("server certificate didn't match pinned leaf cert")
				log.Error(err, "Verify cert")
				return err
			}
			return nil
		},
	}
	log.Info("Configured cert pining", "subject", cert.Subject)
	return config, nil
}

func FindTag(tags []model.Tag, tagScope string) string {
	for _, tag := range tags {
		if *tag.Scope == tagScope {
			return *tag.Tag
		}
	}
	return ""
}

func CasttoPointer(obj interface{}) interface{} {
	if obj == nil {
		return nil
	}
	if reflect.TypeOf(obj).Kind() == reflect.Ptr {
		return obj
	}

	pointer := reflect.New(reflect.TypeOf(obj))
	pointer.Elem().Set(reflect.ValueOf(obj))
	return pointer.Interface()
}

func UpdateURL(reqUrl *url.URL, nsxtHost string) {
	urls := strings.Split(reqUrl.Path, "/")
	index := 0
	// check if it's request sent to envoy
	if strings.Contains(reqUrl.Host, "localhost") {
		for i, url := range urls {
			if url == "http1" {
				index = i
				break
			}
		}
	}
	if index == 0 {
		reqUrl.Host = nsxtHost
	} else {
		urls[index+1] = nsxtHost
		reqUrl.Path = strings.Join(urls, "/")
	}
}

const (
	X509_PEM_HEADER = "-----BEGIN CERTIFICATE-----"
	X509_PEM_FOOTER = "-----END CERTIFICATE-----"
)

func CertPemBytesToHeader(caFile string) string {
	certPem, err := os.ReadFile(caFile)
	cert := ""
	if err != nil {
		cert = caFile
	} else {
		cert = string(certPem)
	}
	certIdx := strings.Index(cert, X509_PEM_FOOTER)
	if certIdx > 0 {
		cert = cert[:certIdx]
	} else {
		log.Info("Not found pem footer", "cert", cert)
		return ""
	}
	cert = strings.ReplaceAll(cert, X509_PEM_HEADER, "")
	cert = strings.ReplaceAll(cert, X509_PEM_FOOTER, "")
	cert = strings.ReplaceAll(cert, "\n", "")
	return strings.TrimSpace(cert)
}

func UpdateRequestURL(reqUrl *url.URL, nsxtHost string, thumbprint string) {
	urls := strings.Split(reqUrl.Path, "/")
	index := 0
	// check if it's request sent to envoy
	if strings.Contains(reqUrl.Host, "localhost") {
		for i, url := range urls {
			if url == "http1" {
				index = i
				break
			}
		}
	}
	if index == 0 {
		reqUrl.Host = nsxtHost
	} else {
		urls[index+1] = nsxtHost
		if strings.Contains(urls[1], "external-tp") {
			urls[index+3] = thumbprint
		}
		reqUrl.Path = strings.Join(urls, "/")
	}
}

func MergeArraysWithoutDuplicate[T comparable](oldArray []T, newArray []T) []T {
	if len(oldArray) == 0 {
		return newArray
	}
	if len(newArray) == 0 {
		return oldArray
	}
	set := sets.New(oldArray...)
	set.Insert(newArray...)
	return set.UnsortedList()
}

func CompareArraysWithoutOrder[T comparable](oldArray []T, newArray []T) bool {
	if len(oldArray) != len(newArray) {
		return false
	}
	oldSet := sets.New(oldArray...)
	newSet := sets.New(newArray...)
	return oldSet.Equal(newSet)
}

func IsInvalidLicense(err error) bool {
	invalidLicense := false
	if apiErr, ok := err.(*NSXApiError); ok {
		errorMessage := ""
		for _, apiErrItem := range apiErr.RelatedErrors {
			if *apiErrItem.ErrorCode == InvalidLicenseErrorCode {
				invalidLicense = true
				errorMessage = *apiErrItem.ErrorMessage
			}
		}
		if *apiErr.ErrorCode == InvalidLicenseErrorCode {
			invalidLicense = true
			errorMessage = *apiErr.ErrorMessage
		}
		if invalidLicense {
			UpdateLicense(FeatureDFW, false)
			log.Error(err, "Invalid license, nsx-operator will restart", "error message", errorMessage)
		}
	}
	return invalidLicense
}

func ParseDHCPMode(mode string) string {
	// Transfer DHCPDeactivated to DHCP_DEACTIVATED
	nsxMode := strings.ToUpper(mode)
	if len(nsxMode) <= 4 {
		log.Error(nil, "Failed to parse DHCP mode", "mode", mode)
		return ""
	}
	nsxMode = nsxMode[:4] + "_" + nsxMode[4:]
	return nsxMode
}
