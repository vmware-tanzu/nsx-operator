/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package converter

import (
	"fmt"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
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

	blocks := make([]easv1alpha1.VPCIPAddressBlock, 0, len(nsxBlocks.IpBlocks))
	for _, b := range nsxBlocks.IpBlocks {
		block := easv1alpha1.VPCIPAddressBlock{
			IPBlockName:    policyPathLeaf(DerefString(b.Path)),
			CIDR:           DerefString(b.Cidr),
			CIDRs:          b.Cidrs,
			Available:      DerefInt64(b.Available),
			Total:          DerefInt64(b.Total),
			PercentageUsed: DerefString(b.PercentageUsed),
			Visibility:     toIPAddressVisibility(DerefString(b.Visibility)),
		}
		block.ExcludedIPs = convertIPPoolRanges(b.ExcludedIps)
		block.Ranges = convertIPPoolRanges(b.Ranges)
		if b.AllocatedByVpc != nil {
			ab := easv1alpha1.AllocatedByVPC{
				AccessMode:     toAccessMode(DerefString(b.AllocatedByVpc.AccessMode)),
				Count:          DerefInt64(b.AllocatedByVpc.Count),
				PercentageUsed: DerefString(b.AllocatedByVpc.PercentageUsed),
			}
			for _, ip := range b.AllocatedByVpc.IpAddresses {
				ab.IPAddresses = append(ab.IPAddresses, vpcIPAddressFromNSX(ip))
			}
			block.AllocatedByVPC = ab
		}
		blocks = append(blocks, block)
	}
	usage.IPBlocks = blocks
	return usage
}

// ConvertIpAddressBlockUsageList converts NSX IpAddressBlockUsageList to K8s IPBlockUsage values.
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
			UsedIPRanges:      u.UsedIpRanges,
			AvailableIPRanges: u.AvailableIpRanges,
			UsedIPsCount:      DerefString(u.UsedIpsCount),
			AvailableIPsCount: DerefString(u.AvailableIpsCount),
			OverallIPsCount:   DerefString(u.OverallIpsCount),
			Visibility:        toIPAddressVisibility(DerefString(u.Visibility)),
		}
		for _, c := range u.CidrUsage {
			item.CIDRUsages = append(item.CIDRUsages, easv1alpha1.CIDRUsage{
				CIDR: DerefString(c.Cidr),
				UsageDetails: easv1alpha1.UsageDetails{
					UsedIPRanges:        c.UsedIpRanges,
					OverallUsedIPRanges: c.OverallUsedIpRanges,
					AvailableIPRanges:   c.AvailableIpRanges,
					UsedIPsCount:        DerefString(c.UsedIpsCount),
					OverallUsedIPsCount: DerefString(c.OverallUsedIpsCount),
					AvailableIPsCount:   DerefString(c.AvailableIpsCount),
				},
			})
		}
		for _, r := range u.RangeUsage {
			item.RangeUsages = append(item.RangeUsages, easv1alpha1.RangeUsage{
				Range: DerefString(r.Range_),
				UsageDetails: easv1alpha1.UsageDetails{
					UsedIPRanges:        r.UsedIpRanges,
					OverallUsedIPRanges: r.OverallUsedIpRanges,
					AvailableIPRanges:   r.AvailableIpRanges,
					UsedIPsCount:        DerefString(r.UsedIpsCount),
					OverallUsedIPsCount: DerefString(r.OverallUsedIpsCount),
					AvailableIPsCount:   DerefString(r.AvailableIpsCount),
				},
			})
		}
		items = append(items, item)
	}
	return items
}

// ConvertDhcpServerStatistics converts NSX DhcpServerStatistics to K8s SubnetDHCPServerStats
// (fields must match eas.nsx.vmware.com_subnetdhcpserverstats).
func ConvertDhcpServerStatistics(nsxStats *model.DhcpServerStatistics, name, namespace string) *easv1alpha1.SubnetDHCPServerStats {
	stats := &easv1alpha1.SubnetDHCPServerStats{
		TypeMeta: metav1.TypeMeta{
			APIVersion: easv1alpha1.GroupVersion.String(),
			Kind:       "SubnetDHCPServerStats",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	if nsxStats == nil {
		return stats
	}
	for _, p := range nsxStats.IpPoolStats {
		stats.IPPoolStats = append(stats.IPPoolStats, easv1alpha1.DHCPIPPoolUsage{
			AllocatedNumber:     DerefInt64(p.AllocatedNumber),
			AllocatedPercentage: DerefInt64(p.AllocatedPercentage),
			ConsumedNumber:      DerefInt64(p.ConsumedNumber),
			PoolSize:            DerefInt64(p.PoolSize),
		})
	}
	return stats
}

func convertIPPoolRanges(nsxRanges []model.IpPoolRange) []easv1alpha1.IPPoolRange {
	if len(nsxRanges) == 0 {
		return nil
	}
	ranges := make([]easv1alpha1.IPPoolRange, 0, len(nsxRanges))
	for _, r := range nsxRanges {
		ranges = append(ranges, easv1alpha1.IPPoolRange{
			Start: DerefString(r.Start),
			End:   DerefString(r.End),
		})
	}
	return ranges
}

func vpcIPAddressFromNSX(ip model.VpcIpAddress) easv1alpha1.VPCIPAddress {
	out := easv1alpha1.VPCIPAddress{
		Address: DerefString(ip.Address),
	}
	path := DerefString(ip.Path)
	if path == "" {
		return out
	}
	lower := strings.ToLower(path)
	switch {
	case strings.Contains(lower, "ip-address-allocation"):
		out.IPAddressAllocationName = policyPathLeaf(path)
	case strings.Contains(lower, "/subnets/"):
		out.SubnetName = policyPathLeaf(path)
	default:
		out.IPAddressAllocationName = policyPathLeaf(path)
	}
	return out
}

func toIPAddressVisibility(v string) easv1alpha1.IPAddressVisibility {
	s := strings.TrimSpace(v)
	if s == "" {
		return ""
	}
	switch strings.ToUpper(s) {
	case "EXTERNAL":
		return easv1alpha1.External
	case "PRIVATE":
		return easv1alpha1.Private
	default:
		return easv1alpha1.IPAddressVisibility(s)
	}
}

func toAccessMode(s string) easv1alpha1.AccessMode {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	switch s {
	case string(easv1alpha1.PublicSubnet), string(easv1alpha1.PrivateTGWSubnet), string(easv1alpha1.PrivateSubnet):
		return easv1alpha1.AccessMode(s)
	}
	switch strings.ToUpper(strings.ReplaceAll(s, "-", "")) {
	case "PUBLIC":
		return easv1alpha1.PublicSubnet
	case "PRIVATETGW":
		return easv1alpha1.PrivateTGWSubnet
	case "PRIVATE":
		return easv1alpha1.PrivateSubnet
	default:
		return easv1alpha1.AccessMode(s)
	}
}

func policyPathLeaf(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		return ""
	}
	if i := strings.LastIndex(path, "/"); i >= 0 && i+1 < len(path) {
		return path[i+1:]
	}
	return path
}

func splitPolicyPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func ipBlockUsageName(intentPath string, index int) string {
	if intentPath != "" {
		parts := splitPolicyPath(intentPath)
		if len(parts) > 0 {
			name := parts[len(parts)-1]
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
