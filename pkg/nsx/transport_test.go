/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

var (
	timeout         = time.Duration(20)
	idleConnTimeout = time.Duration(20)
)

func TestRoundTripConnectionRefused(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "hello")
	}))
	defer ts.Close()
	a := "127.0.0.1"
	config := NewConfig(a, "admin", "passw0rd", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil)
	cluster := &Cluster{}
	tr := cluster.createTransport(config.TokenProvider, idleConnTimeout)
	client := cluster.createHTTPClient(tr, idleConnTimeout)
	noBClient := cluster.createNoBalancerClient(timeout, idleConnTimeout)
	r := ratelimiter.NewRateLimiter(config.APIRateMode)
	eps, _ := cluster.createEndpoints(config.APIManagers, &client, &noBClient, r, nil)
	eps[0].status = UP
	tr.endpoints = eps
	req, err := http.NewRequest("GET", ts.URL, nil)
	resp, err := tr.RoundTrip(req)
	assert.NotNil(t, err, "Should report error")
	_, ok := err.(*util.ServiceClusterUnavailable)
	assert.True(t, ok, fmt.Sprintf("Return wrong error type %v", err))
	assert.Nil(t, resp, "Resp should be nil")
}

func TestRoundTripDecodeBodyFailed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "hello")
	}))
	defer ts.Close()
	index := strings.Index(ts.URL, "//")
	a := ts.URL[index+2:]
	config := NewConfig(a, "admin", "passw0rd", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil)
	cluster := &Cluster{}
	tr := cluster.createTransport(config.TokenProvider, timeout)
	client := cluster.createHTTPClient(tr, 30)
	noBClient := cluster.createNoBalancerClient(30, 20)
	r := ratelimiter.NewRateLimiter(config.APIRateMode)
	eps, _ := cluster.createEndpoints(config.APIManagers, &client, &noBClient, r, nil)
	eps[0].status = UP
	tr.endpoints = eps
	req, err := http.NewRequest("GET", ts.URL, nil)
	_, err = tr.RoundTrip(req)
	_, ok := err.(util.ManagerError)
	assert.True(t, ok, fmt.Sprintf("Return wrong error type %v", err))
}

func TestRoundTripAuthFailed(t *testing.T) {
	assert := assert.New(t)
	result := `{"module_name":"common-services","error_message":"The credentials were incorrect or the account specified has been locked","error_code":403}`
	healthresult := `{
		"healthy" : true,
		"components_health" : "POLICY:UP, SEARCH:UP, MANAGER:UP, NODE_MGMT:UP, UI:UP"
	}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Index(r.URL.Path, "reverse-proxy/node/health") > 1 {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(healthresult))
		} else {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(result))
		}
	}))
	defer ts.Close()
	index := strings.Index(ts.URL, "//")
	a := ts.URL[index+2:]
	config := NewConfig(a, "admin", "passw0rd", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil)
	cluster, err := NewCluster(config)
	assert.Nil(err, fmt.Sprintf("Create cluster error %v", err))
	cluster.endpoints[0], _ = NewEndpoint(ts.URL, &cluster.client, &cluster.noBalancerClient, cluster.endpoints[0].ratelimiter, nil)
	cluster.endpoints[0].keepAlive()
	tr := cluster.transport
	req, err := http.NewRequest("GET", ts.URL, nil)
	req.Header.Add("Accept", "application/json")
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	_, err = tr.RoundTrip(req)
	_, ok := err.(*util.InvalidCredentials)
	assert.True(ok, fmt.Sprintf("Wrong error type %v", err))
}

func TestRoundTripRetry(t *testing.T) {
	assert := assert.New(t)
	result := `{"module_name":"common-services","error_message":"The credentials were incorrect or the account specified has been locked","error_code":98}`
	healthresult := `{
		"healthy" : true,
		"components_health" : "POLICY:UP, SEARCH:UP, MANAGER:UP, NODE_MGMT:UP, UI:UP"
	}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Index(r.URL.Path, "reverse-proxy/node/health") > 1 || strings.Index(r.URL.Path, "api/session/create") > 1 {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(healthresult))
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(result))
		}
	}))
	defer ts.Close()
	index := strings.Index(ts.URL, "//")
	a := ts.URL[index+2:]
	config := NewConfig(a, "admin", "passw0rd", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil)
	cluster, err := NewCluster(config)
	assert.Nil(err, fmt.Sprintf("Create cluster error %v", err))
	cluster.endpoints[0], _ = NewEndpoint(ts.URL, &cluster.client, &cluster.noBalancerClient, cluster.endpoints[0].ratelimiter, nil)
	cluster.endpoints[0].keepAlive()
	tr := cluster.transport
	req, err := http.NewRequest("GET", ts.URL, nil)
	req.Header.Add("Accept", "application/json")
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	_, err = tr.RoundTrip(req)
	log.V(1).Info("", "errorType", err)
	_, ok := err.(*util.CannotConnectToServer)
	assert.True(ok, fmt.Sprintf("Wrong error type %v", err))
}

func TestSelectEndpoint(t *testing.T) {
	assert := assert.New(t)
	a := "127.0.0.1, 127.0.0.2, 127.0.0.3"
	config := NewConfig(a, "admin", "passw0rd", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil)
	cluster := &Cluster{}
	tr := cluster.createTransport(config.TokenProvider, idleConnTimeout)
	client := cluster.createHTTPClient(tr, timeout)
	noBClient := cluster.createNoBalancerClient(timeout, idleConnTimeout)
	r := ratelimiter.NewRateLimiter(config.APIRateMode)
	eps, _ := cluster.createEndpoints(config.APIManagers, &client, &noBClient, r, nil)
	// all eps DOWN
	ep, err := tr.selectEndpoint()
	assert.NotNil(t, err, fmt.Sprintf("Select endpoint error %s", err))
	// one ep UP
	eps[0].status = UP
	tr.endpoints = eps

	ep, err = tr.selectEndpoint()
	assert.Nil(err, fmt.Sprintf("Select endpoint failed due to %v", err))
	assert.Equal(ep.Host(), eps[0].Host(), "Select endpoint error, ep is %s, error is %s", ep.Host(), err)

	// select ep has least connection number
	eps[1].status = UP
	eps[2].status = UP

	eps[0].connnumber = 3
	eps[1].connnumber = 2
	eps[2].connnumber = 2
	ep, err = tr.selectEndpoint()
	assert.Nil(err, fmt.Sprintf("Select endpoint failed due to %v", err))
	assert.Equal(ep.Host(), eps[1].Host(), "Select endpoint error, ep is %s, error is %s", ep.Host(), err)

	eps[0].connnumber = 0
	eps[1].connnumber = 4
	eps[2].connnumber = 0
	ep, err = tr.selectEndpoint()
	assert.Nil(err, fmt.Sprintf("Select endpoint failed due to %v", err))
	assert.Equal(ep.Host(), eps[0].Host(), "Select endpoint error, ep is %s, error is %s", ep.Host(), err)
}
