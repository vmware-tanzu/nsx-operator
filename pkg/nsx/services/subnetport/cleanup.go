package subnetport

import (
	"context"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// SetCleanupFilters sets the targetNamespace and targetVPC filters for selective cleanup
func (service *SubnetPortService) SetCleanupFilters(targetNamespace, targetVPC string) {
	service.targetNamespace = targetNamespace
	service.targetVPC = targetVPC
}

// shouldCleanResource checks if a resource should be cleaned based on namespace/VPC filtering
func (service *SubnetPortService) shouldCleanResource(path *string, tags []model.Tag) bool {
	return common.ShouldCleanResource(service.targetNamespace, service.targetVPC, path, tags)
}

func (service *SubnetPortService) CleanupBeforeVPCDeletion(ctx context.Context) error {
	objs := service.SubnetPortStore.List()
	log.Info("Cleaning up VpcSubnetPorts", "Count", len(objs), "status", "attempting")
	if len(objs) == 0 {
		log.Info("No VpcSubnetPorts found to clean up", "count", 0)
		return nil
	}

	// Mark the resources for delete.
	ports := make([]*model.VpcSubnetPort, 0)
	for _, obj := range objs {
		port := obj.(*model.VpcSubnetPort)
		if service.shouldCleanResource(port.Path, port.Tags) {
			port.MarkedForDelete = &MarkedForDelete
			ports = append(ports, port)
			if service.targetNamespace != "" || service.targetVPC != "" {
				log.Info("Marking subnet port for deletion", "path", *port.Path, "name", *port.DisplayName)
			}
		} else {
			log.Info("Skipping subnet port (not in target)", "path", *port.Path, "targetNamespace", service.targetNamespace, "targetVPC", service.targetVPC)
		}
	}
	log.Info("Starting deletion of VpcSubnetPorts", "count", len(ports))
	err := service.builder.PagingUpdateResources(ctx, ports, common.DefaultHAPIChildrenCount, service.NSXClient, func(delObjs []*model.VpcSubnetPort) {
		service.SubnetPortStore.DeleteMultipleObjects(delObjs)
	})
	if err != nil {
		log.Error(err, "Failed to clean up VpcSubnetPorts", "count", len(ports), "status", "failed")
		return err
	}
	log.Info("Successfully cleaned up VpcSubnetPorts", "count", len(ports), "status", "success")
	return nil
}
