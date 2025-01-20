package subnetport

import (
	"context"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func (service *SubnetPortService) CleanupBeforeVPCDeletion(ctx context.Context) error {
	objs := service.SubnetPortStore.List()
	log.Info("Cleaning up VpcSubnetPorts", "Count", len(objs))
	if len(objs) == 0 {
		return nil
	}

	// Mark the resources for delete.
	ports := make([]*model.VpcSubnetPort, len(objs))
	for i, obj := range objs {
		port := obj.(*model.VpcSubnetPort)
		port.MarkedForDelete = &MarkedForDelete
		ports[i] = port
	}
	return service.builder.PagingDeleteResources(ctx, ports, common.DefaultHAPIChildrenCount, service.NSXClient, func(delObjs []*model.VpcSubnetPort) {
		service.SubnetPortStore.DeleteMultiplePorts(delObjs)
	})
}
