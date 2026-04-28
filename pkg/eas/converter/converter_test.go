/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package converter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
)

func strPtr(s string) *string { return &s }
func int64Ptr(i int64) *int64 { return &i }

func TestDerefString(t *testing.T) {
	assert.Equal(t, "", DerefString(nil))
	s := "x"
	assert.Equal(t, "x", DerefString(&s))
}

func TestDerefInt64(t *testing.T) {
	assert.Equal(t, int64(0), DerefInt64(nil))
	v := int64(42)
	assert.Equal(t, int64(42), DerefInt64(&v))
}

func TestConvertVpcIpAddressBlocks_Nil(t *testing.T) {
	out := ConvertVpcIpAddressBlocks(nil, "vpc1", "ns1")
	require.NotNil(t, out)
	assert.Equal(t, "vpc1", out.Name)
	assert.Equal(t, "ns1", out.Namespace)
	assert.Equal(t, easv1alpha1.GroupVersion.String(), out.APIVersion)
	assert.Empty(t, out.IPBlocks)
}

func TestConvertVpcIpAddressBlocks_WithBlocks(t *testing.T) {
	path := "/orgs/o1/projects/p1/vpcs/vpcid"
	cidr := "10.1.0.0/16"
	pct := "50"
	nsx := &model.VpcIpAddressBlocks{
		IpBlocks: []model.VpcIpAddressBlock{
			{
				Path:           &path,
				Cidr:           &cidr,
				PercentageUsed: &pct,
				Visibility:     strPtr("PRIVATE"),
			},
		},
	}
	out := ConvertVpcIpAddressBlocks(nsx, "vpc1", "ns1")
	require.Len(t, out.IPBlocks, 1)
	b := out.IPBlocks[0]
	assert.Equal(t, "vpcid", b.IPBlockName)
	assert.Equal(t, "10.1.0.0/16", b.CIDR)
	assert.Equal(t, "50", b.PercentageUsed)
	assert.Equal(t, easv1alpha1.Private, b.Visibility)
}

func TestConvertIpAddressBlockUsageList_Nil(t *testing.T) {
	assert.Nil(t, ConvertIpAddressBlockUsageList(nil, "ns"))
}

func TestConvertIpAddressBlockUsageList_NameFromIntentPath(t *testing.T) {
	intent := "/orgs/default/projects/myproject/ip-blocks/shared-block-1"
	nsx := &model.IpAddressBlockUsageList{
		Results: []model.IpAddressBlockUsage{
			{
				IntentPath: &intent,
				Visibility: strPtr("EXTERNAL"),
			},
		},
	}
	items := ConvertIpAddressBlockUsageList(nsx, "ns1")
	require.Len(t, items, 1)
	assert.Equal(t, "myproject:shared-block-1", items[0].Name)
	assert.Equal(t, easv1alpha1.External, items[0].Visibility)
}

func TestConvertDhcpServerStatistics_Nil(t *testing.T) {
	out := ConvertDhcpServerStatistics(nil, "sub1", "ns1")
	require.NotNil(t, out)
	assert.Equal(t, "sub1", out.Name)
	assert.Empty(t, out.IPPoolStats)
}

func TestConvertDhcpServerStatistics_WithPools(t *testing.T) {
	nsx := &model.DhcpServerStatistics{
		IpPoolStats: []model.DhcpIpPoolUsage{
			{
				AllocatedPercentage: int64Ptr(10),
				PoolSize:            int64Ptr(100),
			},
		},
	}
	out := ConvertDhcpServerStatistics(nsx, "sub1", "ns1")
	require.Len(t, out.IPPoolStats, 1)
	assert.Equal(t, int64(10), out.IPPoolStats[0].AllocatedPercentage)
	assert.Equal(t, int64(100), out.IPPoolStats[0].PoolSize)
}
