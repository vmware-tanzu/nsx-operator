/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"bytes"
	"crypto/sha1" // #nosec G505: not used for security purposes
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"

	apierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
)

var log = logger.Log

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
	log.V(2).Info("http response", "status code", statusCode, "body", string(body))
	ec := ErrorDetail{StatusCode: statusCode}
	if len(body) == 0 {
		log.V(1).Info("body length is 0")
		return ec, nil
	}
	var res responseBody
	if err := json.Unmarshal(body, &res); err != nil {
		log.Error(err, "failed to decode response body for extracting HTTP detail")
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
	if !(response.StatusCode == http.StatusOK || response.StatusCode == http.StatusAccepted) {
		err := errors.New("received HTTP Error")
		log.Error(err, "handle http response", "status", response.StatusCode, "requestUrl", response.Request.URL, "response body", string(body))
		return err, nil
	}
	if err != nil || body == nil {
		return err, body
	}
	if result == nil {
		return nil, nil
	}

	if debug {
		log.V(2).Info("received HTTP response", "response", string(body))
	}
	if err := json.Unmarshal(body, result); err != nil {
		log.Error(err, "error converting HTTP response to result", "result type", result)
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

func ParseVPCPath(nsxResourcePath string) (orgID string, projectID string, vpcID string, resourceID string) {
	paras := strings.Split(nsxResourcePath, "/")
	orgID = paras[2]
	projectID = paras[4]
	vpcID = paras[6]
	resourceID = paras[8]
	return
}
func DumpHttpRequest(request *http.Request) {
	var body []byte
	var err error
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
	log.V(2).Info("http request", "url", request.URL, "body", string(body))
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
		log.Info("dump api error", "error not supported", err)
		return nil, nil
	}
}

func castApiError(apiErrorDataValue *data.StructValue) *model.ApiError {
	info := "dump api error"
	if apiErrorDataValue == nil {
		log.Info(info, "no extra error info", apiErrorDataValue)
		return nil
	}
	var typeConverter = bindings.NewTypeConverter()
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
		log.Error(err, "unknown thumbprint length", "thumbprint", tbRaw)
		return err
	}

	if strings.Compare(tbRaw, tbFromCert) == 0 {
		return nil
	}

	err := errors.New("server certificate didn't match trusted fingerprint")
	log.Error(err, "verify thumbprint", "server", tbFromCert, "local", tbRaw)
	return err
}

// GetCommonNameFromLeafCert returns the common name of the first (leaf) cert of
// provided pemCerts. If CA cert is passed, empty CN will be returned.
// Error is returned if pem invalid or not a certificate.
func GetCommonNameFromLeafCert(pemCerts []byte) (string, error) {
	block, _ := pem.Decode(pemCerts)
	if block == nil {
		err := errors.New("decode ca file fail")
		log.Error(err, "failed to get CN from cert", "pem", pemCerts)
		return "", err
	}
	if block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
		err := errors.New("pem not certificate or header not found")
		log.Error(err, "failed to get CN from cert", "pem", pemCerts)
		return "", err
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		log.Error(err, "failed to get CN from cert", "pem", pemCerts)
		return "", err
	}

	if cert.IsCA {
		return "", nil
	}

	return cert.Subject.CommonName, nil
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
	switch v := obj.(type) {
	case model.Rule:
		return &v
	case model.StaticRoutes:
		return &v
	case model.HostTransportNode:
		return &v
	case model.ClusterControlPlane:
		return &v
	case model.IpAddressPool:
		return &v
	case model.GenericPolicyRealizedResource:
		return &v
	case model.Vpc:
		return &v
	case model.IpAddressPoolBlockSubnet:
		return &v
	case model.Group:
		return &v
	case model.SecurityPolicy:
		return &v
	case model.Share:
		return &v
	case model.SegmentPort:
		return &v
	case model.VpcSubnet:
		return &v
	case model.VpcSubnetPort:
		return &v
	case model.IpAddressBlock:
		return &v
	default:
		return nil
	}
}
