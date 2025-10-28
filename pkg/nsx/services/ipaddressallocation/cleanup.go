package ipaddressallocation

import (
	"context"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// SetCleanupFilters sets the targetNamespace and targetVPC filters for selective cleanup
func (service *IPAddressAllocationService) SetCleanupFilters(targetNamespace, targetVPC string) {
	service.targetNamespace = targetNamespace
	service.targetVPC = targetVPC
}

// shouldCleanResource checks if a resource should be cleaned based on namespace/VPC filtering
func (service *IPAddressAllocationService) shouldCleanResource(path *string, tags []model.Tag) bool {
	return common.ShouldCleanResource(service.targetNamespace, service.targetVPC, path, tags)
}

// CleanupVPCChildResources is deleting all the NSX VpcIPAddressAllocations in the given vpcPath on NSX and/or in local cache.
// If vpcPath is not empty, the function is called with an auto-created VPC case, so it only deletes in the local cache for
// the NSX resources are already removed when VPC is deleted recursively. Otherwise, it should delete all cached
// VpcIPAddressAllocations on NSX and in local cache.
func (service *IPAddressAllocationService) CleanupVPCChildResources(ctx context.Context, vpcPath string) error {
	if vpcPath != "" {
		allocations, err := service.ipAddressAllocationStore.GetByVPCPath(vpcPath)
		if err != nil {
			log.Error(err, "Failed to list VpcIPAddressAllocations under the VPC", "path", vpcPath)
		}
		if len(allocations) == 0 {
			log.Info("No VpcIPAddressAllocations found for VPC", "vpcPath", vpcPath, "count", 0)
			return nil
		}
		log.Info("Cleaning VpcIPAddressAllocations from local store for auto-created VPC", "vpcPath", vpcPath, "count", len(allocations))
		// Delete resources from the store and return.
		service.ipAddressAllocationStore.DeleteMultipleObjects(allocations)
		log.Info("Successfully cleaned VpcIPAddressAllocations from local store", "vpcPath", vpcPath, "count", len(allocations), "status", "success")
		return nil
	}

	allocations := make([]*model.VpcIpAddressAllocation, 0)
	// Mark the resources for delete.
	for _, obj := range service.ipAddressAllocationStore.List() {
		allocation := obj.(*model.VpcIpAddressAllocation)
		if service.shouldCleanResource(allocation.Path, allocation.Tags) {
			allocation.MarkedForDelete = &MarkedForDelete
			allocations = append(allocations, allocation)
			if service.targetNamespace != "" || service.targetVPC != "" {
				log.Info("Marking IP allocation for deletion", "path", *allocation.Path, "name", *allocation.DisplayName)
			}
		} else {
			log.Info("Skipping IP allocation (not in target)", "path", *allocation.Path, "targetNamespace", service.targetNamespace, "targetVPC", service.targetVPC)
		}
	}

	log.Info("Cleaning up VpcIPAddressAllocations from pre-created VPC", "count", len(allocations))
	return service.builder.PagingUpdateResources(ctx, allocations, common.DefaultHAPIChildrenCount, service.NSXClient, func(deletedObjs []*model.VpcIpAddressAllocation) {
		service.ipAddressAllocationStore.DeleteMultipleObjects(deletedObjs)
	})
}
