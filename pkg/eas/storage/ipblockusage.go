/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package storage

import (
	"context"
	"fmt"
	"strings"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

const (
	nsxIPAddressTypeIPv4 = "IPV4"
	nsxIPAddressTypeIPv6 = "IPV6"
	crIPAddressTypeIPv4  = easv1alpha1.IPv4
	crIPAddressTypeIPv6  = easv1alpha1.IPv6
)

// IPBlockUsageStorage implements REST operations for IPBlockUsage.
type IPBlockUsageStorage struct {
	nsxClient  *nsx.Client
	vpcService eas.VPCInfoProvider
}

// NewIPBlockUsageStorage creates a new storage instance.
func NewIPBlockUsageStorage(nsxClient *nsx.Client, vpcService eas.VPCInfoProvider) *IPBlockUsageStorage {
	return &IPBlockUsageStorage{
		nsxClient:  nsxClient,
		vpcService: vpcService,
	}
}

// Get retrieves IP block usage for a single IP block identified by name.
//
// If name starts with ":", it is treated as an NSX infra IP block ID (global scope).
// Otherwise it is treated as a VPC-scoped IP block: the namespace's VPC is resolved
// and /orgs/{org}/projects/{project}/vpcs/{vpc}/ip-blocks/usage is queried from NSX.
// The returned object has metadata.name set to the original name.
func (s *IPBlockUsageStorage) Get(_ context.Context, namespace, name string) (*easv1alpha1.IPBlockUsage, error) {
	log := logger.Log

	// Infra scope: name starts with ":"
	if strings.HasPrefix(name, ":") {
		blockID := name[1:]
		if blockID == "" {
			return nil, fmt.Errorf("invalid infra IP block identifier %q: expected format ':<ipBlockID>'", name)
		}
		log.Debug("Fetching infra IP block usage from NSX", "namespace", namespace, "ipBlockID", blockID)
		nsxUsage, err := s.nsxClient.InfraIPBlockUsageClient.Get(blockID)
		if err != nil {
			return nil, HandleEASError(err, "ipblockusages", name, fmt.Errorf("failed to get infra IP block usage for block %s: %w", blockID, err))
		}
		return ConvertIpAddressBlockUsage(&nsxUsage, name, namespace), nil
	}

	// VPC scope: resolve via namespace VPC
	blockID := name
	vpcInfos := s.vpcService.ListVPCInfo(namespace)
	if len(vpcInfos) == 0 {
		return nil, HandleEASError(k8serrors.NewNotFound(schema.GroupResource{Group: easv1alpha1.GroupVersion.Group, Resource: "ipblockusages"}, name), "ipblockusages", name, nil)
	}

	seenVPC := make(map[string]struct{})
	for _, entry := range vpcInfos {
		info := entry.Info
		orgID, pid, vpcID := info.OrgID, info.ProjectID, info.VPCID
		if orgID == "" || pid == "" || vpcID == "" {
			continue
		}
		if _, ok := seenVPC[vpcID]; ok {
			continue
		}
		seenVPC[vpcID] = struct{}{}

		log.Debug("Fetching VPC IP block usage from NSX", "namespace", namespace, "vpcID", vpcID, "ipBlockID", blockID)
		nsxList, err := s.nsxClient.VPCIPBlockUsageClient.List(orgID, pid, vpcID, nil)
		if err != nil {
			return nil, HandleEASError(err, "ipblockusages", name, fmt.Errorf("failed to get IP block usage for VPC %s, block %s: %w", vpcID, blockID, err))
		}
		items := ConvertIpAddressBlockUsageList(&nsxList, pid, namespace)
		for _, item := range items {
			if item.Name == blockID {
				return &item, nil
			}
		}
	}

	return nil, HandleEASError(k8serrors.NewNotFound(schema.GroupResource{Group: easv1alpha1.GroupVersion.Group, Resource: "ipblockusages"}, blockID), "ipblockusages", blockID, nil)
}

// List retrieves IP block usage for all IP blocks used by the VPCs associated with the namespace.
// It resolves the VPC entries in the namespace and calls
// /orgs/{org}/projects/{project}/vpcs/{vpc}/ip-blocks/usage for each unique VPC.
// metadata.name per item uses the block ID (last path segment) regardless of scope.
func (s *IPBlockUsageStorage) List(_ context.Context, namespace string) (*easv1alpha1.IPBlockUsageList, error) {
	log := logger.Log
	vpcInfos := s.vpcService.ListVPCInfo(namespace)
	log.Debug("Listing IP block usage", "namespace", namespace, "vpcCount", len(vpcInfos))

	emptyList := &easv1alpha1.IPBlockUsageList{
		TypeMeta: metav1.TypeMeta{APIVersion: easv1alpha1.GroupVersion.String(), Kind: "IPBlockUsageList"},
		Items:    make([]easv1alpha1.IPBlockUsage, 0),
	}
	if len(vpcInfos) == 0 {
		return emptyList, nil
	}

	list := &easv1alpha1.IPBlockUsageList{
		TypeMeta: metav1.TypeMeta{
			APIVersion: easv1alpha1.GroupVersion.String(),
			Kind:       "IPBlockUsageList",
		},
		Items: make([]easv1alpha1.IPBlockUsage, 0),
	}

	// Deduplicate by VPC ID: multiple entries may reference the same VPC.
	seenVPC := make(map[string]struct{})
	seenBlocks := make(map[string]struct{})
	for _, entry := range vpcInfos {
		info := entry.Info
		orgID, pid, vpcID := info.OrgID, info.ProjectID, info.VPCID
		if orgID == "" || pid == "" || vpcID == "" {
			continue
		}
		if _, ok := seenVPC[vpcID]; ok {
			continue
		}
		seenVPC[vpcID] = struct{}{}

		log.Debug("Fetching VPC IP block usage from NSX", "orgID", orgID, "projectID", pid, "vpcID", vpcID)
		nsxList, err := s.nsxClient.VPCIPBlockUsageClient.List(orgID, pid, vpcID, nil)
		if err != nil {
			return nil, HandleEASError(err, "ipblockusages", "", fmt.Errorf("failed to list IP block usage for VPC %s: %w", vpcID, err))
		}
		items := ConvertIpAddressBlockUsageList(&nsxList, pid, namespace)
		log.Debug("Got VPC IP block usage", "vpcID", vpcID, "itemCount", len(items))
		for _, item := range items {
			if _, ok := seenBlocks[item.Name]; ok {
				continue
			}
			seenBlocks[item.Name] = struct{}{}
			list.Items = append(list.Items, item)
		}
	}

	return list, nil
}

// ConvertIpAddressBlockUsage converts a single NSX IpAddressBlockUsage to a K8s IPBlockUsage.
// name is used verbatim as ObjectMeta.Name.
func ConvertIpAddressBlockUsage(nsxUsage *model.IpAddressBlockUsage, name, namespace string) *easv1alpha1.IPBlockUsage {
	item := &easv1alpha1.IPBlockUsage{
		TypeMeta: metav1.TypeMeta{
			APIVersion: easv1alpha1.GroupVersion.String(),
			Kind:       "IPBlockUsage",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	if nsxUsage == nil {
		return item
	}
	item.UsedIPRanges = nsxUsage.UsedIpRanges
	item.AvailableIPRanges = nsxUsage.AvailableIpRanges
	item.UsedIPsCount = derefCount(nsxUsage.UsedIpsCount)
	item.AvailableIPsCount = derefCount(nsxUsage.AvailableIpsCount)
	item.OverallIPsCount = derefCount(nsxUsage.OverallIpsCount)
	item.Visibility = toIPAddressVisibility(DerefString(nsxUsage.Visibility))
	if nsxUsage.AddressType != nil {
		item.AddressType = toIPAddressType(*nsxUsage.AddressType)
	}
	for _, c := range nsxUsage.CidrUsage {
		item.CIDRUsages = append(item.CIDRUsages, easv1alpha1.CIDRUsage{
			CIDR: DerefString(c.Cidr),
			UsageDetails: easv1alpha1.UsageDetails{
				UsedIPRanges:        c.UsedIpRanges,
				OverallUsedIPRanges: c.OverallUsedIpRanges,
				AvailableIPRanges:   c.AvailableIpRanges,
				UsedIPsCount:        derefCount(c.UsedIpsCount),
				OverallUsedIPsCount: derefCount(c.OverallUsedIpsCount),
				AvailableIPsCount:   derefCount(c.AvailableIpsCount),
			},
		})
	}
	for _, r := range nsxUsage.RangeUsage {
		item.RangeUsages = append(item.RangeUsages, easv1alpha1.RangeUsage{
			Range: DerefString(r.Range_),
			UsageDetails: easv1alpha1.UsageDetails{
				UsedIPRanges:        r.UsedIpRanges,
				OverallUsedIPRanges: r.OverallUsedIpRanges,
				AvailableIPRanges:   r.AvailableIpRanges,
				UsedIPsCount:        derefCount(r.UsedIpsCount),
				OverallUsedIPsCount: derefCount(r.OverallUsedIpsCount),
				AvailableIPsCount:   derefCount(r.AvailableIpsCount),
			},
		})
	}
	return item
}

// ConvertIpAddressBlockUsageList converts NSX IpAddressBlockUsageList to K8s IPBlockUsage values.
// projectID is the caller-supplied project context used to generate metadata.name when an item's
// IntentPath is absent; pass an empty string for infra-level queries.
func ConvertIpAddressBlockUsageList(nsxList *model.IpAddressBlockUsageList, projectID, namespace string) []easv1alpha1.IPBlockUsage {
	if nsxList == nil {
		return nil
	}

	items := make([]easv1alpha1.IPBlockUsage, 0, len(nsxList.Results))
	for i, u := range nsxList.Results {
		name := ipBlockUsageName(DerefString(u.IntentPath), projectID, i)
		item := ConvertIpAddressBlockUsage(&u, name, namespace)
		items = append(items, *item)
	}
	return items
}

// derefCount dereferences a *string count field, returning "0" when nil so
// that zero-count values are always present in API output rather than omitted.
func derefCount(s *string) string {
	if s != nil {
		return *s
	}
	return "0"
}

// ipBlockUsageName derives a metadata.name from the NSX intent path.
// It returns the last path segment (the block ID) for project-scoped blocks.
// For infra-scoped blocks, it prepends a ":" to the block ID.
// The project context is implicit from the namespace / VPC and does not need to be encoded in the name.
func ipBlockUsageName(intentPath, projectID string, index int) string {
	if intentPath != "" {
		parts := splitPolicyPath(intentPath)
		if len(parts) > 0 {
			name := parts[len(parts)-1]
			if strings.HasPrefix(intentPath, "/infra/") {
				return ":" + name
			}
			return name
		}
	}
	return fmt.Sprintf("ipblock-%d", index)
}

func splitPolicyPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// toIPAddressType converts NSX address type string to K8s IPAddressType.
func toIPAddressType(t string) easv1alpha1.IPAddressType {
	if strings.EqualFold(t, nsxIPAddressTypeIPv4) {
		return crIPAddressTypeIPv4
	}
	if strings.EqualFold(t, nsxIPAddressTypeIPv6) {
		return crIPAddressTypeIPv6
	}
	logger.Log.Warn("Unknown IP address type, defaulting to IPv4", "unknownType", t)
	return crIPAddressTypeIPv4
}
