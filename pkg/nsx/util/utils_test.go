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

	"github.com/stretchr/testify/assert"
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
