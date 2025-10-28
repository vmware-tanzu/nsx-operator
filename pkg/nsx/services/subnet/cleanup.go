package subnet

import (
	"context"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// SetCleanupFilters sets the targetNamespace and targetVPC filters for selective cleanup
func (service *SubnetService) SetCleanupFilters(targetNamespace, targetVPC string) {
	service.targetNamespace = targetNamespace
	service.targetVPC = targetVPC
}

// shouldCleanResource checks if a resource should be cleaned based on namespace/VPC filtering
func (service *SubnetService) shouldCleanResource(path *string, tags []model.Tag) bool {
	return common.ShouldCleanResource(service.targetNamespace, service.targetVPC, path, tags)
}

// CleanupVPCChildResources is deleting all the NSX VpcSubnets in the given vpcPath on NSX and/or in the local cache.
// If vpcPath is not empty, the function is called with an auto-created VPC case, so it only deletes in the local cache for
// the NSX resources are already removed when VPC is deleted recursively. Otherwise, it should delete all cached VpcSubnets
// on NSX and in the local cache.
func (service *SubnetService) CleanupVPCChildResources(ctx context.Context, vpcPath string) error {
	if vpcPath != "" {
		subnets := service.SubnetStore.GetByIndex(common.IndexByVPCPathFuncKey, vpcPath)
		if len(subnets) == 0 {
			log.Info("No VpcSubnets found for VPC", "vpcPath", vpcPath, "count", 0)
			return nil
		}
		log.Info("Cleaning VpcSubnets from local store for auto-created VPC", "vpcPath", vpcPath, "count", len(subnets))
		// Delete resources from the store and return.
		service.SubnetStore.DeleteMultipleObjects(subnets)
		log.Info("Successfully cleaned VpcSubnets from local store", "vpcPath", vpcPath, "count", len(subnets), "status", "success")
		return nil
	}

	subnets := make([]*model.VpcSubnet, 0)
	// Mark the resources for delete.
	for _, obj := range service.SubnetStore.List() {
		subnet := obj.(*model.VpcSubnet)
		// Filter by namespace/VPC if specified
		if service.shouldCleanResource(subnet.Path, subnet.Tags) {
			subnet.MarkedForDelete = &MarkedForDelete
			subnets = append(subnets, subnet)
			if service.targetNamespace != "" || service.targetVPC != "" {
				log.Info("Marking subnet for deletion", "path", *subnet.Path, "name", *subnet.DisplayName)
			}
		} else {
			log.Info("Skipping subnet (not in target)", "path", *subnet.Path, "targetNamespace", service.targetNamespace, "targetVPC", service.targetVPC)
		}
	}

	log.Info("Cleaning up VpcSubnets from pre-created VPC", "count", len(subnets))
	return service.builder.PagingUpdateResources(ctx, subnets, common.DefaultHAPIChildrenCount, service.NSXClient, func(deletedObjects []*model.VpcSubnet) {
		service.SubnetStore.DeleteMultipleObjects(deletedObjects)
	})
}
