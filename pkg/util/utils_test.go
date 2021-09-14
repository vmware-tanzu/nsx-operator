// Copyright (c) 2021 VMware, Inc. All rights reserved. VMware Confidential.

package util

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	policyclient "github.com/vmware/vsphere-automation-sdk-go/runtime/protocol/client"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

func TestHttpErrortoNSXError(t *testing.T) {
	assert := assert.New(t)
	testdatas := []ErrorDetail{
		{404, 202, []int{}, []string{}, ""},
		{404, 0, []int{}, []string{}, ""},
		{409, 202, []int{}, []string{}, ""},
		{500, 0, []int{99}, []string{}, ""},
		{505, 0, []int{}, []string{}, ""},
	}

	err := httpErrortoNSXError(&testdatas[0])
	e, ok := err.(*BackendResourceNotFound)
	assert.True(ok, fmt.Sprintf("Transform error : %v", e))

	err = httpErrortoNSXError(&testdatas[1])
	e1, ok := err.(*ResourceNotFound)
	assert.True(ok, fmt.Sprintf("Transform error : %v", e1))

	err = httpErrortoNSXError(&testdatas[2])
	e2, ok := err.(*StaleRevision)
	assert.True(ok, fmt.Sprintf("Transform error : %v", e2))

	err = httpErrortoNSXError(&testdatas[3])
	e3, ok := err.(*ClientCertificateNotTrusted)
	assert.True(ok, fmt.Sprintf("Transform error : %v", e3))

	err = httpErrortoNSXError(&testdatas[4])
	e4, ok := err.(*ManagerError)
	assert.True(ok, fmt.Sprintf("Transform error : %v", e4))

}

func TestExtractHTTPDetail(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "hello, world")
	}
	req := httptest.NewRequest("GET", "http://example.com/foo", nil)
	w := httptest.NewRecorder()
	handler(w, req)
	resp := w.Result()
	_, err := extractHTTPDetail("10.0.0.1", resp)
	if err != nil {
		if _, ok := err.(*ManagerError); !ok {
			t.Errorf("Extract wrong error type : %v", err)
		}
	}
	assert.NotNil(t, err, "Extract wrong error type")

}

func TestInitErrorFromResponse(t *testing.T) {
	assert := assert.New(t)
	result := `{
		"failover_mode" : "NON_PREEMPTIVE",
		"enable_standby_relocation" : false,
		"route_advertisement_types" : [ "TIER1_IPSEC_LOCAL_ENDPOINT" ],
		"force_whitelisting" : false,
		"default_rule_logging" : false,
		"disable_firewall" : false,
		"ipv6_profile_paths" : [ "/infra/ipv6-ndra-profiles/default", "/infra/ipv6-dad-profiles/default" ],
		"pool_allocation" : "ROUTING",
		"resource_type" : "Tier1",
		"id" : "test-tier1-id",
		"display_name" : "test-tier1-id",
		"path" : "/infra/tier-1s/test-tier1-id",
		"relative_path" : "test-tier1-id",
		"parent_path" : "/infra",
		"unique_id" : "2ceae758-0c7b-410c-b675-21de6d435dc0",
		"marked_for_delete" : false,
		"overridden" : false,
		"_create_user" : "admin",
		"_create_time" : 1614067766811,
		"_last_modified_user" : "admin",
		"_last_modified_time" : 1614067766823,
		"_system_owned" : false,
		"_protection" : "NOT_PROTECTED",
		"_revision" : 0
	  } `

	req := httptest.NewRequest("GET", "http://example.com/foo", nil)
	handler := func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, result)
	}
	w := httptest.NewRecorder()
	handler(w, req)
	resp := w.Result()
	err := InitErrorFromResponse("10.0.0.1", resp)
	body, err := ioutil.ReadAll(resp.Body)
	defer resp.Body.Close()

	assert.Equal(err, nil, "Read resp body error")
	assert.Equal(string(body), result, "Read resp body error")

}

func TestShouldGroundPoint(t *testing.T) {
	err := CreateServiceClusterUnavailable("127.0.0.1")
	assert.False(t, ShouldGroundPoint(err), "It's not a ground point error")

	err1 := CreateConnectionError("127.0.0.1")
	assert.True(t, ShouldGroundPoint(err1), "It's a ground point error")
}

func TestShouldRetry(t *testing.T) {
	assert := assert.New(t)
	err := CreateServiceClusterUnavailable("127.0.0.1")
	assert.False(ShouldRetry(err), "It's not a retry error")

	err1 := &APITransactionAborted{}
	assert.True(ShouldRetry(err1), "It's a retry error")
}

func TestShouldRegenerate(t *testing.T) {
	assert := assert.New(t)
	err := CreateServiceClusterUnavailable("127.0.0.1")
	assert.False(ShouldRegenerate(err), "It's not a regenerate error")

	err1 := &InvalidCredentials{}
	assert.True(ShouldRegenerate(err1), "It's a regenerate error")
}

func TestIsorChildError(t *testing.T) {
	assert := assert.New(t)
	err := CreateServiceClusterUnavailable("127.0.0.1")
	mr := &ManagerError{}
	assert.True(IsorChildError(err, mr), "ServiceClusterUnavailable is child type of ManagerError")

	nsxe := &NsxIndexingInProgress{}
	assert.False(IsorChildError(nsxe, mr), "NsxIndexingInProgress is not child type of ManagerError")

	timeout := &NsxSearchTimeout{}
	assert.False(IsorChildError(nsxe, timeout), "NsxIndexingInProgress is not child type of NsxSearchTimeout")

	assert.True(IsorChildError(timeout, &NsxLibException{}), "NsxSearchTimeout is child type of NsxLibException")

}

func TestHeaderConfig(t *testing.T) {
	assert := assert.New(t)
	partial := false
	result := `{
		"healthy" : true,
		"components_health" : "POLICY:UP, SEARCH:UP, MANAGER:UP, NODE_MGMT:UP, UI:UP"
	  }`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Index(r.URL.Path, "tier-1s") > 0 {
			if r.Header.Get("nsx-enable-partial-patch") == "true" {
				partial = true
			}
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(result))

	}))
	defer ts.Close()
	httpClient := http.Client{
		Transport: &http.Transport{},
	}
	connector := policyclient.NewRestConnector(fmt.Sprintf("%s", ts.URL), httpClient)
	header := CreateHeaderConfig(false, false, false)
	header.SetNSXEnablePartialPatch(true).Done(connector)
	client := infra.NewDefaultTier1sClient(connector)
	tier1 := model.Tier1{}
	client.Patch("test-tier1-id", tier1)
	client.Patch("test-tier1-id", tier1)
	assert.True(partial)
}
