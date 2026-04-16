/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package rest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/endpoints/request"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas/storage"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

func newSubnetDHCPStatsREST() *subnetDHCPStatsStorage {
	return NewSubnetDHCPStatsStorage(
		storage.NewSubnetDHCPStatsStorage(&nsx.Client{}, newTestFakeK8sClient().Build()),
	)
}

func TestSubnetDHCPStatsStorage_Metadata(t *testing.T) {
	r := newSubnetDHCPStatsREST()
	assert.IsType(t, &easv1alpha1.SubnetDHCPServerStats{}, r.New())
	assert.True(t, r.NamespaceScoped())
	assert.Equal(t, "subnetdhcpserverstats", r.GetSingularName())
	r.Destroy()
}

func TestSubnetDHCPStatsStorage_Get_NoSubnetCR(t *testing.T) {
	r := newSubnetDHCPStatsREST()
	ctx := request.WithNamespace(context.Background(), "ns1")
	_, err := r.Get(ctx, "sub1", &metav1.GetOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "subnet CR")
}

func TestSubnetDHCPStatsStorage_ConvertToTable_Success(t *testing.T) {
	r := newSubnetDHCPStatsREST()
	obj := &easv1alpha1.SubnetDHCPServerStats{
		ObjectMeta:  metav1.ObjectMeta{Name: "sub1", Namespace: "ns1"},
		IPPoolStats: []easv1alpha1.DHCPIPPoolUsage{{AllocatedPercentage: 50, PoolSize: 100}},
	}
	table, err := r.ConvertToTable(context.Background(), obj, nil)
	require.NoError(t, err)
	require.Len(t, table.Rows, 1)
	assert.Equal(t, "sub1", table.Rows[0].Cells[0])
	assert.Equal(t, subnetDHCPColumns, table.ColumnDefinitions)
}

func TestSubnetDHCPStatsStorage_ConvertToTable_Error(t *testing.T) {
	r := newSubnetDHCPStatsREST()
	_, err := r.ConvertToTable(context.Background(), &easv1alpha1.VPCIPAddressUsage{}, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported type")
}

func TestSubnetDHCPStatsStorage_Destroy(t *testing.T) {
	(&subnetDHCPStatsStorage{}).Destroy()
}
