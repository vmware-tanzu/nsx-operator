package subnetport

import (
	"context"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func (service *SubnetPortService) CleanupBeforeVPCDeletion(ctx context.Context) error {
	if err := service.CleanupDHCPStaticBinding(ctx); err != nil {
		log.Error(err, "Failed to clean up DHCPV4StaticBindings")
		return err
	}
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
	return service.builder.PagingUpdateResources(ctx, ports, common.DefaultHAPIChildrenCount, service.NSXClient, func(delObjs []*model.VpcSubnetPort) {
		service.SubnetPortStore.DeleteMultipleObjects(delObjs)
	})
}

func (service *SubnetPortService) CleanupDHCPStaticBinding(ctx context.Context) error {
	objs := service.DHCPStaticBindingStore.List()
	log.Info("Cleaning up DHCPV4StaticBindingStore", "Count", len(objs))
	if len(objs) == 0 {
		return nil
	}

	// Mark the resources for delete.
	staticBindings := make([]*model.DhcpV4StaticBindingConfig, len(objs))
	for i, obj := range objs {
		staticBinding := obj.(*model.DhcpV4StaticBindingConfig)
		staticBinding.MarkedForDelete = &MarkedForDelete
		staticBindings[i] = staticBinding
	}
	return service.staticBindingBuilder.PagingUpdateResources(ctx, staticBindings, common.DefaultHAPIChildrenCount, service.NSXClient, func(delObjs []*model.DhcpV4StaticBindingConfig) {
		service.DHCPStaticBindingStore.DeleteMultipleObjects(delObjs)
	})
}
