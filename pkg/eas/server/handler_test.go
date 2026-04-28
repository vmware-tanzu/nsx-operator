/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type fakeVPCInfoProvider struct {
	namespaces []string
}

func (f fakeVPCInfoProvider) ListVPCInfo(string) []common.VPCResourceInfo {
	return nil
}

func (f fakeVPCInfoProvider) ListAllVPCNamespaces() []string {
	return f.namespaces
}

type testResourceItem struct {
	Name string
}

type testResourceList struct {
	Items []testResourceItem
}

func TestHandleAPIResourceListRejectsUnsupportedMethod(t *testing.T) {
	s := &EASServer{
		handlers: map[string]resourceHandler{
			"subnetippools": {kind: "SubnetIPPools"},
		},
	}
	req := httptest.NewRequest(http.MethodPost, APIBasePath, nil)
	recorder := httptest.NewRecorder()

	s.handleAPIResourceList(recorder, req)

	require.Equal(t, http.StatusMethodNotAllowed, recorder.Code)
	assert.Equal(t, readMethodsAllowHeader, recorder.Header().Get("Allow"))

	var status metav1.Status
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &status))
	assert.Equal(t, metav1.StatusReasonMethodNotAllowed, status.Reason)
}

func TestHandleNamespacedResourceRejectsUnsupportedMethod(t *testing.T) {
	var called bool
	s := &EASServer{
		handlers: map[string]resourceHandler{
			"subnetippools": {
				namespaced: func(w http.ResponseWriter, r *http.Request, ns, name string) {
					called = true
				},
			},
		},
	}
	req := httptest.NewRequest(http.MethodPost, APIBasePath+"/namespaces/ns-1/subnetippools", nil)
	recorder := httptest.NewRecorder()

	s.handleNamespacedResource(recorder, req)

	require.Equal(t, http.StatusMethodNotAllowed, recorder.Code)
	assert.Equal(t, readMethodsAllowHeader, recorder.Header().Get("Allow"))
	assert.False(t, called)
}

func TestHandleNamespacedResourceRejectsExtraPathSegments(t *testing.T) {
	var called bool
	s := &EASServer{
		handlers: map[string]resourceHandler{
			"subnetippools": {
				namespaced: func(w http.ResponseWriter, r *http.Request, ns, name string) {
					called = true
				},
			},
		},
	}
	req := httptest.NewRequest(http.MethodGet, APIBasePath+"/namespaces/ns-1/subnetippools/pool-1/extra", nil)
	recorder := httptest.NewRecorder()

	s.handleNamespacedResource(recorder, req)

	require.Equal(t, http.StatusNotFound, recorder.Code)
	assert.False(t, called)

	var status metav1.Status
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &status))
	assert.Equal(t, metav1.StatusReasonNotFound, status.Reason)
	assert.Equal(t, "invalid resource path", status.Message)
}

func TestClusterWideHandlerRejectsUnsupportedMethod(t *testing.T) {
	s := &EASServer{
		handlers:    map[string]resourceHandler{},
		vpcProvider: fakeVPCInfoProvider{namespaces: []string{"ns-1"}},
	}
	registerResource(s, "widgets", resourceOps[testResourceItem, testResourceList]{
		kind: "Widget",
		list: func(context.Context, string) (*testResourceList, error) {
			t.Fatal("list should not be called for unsupported methods")
			return nil, nil
		},
		items:   func(l *testResourceList) *[]testResourceItem { return &l.Items },
		newList: func() *testResourceList { return &testResourceList{} },
		toRow:   func(item *testResourceItem) metav1.TableRow { return tableRow(item.Name, "ns-1") },
		getName: func(item *testResourceItem) string { return item.Name },
	})

	req := httptest.NewRequest(http.MethodDelete, APIBasePath+"/widgets", nil)
	recorder := httptest.NewRecorder()

	s.handlers["widgets"].clusterWide(recorder, req)

	require.Equal(t, http.StatusMethodNotAllowed, recorder.Code)
	assert.Equal(t, readMethodsAllowHeader, recorder.Header().Get("Allow"))
}
