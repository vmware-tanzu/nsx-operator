/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package server

import (
	"encoding/json"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
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
	for _, b := range usage.IPBlocks {
		cidr := ""
		if len(b.CIDRs) > 0 {
			cidr = b.CIDRs[0]
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
	id := pools.Name
	if id == "" && pools.PoolUsage == nil && pools.IPAddressType == "" {
		return "<none>"
	}
	var availableIPs int64
	if pools.PoolUsage != nil {
		availableIPs = pools.PoolUsage.AvailableIPs
	}
	return fmt.Sprintf("%s(type:%s,availableIPs:%d,checkOverlap:%v)", id, pools.IPAddressType, availableIPs, pools.CheckOverlapWithExistingPools)
}

func subnetDHCPStatsSummary(stats *easv1alpha1.SubnetDHCPServerStats) string {
	var parts []string
	for i, p := range stats.IPPoolStats {
		parts = append(parts, fmt.Sprintf("pool-%d(allocated_percentage: %d,pool_size: %d)", i, p.AllocatedPercentage, p.PoolSize))
	}
	return strings.Join(parts, ",")
}
