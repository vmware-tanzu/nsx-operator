/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

func TestNewCluster(t *testing.T) {
	result := `{
		"healthy" : true,
		"components_health" : "POLICY:UP, SEARCH:UP, MANAGER:UP, NODE_MGMT:UP, UI:UP"
	  }`
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(result))
	}))
	defer ts.Close()
	index := strings.Index(ts.URL, "//")
	a := ts.URL[index+2:]
	thumbprint := []string{"123"}
	config := NewConfig(a, "admin", "passw0rd", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, thumbprint)
	_, err := NewCluster(config)
	assert.True(t, err == nil, fmt.Sprintf("Created cluster failed %v", err))
}

func TestCluster_getThumbprint(t *testing.T) {
	// one api server, one thumbprint
	thumbprint := []string{"123"}
	host := "127.0.0.1:443"

	cluster := &Cluster{}
	cluster.config = &Config{}
	cluster.config.Thumbprint = thumbprint
	tb := cluster.getThumbprint(host)
	assert.Equal(t, tb, "123")

	// two api server share one thumbprint
	ep1 := &Endpoint{}
	ep2 := &Endpoint{}
	cluster.endpoints = []*Endpoint{ep1, ep2}
	cluster.endpoints[0].provider = &address{host: "127.0.0.1:443"}
	cluster.endpoints[1].provider = &address{host: "127.0.0.2:443"}
	tb = cluster.getThumbprint(host)
	assert.Equal(t, tb, "123")

	// two api server, two thumbprint
	thumbprint = []string{"123", "234"}
	cluster.config.Thumbprint = thumbprint
	tb = cluster.getThumbprint("127.0.0.1:443")
	assert.Equal(t, tb, "123")
	tb = cluster.getThumbprint("127.0.0.2:443")
	assert.Equal(t, tb, "234")

	// two api server no port in addr, two thumbprint
	cluster.endpoints[0].provider = &address{host: "127.0.0.1"}
	cluster.endpoints[1].provider = &address{host: "127.0.0.2"}
	tb = cluster.getThumbprint("127.0.0.1")
	assert.Equal(t, tb, "123")
	tb = cluster.getThumbprint("127.0.0.2")
	assert.Equal(t, tb, "234")

	// test getCaFile without port in addr
	cluster.config.CAFile = []string{"/path/ca1.pem", "/path/ca2.pem"}
	assert.Equal(t, "/path/ca1.pem", cluster.getCaFile("127.0.0.1"))
	assert.Equal(t, "/path/ca2.pem", cluster.getCaFile("127.0.0.2"))
}

func TestCluster_NewRestConnector(t *testing.T) {
	result := `{
		"healthy" : true,
		"components_health" : "POLICY:UP, SEARCH:UP, MANAGER:UP, NODE_MGMT:UP, UI:UP"
	  }`
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(result))
	}))
	defer ts.Close()
	index := strings.Index(ts.URL, "//")
	a := ts.URL[index+2:]
	thumbprint := []string{"123"}
	config := NewConfig(a, "admin", "passw0rd", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, thumbprint)
	c, _ := NewCluster(config)
	con := c.NewRestConnector()
	assert.NotNil(t, con)
}

func TestCluster_createTransport(t *testing.T) {
	result := `{
		"healthy" : true,
		"components_health" : "POLICY:UP, SEARCH:UP, MANAGER:UP, NODE_MGMT:UP, UI:UP"
	  }`
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(result))
	}))
	defer ts.Close()
	index := strings.Index(ts.URL, "//")
	a := ts.URL[index+2:]
	thumbprint := []string{"123"}
	config := NewConfig(a, "admin", "passw0rd", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, thumbprint)
	c, _ := NewCluster(config)
	assert.NotNil(t, c.createTransport(10))
}

func TestCluster_Health(t *testing.T) {
	cluster := &Cluster{}
	addr := &address{host: "10.0.0.1", scheme: "https"}
	addr1 := &address{host: "10.0.0.2", scheme: "https"}
	addr2 := &address{host: "10.0.0.3", scheme: "https"}
	eps := []*Endpoint{{status: DOWN}, {status: DOWN}, {status: DOWN}}
	eps[0].provider = addr
	eps[1].provider = addr1
	eps[2].provider = addr2
	cluster.endpoints = eps
	for _, ep := range cluster.endpoints {
		ep.setStatus(UP)
	}
	health := cluster.Health()
	assert.Equal(t, health, GREEN)

	eps[0].setStatus(DOWN)
	health = cluster.Health()
	assert.Equal(t, health, ORANGE)

	for _, ep := range cluster.endpoints {
		ep.setStatus(DOWN)
	}
	health = cluster.Health()
	assert.Equal(t, health, RED)
}

func TestCluster_enableFeature(t *testing.T) {
	// Test case for enabling feature SecurityPolicy
	nsxVersion := &NsxVersion{}
	nsxVersion.ProductVersion = "3.1.3.3.0.18844962"
	log := logger.ZapCustomLogger(false, 0)
	assert.False(t, nsxVersion.featureSupported(SecurityPolicy, log))
	assert.False(t, nsxVersion.featureSupported(ServiceAccount, log))
	assert.False(t, nsxVersion.featureSupported(ServiceAccountRestore, log))
	assert.False(t, nsxVersion.featureSupported(ServiceAccountCertRotation, log))
	nsxVersion.ProductVersion = "3.2.0.3.0.18844962"
	assert.True(t, nsxVersion.featureSupported(SecurityPolicy, log))
	assert.False(t, nsxVersion.featureSupported(ServiceAccount, log))
	assert.False(t, nsxVersion.featureSupported(ServiceAccountRestore, log))
	assert.False(t, nsxVersion.featureSupported(ServiceAccountCertRotation, log))
	nsxVersion.ProductVersion = "3.11.0.3.0.18844962"
	assert.True(t, nsxVersion.featureSupported(SecurityPolicy, log))
	assert.False(t, nsxVersion.featureSupported(ServiceAccount, log))
	assert.False(t, nsxVersion.featureSupported(ServiceAccountRestore, log))
	assert.False(t, nsxVersion.featureSupported(ServiceAccountCertRotation, log))
	nsxVersion.ProductVersion = "4.0.0"
	assert.True(t, nsxVersion.featureSupported(SecurityPolicy, log))
	assert.False(t, nsxVersion.featureSupported(ServiceAccount, log))
	assert.False(t, nsxVersion.featureSupported(ServiceAccountRestore, log))
	assert.False(t, nsxVersion.featureSupported(ServiceAccountCertRotation, log))
	nsxVersion.ProductVersion = "4.0.1"
	assert.True(t, nsxVersion.featureSupported(SecurityPolicy, log))
	assert.True(t, nsxVersion.featureSupported(ServiceAccount, log))
	assert.False(t, nsxVersion.featureSupported(ServiceAccountRestore, log))
	assert.False(t, nsxVersion.featureSupported(ServiceAccountCertRotation, log))
	nsxVersion.ProductVersion = "4.1.0"
	assert.True(t, nsxVersion.featureSupported(SecurityPolicy, log))
	assert.True(t, nsxVersion.featureSupported(ServiceAccount, log))
	assert.False(t, nsxVersion.featureSupported(ServiceAccountRestore, log))
	assert.False(t, nsxVersion.featureSupported(ServiceAccountCertRotation, log))
	nsxVersion.ProductVersion = "4.1.2"
	assert.True(t, nsxVersion.featureSupported(SecurityPolicy, log))
	assert.True(t, nsxVersion.featureSupported(ServiceAccount, log))
	assert.True(t, nsxVersion.featureSupported(ServiceAccountRestore, log))
	assert.False(t, nsxVersion.featureSupported(ServiceAccountCertRotation, log))
	nsxVersion.ProductVersion = "4.1.3"
	assert.True(t, nsxVersion.featureSupported(SecurityPolicy, log))
	assert.True(t, nsxVersion.featureSupported(ServiceAccount, log))
	assert.True(t, nsxVersion.featureSupported(ServiceAccountRestore, log))
	assert.True(t, nsxVersion.featureSupported(ServiceAccountCertRotation, log))
	nsxVersion.ProductVersion = "3.2.0"
	assert.True(t, nsxVersion.featureSupported(SecurityPolicy, log))
	assert.False(t, nsxVersion.featureSupported(ServiceAccount, log))
	assert.False(t, nsxVersion.featureSupported(ServiceAccountRestore, log))
	assert.False(t, nsxVersion.featureSupported(ServiceAccountCertRotation, log))
	nsxVersion.ProductVersion = "4.2.0"
	assert.True(t, nsxVersion.featureSupported(SecurityPolicy, log))
	assert.True(t, nsxVersion.featureSupported(ServiceAccount, log))
	assert.True(t, nsxVersion.featureSupported(ServiceAccountRestore, log))
	assert.True(t, nsxVersion.featureSupported(ServiceAccountCertRotation, log))
	assert.False(t, nsxVersion.featureSupported(IPv6, log))
	assert.False(t, nsxVersion.featureSupported(SubnetAssociation, log))

	nsxVersion.ProductVersion = "9.2.0"
	assert.True(t, nsxVersion.featureSupported(IPv6, log))
	assert.True(t, nsxVersion.featureSupported(SubnetAssociation, log))

	// Test case for invalid feature
	feature := 3
	nsxVersion.ProductVersion = "3.1.3.3.0.18844962"
	assert.False(t, nsxVersion.featureSupported(feature, log))
	nsxVersion.ProductVersion = "3.2.0"
	assert.False(t, nsxVersion.featureSupported(feature, log))
}

func TestCluster_validate(t *testing.T) {
	log := logger.ZapCustomLogger(false, 0)
	nsxVersion := &NsxVersion{}
	nsxVersion.ProductVersion = "12"
	expect := errors.New("error version format")
	err := nsxVersion.Validate(log)
	assert.Equal(t, err, expect)

	nsxVersion.ProductVersion = "12.3"
	err = nsxVersion.Validate(log)
	assert.Equal(t, err, expect)

	nsxVersion.ProductVersion = "3.2.3.3.0.18844962"
	err = nsxVersion.Validate(log)
	assert.Equal(t, err, nil)

	nsxVersion.ProductVersion = "3.2.3"
	err = nsxVersion.Validate(log)
	assert.Equal(t, err, nil)
}

func TestCluster_getVersion(t *testing.T) {
	resVersion := `{
                    "node_version": "3.1.3.3.0.18844962",
                    "product_version": "3.1.3.3.0.18844959"
                    }`
	resHealth := `{
					"healthy" : true,
					"components_health" : "MANAGER:UP, SEARCH:UP, UI:UP, NODE_MGMT:UP"
					}`
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if strings.Contains(r.URL.Path, "reverse-proxy/node/health") {
			w.Write([]byte(resHealth))
		} else {
			w.Write([]byte(resVersion))
		}
	}))
	defer ts.Close()
	thumbprint := []string{"123"}
	index := strings.Index(ts.URL, "//")
	a := ts.URL[index+2:]
	config := NewConfig(a, "admin", "passw0rd", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, thumbprint)
	cluster, _ := NewCluster(config)
	nsxVersion, err := cluster.GetVersion()
	assert.Equal(t, err, nil)
	assert.Equal(t, nsxVersion.ProductVersion, "3.1.3.3.0.18844959")
}

func TestCluster_GetVersion_invokesOnProductVersionChangedWhenProductVersionChanges(t *testing.T) {
	var (
		callbackCalls int
		gotOld        string
		gotNew        string
	)
	versionCalls := 0
	resHealth := `{
					"healthy" : true,
					"components_health" : "MANAGER:UP, SEARCH:UP, UI:UP, NODE_MGMT:UP"
					}`
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if strings.Contains(r.URL.Path, "reverse-proxy/node/health") {
			w.Write([]byte(resHealth))
			return
		}
		if strings.Contains(r.URL.Path, "/api/v1/node/version") {
			versionCalls++
			var body string
			if versionCalls == 1 {
				body = `{"node_version":"9.1.0.0.0.10000000","product_version":"9.1.0.0200.25524170"}`
			} else {
				body = `{"node_version":"9.2.0.0.0.20000000","product_version":"9.2.0.0.25588556"}`
			}
			_, _ = w.Write([]byte(body))
			return
		}
		_, _ = w.Write([]byte("{}"))
	}))
	defer ts.Close()
	thumbprint := []string{"123"}
	index := strings.Index(ts.URL, "//")
	a := ts.URL[index+2:]
	config := NewConfig(a, "admin", "passw0rd", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, thumbprint)
	cluster, err := NewCluster(config)
	assert.NoError(t, err)

	cluster.SetOnProductVersionChanged(func(oldV, newV string) {
		callbackCalls++
		gotOld, gotNew = oldV, newV
	})

	ver1, err := cluster.GetVersion()
	assert.NoError(t, err)
	assert.Equal(t, "9.1.0.0200.25524170", ver1.ProductVersion)
	assert.Equal(t, 0, callbackCalls, "first successful fetch has no prior product_version, callback must not run")

	cluster.lastTimeGetVersion = time.Now().Add(-40 * time.Minute)
	ver2, err := cluster.GetVersion()
	assert.NoError(t, err)
	assert.Equal(t, "9.2.0.0.25588556", ver2.ProductVersion)
	assert.Equal(t, 1, callbackCalls)
	assert.Equal(t, "9.1.0.0200.25524170", gotOld)
	assert.Equal(t, "9.2.0.0.25588556", gotNew)
}

func TestCluster_GetVersion_noOnProductVersionChangedWhenVersionUnchanged(t *testing.T) {
	var callbackCalls int
	versionCalls := 0
	resHealth := `{
					"healthy" : true,
					"components_health" : "MANAGER:UP, SEARCH:UP, UI:UP, NODE_MGMT:UP"
					}`
	const sameVersion = `{"node_version":"9.1.0.0.0.10000000","product_version":"9.1.0"}`
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if strings.Contains(r.URL.Path, "reverse-proxy/node/health") {
			w.Write([]byte(resHealth))
			return
		}
		if strings.Contains(r.URL.Path, "/api/v1/node/version") {
			versionCalls++
			_, _ = w.Write([]byte(sameVersion))
			return
		}
		_, _ = w.Write([]byte("{}"))
	}))
	defer ts.Close()
	thumbprint := []string{"123"}
	index := strings.Index(ts.URL, "//")
	a := ts.URL[index+2:]
	config := NewConfig(a, "admin", "passw0rd", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, thumbprint)
	cluster, err := NewCluster(config)
	assert.NoError(t, err)

	cluster.SetOnProductVersionChanged(func(_, _ string) {
		callbackCalls++
	})

	_, err = cluster.GetVersion()
	assert.NoError(t, err)
	cluster.lastTimeGetVersion = time.Now().Add(-40 * time.Minute)
	_, err = cluster.GetVersion()
	assert.NoError(t, err)
	assert.Equal(t, 0, callbackCalls)
}

func TestCluster_GetVersion_noPanicWhenOnProductVersionChangedNil(t *testing.T) {
	versionCalls := 0
	resHealth := `{
					"healthy" : true,
					"components_health" : "MANAGER:UP, SEARCH:UP, UI:UP, NODE_MGMT:UP"
					}`
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if strings.Contains(r.URL.Path, "reverse-proxy/node/health") {
			w.Write([]byte(resHealth))
			return
		}
		if strings.Contains(r.URL.Path, "/api/v1/node/version") {
			versionCalls++
			if versionCalls == 1 {
				_, _ = w.Write([]byte(`{"node_version":"4.1.0.0.0.1","product_version":"4.1.0"}`))
			} else {
				_, _ = w.Write([]byte(`{"node_version":"4.1.1.0.0.1","product_version":"4.1.1"}`))
			}
			return
		}
		_, _ = w.Write([]byte("{}"))
	}))
	defer ts.Close()
	thumbprint := []string{"123"}
	index := strings.Index(ts.URL, "//")
	a := ts.URL[index+2:]
	config := NewConfig(a, "admin", "passw0rd", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, thumbprint)
	cluster, err := NewCluster(config)
	assert.NoError(t, err)

	cluster.SetOnProductVersionChanged(nil)
	_, err = cluster.GetVersion()
	assert.NoError(t, err)
	cluster.lastTimeGetVersion = time.Now().Add(-40 * time.Minute)
	_, err = cluster.GetVersion()
	assert.NoError(t, err)
}

func TestCluster_CreateServerUrl(t *testing.T) {
	type fields struct {
		config           *Config
		endpoints        []*Endpoint
		transport        *Transport
		client           *http.Client
		noBalancerClient *http.Client
	}
	type args struct {
		host   string
		scheme string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   string
	}{
		{
			name: "envoy, with thumbprint",
			fields: fields{
				config: &Config{
					Thumbprint: []string{"123"},
					EnvoyPort:  1080,
					EnvoyHost:  "localhost",
				},
			},
			want: "http://localhost:1080/external-tp/http1/domain-c9/443/123",
			args: args{
				host:   "domain-c9",
				scheme: "https",
			},
		},
		{
			name: "envoy, with ca",
			fields: fields{
				config: &Config{
					CAFile:     []string{"./ca.cert"},
					EnvoyPort:  1080,
					EnvoyHost:  "localhost",
					Thumbprint: []string{},
				},
			},
			want: "http://localhost:1080/external-cert/http1/domain-c9/443",
			args: args{
				host:   "domain-c9",
				scheme: "https",
			},
		},
		{
			name: "envoy, no ca, thumbprint",
			fields: fields{
				config: &Config{
					EnvoyPort: 1080,
					EnvoyHost: "localhost",
				},
			},
			want: "",
			args: args{
				host:   "domain-c9",
				scheme: "https",
			},
		},
		{
			name: "not envoy",
			fields: fields{
				config: &Config{
					EnvoyPort: 0,
				},
			},
			want: "https://domain-c9",
			args: args{
				host:   "domain-c9",
				scheme: "https",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &Cluster{
				config:           tt.fields.config,
				endpoints:        tt.fields.endpoints,
				transport:        tt.fields.transport,
				client:           tt.fields.client,
				noBalancerClient: tt.fields.noBalancerClient,
				Mutex:            sync.Mutex{},
			}
			if got := cluster.CreateServerUrl(tt.args.host, tt.args.scheme); got != tt.want {
				t.Errorf("Cluster.CreateServerUrl() = %v, want %v", got, tt.want)
			}
		})
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestFetchLicense(t *testing.T) {
	address := address{
		host:   "1.2.3.4",
		scheme: "https",
	}
	ep := &Endpoint{
		provider: &address,
	}
	cluster := &Cluster{
		endpoints: []*Endpoint{ep},
		config:    &Config{EnvoyPort: 0},
	}

	// HTTP error
	ep.client = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return nil, errors.New("http error")
		}),
	}

	err := cluster.FetchLicense()
	assert.Error(t, err)

	// normal case
	ep.client = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			res := &nsxutil.NsxLicense{
				Results: []struct {
					FeatureName string `json:"feature_name"`
					IsLicensed  bool   `json:"is_licensed"`
				}{
					{
						FeatureName: "CONTAINER",
						IsLicensed:  true,
					},
					{
						FeatureName: "DFW",
						IsLicensed:  true,
					},
				},
				ResultCount: 2,
			}

			jsonBytes, _ := json.Marshal(res)

			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewReader(jsonBytes)),
				Header: http.Header{
					"Content-Type": []string{"application/json"},
				},
				Request: req,
			}, nil
		}),
	}
	err = cluster.FetchLicense()
	assert.Nil(t, err)
}

func TestHttpDelete(t *testing.T) {
	address := address{
		host:   "1.2.3.4",
		scheme: "https",
	}
	ep := &Endpoint{
		provider: &address,
	}
	cluster := &Cluster{
		endpoints: []*Endpoint{ep},
		config:    &Config{EnvoyPort: 0},
	}

	// Success case (200 OK)
	ep.client = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte("{}"))),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Request:    req,
			}, nil
		}),
	}
	err := cluster.HttpDelete("api/v1/resource")
	assert.Nil(t, err)

	// Error case (400 Bad Request)
	ep.client = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"error_code": 500030, "error_message": "Resource in use"}`))),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Request:    req,
			}, nil
		}),
	}
	err = cluster.HttpDelete("api/v1/resource")
	assert.Error(t, err)
}

func TestCluster_StopKeepAlive(t *testing.T) {
	cluster := &Cluster{
		endpoints: []*Endpoint{
			{
				stop: make(chan bool),
			},
			{
				stop: make(chan bool),
			},
		},
	}

	// Simulate KeepAlive goroutines running
	for _, ep := range cluster.endpoints {
		ep.wg.Add(1)
		go func(e *Endpoint) {
			defer e.wg.Done()
			<-e.stop
		}(ep)
	}

	// Call StopKeepAlive, it should close channels and wait for goroutines to finish
	cluster.StopKeepAlive()

	// Verify channels are closed by trying to read from them
	for _, ep := range cluster.endpoints {
		select {
		case _, ok := <-ep.stop:
			assert.False(t, ok, "channel should be closed")
		default:
			t.Errorf("channel was not closed")
		}
	}

	// Calling it again should not panic (testing the select default branch)
	assert.NotPanics(t, func() {
		cluster.StopKeepAlive()
	})
}
