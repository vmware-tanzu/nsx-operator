package staticroute

import (
	"context"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// CleanupVPCChildResources is deleting all the NSX StaticRoutes in the given vpcPath on NSX and/or in local cache.
// If vpcPath is not empty, the function is called with an auto-created VPC case, so it only deletes in the local cache for
// the NSX resources are already removed when VPC is deleted recursively. Otherwise, it should delete all cached StaticRoutes
// on NSX and in local cache.
func (service *StaticRouteService) CleanupVPCChildResources(ctx context.Context, vpcPath string) error {
	if vpcPath != "" {
		routes, err := service.StaticRouteStore.GetByVPCPath(vpcPath)
		if err != nil {
			log.Error(err, "Failed to list StaticRoutes under the VPC", "path", vpcPath)
		}
		if len(routes) == 0 {
			log.Info("No StaticRoutes found for VPC", "vpcPath", vpcPath, "count", 0)
			return nil
		}
		log.Info("Cleaning StaticRoutes from local store for auto-created VPC", "vpcPath", vpcPath, "count", len(routes))
		// Delete resources from the store and return.
		service.StaticRouteStore.DeleteMultipleObjects(routes)
		log.Info("Successfully cleaned StaticRoutes from local store", "vpcPath", vpcPath, "count", len(routes), "status", "success")
		return nil
	}

	routes := make([]*model.StaticRoutes, 0)
	MarkedForDelete := true
	// Mark the resources for delete.
	for _, obj := range service.StaticRouteStore.List() {
		route := obj.(*model.StaticRoutes)
		route.MarkedForDelete = &MarkedForDelete
		routes = append(routes, route)
	}
	log.Info("Cleaning up StaticRoutes from pre-created VPC", "count", len(routes))
	return service.builder.PagingUpdateResources(ctx, routes, common.DefaultHAPIChildrenCount, service.NSXClient, func(deletedObjs []*model.StaticRoutes) {
		service.StaticRouteStore.DeleteMultipleObjects(deletedObjs)
	})
}
