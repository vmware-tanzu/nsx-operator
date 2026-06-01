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

func newIPBlockUsageREST() *ipBlockUsageStorage {
	provider := fakeVPCInfoProvider{namespaces: []string{"ns1"}}
	return NewIPBlockUsageStorage(
		storage.NewIPBlockUsageStorage(&nsx.Client{}, provider),
		provider,
	)
}

func TestIPBlockUsageStorage_Metadata(t *testing.T) {
	r := newIPBlockUsageREST()
	assert.IsType(t, &easv1alpha1.IPBlockUsage{}, r.New())
	assert.IsType(t, &easv1alpha1.IPBlockUsageList{}, r.NewList())
	assert.True(t, r.NamespaceScoped())
	assert.Equal(t, "ipblockusage", r.GetSingularName())
	r.Destroy()
}

func TestIPBlockUsageStorage_Get_InvalidName(t *testing.T) {
	// Name ":" has empty blockID → store returns "invalid" error.
	r := newIPBlockUsageREST()
	ctx := request.WithNamespace(context.Background(), "ns1")
	_, err := r.Get(ctx, ":", &metav1.GetOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}

func TestIPBlockUsageStorage_List_Namespaced(t *testing.T) {
	// Namespace present; provider returns no VPCs → empty list.
	r := newIPBlockUsageREST()
	ctx := request.WithNamespace(context.Background(), "ns1")
	result, err := r.List(ctx, nil)
	require.NoError(t, err)
	list, ok := result.(*easv1alpha1.IPBlockUsageList)
	require.True(t, ok)
	assert.Empty(t, list.Items)
}

func TestIPBlockUsageStorage_List_CrossNamespace(t *testing.T) {
	r := newIPBlockUsageREST()
	result, err := r.List(context.Background(), nil)
	require.NoError(t, err)
	list, ok := result.(*easv1alpha1.IPBlockUsageList)
	require.True(t, ok)
	assert.Empty(t, list.Items)
}

func TestIPBlockUsageStorage_ConvertToTable_Single(t *testing.T) {
	r := newIPBlockUsageREST()
	obj := &easv1alpha1.IPBlockUsage{
		ObjectMeta:        metav1.ObjectMeta{Name: "blk1", Namespace: "ns1"},
		UsedIPRanges:      []string{"10.0.0.1-10.0.0.5"},
		AvailableIPRanges: []string{"10.0.0.6-10.0.0.255"},
	}
	table, err := r.ConvertToTable(context.Background(), obj, nil)
	require.NoError(t, err)
	require.Len(t, table.Rows, 1)
	assert.Equal(t, "blk1", table.Rows[0].Cells[0])
	assert.Equal(t, ipBlockUsageColumns, table.ColumnDefinitions)
}

func TestIPBlockUsageStorage_ConvertToTable_List(t *testing.T) {
	r := newIPBlockUsageREST()
	list := &easv1alpha1.IPBlockUsageList{
		Items: []easv1alpha1.IPBlockUsage{
			{ObjectMeta: metav1.ObjectMeta{Name: "blk1", Namespace: "ns1"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "blk2", Namespace: "ns1"}},
		},
	}
	table, err := r.ConvertToTable(context.Background(), list, nil)
	require.NoError(t, err)
	assert.Len(t, table.Rows, 2)
}

func TestIPBlockUsageStorage_ConvertToTable_Error(t *testing.T) {
	r := newIPBlockUsageREST()
	_, err := r.ConvertToTable(context.Background(), &easv1alpha1.VPCIPAddressUsage{}, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported type")
}

func TestIPBlockUsageStorage_List_CrossNamespace_ErrorSkipped(t *testing.T) {
	// The store returns an error for ns1 (NSX call fails); the REST adapter skips it.
	// This covers the 'continue' branch in ipBlockUsageStorage.List.
	provider := singleEntryVPCProvider{
		entry: eas.VPCEntry{
			DisplayName: "vpc1",
			Info:        common.VPCResourceInfo{OrgID: "o1", ProjectID: "p1", VPCID: "vpc1"},
		},
	}
	nsxClient := &nsx.Client{}
	nsxClient.ProjectIPBlockUsageClient = &fakeErrProjectIPBlockUsageClient{}
	r := NewIPBlockUsageStorage(
		storage.NewIPBlockUsageStorage(nsxClient, provider),
		provider,
	)
	result, err := r.List(context.Background(), nil)
	require.NoError(t, err, "REST adapter must not propagate per-namespace errors")
	list, ok := result.(*easv1alpha1.IPBlockUsageList)
	require.True(t, ok)
	assert.Empty(t, list.Items)
}

func TestIPBlockUsageStorage_Destroy(t *testing.T) {
	(&ipBlockUsageStorage{}).Destroy()
}

// fakeErrProjectIPBlockUsageClient implements ip_blocks.UsageClient; always errors on List.
type fakeErrProjectIPBlockUsageClient struct{}

func (f *fakeErrProjectIPBlockUsageClient) Get(_, _, _ string) (model.IpAddressBlockUsage, error) {
	return model.IpAddressBlockUsage{}, fmt.Errorf("nsx unreachable")
}

func (f *fakeErrProjectIPBlockUsageClient) List(_, _ string, _ *string, _ *bool, _ *string, _ *string, _ *int64, _ *bool, _ *string) (model.IpAddressBlockUsageList, error) {
	return model.IpAddressBlockUsageList{}, fmt.Errorf("nsx unreachable")
}
