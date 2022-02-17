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
