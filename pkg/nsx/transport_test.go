/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
)

var (
	timeout         = time.Duration(20)
	idleConnTimeout = time.Duration(20)
)

func TestRoundTripRetry(t *testing.T) {
	assert := assert.New(t)
	result := `{"module_name":"common-services","error_message":"The credentials were incorrect or the account specified has been locked","error_code":98}`
	healthresult := `{
		"healthy" : true,
		"components_health" : "POLICY:UP, SEARCH:UP, MANAGER:UP, NODE_MGMT:UP, UI:UP"
	}`

	statusCode := 403
	time := 0
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Index(r.URL.Path, "reverse-proxy/node/health") > 1 || strings.Index(r.URL.Path, "api/session/create") > 1 {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(healthresult))
		} else {
			time += 1
			if time == 2 {
				w.WriteHeader(statusCode)
				w.Write([]byte(result))
			} else {
				if time > 3 {
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(healthresult))
				} else {
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte(result))
				}
			}
		}
	}))
	defer ts.Close()
	index := strings.Index(ts.URL, "//")
	a := ts.URL[index+2:]
	config := NewConfig(a, "admin", "passw0rd", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster, err := NewCluster(config)
	assert.Nil(err, fmt.Sprintf("Create cluster error %v", err))
	cluster.endpoints[0], _ = NewEndpoint(ts.URL, cluster.client, cluster.noBalancerClient, cluster.endpoints[0].ratelimiter, nil, config.Logger)
	cluster.endpoints[0].keepAlive()
	tr := cluster.transport
	req, _ := http.NewRequest("GET", ts.URL, nil)
	req.Header.Add("Accept", "application/json")
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	_, err = tr.RoundTrip(req)
	assert.Equal(err, nil)
}

func TestSelectEndpoint(t *testing.T) {
	assert := assert.New(t)
	a := "127.0.0.1, 127.0.0.2, 127.0.0.3"
	config := NewConfig(a, "admin", "passw0rd", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster := &Cluster{config: &Config{}}
	tr := cluster.createTransport(idleConnTimeout)
	client := cluster.createHTTPClient(tr, timeout)
	noBClient := cluster.createNoBalancerClient(timeout, idleConnTimeout)
	r := ratelimiter.NewRateLimiter(config.APIRateMode)
	eps, _ := cluster.createEndpoints(config.APIManagers, client, noBClient, r, nil)
	// all eps DOWN
	_, err := tr.selectEndpoint()
	assert.NotNil(t, err, fmt.Sprintf("Select endpoint error %s", err))
	// one ep UP
	eps[0].status = UP
	tr.endpoints = eps

	ep, err := tr.selectEndpoint()
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

	// Test when connection counts are over 100
	eps[0].connnumber = 150
	eps[1].connnumber = 120
	eps[2].connnumber = 180
	ep, err = tr.selectEndpoint()
	assert.Nil(err, fmt.Sprintf("Select endpoint failed due to %v", err))
	assert.Equal(ep.Host(), eps[1].Host(), "Should select ep with lowest connections even if > 100")
}

func TestTransport_RoundTrip(t *testing.T) {
	type fields struct {
		Base      http.RoundTripper
		endpoints []*Endpoint
		config    *Config
	}
	type args struct {
		r *http.Request
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    *http.Response
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := &Transport{
				Base:      tt.fields.Base,
				endpoints: tt.fields.endpoints,
				config:    tt.fields.config,
			}
			got, err := tr.RoundTrip(tt.args.r)
			if (err != nil) != tt.wantErr {
				t.Errorf("Transport.RoundTrip() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Transport.RoundTrip() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_handleRoundTripError(t *testing.T) {
	a := "127.0.0.1, 127.0.0.2, 127.0.0.3"
	config := NewConfig(a, "admin", "passw0rd", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster := &Cluster{config: &Config{}}
	tr := cluster.createTransport(idleConnTimeout)
	client := cluster.createHTTPClient(tr, timeout)
	noBClient := cluster.createNoBalancerClient(timeout, idleConnTimeout)
	r := ratelimiter.NewRateLimiter(config.APIRateMode)
	eps, _ := cluster.createEndpoints(config.APIManagers, client, noBClient, r, nil)
	cluster.endpoints = eps
	err := errors.New("connection refused")
	assert.NotNil(t, handleRoundTripError(err, eps[0]))
	err = errors.New("i/o timeout")
	assert.NotNil(t, handleRoundTripError(err, eps[1]))
	err = errors.New("others")
	assert.NotNil(t, handleRoundTripError(err, eps[2]))
}

func TestTransport_base(t *testing.T) {
	type fields struct {
		Base      http.RoundTripper
		endpoints []*Endpoint
		config    *Config
	}
	tests := []struct {
		name   string
		fields fields
		want   http.RoundTripper
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := &Transport{
				Base:      tt.fields.Base,
				endpoints: tt.fields.endpoints,
				config:    tt.fields.config,
			}
			if got := tr.base(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Transport.base() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTransport_selectEndpoint(t *testing.T) {
	type fields struct {
		Base      http.RoundTripper
		endpoints []*Endpoint
		config    *Config
	}
	tests := []struct {
		name    string
		fields  fields
		want    *Endpoint
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := &Transport{
				Base:      tt.fields.Base,
				endpoints: tt.fields.endpoints,
				config:    tt.fields.config,
			}
			got, err := tr.selectEndpoint()
			if (err != nil) != tt.wantErr {
				t.Errorf("Transport.selectEndpoint() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Transport.selectEndpoint() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRoundTripRequestBodyPreservedOnRetry(t *testing.T) {
	assert := assert.New(t)
	attempt := 0
	reqBodyRecv := ""

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		body, _ := io.ReadAll(r.Body)
		reqBodyRecv = string(body)
		if attempt == 1 {
			// First attempt fails with retriable ServerBusy / InternalServerError
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"module_name":"common-services","error_code":607,"error_message":"APITransactionAborted"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	config := NewConfig("127.0.0.1", "admin", "password", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	ep, err := NewEndpoint(ts.URL[len("http://"):], http.DefaultClient, http.DefaultClient, ratelimiter.NewFixRateLimiter(10), nil, config.Logger)
	assert.NoError(err)
	ep.setStatus(UP)

	tr := &Transport{
		endpoints: []*Endpoint{ep},
		config:    config,
		Base:      http.DefaultTransport,
	}

	reqPayload := `{"key":"value_test_payload"}`
	req, err := http.NewRequest("POST", ts.URL, strings.NewReader(reqPayload))
	assert.NoError(err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := tr.RoundTrip(req)
	assert.NoError(err)
	assert.Equal(http.StatusOK, resp.StatusCode)
	assert.Equal(reqPayload, reqBodyRecv, "Request body should be preserved on retried RoundTrip attempt")
}
