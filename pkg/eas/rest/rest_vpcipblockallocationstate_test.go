/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package rest

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	vapierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	vpcipblocks "github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/vpcs/ip_blocks"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/endpoints/request"
	apirest "k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/utils/ptr"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas/storage"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type restAllocationStateProvider struct {
	entries             map[string][]eas.VPCEntry
	requestedNamespaces []string
}

func (p *restAllocationStateProvider) ListVPCInfo(namespace string) []eas.VPCEntry {
	p.requestedNamespaces = append(p.requestedNamespaces, namespace)
	return p.entries[namespace]
}

func (p *restAllocationStateProvider) ListAllVPCNamespaces() []string { return nil }

type restAllocationStateCall struct {
	orgID, projectID, vpcID string
	cidr, blockPath         *string
	workloadDetails         *bool
}

type restAllocationStateClient struct {
	vpcipblocks.AllocationStateClient
	results map[string][]model.IpAddressBlockAllocationState
	errs    map[string]error
	calls   []restAllocationStateCall
}

func (c *restAllocationStateClient) List(orgID, projectID, vpcID string, cidr *string, workloadDetails *bool, blockPath *string) (model.IpAddressBlockAllocationStateList, error) {
	c.calls = append(c.calls, restAllocationStateCall{orgID: orgID, projectID: projectID, vpcID: vpcID, cidr: cidr, blockPath: blockPath, workloadDetails: workloadDetails})
	return model.IpAddressBlockAllocationStateList{Results: c.results[vpcID]}, c.errs[vpcID]
}

func newVPCIPBlockAllocationStateREST() (*vpcIPBlockAllocationStateStorage, *restAllocationStateProvider, *restAllocationStateClient) {
	provider := &restAllocationStateProvider{entries: map[string][]eas.VPCEntry{
		"ns1": {{Info: common.VPCResourceInfo{OrgID: "org", ProjectID: "p1", VPCID: "vpc1"}}},
		"ns2": {{Info: common.VPCResourceInfo{OrgID: "org", ProjectID: "p2", VPCID: "vpc2"}}},
	}}
	client := &restAllocationStateClient{
		results: map[string][]model.IpAddressBlockAllocationState{
			"vpc1": {{
				IpBlockPath: ptr.To("/orgs/org/projects/p1/infra/ip-blocks/block-1"), DisplayName: ptr.To("display-name-1"),
				Visibility: ptr.To("PRIVATE"), IpAddressType: ptr.To("IPV4"),
				AllocationStates: []model.IpAddressBlockAllocationStateForCidr{{Cidr: ptr.To("10.0.1.0/28")}},
			}},
			"vpc2": {{
				IpBlockPath: ptr.To("/orgs/org/projects/p2/infra/ip-blocks/block-1"), DisplayName: ptr.To("display-name-2"),
				Visibility: ptr.To("EXTERNAL"), IpAddressType: ptr.To("IPV6"),
				AllocationStates: []model.IpAddressBlockAllocationStateForCidr{{Cidr: ptr.To("2001:db8::/64")}},
			}},
		},
		errs: make(map[string]error),
	}
	store := storage.NewVPCIPBlockAllocationStateStorage(&nsx.Client{VPCIPBlockAllocationStateClient: client}, provider, newTestFakeK8sClient().Build())
	return NewVPCIPBlockAllocationStateStorage(store), provider, client
}

func TestVPCIPBlockAllocationStateStorage_Get_InfraPrefix(t *testing.T) {
	r, _, client := newVPCIPBlockAllocationStateREST()
	client.results["vpc1"] = append(client.results["vpc1"], model.IpAddressBlockAllocationState{
		IpBlockPath: ptr.To("/infra/ip-blocks/block-1"),
		Visibility:  ptr.To("EXTERNAL"),
		AllocationStates: []model.IpAddressBlockAllocationStateForCidr{
			{Cidr: ptr.To("192.0.2.0/28")},
		},
	})
	object, err := r.Get(request.WithNamespace(context.Background(), "ns1"), ":block-1", &metav1.GetOptions{})
	require.NoError(t, err)
	state := object.(*easv1alpha1.VPCIPBlockAllocationState)
	assert.Equal(t, ":block-1", state.Name)
	assert.Equal(t, easv1alpha1.External, state.Visibility)
	assert.Equal(t, []easv1alpha1.VPCIPAddressBlockAllocationStateForCIDR{{CIDR: "192.0.2.0/28"}}, state.AllocationStates)
}

func TestVPCIPBlockAllocationStateStorage_Metadata(t *testing.T) {
	r := NewVPCIPBlockAllocationStateStorage(&storage.VPCIPBlockAllocationStateStorage{})
	assert.IsType(t, &easv1alpha1.VPCIPBlockAllocationState{}, r.New())
	assert.True(t, r.NamespaceScoped())
	assert.Equal(t, "vpcipblockallocationstate", r.GetSingularName())
	_, canList := interface{}(r).(apirest.Lister)
	assert.False(t, canList, "VPCIPBlockAllocationState only supports Get")
	r.Destroy()
}

func TestVPCIPBlockAllocationStateStorage_Get_BlockIDAndNamespace(t *testing.T) {
	r, provider, client := newVPCIPBlockAllocationStateREST()
	for _, tc := range []struct {
		namespace, cidr string
		visibility      easv1alpha1.IPAddressVisibility
		ipType          easv1alpha1.IPAddressType
	}{
		{"ns1", "10.0.1.0/28", easv1alpha1.Private, easv1alpha1.IPv4},
		{"ns2", "2001:db8::/64", easv1alpha1.External, easv1alpha1.IPv6},
	} {
		object, err := r.Get(request.WithNamespace(context.Background(), tc.namespace), "block-1", &metav1.GetOptions{})
		require.NoError(t, err)
		state := object.(*easv1alpha1.VPCIPBlockAllocationState)
		assert.Equal(t, "block-1", state.Name)
		assert.Equal(t, tc.namespace, state.Namespace)
		assert.Equal(t, tc.visibility, state.Visibility)
		assert.Equal(t, tc.ipType, state.IPAddressType)
		assert.Equal(t, []easv1alpha1.VPCIPAddressBlockAllocationStateForCIDR{{CIDR: tc.cidr}}, state.AllocationStates)
	}
	assert.Equal(t, []string{"ns1", "ns2"}, provider.requestedNamespaces)
	assert.Equal(t, []restAllocationStateCall{
		{orgID: "org", projectID: "p1", vpcID: "vpc1"},
		{orgID: "org", projectID: "p2", vpcID: "vpc2"},
	}, client.calls)

	_, err := r.Get(request.WithNamespace(context.Background(), "ns1"), "display-name-1", nil)
	assert.True(t, k8serrors.IsNotFound(err), "Get expects the block ID rather than its display name")
	_, err = r.Get(request.WithNamespace(context.Background(), "unmapped"), "block-1", nil)
	assert.True(t, k8serrors.IsNotFound(err))
}

func TestVPCIPBlockAllocationStateStorage_Get_Errors(t *testing.T) {
	failure := errors.New("NSX allocation state unavailable")
	for _, tc := range []struct {
		name    string
		err     error
		matches func(error) bool
	}{
		{"not found", vapierrors.NotFound{}, k8serrors.IsNotFound},
		{"forbidden", vapierrors.Unauthorized{}, k8serrors.IsForbidden},
		{"other backend error", failure, func(err error) bool { return errors.Is(err, failure) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _, client := newVPCIPBlockAllocationStateREST()
			client.errs["vpc1"] = tc.err
			_, err := r.Get(request.WithNamespace(context.Background(), "ns1"), "block-1", nil)
			require.Error(t, err)
			assert.True(t, tc.matches(err), "unexpected error: %v", err)
		})
	}
}

func TestVPCIPBlockAllocationStateStorage_Get_Cancelled(t *testing.T) {
	r, _, client := newVPCIPBlockAllocationStateREST()
	ctx, cancel := context.WithCancel(request.WithNamespace(context.Background(), "ns1"))
	cancel()
	_, err := r.Get(ctx, "block-1", nil)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, client.calls)
}

func TestVPCIPBlockAllocationStateStorage_ConvertToTable(t *testing.T) {
	r := NewVPCIPBlockAllocationStateStorage(&storage.VPCIPBlockAllocationStateStorage{})
	for _, tc := range []struct {
		name       string
		visibility easv1alpha1.IPAddressVisibility
		ipType     easv1alpha1.IPAddressType
	}{
		{"IPv4 private block", easv1alpha1.Private, easv1alpha1.IPv4},
		{"IPv6 external block", easv1alpha1.External, easv1alpha1.IPv6},
		{"empty optional address type", easv1alpha1.Private, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := &easv1alpha1.VPCIPBlockAllocationState{
				ObjectMeta:    metav1.ObjectMeta{Name: "block-1", Namespace: "ns1"},
				Visibility:    tc.visibility,
				IPAddressType: tc.ipType,
			}
			table, err := r.ConvertToTable(context.Background(), state, nil)
			require.NoError(t, err)
			assert.Equal(t, vpcIPBlockAllocationStateColumns, table.ColumnDefinitions)
			require.Len(t, table.Rows, 1)
			assert.Equal(t, []interface{}{"block-1", string(tc.visibility), string(tc.ipType)}, table.Rows[0].Cells)
			metadata := table.Rows[0].Object.Object.(*metav1.PartialObjectMetadata)
			assert.Equal(t, "block-1", metadata.Name)
			assert.Equal(t, "ns1", metadata.Namespace)
		})
	}
	_, err := r.ConvertToTable(context.Background(), &easv1alpha1.IPBlockUsage{}, nil)
	assert.ErrorContains(t, err, "unsupported type")
}

func TestVPCIPBlockAllocationStateStorage_ConvertToTable_JSONNamespaceMetadata(t *testing.T) {
	r := &vpcIPBlockAllocationStateStorage{}
	state := &easv1alpha1.VPCIPBlockAllocationState{
		ObjectMeta:    metav1.ObjectMeta{Name: "block-1", Namespace: "ns1"},
		Visibility:    easv1alpha1.External,
		IPAddressType: easv1alpha1.IPv6,
	}
	table, err := r.ConvertToTable(context.Background(), state, nil)
	require.NoError(t, err)
	require.Len(t, table.ColumnDefinitions, 3)
	for _, column := range table.ColumnDefinitions {
		assert.Zero(t, column.Priority, "all columns are available in normal and wide output")
	}
	table = tableAfterJSONSerialization(t, table)
	require.Len(t, table.Rows, 1)
	row := table.Rows[0]
	require.Len(t, row.Cells, len(table.ColumnDefinitions), "namespace must not add a display cell")
	assert.Equal(t, []interface{}{"block-1", "External", "IPv6"}, row.Cells)
	assert.Equal(t, metav1.SchemeGroupVersion.WithKind("PartialObjectMetadata"), row.Object.Object.GetObjectKind().GroupVersionKind())
	metadata, err := meta.Accessor(row.Object.Object)
	require.NoError(t, err)
	assert.Equal(t, "block-1", metadata.GetName())
	assert.Equal(t, "ns1", metadata.GetNamespace(), "namespace remains separate from visibility and IP address type")
}
