// Copyright (c) 2021 VMware, Inc. All rights reserved. VMware Confidential.

package nsx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"gitlab.eng.vmware.com/nsx-container-plugin/vmware-nsxlib-go/pkg/nsx/ratelimiter"
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
	config := NewConfig(a, "admin", "passw0rd", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil)
	cluster, _ := NewCluster(config)
	cluster1, _ := NewCluster(config)
	assert.Equal(t, cluster, cluster1, "Created cluster twice")
}
