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
		if strings.Contains(r.URL.Path, "api/session/create") {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"healthy":true}`))
			return
		}
		attempt++
		body, _ := io.ReadAll(r.Body)
		reqBodyRecv = string(body)
		if attempt == 1 {
			// First attempt fails with retriable InvalidCredentials
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"module_name":"common-services","error_code":98,"error_message":"InvalidCredentials"}`))
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

func TestRoundTripRetryOnGroundPoint(t *testing.T) {
	assert := assert.New(t)
	attempt := 0
	reqBodyRecv := ""

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		body, _ := io.ReadAll(r.Body)
		reqBodyRecv = string(body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	config := NewConfig("127.0.0.1", "admin", "password", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	ep1, err := NewEndpoint(ts.URL[len("http://"):], http.DefaultClient, http.DefaultClient, ratelimiter.NewFixRateLimiter(10), nil, config.Logger)
	assert.NoError(err)
	ep1.setStatus(UP)

	ep2, err := NewEndpoint(ts.URL[len("http://"):], http.DefaultClient, http.DefaultClient, ratelimiter.NewFixRateLimiter(10), nil, config.Logger)
	assert.NoError(err)
	ep2.setStatus(UP)

	// Mock a base round tripper that returns a connection error on the first attempt
	mockBase := &mockRoundTripper{
		roundTripFunc: func(req *http.Request) (*http.Response, error) {
			if attempt == 0 {
				attempt++
				// Simulate consuming body on first failed attempt
				if req.Body != nil {
					_, _ = io.ReadAll(req.Body)
					req.Body.Close()
				}
				return nil, errors.New("connection refused")
			}
			return http.DefaultTransport.RoundTrip(req)
		},
	}

	tr := &Transport{
		endpoints: []*Endpoint{ep1, ep2},
		config:    config,
		Base:      mockBase,
	}

	reqPayload := `{"key":"failover_body_test"}`
	req, err := http.NewRequest("POST", ts.URL, strings.NewReader(reqPayload))
	assert.NoError(err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := tr.RoundTrip(req)
	assert.NoError(err)
	assert.Equal(http.StatusOK, resp.StatusCode)
	assert.Equal(2, attempt, "Should have retried once after connection refused")
	assert.Equal(reqPayload, reqBodyRecv, "Request body should be preserved across endpoint failover")
}

func TestRoundTripNonRetriableErrors(t *testing.T) {
	assert := assert.New(t)
	attempt := 0

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"module_name":"common-services","error_code":202,"error_message":"ObjectNotFound"}`))
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

	req, err := http.NewRequest("GET", ts.URL, nil)
	assert.NoError(err)

	resp, err := tr.RoundTrip(req)
	// Must return nil error according to http.RoundTripper contract, with non-nil resp
	assert.NoError(err)
	assert.NotNil(resp)
	assert.Equal(http.StatusNotFound, resp.StatusCode)
	assert.Equal(1, attempt, "Non-retriable 404 error should not be retried in transport layer")

	// Ensure body is readable by caller (e.g. for VAPI error decoding)
	body, err := io.ReadAll(resp.Body)
	assert.NoError(err)
	assert.Contains(string(body), "ObjectNotFound")
}

type mockRoundTripper struct {
	roundTripFunc func(req *http.Request) (*http.Response, error)
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.roundTripFunc(req)
}

func TestBackoffWithJitter(t *testing.T) {
	start := time.Now()
	backoffWithJitter(0) // base delay: 100ms
	elapsed := time.Since(start)
	assert.True(t, elapsed >= 100*time.Millisecond, "elapsed time should be at least 100ms")
	assert.True(t, elapsed < 250*time.Millisecond, "elapsed time should be less than 250ms (100ms + 50ms jitter + buffer)")

	start = time.Now()
	backoffWithJitter(1) // base delay: 200ms
	elapsed = time.Since(start)
	assert.True(t, elapsed >= 200*time.Millisecond, "elapsed time should be at least 200ms")
	assert.True(t, elapsed < 350*time.Millisecond, "elapsed time should be less than 350ms (200ms + 50ms jitter + buffer)")
}

func TestRoundTripEmptyBodyOnRetry(t *testing.T) {
	assert := assert.New(t)
	attempt := 0
	serverReceivedBodyLength := -1

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "api/session/create") {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"healthy":true}`))
			return
		}
		attempt++
		body, _ := io.ReadAll(r.Body)
		serverReceivedBodyLength = len(body)
		if attempt == 1 {
			// First attempt fails with retriable InvalidCredentials
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"module_name":"common-services","error_code":98,"error_message":"InvalidCredentials"}`))
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

	// POST request with an empty body (non-nil Body with 0 bytes)
	req, err := http.NewRequest("POST", ts.URL, strings.NewReader(""))
	assert.NoError(err)
	assert.NotNil(req.Body)

	resp, err := tr.RoundTrip(req)
	assert.NoError(err)
	assert.Equal(http.StatusOK, resp.StatusCode)
	assert.Equal(2, attempt, "Should retry after 403 InvalidCredentials")
	assert.Equal(0, serverReceivedBodyLength, "Server should receive 0-byte body on retried request")
	assert.NotNil(req.Body, "req.Body should remain non-nil after retry")
}

func TestRoundTripNilBodyOnRetry(t *testing.T) {
	assert := assert.New(t)
	attempt := 0

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "api/session/create") {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"healthy":true}`))
			return
		}
		attempt++
		if attempt == 1 {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"module_name":"common-services","error_code":98,"error_message":"InvalidCredentials"}`))
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

	// GET request with nil Body
	req, err := http.NewRequest("GET", ts.URL, nil)
	assert.NoError(err)
	assert.Nil(req.Body)

	resp, err := tr.RoundTrip(req)
	assert.NoError(err)
	assert.Equal(http.StatusOK, resp.StatusCode)
	assert.Equal(2, attempt, "Should retry after 403 InvalidCredentials")
	assert.Nil(req.Body, "req.Body should stay nil throughout retries")
}

func TestBackupRequestBody(t *testing.T) {
	assert := assert.New(t)

	// Case 1: nil request
	bytes1, hasBody1 := backupRequestBody(nil)
	assert.False(hasBody1)
	assert.Nil(bytes1)

	// Case 2: nil body
	req2, _ := http.NewRequest("GET", "http://localhost", nil)
	bytes2, hasBody2 := backupRequestBody(req2)
	assert.False(hasBody2)
	assert.Nil(bytes2)
	assert.Nil(req2.Body)

	// Case 3: empty body (0 bytes)
	req3, _ := http.NewRequest("POST", "http://localhost", strings.NewReader(""))
	bytes3, hasBody3 := backupRequestBody(req3)
	assert.True(hasBody3)
	assert.Equal(0, len(bytes3))
	assert.NotNil(req3.Body)
	read3, _ := io.ReadAll(req3.Body)
	assert.Equal(0, len(read3))

	// Case 4: non-empty body
	payload := "hello world payload"
	req4, _ := http.NewRequest("POST", "http://localhost", strings.NewReader(payload))
	bytes4, hasBody4 := backupRequestBody(req4)
	assert.True(hasBody4)
	assert.Equal([]byte(payload), bytes4)
	assert.NotNil(req4.Body)
	read4, _ := io.ReadAll(req4.Body)
	assert.Equal(payload, string(read4))
}
