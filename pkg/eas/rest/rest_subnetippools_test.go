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

func newSubnetIPPoolsREST() *subnetIPPoolsStorage {
	return NewSubnetIPPoolsStorage(
		storage.NewSubnetIPPoolsStorage(&nsx.Client{}, newTestFakeK8sClient().Build()),
	)
}

func TestSubnetIPPoolsStorage_Metadata(t *testing.T) {
	r := newSubnetIPPoolsREST()
	assert.IsType(t, &easv1alpha1.SubnetIPPools{}, r.New())
	assert.True(t, r.NamespaceScoped())
	assert.Equal(t, "subnetippools", r.GetSingularName())
	r.Destroy()
}

func TestSubnetIPPoolsStorage_Get_NoSubnetCR(t *testing.T) {
	// No Subnet CR pre-loaded in the fake k8s client → "subnet CR not found".
	r := newSubnetIPPoolsREST()
	ctx := request.WithNamespace(context.Background(), "ns1")
	_, err := r.Get(ctx, "sub1", &metav1.GetOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "subnet CR")
}

func TestSubnetIPPoolsStorage_ConvertToTable_Success(t *testing.T) {
	r := newSubnetIPPoolsREST()
	obj := &easv1alpha1.SubnetIPPools{
		ObjectMeta:    metav1.ObjectMeta{Name: "sub1", Namespace: "ns1"},
		IPAddressType: "IPv4",
		PoolUsage:     &easv1alpha1.PoolUsage{AvailableIPs: 200},
	}
	table, err := r.ConvertToTable(context.Background(), obj, nil)
	require.NoError(t, err)
	require.Len(t, table.Rows, 1)
	assert.Equal(t, "sub1", table.Rows[0].Cells[0])
	assert.Equal(t, subnetIPPoolsColumns, table.ColumnDefinitions)
}

func TestSubnetIPPoolsStorage_ConvertToTable_Error(t *testing.T) {
	r := newSubnetIPPoolsREST()
	_, err := r.ConvertToTable(context.Background(), &easv1alpha1.VPCIPAddressUsage{}, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported type")
}

func TestSubnetIPPoolsStorage_Destroy(t *testing.T) {
	(&subnetIPPoolsStorage{}).Destroy()
}
