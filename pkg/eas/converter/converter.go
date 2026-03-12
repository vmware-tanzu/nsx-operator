/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package converter

import (
	"fmt"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

// ConvertVpcIpAddressBlocks converts NSX VpcIpAddressBlocks to K8s VPCIPAddressUsage.
func ConvertVpcIpAddressBlocks(nsxBlocks *model.VpcIpAddressBlocks, vpcName, namespace string) *easv1alpha1.VPCIPAddressUsage {
	usage := &easv1alpha1.VPCIPAddressUsage{
		TypeMeta: metav1.TypeMeta{
			APIVersion: easv1alpha1.GroupVersion.String(),
			Kind:       "VPCIPAddressUsage",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      vpcName,
			Namespace: namespace,
		},
	}

	if nsxBlocks == nil {
		return usage
	}

	blocks := make([]easv1alpha1.VpcIpAddressBlock, 0, len(nsxBlocks.IpBlocks))
	for _, b := range nsxBlocks.IpBlocks {
		block := easv1alpha1.VpcIpAddressBlock{
			Available:      DerefInt64(b.Available),
			Cidrs:          b.Cidrs,
			Path:           DerefString(b.Path),
			PercentageUsed: DerefString(b.PercentageUsed),
			Total:          DerefInt64(b.Total),
			Visibility:     DerefString(b.Visibility),
		}
		block.ExcludedIps = convertIpPoolRanges(b.ExcludedIps)
		block.Ranges = convertIpPoolRanges(b.Ranges)
		if b.AllocatedByVpc != nil {
			block.AllocatedByVpc = &easv1alpha1.AllocatedByVpc{
				AccessMode:     DerefString(b.AllocatedByVpc.AccessMode),
				Count:          DerefInt64(b.AllocatedByVpc.Count),
				PercentageUsed: DerefString(b.AllocatedByVpc.PercentageUsed),
			}
			for _, ip := range b.AllocatedByVpc.IpAddresses {
				block.AllocatedByVpc.IpAddresses = append(block.AllocatedByVpc.IpAddresses,
					easv1alpha1.VpcIpAddress{
						IpAddress: DerefString(ip.Address),
						Path:      DerefString(ip.Path),
					})
			}
		}
		blocks = append(blocks, block)
	}
	usage.Spec.IpBlocks = blocks
	return usage
}

// ConvertIpAddressBlockUsageList converts NSX IpAddressBlockUsageList to K8s IPBlockUsageList.
func ConvertIpAddressBlockUsageList(nsxList *model.IpAddressBlockUsageList, namespace string) []easv1alpha1.IPBlockUsage {
	if nsxList == nil {
		return nil
	}

	items := make([]easv1alpha1.IPBlockUsage, 0, len(nsxList.Results))
	for i, u := range nsxList.Results {
		item := easv1alpha1.IPBlockUsage{
			TypeMeta: metav1.TypeMeta{
				APIVersion: easv1alpha1.GroupVersion.String(),
				Kind:       "IPBlockUsage",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      ipBlockUsageName(DerefString(u.IntentPath), i),
				Namespace: namespace,
			},
			Spec: easv1alpha1.IPBlockUsageSpec{
				AddressType:       DerefString(u.AddressType),
				AvailableIpRanges: u.AvailableIpRanges,
				AvailableIpsCount: DerefString(u.AvailableIpsCount),
				Description:       DerefString(u.Description),
				DisplayName:       DerefString(u.DisplayName),
				IntentPath:        DerefString(u.IntentPath),
				OverallIpsCount:   DerefString(u.OverallIpsCount),
				UsedIpRanges:      u.UsedIpRanges,
				UsedIpsCount:      DerefString(u.UsedIpsCount),
				Visibility:        DerefString(u.Visibility),
			},
		}
		for _, c := range u.CidrUsage {
			item.Spec.CidrUsage = append(item.Spec.CidrUsage, easv1alpha1.CidrUsageDetails{
				Cidr:              DerefString(c.Cidr),
				UsedIpsCount:      DerefString(c.UsedIpsCount),
				AvailableIpsCount: DerefString(c.AvailableIpsCount),
			})
		}
		for _, r := range u.RangeUsage {
			item.Spec.RangeUsage = append(item.Spec.RangeUsage, easv1alpha1.RangeUsageDetails{
				Start:             DerefString(r.Range_),
				UsedIpsCount:      DerefString(r.UsedIpsCount),
				AvailableIpsCount: DerefString(r.AvailableIpsCount),
			})
		}
		items = append(items, item)
	}
	return items
}

// ConvertDhcpServerStatistics converts NSX DhcpServerStatistics to K8s SubnetDHCPServerConfigStats.
func ConvertDhcpServerStatistics(nsxStats *model.DhcpServerStatistics, name, namespace string) *easv1alpha1.SubnetDHCPServerConfigStats {
	stats := &easv1alpha1.SubnetDHCPServerConfigStats{
		TypeMeta: metav1.TypeMeta{
			APIVersion: easv1alpha1.GroupVersion.String(),
			Kind:       "SubnetDHCPServerConfigStats",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	if nsxStats == nil {
		return stats
	}

	stats.Spec = easv1alpha1.SubnetDHCPServerConfigStatsSpec{
		Acks:         DerefInt64(nsxStats.Acks),
		Declines:     DerefInt64(nsxStats.Declines),
		DhcpServerId: DerefString(nsxStats.DhcpServerId),
		Discovers:    DerefInt64(nsxStats.Discovers),
		Errors:       DerefInt64(nsxStats.Errors),
		Informs:      DerefInt64(nsxStats.Informs),
		Nacks:        DerefInt64(nsxStats.Nacks),
		Offers:       DerefInt64(nsxStats.Offers),
		Releases:     DerefInt64(nsxStats.Releases),
	}
	for _, p := range nsxStats.IpPoolStats {
		stats.Spec.IpPoolStats = append(stats.Spec.IpPoolStats, easv1alpha1.DhcpIpPoolUsage{
			AllocatedNumber:     DerefInt64(p.AllocatedNumber),
			AllocatedPercentage: DerefInt64(p.AllocatedPercentage),
			ConsumedNumber:      DerefInt64(p.ConsumedNumber),
			DhcpIpPoolId:        DerefString(p.DhcpIpPoolId),
			PoolSize:            DerefInt64(p.PoolSize),
		})
	}
	return stats
}

func convertIpPoolRanges(nsxRanges []model.IpPoolRange) []easv1alpha1.IpPoolRange {
	if len(nsxRanges) == 0 {
		return nil
	}
	ranges := make([]easv1alpha1.IpPoolRange, 0, len(nsxRanges))
	for _, r := range nsxRanges {
		ranges = append(ranges, easv1alpha1.IpPoolRange{
			Start: DerefString(r.Start),
			End:   DerefString(r.End),
		})
	}
	return ranges
}

func ipBlockUsageName(intentPath string, index int) string {
	if intentPath != "" {
		parts := util.SplitPathSegments(intentPath)
		if len(parts) > 0 {
			name := parts[len(parts)-1]
			// If a path contains projects, prefix with the project name.
			// e.g., /orgs/default/projects/Dev_project/infra/ip-blocks/xxx -> Dev_project:xxx
			for i, p := range parts {
				if p == "projects" && i+1 < len(parts) {
					project := parts[i+1]
					normalizedName := name
					if strings.HasPrefix(normalizedName, project+"-") {
						normalizedName = strings.TrimPrefix(normalizedName, project+"-")
					} else if strings.HasPrefix(normalizedName, project+"_") {
						normalizedName = strings.TrimPrefix(normalizedName, project+"_")
					}
					return project + ":" + normalizedName
				}
			}
			return name
		}
	}
	return fmt.Sprintf("ipblock-%d", index)
}

// DerefString safely dereferences a *string, returning empty string if nil.
func DerefString(s *string) string {
	if s != nil {
		return *s
	}
	return ""
}

// DerefInt64 safely dereferences a *int64, returning 0 if nil.
func DerefInt64(i *int64) int64 {
	if i != nil {
		return *i
	}
	return 0
}
