/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package rest

import (
	"strings"
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
			{AllocatedPercentage: 12, PoolSize: 80},
		},
	}
	assert.Equal(t, "pool-0(alloc:25%,size:40),pool-1(alloc:12%,size:80)", subnetDHCPStatsSummary(stats))
}

func TestSubnetDHCPStatsSummary_Empty(t *testing.T) {
	assert.Equal(t, "", subnetDHCPStatsSummary(&easv1alpha1.SubnetDHCPServerStats{}))
}

func TestSubnetDHCPStatsSummary_IPv6(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stats easv1alpha1.SubnetDHCPServerStats
		want  string
	}{
		{
			name: "IPv6 only with large pool and fractional percentage",
			stats: easv1alpha1.SubnetDHCPServerStats{
				IPv6PoolStats: []easv1alpha1.DHCPIPv6IPPoolUsage{
					{AllocatedPercentage: 12.5, PoolSize: "18446744073709551616"},
				},
			},
			want: "ipv6-pool-0(alloc:12.5%,size:18446744073709551616)",
		},
		{
			name: "dual stack",
			stats: easv1alpha1.SubnetDHCPServerStats{
				IPPoolStats: []easv1alpha1.DHCPIPPoolUsage{
					{AllocatedPercentage: 25, PoolSize: 40},
				},
				IPv6PoolStats: []easv1alpha1.DHCPIPv6IPPoolUsage{
					{AllocatedPercentage: 12.5, PoolSize: "80"},
				},
			},
			want: "pool-0(alloc:25%,size:40),ipv6-pool-0(alloc:12.5%,size:80)",
		},
		{
			name: "multiple IPv6 pools including zero usage",
			stats: easv1alpha1.SubnetDHCPServerStats{
				IPv6PoolStats: []easv1alpha1.DHCPIPv6IPPoolUsage{
					{AllocatedPercentage: 0, PoolSize: "10"},
					{AllocatedPercentage: 50, PoolSize: "20"},
				},
			},
			want: "ipv6-pool-0(alloc:0%,size:10),ipv6-pool-1(alloc:50%,size:20)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, subnetDHCPStatsSummary(&tc.stats))
		})
	}
}

func TestSubnetDHCPStatsSummary_IPv6Truncated(t *testing.T) {
	stats := &easv1alpha1.SubnetDHCPServerStats{
		IPv6PoolStats: []easv1alpha1.DHCPIPv6IPPoolUsage{
			{AllocatedPercentage: 12.5, PoolSize: "18446744073709551616"},
			{AllocatedPercentage: 25, PoolSize: "18446744073709551616"},
		},
	}
	summary := subnetDHCPStatsSummary(stats)
	assert.Len(t, summary, maxColLen)
	assert.True(t, strings.HasSuffix(summary, "..."))
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
