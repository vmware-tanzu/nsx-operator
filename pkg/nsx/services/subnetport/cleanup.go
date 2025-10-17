package subnetport

import (
	"context"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func (service *SubnetPortService) CleanupBeforeVPCDeletion(ctx context.Context) error {
	objs := service.SubnetPortStore.List()
	log.Info("Cleaning up VpcSubnetPorts", "Count", len(objs), "status", "attempting")
	if len(objs) == 0 {
		log.Info("No VpcSubnetPorts found to clean up", "count", 0)
		return nil
	}

	// Mark the resources for delete.
	ports := make([]*model.VpcSubnetPort, len(objs))
	for i, obj := range objs {
		port := obj.(*model.VpcSubnetPort)
		port.MarkedForDelete = &MarkedForDelete
		ports[i] = port
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
