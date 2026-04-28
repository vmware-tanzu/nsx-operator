/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
)

func TestVpcIPBlocksSummary(t *testing.T) {
	u := &easv1alpha1.VPCIPAddressUsage{
		IPBlocks: []easv1alpha1.VPCIPAddressBlock{
			{CIDRs: []string{"192.168.0.0/24"}, PercentageUsed: "10"},
			{CIDRs: []string{"10.0.0.0/8"}, PercentageUsed: "5"},
		},
	}
	assert.Equal(t, "192.168.0.0/24(10%),10.0.0.0/8(5%)", vpcIPBlocksSummary(u))
}

func TestVpcIPBlocksSummary_Empty(t *testing.T) {
	assert.Equal(t, "", vpcIPBlocksSummary(&easv1alpha1.VPCIPAddressUsage{}))
}

func TestIpBlockRangesSummary(t *testing.T) {
	assert.Equal(t, "", ipBlockRangesSummary(nil))
	assert.Equal(t, `["a","b"]`, ipBlockRangesSummary([]string{"a", "b"}))
}

func TestSubnetIPPoolsSummary(t *testing.T) {
	assert.Equal(t, "<none>", subnetIPPoolsSummary(&easv1alpha1.SubnetIPPools{}))

	pools := &easv1alpha1.SubnetIPPools{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "id1",
			Namespace: "ns",
		},
		IPAddressType:                 "IPv4",
		CheckOverlapWithExistingPools: true,
		PoolUsage: &easv1alpha1.PoolUsage{
			AvailableIPs: 7,
		},
	}
	s := subnetIPPoolsSummary(pools)
	assert.Contains(t, s, "id1")
	assert.Contains(t, s, "IPv4")
	assert.Contains(t, s, "availableIPs:7")
	assert.Contains(t, s, "checkOverlap:true")
}

func TestSubnetDHCPStatsSummary(t *testing.T) {
	stats := &easv1alpha1.SubnetDHCPServerStats{
		IPPoolStats: []easv1alpha1.DHCPIPPoolUsage{
			{AllocatedPercentage: 25, PoolSize: 40},
		},
	}
	assert.Equal(t, "pool-0(allocated_percentage: 25,pool_size: 40)", subnetDHCPStatsSummary(stats))
}

func TestSubnetDHCPStatsSummary_Empty(t *testing.T) {
	assert.Equal(t, "", subnetDHCPStatsSummary(&easv1alpha1.SubnetDHCPServerStats{}))
}
