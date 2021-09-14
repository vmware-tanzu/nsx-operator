// Copyright (c) 2021 VMware, Inc. All rights reserved. VMware Confidential.

package util

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/protocol/client"
	"gitlab.eng.vmware.com/nsx-container-plugin/vmware-nsxlib-go/pkg/logger"
)

var (
	log = logger.GetInstance()
)

// ErrorDetail is error detail which info extracted from http.Reponse.Body.
type ErrorDetail struct {
	StatusCode         int
	ErrorCode          int
	RelatedErrorCodes  []int
	RelatedStatusCodes []string
	Details            string
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
	catagoryTable      = map[string][]string{"groundTriggers": groundTriggers, "retriables": retriables, "regenerateTriggers": regenerateTriggers}
)

func catagory(err error, cata string) bool {
	table := catagoryTable[cata]
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
	return catagory(err, "groundTriggers")
}

// ShouldRetry checks if it's a retriable error.
func ShouldRetry(err error) bool {
	return catagory(err, "retriables")
}

// ShouldRegenerate check if it's a error should regenerate pool.
func ShouldRegenerate(err error) bool {
	return catagory(err, "regenerateTriggers")
}

var (
	baseStruct = map[string]error{
		"ManagerError":   &ManagerError{},
		"ServerBusy":     &ServerBusy{},
		"NsxSearchError": &NsxSearchError{},
	}

	nsxLibErrorType = reflect.TypeOf(NsxLibException{})
)

// IsorChildError checks if source error is or the child of target error, both errors should be NsxLibException or child of NsxLibException.
func IsorChildError(src, target error) bool {
	srcp := reflect.TypeOf(src)
	srct := srcp.Elem()
	tarp := reflect.TypeOf(target)
	tart := tarp.Elem()

	if srct.Name() == tart.Name() {
		return true
	}
	if srct == nsxLibErrorType {
		return false
	}
	if tart == nsxLibErrorType {
		return true
	}

	b, ok := srct.FieldByName("msg")
	if !ok {
		return false
	}
	parent := b.Tag.Get("parent")
	if parent == tart.Name() {
		return true
	}
	if src, ok := baseStruct[parent]; ok {
		return IsorChildError(src, target)
	}
	return false

}

// InitErrorFromResponse returns error based on http.Response
func InitErrorFromResponse(host string, resp *http.Response) error {
	if resp == nil {
		return nil
	}
	detail, err := extractHTTPDetail(host, resp)
	if err != nil {
		return err
	}
	if detail.ErrorCode == 0 || detail.StatusCode == http.StatusOK {
		return nil
	}
	return httpErrortoNSXError(&detail)
}

func extractHTTPDetail(host string, resp *http.Response) (ErrorDetail, error) {
	ed := ErrorDetail{StatusCode: resp.StatusCode}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Debug(fmt.Sprintf("Extract HTTP detail error %v", err))
		return ed, CreateManagerError(host, "extract http", err.Error())
	}
	resp.Body = ioutil.NopCloser(bytes.NewReader(body))
	return extractHTTPDetailFromBody(host, resp.StatusCode, body)
}

func extractHTTPDetailFromBody(host string, statusCode int, body []byte) (ErrorDetail, error) {
	ec := ErrorDetail{StatusCode: statusCode}
	if len(body) == 0 {
		log.Debug(fmt.Sprintf("Extract HTTP detail quit since body len is 0 %v", ec))
		return ec, nil
	}
	var res responseBody
	if err := json.Unmarshal(body, &res); err != nil {
		log.Debug(fmt.Sprintf("ExtractHTTPDetail failed to decode response body, error :[%s], body: [%s]", err, string(body)))
		return ec, CreateManagerError(host, "decode body", err.Error())
	}

	ec.ErrorCode = res.ErrorCode
	var msg []string
	for _, a := range res.RelatedErr {
		ec.RelatedErrorCodes = append(ec.RelatedErrorCodes, a.ErrorCode)
		ec.RelatedStatusCodes = append(ec.RelatedStatusCodes, a.HTTPStatus)
		msg = append(msg, a.ErrorMessage)
	}
	ec.Details = res.ErrorMsg
	ec.Details += strings.Join(msg, " ")
	return ec, nil
}

type errmap map[string]error

var (
	errorTable = map[string]errmap{
		"404": //http.StatusNotFound
		{"202": &BackendResourceNotFound{},
			"500090":  &StaleRevision{},
			"default": &ResourceNotFound{}},
		"400": //http.StatusBadRequest
		{"60508": &NsxIndexingInProgress{},
			"60514":  &NsxSearchTimeout{},
			"60515":  &NsxSearchOutOfSync{},
			"8327":   &NsxOverlapVlan{},
			"500045": &NsxPendingDelete{},
			"500030": &ResourceInUse{},
			"500087": &StaleRevision{},
			"500105": &NsxOverlapAddresses{},
			"500232": &StaleRevision{},
			"503040": &NsxSegemntWithVM{},
			"100148": &StaleRevision{}},
		"500": //http.StatusInternalServerError
		{"98": &CannotConnectToServer{},
			"99":  &ClientCertificateNotTrusted{},
			"607": &APITransactionAborted{}},
		"403": //http.StatusForbidden
		{"98": &BadXSRFToken{},
			"403": &InvalidCredentials{},
			"505": &InvalidLicense{}},
	}

	errorTable1 = map[string]error{
		"409"://http.StatusConflict
		&StaleRevision{},
		"412"://http.StatusPreconditionFailed
		&StaleRevision{},
		"429"://http.statusTooManyRequests
		&TooManyRequests{},
		"503"://http.StatusServiceUnavailable
		&ServiceUnavailable{},
	}
)

func httpErrortoNSXError(detail *ErrorDetail) error {
	statusCode := detail.StatusCode
	errorCode := detail.ErrorCode
	relatedErrorCode := detail.RelatedErrorCodes

	if e, ok := errorTable[strconv.Itoa(statusCode)]; ok {
		if errorCode > 0 {
			if e1, ok := e[strconv.Itoa(errorCode)]; ok {
				if e2, ok := e1.(nsxError); ok {
					e2.setDetail(detail)

				}
				return e1
			}
		}
		if len(relatedErrorCode) > 0 {
			for _, i := range relatedErrorCode {
				if e1, ok := e[strconv.Itoa(i)]; ok {
					if e2, ok := e1.(nsxError); ok {
						e2.setDetail(detail)

					}
					return e1
				}
			}
		}
		if e1, ok := e["default"]; ok {
			if e2, ok := e1.(nsxError); ok {
				e2.setDetail(detail)
			}
			return e1
		}
	}
	if e, ok := errorTable1[strconv.Itoa(statusCode)]; ok {
		if e2, ok := e.(nsxError); ok {
			e2.setDetail(detail)
		}
		return e
	}
	err := &ManagerError{}
	err.detail = detail
	return err
}

// HeaderConfig updates http request header.
type HeaderConfig struct {
	xAllowOverwrite       bool
	nsxEnablePartialPatch bool
	// configXallowOverwrite comes from config, it's a global parameter.
	configXallowOverwrite bool
}

// CreateHeaderConfig creates HeaderConfig.
func CreateHeaderConfig(xAllowOverwrite bool, nsxEnablePartialPatch bool, configXallowOverwrite bool) *HeaderConfig {
	header := &HeaderConfig{
		xAllowOverwrite:       xAllowOverwrite,
		nsxEnablePartialPatch: nsxEnablePartialPatch,
		configXallowOverwrite: configXallowOverwrite,
	}
	return header
}

// Process adds header to http.Request depending on configuration.
func (headerConfig *HeaderConfig) Process(req *http.Request) error {
	if headerConfig.configXallowOverwrite {
		if headerConfig.xAllowOverwrite {
			req.Header["X-Allow-Overwrite"] = []string{"true"}
		}
	}
	if headerConfig.nsxEnablePartialPatch {
		req.Header.Set("nsx-enable-partial-patch", "true")
	}
	return nil
}

// SetXAllowOverrite sets XAllowoverrite.
func (headerConfig *HeaderConfig) SetXAllowOverrite(value bool) *HeaderConfig {
	headerConfig.xAllowOverwrite = value
	return headerConfig
}

// SetNSXEnablePartialPatch sets NSXEnablePartialPatch.
func (headerConfig *HeaderConfig) SetNSXEnablePartialPatch(value bool) *HeaderConfig {
	headerConfig.nsxEnablePartialPatch = value
	return headerConfig
}

// SetConfigXallowOverwrite sets configXallowOverwrite.
func (headerConfig *HeaderConfig) SetConfigXallowOverwrite(value bool) *HeaderConfig {
	headerConfig.configXallowOverwrite = value
	return headerConfig
}

// Done updates request process of RestConnector.
func (headerConfig *HeaderConfig) Done(connector *client.RestConnector) {
	connector.AddRequestProcessor(headerConfig)
}
