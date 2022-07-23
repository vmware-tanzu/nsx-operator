/* Copyright (c) 2021 VMware, Inc. All rights reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/auth"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/auth/jwt"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
)

func TestParseURL(t *testing.T) {
	testcases := []struct {
		sourceURL  string
		expectHost string
		expectURL  string
	}{
		{
			sourceURL:  "10.1.0.1",
			expectHost: "10.1.0.1",
			expectURL:  "https://10.1.0.1",
		},
		{
			sourceURL:  "10.1.0.1/",
			expectHost: "10.1.0.1",
			expectURL:  "https://10.1.0.1",
		},
		{
			sourceURL:  "https://10.1.0.1",
			expectHost: "10.1.0.1",
			expectURL:  "https://10.1.0.1",
		},
		{
			sourceURL:  "http://10.1.0.1",
			expectHost: "10.1.0.1",
			expectURL:  "http://10.1.0.1",
		},
		{
			sourceURL:  "http://10.1.0.1:81",
			expectHost: "10.1.0.1:81",
			expectURL:  "http://10.1.0.1:81",
		},
		{
			sourceURL:  "http://10.1.0.1:81/",
			expectHost: "10.1.0.1:81",
			expectURL:  "http://10.1.0.1:81",
		},
		{
			sourceURL:  "http://10.1.0.1:81/test",
			expectHost: "10.1.0.1:81",
			expectURL:  "http://10.1.0.1:81",
		},
	}

	for _, v := range testcases {
		src := v.sourceURL
		host, scheme, _ := parseURL(src)
		assert.Equal(t, host, v.expectHost, "they should be equal")
		assert.Equal(t, scheme+"://"+host, v.expectURL, "they should be equal")
	}
}

type ncpCertProvider struct{}

func (cert *ncpCertProvider) FileName() string {
	return "certProvider"
}

func createNcpPovider() auth.ClientCertProvider {
	return &ncpCertProvider{}
}

type mockObject struct {
	mock.Mock
	url string
}

func (m *mockObject) Host() string {
	args := m.Called()
	return args.String(0)
}

func (m *mockObject) Scheme() string {
	args := m.Called()
	return args.String(0)
}

var (
	result = []string{
		`{"module_name":"common-services","error_message":"The credentials were incorrect or the account specified has been locked.","error_code":403}`,
		`{"module_name":"common-services","error_message":"The credentials were incorrect or the account specified has been locked.","error_code":403}`,
		`{"module_name":"common-services","error_message":"The credentials were incorrect or the account specified has been locked.","error_code":403}`,
		`{"module_name":"common-services","error_message":"The credentials were incorrect or the account specified has been locked.","error_code":403}`,
		`{"module_name":"common-services","error_message":"User account locked","error_code":401}`,
	}
	status = []int{
		http.StatusForbidden,
		http.StatusOK,
		http.StatusOK,
		http.StatusOK,
		http.StatusOK,
	}
)

func TestCreateAuthSession(t *testing.T) {
	assert := assert.New(t)
	jar := NewJar()
	cluster := &Cluster{}
	tr := cluster.createTransport(10)
	client := cluster.createHTTPClient(tr, 30)
	noBClient := cluster.createNoBalancerClient(90, 90)
	rl := ratelimiter.NewFixRateLimiter(10)
	ep, err := NewEndpoint("10.0.0.1", &client, &noBClient, rl, nil)
	assert.Nil(err, fmt.Sprintf("Endpoint create failed due to %v", err))

	certProvider := createNcpPovider()

	mockObj := new(mockObject)
	// setup mock server
	index := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status[index])
		w.Write([]byte(result[index]))
		index++
		if index >= len(result) {
			index = 0
		}
	}))
	defer ts.Close()
	// certProvider is provided, tokenProvider is nil
	mockObj.AssertNotCalled(t, "Host")
	ep.provider = mockObj
	ep.createAuthSession(certProvider, nil, "admin", "password", jar)
	// certProvider is nil, tokenProvider is nil
	mockObj.On("Host").Return(ts.URL[len("http://"):])
	mockObj.On("Scheme").Return("http")

	// createRequest will use ts.URL
	mockObj.url = ts.URL
	ep.setStatus(UP)

	err = ep.createAuthSession(nil, nil, "admin", "password", jar)
	mockObj.AssertCalled(t, "Host")
	mockObj.AssertCalled(t, "Scheme")
	assert.Equal(err.Error(), "session creation failed, unexpected status code 403", "Auth should be failed")

	err = ep.createAuthSession(nil, nil, "admin", "password", jar)
	assert.Equal(err.Error(), "no token in response", "Auth should be failed")
}

func TestKeepAlive(t *testing.T) {
	assert := assert.New(t)
	// mock http server
	index := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status[index])
		w.Write([]byte(result[index]))
		index++
		if index >= len(result) {
			index = 0
		}
	}))
	defer ts.Close()
	cluster := &Cluster{}
	tr := cluster.createTransport(10)
	client := cluster.createHTTPClient(tr, 30)
	noBClient := cluster.createNoBalancerClient(90, 90)
	rl := ratelimiter.NewFixRateLimiter(10)
	ep, err := NewEndpoint(ts.URL[len("http://"):], &client, &noBClient, rl, nil)

	assert.Nil(err, fmt.Sprintf("Endpoint create failed due to %v", err))
	ep.setStatus(UP)
	// Host() is called by keepAlive, check if the keepAlive has been called
	mockObj := new(mockObject)
	ep.provider = mockObj
	ep.client.Jar = NewJar()
	ep.setXSRFToken("testtoken")
	mockObj.On("Host").Return(ts.URL[len("http://"):])
	mockObj.On("Scheme").Return("http")
	ep.keepaliveperiod = 1
	go ep.keepAlive()
	time.Sleep(1500 * time.Microsecond)
	mockObj.AssertCalled(t, "Host")

	// try to trigger using user/password
	ep.setXSRFToken("")
	ep.keepAlive()
	time.Sleep(1500 * time.Microsecond)
	ep.keepAlive()

	// test stop ep
	start := time.Now()
	go func() {
		time.Sleep(2 * time.Second)
		close(ep.stop)
	}()
	ep.KeepAlive()
	waitTime := time.Since(start)
	assert.True(waitTime/time.Second <= 2, "keepalive should stop within 2 seconds")

	// ep.KeepAlive will break after 10 times retry
	ep.tokenProvider = &jwt.JWTTokenProvider{}
	ep.lockWait = time.Millisecond * 300
	patch := gomonkey.ApplyMethod(reflect.TypeOf(ep.tokenProvider), "GetToken", func(_ *jwt.JWTTokenProvider, refreshToken bool) (string, error) {
		return "", errors.New("The account of the user trying to authenticate is locked. :: User account locked")
	})
	defer patch.Reset()
	ep.KeepAlive()
	assert.Equal(ep.Status(), DOWN)
}
