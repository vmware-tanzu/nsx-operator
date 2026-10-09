/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package ipaddressallocation

import (
	"fmt"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

// GetIPAddressAllocationForTransition locates an existing NSX VpcIpAddressAllocation either by CR UID
// or by matching its namespace and cr_name tags.
func (service *IPAddressAllocationService) GetIPAddressAllocationForTransition(uid types.UID, ns, name string) (*model.VpcIpAddressAllocation, error) {
	if len(uid) > 0 {
		alloc, err := service.indexedIPAddressAllocation(uid)
		if err == nil && alloc != nil {
			return alloc, nil
		}
	}
	// Fallback to searching store by namespace and cr_name tags
	for _, item := range service.ipAddressAllocationStore.List() {
		alloc, ok := item.(*model.VpcIpAddressAllocation)
		if !ok || alloc == nil {
			continue
		}
		nsMatch, nameMatch := false, false
		for _, tag := range alloc.Tags {
			if tag.Scope != nil && *tag.Scope == common.TagScopeNamespace && tag.Tag != nil && *tag.Tag == ns {
				nsMatch = true
			}
			if tag.Scope != nil && *tag.Scope == common.TagScopeIPAddressAllocationCRName && tag.Tag != nil && *tag.Tag == name {
				nameMatch = true
			}
		}
		if nsMatch && nameMatch {
			return alloc, nil
		}
	}
	return nil, fmt.Errorf("ipaddressallocation not found in store for %s/%s (UID: %s)", ns, name, uid)
}

// AdoptIPAddressAllocationTags performs an in-place NSX Policy tag update on an existing VpcIpAddressAllocation
// to transition ownership from source to target namespace and CR without releasing or reallocating the VIP.
func (service *IPAddressAllocationService) AdoptIPAddressAllocationTags(existingAlloc *model.VpcIpAddressAllocation, targetNs, targetNsUID, targetName string, targetUID types.UID) (*model.VpcIpAddressAllocation, error) {
	if existingAlloc == nil || existingAlloc.Path == nil || existingAlloc.Id == nil {
		return nil, fmt.Errorf("invalid existing NSX allocation")
	}
	vpcResourceInfo, err := common.ParseVPCResourcePath(*existingAlloc.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to parse VPC path from %s: %w", *existingAlloc.Path, err)
	}

	// Construct updated tags
	newTags := []model.Tag{
		{
			Scope: String(common.TagScopeCluster),
			Tag:   String(service.NSXConfig.Cluster),
		},
		{
			Scope: String(common.TagScopeVersion),
			Tag:   String(strings.Join(common.TagValueVersion, ".")),
		},
		{
			Scope: String(common.TagScopeNamespace),
			Tag:   String(targetNs),
		},
		{
			Scope: String(common.TagScopeIPAddressAllocationCRName),
			Tag:   String(targetName),
		},
		{
			Scope: String(common.TagScopeIPAddressAllocationCRUID),
			Tag:   String(string(targetUID)),
		},
	}
	if len(targetNsUID) > 0 {
		newTags = append(newTags, model.Tag{
			Scope: String(common.TagScopeNamespaceUID),
			Tag:   String(targetNsUID),
		})
	}
	// Preserve lb tag if present
	if nsxutil.FindTag(existingAlloc.Tags, common.TagScopeIPAllocLB) == "true" {
		newTags = append(newTags, model.Tag{
			Scope: String(common.TagScopeIPAllocLB),
			Tag:   String("true"),
		})
	}

	updatedAlloc := *existingAlloc
	updatedAlloc.Tags = newTags
	updatedAlloc.DisplayName = String(util.GenerateTruncName(common.MaxNameLength, targetName, "", "", "", ""))

	// Pre-apply to local store immediately so that any concurrent store lookups by target UID succeed without delay.
	if err := service.ipAddressAllocationStore.Apply(&updatedAlloc); err != nil {
		log.Error(err, "Failed to pre-update IPAddressAllocation store before tag adoption")
	}

	errPatch := service.NSXClient.IPAddressAllocationClient.Patch(
		vpcResourceInfo.OrgID,
		vpcResourceInfo.ProjectID,
		vpcResourceInfo.VPCID,
		*existingAlloc.Id,
		updatedAlloc,
	)
	errPatch = nsxutil.TransNSXApiError(errPatch)
	if errPatch != nil {
		log.Error(errPatch, "Failed to patch tags on existing NSX IPAddressAllocation for transition", "Id", *existingAlloc.Id)
		return nil, errPatch
	}

	newAlloc, errGet := service.NSXClient.IPAddressAllocationClient.Get(
		vpcResourceInfo.OrgID,
		vpcResourceInfo.ProjectID,
		vpcResourceInfo.VPCID,
		*existingAlloc.Id,
	)
	errGet = nsxutil.TransNSXApiError(errGet)
	if errGet != nil {
		log.Error(errGet, "Failed to fetch updated NSX IPAddressAllocation after tag adoption", "Id", *existingAlloc.Id)
		return nil, errGet
	}

	if err := service.ipAddressAllocationStore.Apply(&newAlloc); err != nil {
		log.Error(err, "Failed to update IPAddressAllocation store after tag adoption")
		return nil, err
	}

	log.Info("Successfully adopted NSX IPAddressAllocation tags", "Id", *existingAlloc.Id, "targetNs", targetNs, "targetName", targetName)
	return &newAlloc, nil
}
