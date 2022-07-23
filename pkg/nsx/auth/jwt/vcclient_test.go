/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package jwt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/govmomi/sts"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/soap"
)

func TestVCClient_NewVCClient(t *testing.T) {
	result1 := `{
		"value": "hello"
	  }`
	result2 := `{
		"test": "hello"
	  }`
	offset := 0
	result := []string{result1, result2}
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(result[offset]))
	}))
	defer ts.Close()

	index := strings.Index(ts.URL, "//")
	hostURL := ts.URL[index+2:]

	buf := strings.Split(hostURL, ":")
	host := buf[0]
	port, _ := strconv.Atoi(buf[1])

	userName := "admin"
	password := "admin"
	ssoDomain := "vsphere.local"

	patch := gomonkey.ApplyFunc(vim25.NewClient, func(ctx context.Context, rt soap.RoundTripper) (*vim25.Client, error) {
		client := &vim25.Client{}
		return client, nil
	})
	defer patch.Reset()
	soapClient := soap.NewClient(&url.URL{}, false)
	patch.ApplyMethod(reflect.TypeOf(soapClient), "NewServiceClient", func(_ *soap.Client, path string, namespace string) *soap.Client {
		return soapClient
	})
	stsClient := &sts.Client{}
	patch.ApplyMethod(reflect.TypeOf(stsClient), "Issue", func(_ *sts.Client, ctx context.Context, req sts.TokenRequest) (*sts.Signer, error) {
		singer := &sts.Signer{}
		return singer, nil
	})
	vcClient, err := NewVCClient(host, port, ssoDomain, userName, password, nil, true)
	assert.Equal(t, err, nil)
	assert.NotEqual(t, vcClient, nil)

	err = vcClient.getorRenewVAPISession()
	assert.Nil(t, err)

	// bad session data
	offset += 1
	_, err = NewVCClient(host, port, ssoDomain, userName, password, nil, true)
	assert.Equal(t, err, nil)
}

func TestVCClient_createHttpClient(t *testing.T) {
	// no cert
	client := createHttpClient(false, []byte{})
	assert.NotEqual(t, client, nil)

	// invalid cert
	client = createHttpClient(false, []byte{1, 2})
	assert.NotEqual(t, client, nil)
}

func TestVCClient_getorRenewVAPISession(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))
	defer ts.Close()

	index := strings.Index(ts.URL, "//")
	hostURL := ts.URL[index+2:]

	buf := strings.Split(hostURL, ":")
	host := buf[0]
	port, _ := strconv.Atoi(buf[1])
	userName := "admin"
	password := "admin"
	ssoDomain := "vsphere.local"
	vcClient, err := NewVCClient(host, port, ssoDomain, userName, password, nil, true)
	assert.Equal(t, err, nil)
	assert.NotEqual(t, vcClient, nil)

	err = vcClient.getorRenewVAPISession()
	assert.NotNil(t, err)
}

func TestVCClient_reloadUsernamePass(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))
	defer ts.Close()

	index := strings.Index(ts.URL, "//")
	hostURL := ts.URL[index+2:]

	buf := strings.Split(hostURL, ":")
	host := buf[0]
	port, _ := strconv.Atoi(buf[1])
	userName := "admin"
	password := "admin"
	ssoDomain := "vsphere.local"
	vcClient, _ := NewVCClient(host, port, ssoDomain, userName, password, nil, true)
	err := vcClient.reloadUsernamePass()
	assert.NotNil(t, err)

	patch := gomonkey.ApplyFunc(os.ReadFile, func(filename string) ([]byte, error) {
		return []byte{}, nil
	})
	defer patch.Reset()
	err = vcClient.reloadUsernamePass()
	assert.Nil(t, err)
}
