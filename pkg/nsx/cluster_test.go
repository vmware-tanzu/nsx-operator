/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"errors"
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

	// two api server, two thumbprint
	thumbprint = []string{"123", "234"}
	cluster.config.Thumbprint = thumbprint
	tb = cluster.getThumbprint("127.0.0.1:443")
	assert.Equal(t, tb, "123")
	tb = cluster.getThumbprint("127.0.0.2:443")
	assert.Equal(t, tb, "234")

	// two api server no port, two thumbprint
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
	assert.NotNil(t, c.createTransport(10))
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
	miniVersion := [3]int64{3, 2, 0}
	nsxVersion := &NsxVersion{}
	nsxVersion.NodeVersion = "3.1.3.3.0.18844962"
	assert.False(t, nsxVersion.featureSupported(miniVersion))
	nsxVersion.NodeVersion = "3.2.0.3.0.18844962"
	assert.True(t, nsxVersion.featureSupported(miniVersion))
	nsxVersion.NodeVersion = "3.11.0.3.0.18844962"
	assert.True(t, nsxVersion.featureSupported(miniVersion))
	nsxVersion.NodeVersion = "4.1.0"
	assert.True(t, nsxVersion.featureSupported(miniVersion))
	nsxVersion.NodeVersion = "3.2.0"
	assert.True(t, nsxVersion.featureSupported(miniVersion))
}

func TestCluster_validate(t *testing.T) {
	miniVersion := [3]int64{3, 2, 0}
	nsxVersion := &NsxVersion{}
	nsxVersion.NodeVersion = "12"
	expect := errors.New("error version format")
	err := nsxVersion.Validate(miniVersion)
	assert.Equal(t, err, expect)

	nsxVersion.NodeVersion = "12.3"
	err = nsxVersion.Validate(miniVersion)
	assert.Equal(t, err, expect)

	nsxVersion.NodeVersion = "3.2.3.3.0.18844962"
	err = nsxVersion.Validate(miniVersion)
	assert.Equal(t, err, nil)

	nsxVersion.NodeVersion = "3.2.3"
	err = nsxVersion.Validate(miniVersion)
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
			w.Write(([]byte(resHealth)))
		} else {
			w.Write([]byte(resVersion))
		}
	}))
	defer ts.Close()
	thumbprint := []string{"123"}
	index := strings.Index(ts.URL, "//")
	a := ts.URL[index+2:]
	config := NewConfig(a, "admin", "passw0rd", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, thumbprint)
	cluster, _ := NewCluster(config)
	nsxVersion, err := cluster.GetVersion()
	assert.True(t, err == nil)
	assert.Equal(t, nsxVersion.NodeVersion, "3.1.3.3.0.18844962")
}
