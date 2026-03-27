/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package server

import (
	"encoding/json"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

// Per-resource table column definitions for kubectl Table format.
var (
	vpcIPUsageColumns = []metav1.TableColumnDefinition{
		{Name: "Name", Type: "string", Format: "name", Description: "Name of the resource"},
		{Name: "IPBLOCKS", Type: "string", Description: "IP blocks summary"},
	}
	ipBlockUsageColumns = []metav1.TableColumnDefinition{
		{Name: "Name", Type: "string", Format: "name", Description: "Name of the resource"},
		{Name: "USED IP RANGES", Type: "string", Description: "Used IP ranges"},
		{Name: "AVAILABLE IP RANGES", Type: "string", Description: "Available IP ranges"},
	}
	subnetIPPoolsColumns = []metav1.TableColumnDefinition{
		{Name: "Name", Type: "string", Format: "name", Description: "Name of the resource"},
		{Name: "RESULTS", Type: "string", Description: "IP pool results"},
	}
	subnetDHCPColumns = []metav1.TableColumnDefinition{
		{Name: "Name", Type: "string", Format: "name", Description: "Name of the resource"},
		{Name: "IP_POOL_STATS", Type: "string", Description: "IP pool statistics"},
	}
)

// Summary helpers for per-resource table columns.

func vpcIPBlocksSummary(usage *easv1alpha1.VPCIPAddressUsage) string {
	var parts []string
	for _, b := range usage.Spec.IpBlocks {
		cidr := ""
		if len(b.Cidrs) > 0 {
			cidr = b.Cidrs[0]
		}
		parts = append(parts, fmt.Sprintf("%s(%s%%)", cidr, b.PercentageUsed))
	}
	return strings.Join(parts, ",")
}

func ipBlockRangesSummary(ranges []string) string {
	if len(ranges) == 0 {
		return ""
	}
	data, _ := json.Marshal(ranges)
	return string(data)
}

func subnetIPPoolsSummary(pools *easv1alpha1.SubnetIPPools) string {
	id := pools.Spec.ID
	if id == "" {
		id = pools.Spec.DisplayName
	}
	if id == "" && pools.Spec.PoolUsage == nil {
		return "<none>"
	}
	var availableIps int64
	if pools.Spec.PoolUsage != nil {
		availableIps = pools.Spec.PoolUsage.AvailableIps
	}
	return fmt.Sprintf("%s(available_ips: %d)", id, availableIps)
}

func subnetDHCPStatsSummary(stats *easv1alpha1.SubnetDHCPServerConfigStats) string {
	var parts []string
	for _, p := range stats.Spec.IpPoolStats {
		name := util.LastPathSegment(p.DhcpIpPoolId)
		if name == "" {
			name = p.DhcpIpPoolId
		}
		parts = append(parts, fmt.Sprintf("%s(allocated_percentage: %d)", name, p.AllocatedPercentage))
	}
	return strings.Join(parts, ",")
}
