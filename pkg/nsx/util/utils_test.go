/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/openlyinc/pointy"
	"github.com/stretchr/testify/assert"
	apierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	mpmodel "github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

func TestHttpErrortoNSXError(t *testing.T) {
	assert := assert.New(t)
	testdatas := []ErrorDetail{
		{404, 202, []int{}, []string{}, ""},
		{404, 0, []int{}, []string{}, ""},
		{409, 202, []int{}, []string{}, ""},
		{500, 0, []int{99}, []string{}, ""},
		{505, 0, []int{}, []string{}, ""},
	}

	err := httpErrortoNSXError(&testdatas[0])
	e, ok := err.(*BackendResourceNotFound)
	assert.True(ok, fmt.Sprintf("Transform error : %v", e))

	err = httpErrortoNSXError(&testdatas[1])
	e1, ok := err.(*ResourceNotFound)
	assert.True(ok, fmt.Sprintf("Transform error : %v", e1))

	err = httpErrortoNSXError(&testdatas[2])
	e2, ok := err.(*StaleRevision)
	assert.True(ok, fmt.Sprintf("Transform error : %v", e2))

	err = httpErrortoNSXError(&testdatas[3])
	e3, ok := err.(*ClientCertificateNotTrusted)
	assert.True(ok, fmt.Sprintf("Transform error : %v", e3))

	err = httpErrortoNSXError(&testdatas[4])
	e4, ok := err.(ManagerError)
	assert.True(ok, fmt.Sprintf("Transform error : %v", e4))
}

func TestInitErrorFromResponse(t *testing.T) {
	assert := assert.New(t)
	result := `{
		"failover_mode" : "NON_PREEMPTIVE",
		"enable_standby_relocation" : false,
		"route_advertisement_types" : [ "TIER1_IPSEC_LOCAL_ENDPOINT" ],
		"force_whitelisting" : false,
		"default_rule_logging" : false,
		"disable_firewall" : false,
		"ipv6_profile_paths" : [ "/infra/ipv6-ndra-profiles/default", "/infra/ipv6-dad-profiles/default" ],
		"pool_allocation" : "ROUTING",
		"resource_type" : "Tier1",
		"id" : "test-tier1-id",
		"display_name" : "test-tier1-id",
		"path" : "/infra/tier-1s/test-tier1-id",
		"relative_path" : "test-tier1-id",
		"parent_path" : "/infra",
		"unique_id" : "2ceae758-0c7b-410c-b675-21de6d435dc0",
		"marked_for_delete" : false,
		"overridden" : false,
		"_create_user" : "admin",
		"_create_time" : 1614067766811,
		"_last_modified_user" : "admin",
		"_last_modified_time" : 1614067766823,
		"_system_owned" : false,
		"_protection" : "NOT_PROTECTED",
		"_revision" : 0
	  } `

	req := httptest.NewRequest("GET", "http://example.com/foo", nil)
	handler := func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, result)
	}
	w := httptest.NewRecorder()
	handler(w, req)
	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	err := InitErrorFromResponse("10.0.0.1", resp.StatusCode, body)

	assert.Equal(err, nil, "Read resp body error")
	assert.Equal(string(body), result, "Read resp body error")

	//body is nil
	handler = func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "")
	}

	handler(w, req)
	resp = w.Result()
	body, _ = io.ReadAll(resp.Body)
	err = InitErrorFromResponse("10.0.0.1", resp.StatusCode, body)
	assert.Equal(err, nil, "Empty body")
	assert.Equal(string(body), "", "Empty body")

}

func TestShouldGroundPoint(t *testing.T) {
	err := CreateServiceClusterUnavailable("127.0.0.1")
	assert.False(t, ShouldGroundPoint(err), "It's not a ground point error")

	err1 := CreateConnectionError("127.0.0.1")
	assert.True(t, ShouldGroundPoint(err1), "It's a ground point error")
}

func TestShouldRetry(t *testing.T) {
	assert := assert.New(t)
	err := CreateServiceClusterUnavailable("127.0.0.1")
	assert.False(ShouldRetry(err), "It's not a retry error")

	err1 := &APITransactionAborted{}
	assert.True(ShouldRetry(err1), "It's a retry error")
}

func TestShouldRegenerate(t *testing.T) {
	assert := assert.New(t)
	err := CreateServiceClusterUnavailable("127.0.0.1")
	assert.False(ShouldRegenerate(err), "It's not a regenerate error")

	err1 := &InvalidCredentials{}
	assert.True(ShouldRegenerate(err1), "It's a regenerate error")
}

func TestUtil_InitErrorFromResponse(t *testing.T) {
	body := `{"httpStatus": "BAD_REQUEST", "error_code": 8327, "module_name": "common-services", "error_message": "Principal attempts to delete or modify an object of type nsx$LrPortEcResourceAllocation it doesn't own. (createUser=nsx_policy, allowOverwrite=null)"}`
	statusCode := 400
	err := InitErrorFromResponse("10.0.0.1", statusCode, []byte(body))
	assert.NotEqual(t, err, nil)
	_, ok := err.(*NsxOverlapVlan)
	assert.Equal(t, ok, true)
	result := ShouldRegenerate(err)
	assert.Equal(t, result, false)

	body = `{"httpStatus": "BAD_REQUEST", "error_code": 98, "module_name": "common-services", "error_message": "Principal attempts to delete or modify an object of type nsx$LrPortEcResourceAllocation it doesn't own. (createUser=nsx_policy, allowOverwrite=null)"}`
	statusCode = 403
	err = InitErrorFromResponse("10.0.0.1", statusCode, []byte(body))
	assert.NotEqual(t, err, nil)
	_, ok = err.(*BadXSRFToken)
	assert.Equal(t, ok, true)
	result = ShouldRegenerate(err)
	assert.Equal(t, result, true)

	body = `{"httpStatus": "BAD_REQUEST", "error_code": 98, "module_name": "common-services", "error_message": "Principal attempts to delete or modify an object of type nsx$LrPortEcResourceAllocation it doesn't own. (createUser=nsx_policy, allowOverwrite=null)"}`
	statusCode = 500
	err = InitErrorFromResponse("10.0.0.1", statusCode, []byte(body))
	assert.NotEqual(t, err, nil)
	_, ok = err.(*CannotConnectToServer)
	assert.Equal(t, ok, true)
	result = ShouldRegenerate(err)
	assert.Equal(t, result, false)
	result = ShouldRetry(err)
	assert.Equal(t, result, true)
}

func TestUtil_setDetail(t *testing.T) {
	nsxerr := CreateCannotConnectToServer()
	detail := ErrorDetail{
		ErrorCode:          287,
		StatusCode:         400,
		RelatedErrorCodes:  []int{123, 222},
		RelatedStatusCodes: []string{"error1", "erro2"},
		Details:            "connect to serve fail",
	}
	nsxerr.setDetail(&detail)
	assert.Equal(t, nsxerr.ErrorCode, 287)
	assert.Equal(t, nsxerr.StatusCode, 400)
	assert.Equal(t, nsxerr.RelatedErrorCodes, []int{123, 222})
}

func TestVCClient_handleHTTPResponse(t *testing.T) {
	response := &http.Response{}
	response.Request = &http.Request{}
	response.Request.URL = &url.URL{Host: "10.0.0.1"}
	response.StatusCode = 301
	response.Body = io.NopCloser(strings.NewReader("Hello, World!"))
	var sessionData map[string]string

	// http status code > 300
	err, _ := HandleHTTPResponse(response, &sessionData, false)
	expect := errors.New("received HTTP Error")
	assert.Equal(t, err, expect)

	// result interface is null
	response.StatusCode = 200
	err, _ = HandleHTTPResponse(response, nil, false)
	assert.Equal(t, err, nil)

	// 	response.StatusCode = 200， body content correct
	response.Body = io.NopCloser(bytes.NewReader([]byte(`{"value": "hello"}`)))
	err, _ = HandleHTTPResponse(response, &sessionData, false)
	assert.Equal(t, err, nil)

	// 	response.StatusCode = 200， body content invalid
	response.Body = io.NopCloser(bytes.NewReader([]byte(`{"value": 4}`)))
	err, _ = HandleHTTPResponse(response, &sessionData, false)
	_, ok := err.(*json.UnmarshalTypeError)
	assert.Equal(t, ok, true)
}

func TestVerifyNsxCertWithThumbprint(t *testing.T) {
	type args struct {
		der        []byte
		thumbprint string
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "SHA-1",
			args: args{
				der:        []byte("It is byte."),
				thumbprint: "5C:1D:AE:31:3A:EA:74:74:FE:69:BA:9F:0B:1D:86:5E:39:97:43:4F"},
			wantErr: false,
		},
		{
			name: "SHA-256",
			args: args{
				der:        []byte("It is byte."),
				thumbprint: "2F:CB:42:CD:71:96:A2:47:D2:BC:8B:A9:A6:2F:E0:97:BF:4A:5E:2C:45:8F:1C:BE:5B:1F:4D:36:8B:DD:06:25"},
			wantErr: false,
		},
		{
			name: "SHA mismatched",
			args: args{
				der:        []byte("It is another byte."),
				thumbprint: "2F:CB:42:CD:71:96:A2:47:D2:BC:8B:A9:A6:2F:E0:97:BF:4A:5E:2C:45:8F:1C:BE:5B:1F:4D:36:8B:DD:06:25"},
			wantErr: true,
		},
		{
			name: "malformed fingerprint",
			args: args{
				der:        []byte("It is byte."),
				thumbprint: "2F:CB:42:CD:71:96:A2:47:D2:BC:8B:A9:A6:2F:E0:97"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyNsxCertWithThumbprint(tt.args.der, tt.args.thumbprint)
			if tt.wantErr {
				assert.Error(t, err, "VerifyNsxCertWithThumbprint expected err returned")
			} else {
				assert.NoError(t, err, "VerifyNsxCertWithThumbprint expected no error returned")
			}
		})
	}
}

func TestGetTLSConfigForCert(t *testing.T) {
	tests := []struct {
		name    string
		pem     []byte
		isCA    bool
		wantErr bool
	}{
		{
			name: "One leaf cert",
			pem: []byte(`
-----BEGIN CERTIFICATE-----
MIID5DCCAsygAwIBAgIJAIJaVMN4AJHVMA0GCSqGSIb3DQEBCwUAMIGIMTQwMgYD
VQQDDCtuc3htYW5hZ2VyLW9iLTIyOTQ1MzY4LTEtZGV2LWludGVnLW5zeC05Mzg5
MQwwCgYDVQQLDANOU1gxFDASBgNVBAoMC1ZNd2FyZSBJbmMuMRIwEAYDVQQHDAlQ
YWxvIEFsdG8xCzAJBgNVBAgMAkNBMQswCQYDVQQGEwJVUzAeFw0yNDAxMDIxNjMw
NDVaFw0yNjA0MDYxNjMwNDVaMIGIMTQwMgYDVQQDDCtuc3htYW5hZ2VyLW9iLTIy
OTQ1MzY4LTEtZGV2LWludGVnLW5zeC05Mzg5MQwwCgYDVQQLDANOU1gxFDASBgNV
BAoMC1ZNd2FyZSBJbmMuMRIwEAYDVQQHDAlQYWxvIEFsdG8xCzAJBgNVBAgMAkNB
MQswCQYDVQQGEwJVUzCCASIwDQYJKoZIhvcNAQEBBQADggEPADCCAQoCggEBAMu8
y0kFT2knFi0TVDD344n3QQWx/8y+Y0wKE3oo6jSXlL8h7JQId7Zp4sdgemunt3nL
rjXFjujgMQzDiBILiYvrO4sWbht7v71sOLjj00RofC4vAPM+Xl3vhn4VCRRix99w
f0IDphOPfbVgNajHUIe3Gt3U69ZngUBfXXbpGVK8cCEo6C2JZ7bVj95zNj0n6Tuw
SjMdVfZh4iCqqacnZWqZScRbuyg/t7Gog2GN9XBePkklsmhNlbXwWggZsYosTs/u
U1zo2O92OZ4wnZXLBtAbv77FyLfV7zq7K42QfkG+RcHDbK9ygVsuadXoW+X2FRcc
hYod4nPS/MNxkCUqcF0CAwEAAaNPME0wEwYDVR0lBAwwCgYIKwYBBQUHAwEwNgYD
VR0RBC8wLYIrbnN4bWFuYWdlci1vYi0yMjk0NTM2OC0xLWRldi1pbnRlZy1uc3gt
OTM4OTANBgkqhkiG9w0BAQsFAAOCAQEAfFMmJiIHrpIzLzrCSq2o+6SCyhedZSUF
xnXwrdgPPFiceWBWGN8rMvu1qq1RCG/xxQkxo5KyaAUSWxwPltpPCLQOedn9VUkY
MY2o/ZVAMwp8QoJqHz4RvSeWXEaymh22SKhN4lOnTITSalP8NSxK9G1DM8T828hj
dvHtRf4o3AyJwWRwyLM7qn8s5C1O/JJfyZ/P17nD4yn1HU9StAwWJUvLKVXeFq+Q
XkMSQJYdYDsUkiu98jNxh+oT8Cqdruwtg73pw8pP17EPltBABlHkYOEznw3dgDH3
jSy6ts7e8AND6YWulG9jLmrI1xWwjbVqAoapxJQeSRYQ6Wb/KODPlg==
-----END CERTIFICATE-----
`),
			isCA:    false,
			wantErr: false,
		},
		{
			name: "Cert chain",
			pem: []byte(`
-----BEGIN CERTIFICATE-----
MIIGuzCCBaOgAwIBAgIQdL+xbvFsTSlVxSEtNypPzzANBgkqhkiG9w0BAQsFADCB
ujELMAkGA1UEBhMCVVMxFjAUBgNVBAoTDUVudHJ1c3QsIEluYy4xKDAmBgNVBAsT
H1NlZSB3d3cuZW50cnVzdC5uZXQvbGVnYWwtdGVybXMxOTA3BgNVBAsTMChjKSAy
MDEyIEVudHJ1c3QsIEluYy4gLSBmb3IgYXV0aG9yaXplZCB1c2Ugb25seTEuMCwG
A1UEAxMlRW50cnVzdCBDZXJ0aWZpY2F0aW9uIEF1dGhvcml0eSAtIEwxSzAeFw0y
MzExMjkxMTU5MDZaFw0yNDExMjgxMTU5MDVaMIGDMQswCQYDVQQGEwJVUzETMBEG
A1UECBMKQ2FsaWZvcm5pYTESMBAGA1UEBxMJUGFsbyBBbHRvMRQwEgYDVQQKEwtW
TXdhcmUsIEluYzE1MDMGA1UEAxMsbnN4TWFuYWdlci5zZGRjLTEwLTIxNS0yMDgt
MjUwLnZtd2FyZXZtYy5jb20wggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIB
AQDeIz8U7Pgkl6NKnqu7kqtwDjBMUN4fxdC54jTqaKr4zDG7d67DwivcPfMl3S1m
CXKaJr4Ma+Yw0ZCJTwJNv1lGR2R9D+ZgWrvzlPk2ZpfQTuBGHgQnRu960XnsQ31f
u4jDZsNTlhU99KrycFQEJ7RJh+yFaCgiO3OZDHr11vDuunaixswGUIUiCjE4tHRu
/p6ZCItxClgqKTkCkc2s69f21l1CsyM6+PEAIOQ0xfgh8OKBMjbEhrhNdWRluFeW
FJ9LwBNDVBcYM0csflwfVLp4Bz8xZYg7EOfwTU13tDvf2Haekhs6JAftFbeY3BoO
DiOck8hs/TtTyEkVm3RQSM6rAgMBAAGjggLwMIIC7DAMBgNVHRMBAf8EAjAAMB0G
A1UdDgQWBBROPMWy5jA5THUuB9g0O4aFqNllWjAfBgNVHSMEGDAWgBSConB03bxT
P8971PfNf6dgxgpMvzBoBggrBgEFBQcBAQRcMFowIwYIKwYBBQUHMAGGF2h0dHA6
Ly9vY3NwLmVudHJ1c3QubmV0MDMGCCsGAQUFBzAChidodHRwOi8vYWlhLmVudHJ1
c3QubmV0L2wxay1jaGFpbjI1Ni5jZXIwMwYDVR0fBCwwKjAooCagJIYiaHR0cDov
L2NybC5lbnRydXN0Lm5ldC9sZXZlbDFrLmNybDA3BgNVHREEMDAugixuc3hNYW5h
Z2VyLnNkZGMtMTAtMjE1LTIwOC0yNTAudm13YXJldm1jLmNvbTAOBgNVHQ8BAf8E
BAMCBaAwHQYDVR0lBBYwFAYIKwYBBQUHAwEGCCsGAQUFBwMCMBMGA1UdIAQMMAow
CAYGZ4EMAQICMIIBfgYKKwYBBAHWeQIEAgSCAW4EggFqAWgAdgA/F0tP1yJHWJQd
ZRyEvg0S7ZA3fx+FauvBvyiF7PhkbgAAAYwa8nZuAAAEAwBHMEUCIQCg2ui4od8a
O8yY5PtUZqTAt6joqU18H26ei+50IDFgpQIgY3QovIXCUvO/KxCuaoBCLyP4N9bX
PkbJ34+udBcn0LoAdgBVgdTCFpA2AUrqC5tXPFPwwOQ4eHAlCBcvo6odBxPTDAAA
AYwa8naOAAAEAwBHMEUCIQD6VTtewFcapQcv8o0Kt2PEy2vp6D3EW1Ewb0op1fTP
FQIgb9TOwY0Mu1xNyuCcwhrtBhJfiq0wpGabPufrg/yXYosAdgDuzdBk1dsazsVc
t520zROiModGfLzs3sNRSFlGcR+1mwAAAYwa8naeAAAEAwBHMEUCIQDUfeW1yQqL
m/ECwaEGK/FIUM8ecofKHt1SE57sbCAbnAIgW7SOcSFSXR8t2rnkQx2YlvS0MV5g
qzxN5NjPODJPc7IwDQYJKoZIhvcNAQELBQADggEBAAvgvh1vJlyUewvUgWmOuVim
bXVnMu2nsqhE3tU5yDVyfG48IWAe6L/YMOSCjGfRPHKOLU0YbgLVTkoIDZqIdphQ
uXfDOmJb542/EyKq+0DYLj4WOfxvL85ALxpwt1w1YXxO68+eDJmGDSHkoGYlCx56
TZizCvI/Dpvv2SAjX8RwO+DJ/etTYmdpu5DC2yY7ucxcM6SelSGcGP/kSNZjZ2Q9
k3HQdLg+sEm+YBCm5FInysatHz5HLgzOOKtNttGnR4E+AxAnL7yig6ZjTQFMu5B3
JkRniwg2hfDP088aI5f+5vCePx7ai6R9MHEvpgHkExeWDXX/Z6RyXfQzESPWA6M=
-----END CERTIFICATE-----
-----BEGIN CERTIFICATE-----
MIIFDjCCA/agAwIBAgIMDulMwwAAAABR03eFMA0GCSqGSIb3DQEBCwUAMIG+MQsw
CQYDVQQGEwJVUzEWMBQGA1UEChMNRW50cnVzdCwgSW5jLjEoMCYGA1UECxMfU2Vl
IHd3dy5lbnRydXN0Lm5ldC9sZWdhbC10ZXJtczE5MDcGA1UECxMwKGMpIDIwMDkg
RW50cnVzdCwgSW5jLiAtIGZvciBhdXRob3JpemVkIHVzZSBvbmx5MTIwMAYDVQQD
EylFbnRydXN0IFJvb3QgQ2VydGlmaWNhdGlvbiBBdXRob3JpdHkgLSBHMjAeFw0x
NTEwMDUxOTEzNTZaFw0zMDEyMDUxOTQzNTZaMIG6MQswCQYDVQQGEwJVUzEWMBQG
A1UEChMNRW50cnVzdCwgSW5jLjEoMCYGA1UECxMfU2VlIHd3dy5lbnRydXN0Lm5l
dC9sZWdhbC10ZXJtczE5MDcGA1UECxMwKGMpIDIwMTIgRW50cnVzdCwgSW5jLiAt
IGZvciBhdXRob3JpemVkIHVzZSBvbmx5MS4wLAYDVQQDEyVFbnRydXN0IENlcnRp
ZmljYXRpb24gQXV0aG9yaXR5IC0gTDFLMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8A
MIIBCgKCAQEA2j+W0E25L0Tn2zlem1DuXKVh2kFnUwmqAJqOV38pa9vH4SEkqjrQ
jUcj0u1yFvCRIdJdt7hLqIOPt5EyaM/OJZMssn2XyP7BtBe6CZ4DkJN7fEmDImiK
m95HwzGYei59QAvS7z7Tsoyqj0ip/wDoKVgG97aTWpRzJiatWA7lQrjV6nN5ZGhT
JbiEz5R6rgZFDKNrTdDGvuoYpDbwkrK6HIiPOlJ/915tgxyd8B/lw9bdpXiSPbBt
LOrJz5RBGXFEaLpHPATpXbo+8DX3Fbae8i4VHj9HyMg4p3NFXU2wO7GOFyk36t0F
ASK7lDYqjVs1/lMZLwhGwSqzGmIdTivZGwIDAQABo4IBDDCCAQgwDgYDVR0PAQH/
BAQDAgEGMBIGA1UdEwEB/wQIMAYBAf8CAQAwMwYIKwYBBQUHAQEEJzAlMCMGCCsG
AQUFBzABhhdodHRwOi8vb2NzcC5lbnRydXN0Lm5ldDAwBgNVHR8EKTAnMCWgI6Ah
hh9odHRwOi8vY3JsLmVudHJ1c3QubmV0L2cyY2EuY3JsMDsGA1UdIAQ0MDIwMAYE
VR0gADAoMCYGCCsGAQUFBwIBFhpodHRwOi8vd3d3LmVudHJ1c3QubmV0L3JwYTAd
BgNVHQ4EFgQUgqJwdN28Uz/Pe9T3zX+nYMYKTL8wHwYDVR0jBBgwFoAUanImetAe
733nO2lR1GyNn5ASZqswDQYJKoZIhvcNAQELBQADggEBADnVjpiDYcgsY9NwHRkw
y/YJrMxp1cncN0HyMg/vdMNY9ngnCTQIlZIv19+4o/0OgemknNM/TWgrFTEKFcxS
BJPok1DD2bHi4Wi3Ogl08TRYCj93mEC45mj/XeTIRsXsgdfJghhcg85x2Ly/rJkC
k9uUmITSnKa1/ly78EqvIazCP0kkZ9Yujs+szGQVGHLlbHfTUqi53Y2sAEo1GdRv
c6N172tkw+CNgxKhiucOhk3YtCAbvmqljEtoZuMrx1gL+1YQ1JH7HdMxWBCMRON1
exCdtTix9qrKgWRs6PLigVWXUX/hwidQosk8WwBD9lu51aX8/wdQQGcHsFXwt35u
Lcw=
-----END CERTIFICATE-----
-----BEGIN CERTIFICATE-----
MIIEPjCCAyagAwIBAgIESlOMKDANBgkqhkiG9w0BAQsFADCBvjELMAkGA1UEBhMC
VVMxFjAUBgNVBAoTDUVudHJ1c3QsIEluYy4xKDAmBgNVBAsTH1NlZSB3d3cuZW50
cnVzdC5uZXQvbGVnYWwtdGVybXMxOTA3BgNVBAsTMChjKSAyMDA5IEVudHJ1c3Qs
IEluYy4gLSBmb3IgYXV0aG9yaXplZCB1c2Ugb25seTEyMDAGA1UEAxMpRW50cnVz
dCBSb290IENlcnRpZmljYXRpb24gQXV0aG9yaXR5IC0gRzIwHhcNMDkwNzA3MTcy
NTU0WhcNMzAxMjA3MTc1NTU0WjCBvjELMAkGA1UEBhMCVVMxFjAUBgNVBAoTDUVu
dHJ1c3QsIEluYy4xKDAmBgNVBAsTH1NlZSB3d3cuZW50cnVzdC5uZXQvbGVnYWwt
dGVybXMxOTA3BgNVBAsTMChjKSAyMDA5IEVudHJ1c3QsIEluYy4gLSBmb3IgYXV0
aG9yaXplZCB1c2Ugb25seTEyMDAGA1UEAxMpRW50cnVzdCBSb290IENlcnRpZmlj
YXRpb24gQXV0aG9yaXR5IC0gRzIwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEK
AoIBAQC6hLZy254Ma+KZ6TABp3bqMriVQRrJ2mFOWHLP/vaCeb9zYQYKpSfYs1/T
RU4cctZOMvJyig/3gxnQaoCAAEUesMfnmr8SVycco2gvCoe9amsOXmXzHHfV1IWN
cCG0szLni6LVhjkCsbjSR87kyUnEO6fe+1R9V77w6G7CebI6C1XiUJgWMhNcL3hW
wcKUs/Ja5CeanyTXxuzQmyWC48zCxEXFjJd6BmsqEZ+pCm5IO2/b1BEZQvePB7/1
U1+cPvQXLOZprE4yTGJ36rfo5bs0vBmLrpxR57d+tVOxMyLlbc9wPBr64ptntoP0
jaWvYkxN4FisZDQSA/i2jZRjJKRxAgMBAAGjQjBAMA4GA1UdDwEB/wQEAwIBBjAP
BgNVHRMBAf8EBTADAQH/MB0GA1UdDgQWBBRqciZ60B7vfec7aVHUbI2fkBJmqzAN
BgkqhkiG9w0BAQsFAAOCAQEAeZ8dlsa2eT8ijYfThwMEYGprmi5ZiXMRrEPR9RP/
jTkrwPK9T3CMqS/qF8QLVJ7UG5aYMzyorWKiAHarWWluBh1+xLlEjZivEtRh2woZ
Rkfz6/djwUAFQKXSt/S1mja/qYh2iARVBCuch38aNzx+LaUa2NSJXsq9rD1s2G2v
1fN2D807iDginWyTmsQ9v4IbZT+mD12q/OWyFcq1rca8PdCE6OoGcrBNOTJ4vz4R
nAuknZoh8/CbCzB428Hch0P+vGOaysXCHMnHjf87ElgI5rY97HosTvuDls4MPGmH
VHOkc8KT/1EQrBVUAdj8BbGJoX90g5pJ19xOe4pIb4tF9g==
-----END CERTIFICATE-----
`),
			isCA:    false,
			wantErr: false,
		},
		{
			name: "ca cert",
			pem: []byte(`
-----BEGIN CERTIFICATE-----
MIIFDjCCA/agAwIBAgIMDulMwwAAAABR03eFMA0GCSqGSIb3DQEBCwUAMIG+MQsw
CQYDVQQGEwJVUzEWMBQGA1UEChMNRW50cnVzdCwgSW5jLjEoMCYGA1UECxMfU2Vl
IHd3dy5lbnRydXN0Lm5ldC9sZWdhbC10ZXJtczE5MDcGA1UECxMwKGMpIDIwMDkg
RW50cnVzdCwgSW5jLiAtIGZvciBhdXRob3JpemVkIHVzZSBvbmx5MTIwMAYDVQQD
EylFbnRydXN0IFJvb3QgQ2VydGlmaWNhdGlvbiBBdXRob3JpdHkgLSBHMjAeFw0x
NTEwMDUxOTEzNTZaFw0zMDEyMDUxOTQzNTZaMIG6MQswCQYDVQQGEwJVUzEWMBQG
A1UEChMNRW50cnVzdCwgSW5jLjEoMCYGA1UECxMfU2VlIHd3dy5lbnRydXN0Lm5l
dC9sZWdhbC10ZXJtczE5MDcGA1UECxMwKGMpIDIwMTIgRW50cnVzdCwgSW5jLiAt
IGZvciBhdXRob3JpemVkIHVzZSBvbmx5MS4wLAYDVQQDEyVFbnRydXN0IENlcnRp
ZmljYXRpb24gQXV0aG9yaXR5IC0gTDFLMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8A
MIIBCgKCAQEA2j+W0E25L0Tn2zlem1DuXKVh2kFnUwmqAJqOV38pa9vH4SEkqjrQ
jUcj0u1yFvCRIdJdt7hLqIOPt5EyaM/OJZMssn2XyP7BtBe6CZ4DkJN7fEmDImiK
m95HwzGYei59QAvS7z7Tsoyqj0ip/wDoKVgG97aTWpRzJiatWA7lQrjV6nN5ZGhT
JbiEz5R6rgZFDKNrTdDGvuoYpDbwkrK6HIiPOlJ/915tgxyd8B/lw9bdpXiSPbBt
LOrJz5RBGXFEaLpHPATpXbo+8DX3Fbae8i4VHj9HyMg4p3NFXU2wO7GOFyk36t0F
ASK7lDYqjVs1/lMZLwhGwSqzGmIdTivZGwIDAQABo4IBDDCCAQgwDgYDVR0PAQH/
BAQDAgEGMBIGA1UdEwEB/wQIMAYBAf8CAQAwMwYIKwYBBQUHAQEEJzAlMCMGCCsG
AQUFBzABhhdodHRwOi8vb2NzcC5lbnRydXN0Lm5ldDAwBgNVHR8EKTAnMCWgI6Ah
hh9odHRwOi8vY3JsLmVudHJ1c3QubmV0L2cyY2EuY3JsMDsGA1UdIAQ0MDIwMAYE
VR0gADAoMCYGCCsGAQUFBwIBFhpodHRwOi8vd3d3LmVudHJ1c3QubmV0L3JwYTAd
BgNVHQ4EFgQUgqJwdN28Uz/Pe9T3zX+nYMYKTL8wHwYDVR0jBBgwFoAUanImetAe
733nO2lR1GyNn5ASZqswDQYJKoZIhvcNAQELBQADggEBADnVjpiDYcgsY9NwHRkw
y/YJrMxp1cncN0HyMg/vdMNY9ngnCTQIlZIv19+4o/0OgemknNM/TWgrFTEKFcxS
BJPok1DD2bHi4Wi3Ogl08TRYCj93mEC45mj/XeTIRsXsgdfJghhcg85x2Ly/rJkC
k9uUmITSnKa1/ly78EqvIazCP0kkZ9Yujs+szGQVGHLlbHfTUqi53Y2sAEo1GdRv
c6N172tkw+CNgxKhiucOhk3YtCAbvmqljEtoZuMrx1gL+1YQ1JH7HdMxWBCMRON1
exCdtTix9qrKgWRs6PLigVWXUX/hwidQosk8WwBD9lu51aX8/wdQQGcHsFXwt35u
Lcw=
-----END CERTIFICATE-----
`),
			isCA:    true,
			wantErr: false,
		},
		{
			name:    "malformed pem",
			pem:     []byte("MIID5DCCAsygAwIBAgIJAIJaVMN4AJHVMA0GCSqGSIb3DQEBCwUAMIGIMTQwMgYD"),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := GetTLSConfigForCert(tt.pem)
			if tt.wantErr {
				assert.Error(t, err, "GetTLSConfigForCert expected err returned")
				return
			}

			if tt.isCA {
				assert.False(t, config.InsecureSkipVerify)
				expected := x509.NewCertPool()
				expected.AppendCertsFromPEM(tt.pem)
				assert.True(t, config.RootCAs.Equal(expected))
			} else {
				assert.True(t, config.InsecureSkipVerify)
				assert.Nil(t, config.RootCAs)
				assert.NotNil(t, config.VerifyConnection)
				certDer, _ := pem.Decode(tt.pem)
				cert, _ := x509.ParseCertificate(certDer.Bytes)
				assert.NoError(t, config.VerifyConnection(tls.ConnectionState{
					PeerCertificates: []*x509.Certificate{cert},
				}))
			}
		})
	}
}

func TestUpdateRequestURL(t *testing.T) {
	// updating host
	a := "/test1/test2/test3"
	c := strings.Split(a, "/")
	assert.Equal(t, c[0], "")
	reqUrl, _ := url.Parse("https://10.186.66.241/policy/api/v1/search/query")
	newHost := "newhost"
	newTP := "AA004DFC146CE84FE4B8B99DDF14170202DE1BDC"
	UpdateRequestURL(reqUrl, newHost, newTP)
	assert.Equal(t, newHost, reqUrl.Host)

	// updating path, thumbprint
	reqUrl, _ = url.Parse("http://localhost:1080/external-tp/http1/10.186.66.241/443/F8004DFC146CE84FE4B8B99DDF14170202DE1BDC")
	UpdateRequestURL(reqUrl, newHost, newTP)
	assert.Equal(t, "/external-tp/http1/newhost/443/AA004DFC146CE84FE4B8B99DDF14170202DE1BDC", reqUrl.Path)

	// updating path, ca
	reqUrl, _ = url.Parse("http://localhost:1080/external-cert/http1/10.186.66.241/443/policy/api/v1/search/")
	UpdateRequestURL(reqUrl, newHost, newTP)
	assert.Equal(t, "/external-cert/http1/newhost/443/policy/api/v1/search/", reqUrl.Path)
}

func TestCertPemBytesToHeader(t *testing.T) {
	// Test with valid cert PEM file
	certPem := []byte(`-----BEGIN CERTIFICATE-----
MIIDYzCCAkugAwIBAgIUc+zM3o4BZsmb9RrPms5SBmxGw+kwDQYJKoZIhvcNAQEL
BQAwWjELMAkGA1UEBhMCVVMxCzAJBgNVBAgMAkNBMRIwEAYDVQQHDAlQYWxvIEFs
dG8xDDAKBgNVBAoMA05DUDEPMA0GA1UECwwGVk13YXJlMQswCQYDVQQDDAJDSTAe
Fw0yNDAyMDcwNzM2MDFaFw0yNTAyMDYwNzM2MDFaMFoxCzAJBgNVBAYTAlVTMQsw
CQYDVQQIDAJDQTESMBAGA1UEBwwJUGFsbyBBbHRvMQwwCgYDVQQKDANOQ1AxDzAN
BgNVBAsMBlZNd2FyZTELMAkGA1UEAwwCQ0kwggEiMA0GCSqGSIb3DQEBAQUAA4IB
DwAwggEKAoIBAQC34DKcV7BescXTdiBDvMnh7D0ar9fVm8w+WHnMCWF0IQ7FaHCV
+xPdXEkaQNuLgsSEcA/Bh2evjNNwN9jdO7edxl+zLWlpiWop/6UC4o7iR+Vg20H3
S6DTIZN3KsEJGrwE2mlJs75SrE8aCF+BNzVP3y74JKs5mYcf6hJQxKU09VKw2ZeK
a1hoJJb2gpNBhbZkzXoPA0JEPDyVK9oCevODYN3KN2cjX7fwMjR4efHLjm88uBYn
fVRqH5dnc4xHctjqucI2XdOgGXQVtxWrmc+BIuPY8fjPf/9yayWrzV8q69eO7vYC
gKNt323m53WLw91PL59Q5eXr9xT4i56eHJoPAgMBAAGjITAfMB0GA1UdDgQWBBSn
zwwivhs6ljZJ6UibSf/+P52jnzANBgkqhkiG9w0BAQsFAAOCAQEAkQxyoIguMv7K
YaJNNlc89Yj2cR+I2Yt/QAIAx1X3UCq21C5pK+Wc9vFgBC5w1hlxiPSh68Y1RPyB
qvDdeeRl2Em+H8Otk8SfBkuResv0iJ3YO0/MR0bL10n1ruI+uCY+4gNXFUSxHcuz
G0ioSnkoCAISqkafS//XXaBeorFnWo1oPYViBpeOaNF0tqOpc3o7IQyFfFSybJK6
g0+BYo0SBb1VFAteCqF8dixy0N3wBFT4obL3FFlY3EX17qfxARp48brruWq6zSnI
Hce3uM6Xn8sAglod/r+0onZ09yoiH2Qj5EY50wUIOPtey2ilhuhwoo/M7Nt/yomF
0yUtxsuObg==
-----END CERTIFICATE-----`)

	header := CertPemBytesToHeader(string(certPem))

	assert.Contains(t, header, "MIIDYzCCAkugAwIBAgIUc+zM3o4")
	assert.NotContains(t, header, "-----BEGIN CERTIFICATE-----")
	assert.NotContains(t, header, "-----END CERTIFICATE-----")

	header = CertPemBytesToHeader("/tmp/test.pem")
	assert.Equal(t, "", header)
}

func TestCasttoPointer(t *testing.T) {
	var share *model.Share
	Id := "test-id"
	principalI := mpmodel.PrincipalIdentity{Id: &Id}
	rule := model.Rule{Id: &Id}
	tag := model.Tag{Scope: &Id}
	lbs := model.LBService{Id: &Id}
	share = nil
	tests := []struct {
		name string
		obj  interface{}
		want interface{}
	}{

		{
			name: "PrincipalIdentity",
			obj:  principalI,
			want: &principalI,
		},
		{
			name: "Rule",
			obj:  rule,
			want: &rule,
		},
		// Add more test cases for other types
		{
			name: "Tag",
			obj:  tag,
			want: &tag,
		},
		{
			name: "LBService pointer",
			obj:  &lbs,
			want: &lbs,
		},
		{
			name: "nil",
			obj:  nil,
			want: nil,
		},

		{
			name: "typed nil",
			obj:  share,
			want: share,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CasttoPointer(tt.obj); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CasttoPointer() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCompareArraysWithoutOrder(t *testing.T) {
	assert.True(t, CompareArraysWithoutOrder([]string{"str1", "str2", "str3"}, []string{"str3", "str2", "str1"}))
	assert.False(t, CompareArraysWithoutOrder([]string{"str1", "str2", "str3", "str4"}, []string{"str3", "str2", "str1"}))
}

func TestMergeArraysWithoutDuplicate(t *testing.T) {
	tests := []struct {
		name           string
		old            []string
		new            []string
		expectedMerged []string
	}{
		{
			name:           "MergeEmptyNew",
			old:            []string{"str1", "str2"},
			new:            nil,
			expectedMerged: []string{"str1", "str2"},
		},
		{
			name:           "MergeEmptyOld",
			old:            []string{},
			new:            []string{"str1", "str2"},
			expectedMerged: []string{"str1", "str2"},
		},
		{
			name:           "CommonMerge",
			old:            []string{"str1", "str2", "str5"},
			new:            []string{"str2", "str3", "str4", "str5"},
			expectedMerged: []string{"str1", "str2", "str3", "str4", "str5"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualMerged := MergeArraysWithoutDuplicate(tt.old, tt.new)
			assert.True(t, CompareArraysWithoutOrder(tt.expectedMerged, actualMerged))
		})
	}
}

func TestCategory(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		category string
		expected bool
	}{
		{
			name:     "GroundTriggerError",
			err:      CreateConnectionError("127.0.0.1"),
			category: "groundTriggers",
			expected: true,
		},
		{
			name:     "RetriableError",
			err:      errors.New("ServerBusy"),
			category: "retriables",
			expected: false,
		},
		{
			name:     "RegenerateTriggerError",
			err:      errors.New("InvalidCredentials"),
			category: "regenerateTriggers",
			expected: false,
		},
		{
			name:     "NonMatchingError",
			err:      errors.New("SomeOtherError"),
			category: "groundTriggers",
			expected: false,
		},
		{
			name:     "NilError",
			err:      nil,
			category: "groundTriggers",
			expected: false,
		},
		{
			name:     "InvalidCategory",
			err:      errors.New("ConnectionError"),
			category: "invalidCategory",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := category(tt.err, tt.category)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractHTTPDetailFromBody(t *testing.T) {
	tests := []struct {
		name       string
		host       string
		statusCode int
		body       []byte
		wantDetail ErrorDetail
		wantErr    bool
	}{
		{
			name:       "EmptyBody",
			host:       "10.0.0.1",
			statusCode: 200,
			body:       []byte(""),
			wantDetail: ErrorDetail{StatusCode: 200},
			wantErr:    false,
		},
		{
			name:       "InvalidJSON",
			host:       "10.0.0.1",
			statusCode: 400,
			body:       []byte("{invalid json}"),
			wantDetail: ErrorDetail{StatusCode: 400},
			wantErr:    true,
		},
		{
			name:       "ValidResponse",
			host:       "10.0.0.1",
			statusCode: 500,
			body:       []byte(`{"error_code": 123, "related_errors": [{"httpStatus": "404", "error_code": 456, "error_message": "Related error"}], "error_message": "Main error"}`),
			wantDetail: ErrorDetail{
				StatusCode:         500,
				ErrorCode:          123,
				RelatedErrorCodes:  []int{456},
				RelatedStatusCodes: []string{"404"},
				Details:            "Main errorMain error Related error",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotDetail, err := extractHTTPDetailFromBody(tt.host, tt.statusCode, tt.body)
			if (err != nil) != tt.wantErr {
				t.Errorf("extractHTTPDetailFromBody() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(gotDetail, tt.wantDetail) {
				t.Errorf("extractHTTPDetailFromBody() = %v, want %v", gotDetail, tt.wantDetail)
			}
		})
	}
}

func TestMergeAddressByPort(t *testing.T) {
	tests := []struct {
		name     string
		input    []PortAddress
		expected []PortAddress
	}{
		{
			name: "SinglePortSingleIP",
			input: []PortAddress{
				{Port: 80, IPs: []string{"192.168.1.1"}},
			},
			expected: []PortAddress{
				{Port: 80, IPs: []string{"192.168.1.1"}},
			},
		},
		{
			name: "SinglePortMultipleIPs",
			input: []PortAddress{
				{Port: 80, IPs: []string{"192.168.1.1", "192.168.1.2"}},
			},
			expected: []PortAddress{
				{Port: 80, IPs: []string{"192.168.1.1", "192.168.1.2"}},
			},
		},
		{
			name: "MultiplePortsSingleIP",
			input: []PortAddress{
				{Port: 80, IPs: []string{"192.168.1.1"}},
				{Port: 443, IPs: []string{"192.168.1.2"}},
			},
			expected: []PortAddress{
				{Port: 80, IPs: []string{"192.168.1.1"}},
				{Port: 443, IPs: []string{"192.168.1.2"}},
			},
		},
		{
			name: "MultiplePortsMultipleIPs",
			input: []PortAddress{
				{Port: 80, IPs: []string{"192.168.1.1"}},
				{Port: 443, IPs: []string{"192.168.1.2"}},
				{Port: 80, IPs: []string{"192.168.1.3"}},
			},
			expected: []PortAddress{
				{Port: 80, IPs: []string{"192.168.1.1", "192.168.1.3"}},
				{Port: 443, IPs: []string{"192.168.1.2"}},
			},
		},
		{
			name:     "EmptyInput",
			input:    []PortAddress{},
			expected: []PortAddress(nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MergeAddressByPort(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseVPCPath(t *testing.T) {
	tests := []struct {
		name               string
		nsxResourcePath    string
		expectedOrgID      string
		expectedProjectID  string
		expectedVpcID      string
		expectedResourceID string
	}{
		{
			name:               "ValidPath",
			nsxResourcePath:    "/orgs/org1/projects/proj1/vpcs/vpc1/resources/res1",
			expectedOrgID:      "org1",
			expectedProjectID:  "proj1",
			expectedVpcID:      "vpc1",
			expectedResourceID: "res1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orgID, projectID, vpcID, resourceID := ParseVPCPath(tt.nsxResourcePath)
			assert.Equal(t, tt.expectedOrgID, orgID)
			assert.Equal(t, tt.expectedProjectID, projectID)
			assert.Equal(t, tt.expectedVpcID, vpcID)
			assert.Equal(t, tt.expectedResourceID, resourceID)
		})
	}
}

func TestDumpHttpRequest(t *testing.T) {
	tests := []struct {
		name       string
		request    *http.Request
		expectBody string
	}{
		{
			name: "ValidRequestWithBody",
			request: &http.Request{
				Method: "POST",
				URL:    &url.URL{Path: "/test"},
				Body:   io.NopCloser(strings.NewReader("test body")),
				Header: http.Header{"Content-Type": []string{"application/json"}},
			},
			expectBody: "test body",
		},
		{
			name: "ValidRequestWithoutBody",
			request: &http.Request{
				Method: "GET",
				URL:    &url.URL{Path: "/test"},
				Body:   nil,
				Header: http.Header{"Content-Type": []string{"application/json"}},
			},
			expectBody: "",
		},
		{
			name:       "NilRequest",
			request:    nil,
			expectBody: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			DumpHttpRequest(tt.request)
			if tt.request != nil && tt.request.Body != nil {
				body, err := io.ReadAll(tt.request.Body)
				assert.NoError(t, err)
				assert.Equal(t, tt.expectBody, string(body))
			}
		})
	}
}

func TestNewNSXApiError(t *testing.T) {
	tests := []struct {
		name       string
		apiError   *model.ApiError
		errTypeNum apierrors.ErrorTypeEnum
		wantError  *NSXApiError
	}{
		{
			name:       "ValidApiError",
			errTypeNum: apierrors.ErrorType_NOT_FOUND,
			apiError: &model.ApiError{
				ErrorCode:    pointy.Int64(123),
				ErrorMessage: pointy.String("Test error message"),
				Details:      pointy.String("Test details"),
			},
			wantError: &NSXApiError{
				ErrorTypeEnum: apierrors.ErrorType_NOT_FOUND,
				ApiError: &model.ApiError{
					ErrorCode:    pointy.Int64(123),
					ErrorMessage: pointy.String("Test error message"),
					Details:      pointy.String("Test details"),
				},
			},
		},
		{
			name:      "NilApiError",
			apiError:  nil,
			wantError: &NSXApiError{ApiError: nil},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotError := NewNSXApiError(tt.apiError, tt.errTypeNum)
			assert.Equal(t, tt.wantError, gotError)
		})
	}
}

func TestNSXApiError_Error(t *testing.T) {
	tests := []struct {
		name     string
		apiError *model.ApiError
		expected string
	}{
		{
			name: "ValidApiError",
			apiError: &model.ApiError{
				ErrorCode:    pointy.Int64(123),
				ErrorMessage: pointy.String("Test error message"),
				Details:      pointy.String("Test details"),
				RelatedErrors: []model.RelatedApiError{
					{
						ErrorCode:    pointy.Int64(456),
						ErrorMessage: pointy.String("Related error message"),
					},
				},
			},
			expected: "nsx error code: 123, message: Test error message, details: Test details, related error: [{Details: , ErrorCode: 456,  ErrorMessage: Related error message, ModuleName: }]",
		},
		{
			name:     "NilApiError",
			apiError: nil,
			expected: "SDKError: unknown error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nsxApiError := &NSXApiError{ApiError: tt.apiError}
			result := nsxApiError.Error()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTransNSXApiError(t *testing.T) {
	errortype := apierrors.ErrorTypeEnum("INVALID_REQUEST")
	tests := []struct {
		name     string
		err      error
		expected error
	}{
		{
			name:     "NilError",
			err:      nil,
			expected: nil,
		},
		{
			name:     "NonApiError",
			err:      errors.New("some error"),
			expected: errors.New("some error"),
		},
		{
			name: "ApiError",
			err: apierrors.InvalidRequest{
				Data:      &data.StructValue{},
				ErrorType: &errortype,
			},
			expected: &NSXApiError{
				ErrorTypeEnum: "INVALID_REQUEST",
				ApiError:      &model.ApiError{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := TransNSXApiError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
	err := apierrors.NewNotFound()
	err.Data = data.NewStructValue("test", nil)
	t.Log(err)
	got := TransNSXApiError(*err)
	t.Log(got)
	gotErr, ok := got.(*NSXApiError)
	t.Log(gotErr)

	assert.Equal(t, ok, true)
	assert.Equal(t, gotErr.Type(), apierrors.ErrorType_NOT_FOUND)
}

func TestParseDHCPMode(t *testing.T) {
	nsxMode := ParseDHCPMode("DHCPDeactivated")
	assert.Equal(t, "DHCP_DEACTIVATED", nsxMode)

	nsxMode = ParseDHCPMode("DHCPServer")
	assert.Equal(t, "DHCP_SERVER", nsxMode)

	nsxMode = ParseDHCPMode("DHCPRelay")
	assert.Equal(t, "DHCP_RELAY", nsxMode)

	nsxMode = ParseDHCPMode("None")
	assert.Equal(t, "", nsxMode)
}

func TestUpdateURL(t *testing.T) {
	tests := []struct {
		name         string
		reqUrl       string
		nsxtHost     string
		expectedHost string
		expectedPath string
	}{
		{
			name:         "UpdateHost",
			reqUrl:       "https://10.186.66.241/policy/api/v1/search/query",
			nsxtHost:     "newhost",
			expectedHost: "newhost",
			expectedPath: "/policy/api/v1/search/query",
		},
		{
			name:         "UpdatePathWithEnvoy",
			reqUrl:       "http://localhost:1080/http1/10.186.66.241/443",
			nsxtHost:     "newhost",
			expectedHost: "localhost:1080",
			expectedPath: "/http1/newhost/443",
		},
		{
			name:         "UpdatePathWithoutEnvoy",
			reqUrl:       "http://localhost:1080/external-cert/http1/10.186.66.241/443/policy/api/v1/search/",
			nsxtHost:     "newhost",
			expectedHost: "localhost:1080",
			expectedPath: "/external-cert/http1/newhost/443/policy/api/v1/search/",
		},
		{
			name:         "NoEnvoyInPath",
			reqUrl:       "http://localhost:1080/some/other/path",
			nsxtHost:     "newhost",
			expectedHost: "newhost",
			expectedPath: "/some/other/path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqUrl, _ := url.Parse(tt.reqUrl)
			UpdateURL(reqUrl, tt.nsxtHost)
			assert.Equal(t, tt.expectedHost, reqUrl.Host)
			assert.Equal(t, tt.expectedPath, reqUrl.Path)
		})
	}
}

func TestDumpAPIError(t *testing.T) {
	errortype := apierrors.ErrorTypeEnum("INVALID_REQUEST")
	tests := []struct {
		name     string
		err      error
		expected *model.ApiError
	}{
		{
			name: "AlreadyExists",
			err: apierrors.AlreadyExists{
				Data:      &data.StructValue{},
				ErrorType: &errortype,
			},
			expected: &model.ApiError{},
		},
		{
			name: "AlreadyInDesiredState",
			err: apierrors.AlreadyInDesiredState{
				Data:      &data.StructValue{},
				ErrorType: &errortype,
			},
			expected: &model.ApiError{},
		},
		{
			name: "Canceled",
			err: apierrors.Canceled{
				Data:      &data.StructValue{},
				ErrorType: &errortype,
			},
			expected: &model.ApiError{},
		},
		{
			name: "ConcurrentChange",
			err: apierrors.ConcurrentChange{
				Data:      &data.StructValue{},
				ErrorType: &errortype,
			},
			expected: &model.ApiError{},
		},
		{
			name: "Error",
			err: apierrors.Error{
				Data:      &data.StructValue{},
				ErrorType: &errortype,
			},
			expected: &model.ApiError{},
		},
		{
			name: "FeatureInUse",
			err: apierrors.FeatureInUse{
				Data:      &data.StructValue{},
				ErrorType: &errortype,
			},
			expected: &model.ApiError{},
		},
		{
			name: "InternalServerError",
			err: apierrors.InternalServerError{
				Data:      &data.StructValue{},
				ErrorType: &errortype,
			},
			expected: &model.ApiError{},
		},
		{
			name: "InvalidRequest",
			err: apierrors.InvalidRequest{
				Data:      &data.StructValue{},
				ErrorType: &errortype,
			},
			expected: &model.ApiError{},
		},
		{
			name: "InvalidArgument",
			err: apierrors.InvalidArgument{
				Data:      &data.StructValue{},
				ErrorType: &errortype,
			},
			expected: &model.ApiError{},
		},
		{
			name: "InvalidElementConfiguration",
			err: apierrors.InvalidElementConfiguration{
				Data:      &data.StructValue{},
				ErrorType: &errortype,
			},
			expected: &model.ApiError{},
		},
		{
			name: "InvalidElementType",
			err: apierrors.InvalidElementType{
				Data:      &data.StructValue{},
				ErrorType: &errortype,
			},
			expected: &model.ApiError{},
		},
		{
			name: "NotAllowedInCurrentState",
			err: apierrors.NotAllowedInCurrentState{
				Data:      &data.StructValue{},
				ErrorType: &errortype,
			},
			expected: &model.ApiError{},
		},
		{
			name: "NotFound",
			err: apierrors.NotFound{
				Data:      &data.StructValue{},
				ErrorType: &errortype,
			},
			expected: &model.ApiError{},
		},
		{
			name: "OperationNotFound",
			err: apierrors.OperationNotFound{
				Data:      &data.StructValue{},
				ErrorType: &errortype,
			},
			expected: &model.ApiError{},
		},
		{
			name: "ResourceBusy",
			err: apierrors.ResourceBusy{
				Data:      &data.StructValue{},
				ErrorType: &errortype,
			},
			expected: &model.ApiError{},
		},
		{
			name: "ResourceInUse",
			err: apierrors.ResourceInUse{
				Data:      &data.StructValue{},
				ErrorType: &errortype,
			},
			expected: &model.ApiError{},
		},
		{
			name: "ResourceInaccessible",
			err: apierrors.ResourceInaccessible{
				Data:      &data.StructValue{},
				ErrorType: &errortype,
			},
			expected: &model.ApiError{},
		},
		{
			name: "ServiceUnavailable",
			err: apierrors.ServiceUnavailable{
				Data:      &data.StructValue{},
				ErrorType: &errortype,
			},
			expected: &model.ApiError{},
		},
		{
			name: "TimedOut",
			err: apierrors.TimedOut{
				Data:      &data.StructValue{},
				ErrorType: &errortype,
			},
			expected: &model.ApiError{},
		},
		{
			name: "UnableToAllocateResource",
			err: apierrors.UnableToAllocateResource{
				Data:      &data.StructValue{},
				ErrorType: &errortype,
			},
			expected: &model.ApiError{},
		},
		{
			name: "Unauthenticated",
			err: apierrors.Unauthenticated{
				Data:      &data.StructValue{},
				ErrorType: &errortype,
			},
			expected: &model.ApiError{},
		},
		{
			name: "Unauthorized",
			err: apierrors.Unauthorized{
				Data:      &data.StructValue{},
				ErrorType: &errortype,
			},
			expected: &model.ApiError{},
		},
		{
			name: "UnexpectedInput",
			err: apierrors.UnexpectedInput{
				Data:      &data.StructValue{},
				ErrorType: &errortype,
			},
			expected: &model.ApiError{},
		},
		{
			name: "Unsupported",
			err: apierrors.Unsupported{
				Data:      &data.StructValue{},
				ErrorType: &errortype,
			},
			expected: &model.ApiError{},
		},
		{
			name: "UnverifiedPeer",
			err: apierrors.UnverifiedPeer{
				Data:      &data.StructValue{},
				ErrorType: &errortype,
			},
			expected: &model.ApiError{},
		},
		{
			name:     "NonMatchingError",
			err:      errors.New("SomeOtherError"),
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiError, _ := DumpAPIError(tt.err)
			assert.Equal(t, tt.expected, apiError)
		})
	}
}

func TestRelatedErrorToString(t *testing.T) {
	tests := []struct {
		name     string
		err      *model.RelatedApiError
		expected string
	}{
		{
			name:     "NilError",
			err:      nil,
			expected: "nil",
		},
		{
			name: "ValidError",
			err: &model.RelatedApiError{
				Details:      pointy.String("Some details"),
				ErrorCode:    pointy.Int64(123),
				ErrorMessage: pointy.String("Some error message"),
				ModuleName:   pointy.String("Some module"),
			},
			expected: "{Details: Some details, ErrorCode: 123,  ErrorMessage: Some error message, ModuleName: Some module}",
		},
		{
			name: "EmptyFields",
			err: &model.RelatedApiError{
				Details:      nil,
				ErrorCode:    nil,
				ErrorMessage: nil,
				ModuleName:   nil,
			},
			expected: "{Details: , ErrorCode: 0,  ErrorMessage: , ModuleName: }",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := relatedErrorToString(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}
func TestRelatedErrorsToString(t *testing.T) {
	tests := []struct {
		name     string
		errors   []model.RelatedApiError
		expected string
	}{
		{
			name:     "NilErrors",
			errors:   nil,
			expected: "nil",
		},
		{
			name:     "EmptyErrors",
			errors:   []model.RelatedApiError{},
			expected: "[]",
		},
		{
			name: "SingleError",
			errors: []model.RelatedApiError{
				{
					Details:      pointy.String("Some details"),
					ErrorCode:    pointy.Int64(123),
					ErrorMessage: pointy.String("Some error message"),
					ModuleName:   pointy.String("Some module"),
				},
			},
			expected: "[{Details: Some details, ErrorCode: 123,  ErrorMessage: Some error message, ModuleName: Some module}]",
		},
		{
			name: "MultipleErrors",
			errors: []model.RelatedApiError{
				{
					Details:      pointy.String("First details"),
					ErrorCode:    pointy.Int64(123),
					ErrorMessage: pointy.String("First error message"),
					ModuleName:   pointy.String("First module"),
				},
				{
					Details:      pointy.String("Second details"),
					ErrorCode:    pointy.Int64(456),
					ErrorMessage: pointy.String("Second error message"),
					ModuleName:   pointy.String("Second module"),
				},
			},
			expected: "[{Details: First details, ErrorCode: 123,  ErrorMessage: First error message, ModuleName: First module}, {Details: Second details, ErrorCode: 456,  ErrorMessage: Second error message, ModuleName: Second module}]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := relatedErrorsToString(tt.errors)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSafeString(t *testing.T) {
	tests := []struct {
		name     string
		input    *string
		expected string
	}{
		{
			name:     "NilPointer",
			input:    nil,
			expected: "",
		},
		{
			name:     "NonNilPointer",
			input:    pointy.String("test string"),
			expected: "test string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := safeString(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSafeInt(t *testing.T) {
	tests := []struct {
		name     string
		input    *int64
		expected int64
	}{
		{
			name:     "NilPointer",
			input:    nil,
			expected: 0,
		},
		{
			name:     "NonNilPointer",
			input:    pointy.Int64(123),
			expected: 123,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := safeInt(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
func TestIsEmptyAPIError(t *testing.T) {
	tests := []struct {
		name     string
		apiError model.ApiError
		expected bool
	}{
		{
			name:     "EmptyApiError",
			apiError: model.ApiError{},
			expected: true,
		},
		{
			name: "NonEmptyApiErrorWithErrorCode",
			apiError: model.ApiError{
				ErrorCode: pointy.Int64(123),
			},
			expected: false,
		},
		{
			name: "NonEmptyApiErrorWithErrorMessage",
			apiError: model.ApiError{
				ErrorMessage: pointy.String("Some error message"),
			},
			expected: false,
		},
		{
			name: "NonEmptyApiErrorWithBothFields",
			apiError: model.ApiError{
				ErrorCode:    pointy.Int64(123),
				ErrorMessage: pointy.String("Some error message"),
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isEmptyAPIError(tt.apiError)
			assert.Equal(t, tt.expected, result)
		})
	}
}
func TestCastApiError(t *testing.T) {
	tests := []struct {
		name               string
		apiErrorDataValue  *data.StructValue
		expectedApiError   *model.ApiError
		expectedLogMessage string
	}{
		{
			name:               "NilApiErrorDataValue",
			apiErrorDataValue:  nil,
			expectedApiError:   nil,
			expectedLogMessage: "Dump api error no extra error info",
		},
		{
			name: "ValidApiErrorDataValue",
			apiErrorDataValue: data.NewStructValue("ApiError", map[string]data.DataValue{
				"error_code":    data.NewIntegerValue(123),
				"error_message": data.NewStringValue("Test error message"),
			}),
			expectedApiError: &model.ApiError{
				ErrorCode:    pointy.Int64(123),
				ErrorMessage: pointy.String("Test error message"),
			},
			expectedLogMessage: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiError := castApiError(tt.apiErrorDataValue)
			assert.Equal(t, tt.expectedApiError, apiError)
		})
	}
}
func TestFindTag(t *testing.T) {
	tests := []struct {
		name     string
		tags     []model.Tag
		tagScope string
		expected string
	}{
		{
			name: "TagFound",
			tags: []model.Tag{
				{Scope: pointy.String("scope1"), Tag: pointy.String("tag1")},
				{Scope: pointy.String("scope2"), Tag: pointy.String("tag2")},
			},
			tagScope: "scope1",
			expected: "tag1",
		},
		{
			name: "TagNotFound",
			tags: []model.Tag{
				{Scope: pointy.String("scope1"), Tag: pointy.String("tag1")},
				{Scope: pointy.String("scope2"), Tag: pointy.String("tag2")},
			},
			tagScope: "scope3",
			expected: "",
		},
		{
			name:     "EmptyTags",
			tags:     []model.Tag{},
			tagScope: "scope1",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FindTag(tt.tags, tt.tagScope)
			assert.Equal(t, tt.expected, result)
		})
	}
}
