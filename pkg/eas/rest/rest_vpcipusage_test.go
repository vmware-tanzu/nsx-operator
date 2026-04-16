/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package rest

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/endpoints/request"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas/storage"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func newVPCIPUsageREST() *vpcIPUsageStorage {
	provider := fakeVPCInfoProvider{namespaces: []string{"ns1"}}
	return NewVPCIPUsageStorage(
		storage.NewVPCIPAddressUsageStorage(&nsx.Client{}, provider),
		provider,
	)
}

func TestVPCIPUsageStorage_Metadata(t *testing.T) {
	r := newVPCIPUsageREST()
	assert.IsType(t, &easv1alpha1.VPCIPAddressUsage{}, r.New())
	assert.IsType(t, &easv1alpha1.VPCIPAddressUsageList{}, r.NewList())
	assert.True(t, r.NamespaceScoped())
	assert.Equal(t, "vpcipaddressusage", r.GetSingularName())
	r.Destroy() // no-op; verify no panic
}

func TestVPCIPUsageStorage_Get_ReturnsError(t *testing.T) {
	// fakeVPCInfoProvider.ListVPCInfo returns nil (no VPCs), so the store returns "no VPC found".
	r := newVPCIPUsageREST()
	ctx := request.WithNamespace(context.Background(), "ns1")
	_, err := r.Get(ctx, "vpc1", &metav1.GetOptions{})
	assert.Error(t, err)
}

func TestVPCIPUsageStorage_List_Namespaced(t *testing.T) {
	// Namespace present → namespaced list; provider returns no VPCs → empty list.
	r := newVPCIPUsageREST()
	ctx := request.WithNamespace(context.Background(), "ns1")
	result, err := r.List(ctx, nil)
	require.NoError(t, err)
	list, ok := result.(*easv1alpha1.VPCIPAddressUsageList)
	require.True(t, ok)
	assert.Empty(t, list.Items)
}

func TestVPCIPUsageStorage_List_CrossNamespace(t *testing.T) {
	// No namespace in context → cross-namespace path; provider returns "ns1" which
	// has no VPCs (fakeVPCInfoProvider.ListVPCInfo returns nil) → empty merged list.
	r := newVPCIPUsageREST()
	result, err := r.List(context.Background(), nil)
	require.NoError(t, err)
	list, ok := result.(*easv1alpha1.VPCIPAddressUsageList)
	require.True(t, ok)
	assert.Empty(t, list.Items)
}

func TestVPCIPUsageStorage_List_CrossNamespace_NoNamespaces(t *testing.T) {
	// Provider has no namespaces → cross-namespace list returns empty list immediately.
	r := NewVPCIPUsageStorage(
		storage.NewVPCIPAddressUsageStorage(&nsx.Client{}, fakeVPCInfoProvider{}),
		fakeVPCInfoProvider{}, // ListAllVPCNamespaces returns nil
	)
	result, err := r.List(context.Background(), nil)
	require.NoError(t, err)
	list, ok := result.(*easv1alpha1.VPCIPAddressUsageList)
	require.True(t, ok)
	assert.Empty(t, list.Items)
}

func TestVPCIPUsageStorage_ConvertToTable_Single(t *testing.T) {
	r := newVPCIPUsageREST()
	obj := &easv1alpha1.VPCIPAddressUsage{
		ObjectMeta: metav1.ObjectMeta{Name: "vpc1", Namespace: "ns1"},
		IPBlocks: []easv1alpha1.VPCIPAddressBlock{
			{CIDRs: []string{"10.0.0.0/8"}, PercentageUsed: "25"},
		},
	}
	table, err := r.ConvertToTable(context.Background(), obj, nil)
	require.NoError(t, err)
	require.Len(t, table.Rows, 1)
	assert.Equal(t, "vpc1", table.Rows[0].Cells[0])
	assert.Equal(t, vpcIPUsageColumns, table.ColumnDefinitions)
}

func TestVPCIPUsageStorage_ConvertToTable_List(t *testing.T) {
	r := newVPCIPUsageREST()
	list := &easv1alpha1.VPCIPAddressUsageList{
		Items: []easv1alpha1.VPCIPAddressUsage{
			{ObjectMeta: metav1.ObjectMeta{Name: "vpc1", Namespace: "ns1"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "vpc2", Namespace: "ns1"}},
		},
	}
	table, err := r.ConvertToTable(context.Background(), list, nil)
	require.NoError(t, err)
	assert.Len(t, table.Rows, 2)
}

func TestVPCIPUsageStorage_ConvertToTable_EmptyList(t *testing.T) {
	r := newVPCIPUsageREST()
	table, err := r.ConvertToTable(context.Background(), &easv1alpha1.VPCIPAddressUsageList{}, nil)
	require.NoError(t, err)
	assert.Empty(t, table.Rows)
}

func TestVPCIPUsageStorage_ConvertToTable_Error(t *testing.T) {
	r := newVPCIPUsageREST()
	_, err := r.ConvertToTable(context.Background(), &easv1alpha1.IPBlockUsage{}, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported type")
}

func TestVPCIPUsageStorage_List_CrossNamespace_ErrorSkipped(t *testing.T) {
	// The store returns an error for ns1 (NSX call fails); the REST adapter skips it.
	// This covers the 'continue' branch in vpcIPUsageStorage.List.
	provider := singleEntryVPCProvider{
		entry: eas.VPCEntry{
			DisplayName: "vpc1",
			Info:        common.VPCResourceInfo{OrgID: "o1", ProjectID: "p1", VPCID: "vpc1"},
		},
	}
	nsxClient := &nsx.Client{}
	nsxClient.IPAddressUsageClient = &fakeErrIPAddressUsageClient{}
	r := NewVPCIPUsageStorage(
		storage.NewVPCIPAddressUsageStorage(nsxClient, provider),
		provider,
	)
	// Cross-namespace list: no namespace in context.
	result, err := r.List(context.Background(), nil)
	require.NoError(t, err, "REST adapter must not propagate per-namespace errors")
	list, ok := result.(*easv1alpha1.VPCIPAddressUsageList)
	require.True(t, ok)
	assert.Empty(t, list.Items)
}

func TestVPCIPUsageStorage_Destroy(t *testing.T) {
	// Destroy is a documented no-op; calling it must not panic.
	(&vpcIPUsageStorage{}).Destroy()
}

func TestTableRow_ObjectHasNameAndNamespace(t *testing.T) {
	// tableRow is called by ConvertToTable; verify PartialObjectMetadata is populated.
	r := newVPCIPUsageREST()
	obj := &easv1alpha1.VPCIPAddressUsage{
		ObjectMeta: metav1.ObjectMeta{Name: "myvpc", Namespace: "mynamespace"},
	}
	table, err := r.ConvertToTable(context.Background(), obj, nil)
	require.NoError(t, err)
	require.Len(t, table.Rows, 1)
	row := table.Rows[0]
	meta, ok := row.Object.Object.(*metav1.PartialObjectMetadata)
	require.True(t, ok)
	assert.Equal(t, "myvpc", meta.Name)
	assert.Equal(t, "mynamespace", meta.Namespace)
}

// singleEntryVPCProvider returns one VPC entry for any namespace and lists that
// namespace.  This drives the underlying storage to make NSX API calls (enabling
// error-path coverage).
type singleEntryVPCProvider struct {
	entry eas.VPCEntry
}

func (p singleEntryVPCProvider) ListVPCInfo(string) []eas.VPCEntry { return []eas.VPCEntry{p.entry} }
func (p singleEntryVPCProvider) ListAllVPCNamespaces() []string    { return []string{"ns1"} }

// fakeErrIPAddressUsageClient implements vpcs.IpAddressUsageClient; always errors.
type fakeErrIPAddressUsageClient struct{}

func (f *fakeErrIPAddressUsageClient) Get(_, _, _ string) (model.VpcIpAddressBlocks, error) {
	return model.VpcIpAddressBlocks{}, fmt.Errorf("nsx unreachable")
}

// Compile-time interface check
var _ eas.VPCInfoProvider = (*fakeVPCInfoProvider)(nil)
