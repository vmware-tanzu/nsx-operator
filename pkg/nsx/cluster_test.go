/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey"
	"github.com/stretchr/testify/assert"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
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
	config := NewConfig(a, "admin", "passw0rd", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, thumbprint)
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
	patch := gomonkey.ApplyMethod(reflect.TypeOf(ep1), "KeepAlive", func(_ *Endpoint) {
	})
	defer patch.Reset()
	cluster.endpoints = []*Endpoint{ep1, ep2}
	cluster.endpoints[0].provider = &address{host: "127.0.0.1:443"}
	cluster.endpoints[1].provider = &address{host: "127.0.0.2:443"}
	tb = cluster.getThumbprint(host)
	assert.Equal(t, tb, "123")

	//two api server, two thumbprint
	thumbprint = []string{"123", "234"}
	cluster.config.Thumbprint = thumbprint
	tb = cluster.getThumbprint("127.0.0.1:443")
	assert.Equal(t, tb, "123")
	tb = cluster.getThumbprint("127.0.0.2:443")
	assert.Equal(t, tb, "234")

	//two api server no port, two thumbprint
	cluster.endpoints[0].provider = &address{host: "127.0.0.1"}
	cluster.endpoints[1].provider = &address{host: "127.0.0.2"}
	tb = cluster.getThumbprint("127.0.0.1:443")
	assert.Equal(t, tb, "123")
	tb = cluster.getThumbprint("127.0.0.2:443")
	assert.Equal(t, tb, "234")
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
	config := NewConfig(a, "admin", "passw0rd", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, thumbprint)
	c, _ := NewCluster(config)
	con, _ := c.NewRestConnector()
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
	config := NewConfig(a, "admin", "passw0rd", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, thumbprint)
	c, _ := NewCluster(config)
	assert.NotNil(t, c.createTransport(nil, 10))
}

func Test_calcFingerprint(t *testing.T) {
	type args struct {
		der []byte
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{"1", args{der: []byte("It is byte.")}, "5C:1D:AE:31:3A:EA:74:74:FE:69:BA:9F:0B:1D:86:5E:39:97:43:4F"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := calcFingerprint(tt.args.der); got != tt.want {
				t.Errorf("calcFingerprint() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCluster_createSecurity(t *testing.T) {
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
	config := NewConfig(a, "admin", "passw0rd", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, thumbprint)
	c, _ := NewCluster(config)
	assert.NotNil(t, c.createSecurity("username", "password"))
}

func TestCluster_Health(t *testing.T) {
	a := "127.0.0.1, 127.0.0.2, 127.0.0.3"
	config := NewConfig(a, "admin", "passw0rd", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster := &Cluster{}
	tr := cluster.createTransport(config.TokenProvider, idleConnTimeout)
	client := cluster.createHTTPClient(tr, timeout)
	noBClient := cluster.createNoBalancerClient(timeout, idleConnTimeout)
	r := ratelimiter.NewRateLimiter(config.APIRateMode)
	eps, _ := cluster.createEndpoints(config.APIManagers, &client, &noBClient, r, nil)
	cluster.endpoints = eps
	for _, ep := range cluster.endpoints {
		ep.status = UP
	}
	assert.NotNil(t, cluster.Health())
}
