/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package storage

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

// VPCIPAddressUsageStorage implements REST operations for VPCIPAddressUsage.
type VPCIPAddressUsageStorage struct {
	nsxClient  *nsx.Client
	vpcService eas.VPCInfoProvider
}

// NewVPCIPAddressUsageStorage creates a new storage instance.
func NewVPCIPAddressUsageStorage(nsxClient *nsx.Client, vpcService eas.VPCInfoProvider) *VPCIPAddressUsageStorage {
	return &VPCIPAddressUsageStorage{
		nsxClient:  nsxClient,
		vpcService: vpcService,
	}
}

// Get retrieves IP address usage for the VPC identified by vpcName within the namespace.
// vpcName must be the NSX VPC ID (last segment of the VPC policy path, e.g. "sean-ns_2oq3d").
// The returned object's metadata.name is the NSX VPC ID from the resolved VPC path.
func (s *VPCIPAddressUsageStorage) Get(_ context.Context, namespace, vpcName string) (*easv1alpha1.VPCIPAddressUsage, error) {
	log := logger.Log
	vpcEntries := s.vpcService.ListVPCInfo(namespace)
	if len(vpcEntries) == 0 {
		return nil, fmt.Errorf("no VPC found for namespace %s", namespace)
	}

	for _, entry := range vpcEntries {
		if !vpcMatchesByName(entry, vpcName) {
			continue
		}
		info := entry.Info
		log.Debug("Fetching VPC IP address usage from NSX",
			"namespace", namespace, "vpcName", vpcName,
			"vpcID", info.VPCID, "projectID", info.ProjectID)
		nsxBlocks, err := s.nsxClient.IPAddressUsageClient.Get(info.OrgID, info.ProjectID, info.VPCID)
		if err != nil {
			return nil, fmt.Errorf("failed to get VPC IP address usage from NSX: %w", err)
		}
		return ConvertVpcIpAddressBlocks(&nsxBlocks, info.VPCID, namespace), nil
	}

	return nil, fmt.Errorf("VPC %q not found for namespace %s", vpcName, namespace)
}

// List retrieves IP address usage for all VPCs associated with the given namespace.
// Each returned item's metadata.name is the NSX VPC ID.
func (s *VPCIPAddressUsageStorage) List(_ context.Context, namespace string) (*easv1alpha1.VPCIPAddressUsageList, error) {
	log := logger.Log
	vpcEntries := s.vpcService.ListVPCInfo(namespace)
	log.Debug("Listing VPC IP address usage", "namespace", namespace, "vpcCount", len(vpcEntries))

	list := &easv1alpha1.VPCIPAddressUsageList{
		TypeMeta: metav1.TypeMeta{
			APIVersion: easv1alpha1.GroupVersion.String(),
			Kind:       "VPCIPAddressUsageList",
		},
		Items: make([]easv1alpha1.VPCIPAddressUsage, 0),
	}

	for _, entry := range vpcEntries {
		info := entry.Info
		nsxBlocks, err := s.nsxClient.IPAddressUsageClient.Get(info.OrgID, info.ProjectID, info.VPCID)
		if err != nil {
			return nil, fmt.Errorf("failed to get VPC IP address usage for VPC %s: %w", info.VPCID, err)
		}
		usage := ConvertVpcIpAddressBlocks(&nsxBlocks, info.VPCID, namespace)
		list.Items = append(list.Items, *usage)
	}

	return list, nil
}

// vpcMatchesByName returns true if the entry's NSX VPC ID matches name.
func vpcMatchesByName(entry eas.VPCEntry, name string) bool {
	return entry.Info.VPCID == name
}

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
		visibility := toIPAddressVisibility(DerefString(b.Visibility))
		ipBlockName := policyPathLeaf(DerefString(b.Path))
		if visibility == easv1alpha1.External {
			ipBlockName = ":" + ipBlockName
		}
		block := easv1alpha1.VPCIPAddressBlock{
			IPBlockName:    ipBlockName,
			CIDRs:          b.Cidrs,
			Available:      DerefInt64(b.Available),
			Total:          DerefInt64(b.Total),
			PercentageUsed: DerefString(b.PercentageUsed),
			Visibility:     visibility,
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

// DerefString safely dereferences a *string, returning empty string if nil.
func DerefString(s *string) string {
	if s != nil {
		return *s
	}
	return ""
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

// DerefInt64 safely dereferences a *int64, returning 0 if nil.
func DerefInt64(i *int64) int64 {
	if i != nil {
		return *i
	}
	return 0
}
