package ipaddressallocation

import (
	"context"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

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
			return nil
		}
		// Delete resources from the store and return.
		service.ipAddressAllocationStore.DeleteMultipleObjects(allocations)
		return nil
	}

	allocations := make([]*model.VpcIpAddressAllocation, 0)
	// Mark the resources for delete.
	for _, obj := range service.ipAddressAllocationStore.List() {
		allocation := obj.(*model.VpcIpAddressAllocation)
		allocation.MarkedForDelete = &MarkedForDelete
		allocations = append(allocations, allocation)
	}

	return service.builder.PagingDeleteResources(ctx, allocations, common.DefaultHAPIChildrenCount, service.NSXClient, func(deletedObjs []*model.VpcIpAddressAllocation) {
		service.ipAddressAllocationStore.DeleteMultipleObjects(deletedObjs)
	})
}
