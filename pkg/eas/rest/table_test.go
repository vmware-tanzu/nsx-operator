/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package rest

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
		IPAddressType: "IPv4",
		PoolUsage: &easv1alpha1.PoolUsage{
			AvailableIPs: 7,
		},
	}
	s := subnetIPPoolsSummary(pools)
	assert.Contains(t, s, "id1")
	assert.Contains(t, s, "IPv4")
	assert.Contains(t, s, "availableIPs:7")
}

func TestSubnetDHCPStatsSummary(t *testing.T) {
	stats := &easv1alpha1.SubnetDHCPServerStats{
		IPPoolStats: []easv1alpha1.DHCPIPPoolUsage{
			{AllocatedPercentage: 25, PoolSize: 40},
		},
	}
	assert.Equal(t, "pool-0(alloc:25%,size:40)", subnetDHCPStatsSummary(stats))
}

func TestSubnetDHCPStatsSummary_Empty(t *testing.T) {
	assert.Equal(t, "", subnetDHCPStatsSummary(&easv1alpha1.SubnetDHCPServerStats{}))
}

func TestTruncateCol(t *testing.T) {
	short := "hello"
	assert.Equal(t, short, truncateCol(short))

	exact := string(make([]byte, maxColLen))
	assert.Equal(t, exact, truncateCol(exact))

	long := string(make([]byte, maxColLen+10))
	result := truncateCol(long)
	assert.Equal(t, maxColLen, len(result))
	assert.True(t, result[maxColLen-3:] == "...", "truncated string must end with ...")
}

func TestVpcIPBlocksSummary_Truncated(t *testing.T) {
	// Build many blocks so the summary exceeds maxColLen and triggers truncation.
	blocks := make([]easv1alpha1.VPCIPAddressBlock, 10)
	for i := range blocks {
		blocks[i] = easv1alpha1.VPCIPAddressBlock{
			CIDRs:          []string{"192.168.100.0/24"},
			PercentageUsed: "99.99",
		}
	}
	s := vpcIPBlocksSummary(&easv1alpha1.VPCIPAddressUsage{IPBlocks: blocks})
	assert.LessOrEqual(t, len(s), maxColLen)
	assert.True(t, len(s) == maxColLen && s[maxColLen-3:] == "..." || len(s) < maxColLen)
}

func TestIPBlockRangesSummary_Truncated(t *testing.T) {
	// Long list of ranges that will exceed maxColLen.
	ranges := make([]string, 20)
	for i := range ranges {
		ranges[i] = "192.168.100.0/24"
	}
	s := ipBlockRangesSummary(ranges)
	assert.LessOrEqual(t, len(s), maxColLen)
}
