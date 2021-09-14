// Copyright (c) 2021 VMware, Inc. All rights reserved. VMware Confidential.

package nsx

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"gitlab.eng.vmware.com/nsx-container-plugin/vmware-nsxlib-go/pkg/nsx/auth"
	"gitlab.eng.vmware.com/nsx-container-plugin/vmware-nsxlib-go/pkg/nsx/ratelimiter"
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
	result = []string{`{"module_name":"common-services","error_message":"The credentials were incorrect or the account specified has been locked.","error_code":403}`,
		`{"module_name":"common-services","error_message":"The credentials were incorrect or the account specified has been locked.","error_code":200}`,
		`{"module_name":"common-services","error_message":"The credentials were incorrect or the account specified has been locked.","error_code":200}`,
		`{"module_name":"common-services","error_message":"The credentials were incorrect or the account specified has been locked.","error_code":200}`,
	}
	status = []int{
		http.StatusForbidden,
		http.StatusOK,
		http.StatusOK,
		http.StatusOK,
	}
)

func TestCreateAuthSession(t *testing.T) {
	assert := assert.New(t)
	jar := newJar()
	tr := createTransport(nil, 10)
	client := createClient(tr, 30)
	noBClient := createNoBalancerClient(90, 90)
	rl := ratelimiter.CreateFixRateLimiter(10)
	ep, err := NewEndpoint("10.0.0.1", &client, &noBClient, rl)
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
	assert.Equal(err.Error(), "Session create failed for response error 403", "Auth should be failed")

	err = ep.createAuthSession(nil, nil, "admin", "password", jar)
	assert.Equal(err.Error(), "Session create failed for response body no token", "Auth should be failed")

}

func TestKeepAlive(t *testing.T) {
	assert := assert.New(t)
	//mock http server
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
	tr := createTransport(nil, 10)
	client := createClient(tr, 30)
	noBClient := createNoBalancerClient(90, 90)
	rl := ratelimiter.CreateFixRateLimiter(10)
	ep, err := NewEndpoint(ts.URL[len("http://"):], &client, &noBClient, rl)

	assert.Nil(err, fmt.Sprintf("Endpoint create failed due to %v", err))
	ep.setStatus(UP)
	// Host() is called by keepAlive, check if the keepAlive has been called
	mockObj := new(mockObject)
	ep.provider = mockObj
	ep.client.Jar = &jar{}
	ep.setXSRFToken("testtoken")
	mockObj.On("Host").Return(ts.URL[len("http://"):])
	mockObj.On("Scheme").Return("http")
	ep.setKeepAlivePeriod(1)
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
}
