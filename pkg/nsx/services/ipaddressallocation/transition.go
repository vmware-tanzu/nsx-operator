/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package ipaddressallocation

import (
	"fmt"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

// GetIPAddressAllocationForTransition resolves a current CR UID. Name-based
// recovery is allowed only when no UID is supplied; absence returns nil, nil.
func (service *IPAddressAllocationService) GetIPAddressAllocationForTransition(uid types.UID, ns, name string) (*model.VpcIpAddressAllocation, error) {
	if len(uid) > 0 {
		unlock := service.readAllocationUID(uid)
		defer unlock()
		return service.indexedIPAddressAllocation(uid)
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
	return nil, nil
}

// AdoptIPAddressAllocationTags performs an in-place NSX Policy tag update on an existing VpcIpAddressAllocation
// to transition ownership from source to target namespace and CR without releasing or reallocating the VIP.
func (service *IPAddressAllocationService) AdoptIPAddressAllocationTags(existingAlloc *model.VpcIpAddressAllocation, targetNs, targetNsUID, targetName string, targetUID types.UID) (*model.VpcIpAddressAllocation, error) {
	if existingAlloc == nil || existingAlloc.Path == nil || existingAlloc.Id == nil {
		return nil, fmt.Errorf("invalid existing NSX allocation")
	}
	if targetUID == "" {
		return nil, fmt.Errorf("target IPAddressAllocation UID is required")
	}
	sourceUID := getOwnerUID(existingAlloc)
	unlock := service.lockAllocationUIDs(types.UID(sourceUID), targetUID)
	defer unlock()

	// Lookups can precede another handoff. Recheck both owners under the shared
	// allocation locks before using that snapshot to overwrite NSX ownership.
	current := service.ipAddressAllocationStore.GetByKey(*existingAlloc.Id)
	if current == nil {
		return nil, fmt.Errorf("source allocation %s is no longer in the store", *existingAlloc.Id)
	}
	currentAlloc := current.(*model.VpcIpAddressAllocation)
	currentUID := getOwnerUID(currentAlloc)
	if currentUID != sourceUID && currentUID != string(targetUID) {
		return nil, fmt.Errorf("allocation %s ownership changed during transition", *existingAlloc.Id)
	}
	targetAlloc, err := service.indexedIPAddressAllocation(targetUID)
	if err != nil {
		return nil, err
	}
	if targetAlloc != nil && (targetAlloc.Path == nil || *targetAlloc.Path != *existingAlloc.Path) {
		return nil, fmt.Errorf("target UID %s already owns another allocation", targetUID)
	}
	if currentUID == string(targetUID) {
		if !util.IPAddressAllocationHasOwner(currentAlloc, targetUID, targetNs, targetName) {
			return nil, fmt.Errorf("target allocation identity does not match %s/%s", targetNs, targetName)
		}
		return currentAlloc, nil
	}
	vpcResourceInfo, err := common.ParseVPCResourcePath(*existingAlloc.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to parse VPC path from %s: %w", *existingAlloc.Path, err)
	}

	updatedAlloc := *existingAlloc
	updatedAlloc.Tags = service.buildTransitionTargetTags(targetNs, targetNsUID, targetName, targetUID,
		nsxutil.FindTag(existingAlloc.Tags, common.TagScopeIPAllocLB) == "true")

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

	// Publish target ownership only after a successful PATCH is confirmed by GET.
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
	if newAlloc.Path == nil || *newAlloc.Path != *existingAlloc.Path ||
		newAlloc.AllocationIps == nil || existingAlloc.AllocationIps == nil || *newAlloc.AllocationIps != *existingAlloc.AllocationIps ||
		!util.IPAddressAllocationHasOwner(&newAlloc, targetUID, targetNs, targetName) {
		return nil, fmt.Errorf("NSX allocation %s adoption is not confirmed", *existingAlloc.Id)
	}

	if err := service.ipAddressAllocationStore.Apply(&newAlloc); err != nil {
		log.Error(err, "Failed to update IPAddressAllocation store after tag adoption")
		return nil, err
	}

	log.Info("Successfully adopted NSX IPAddressAllocation tags", "Id", *existingAlloc.Id, "targetNs", targetNs, "targetName", targetName)
	return &newAlloc, nil
}
