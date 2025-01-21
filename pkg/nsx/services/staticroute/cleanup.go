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
			return nil
		}
		// Delete resources from the store and return.
		service.StaticRouteStore.DeleteMultipleObjects(routes)
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
	return service.builder.PagingDeleteResources(ctx, routes, common.DefaultHAPIChildrenCount, service.NSXClient, func(deletedObjs []*model.StaticRoutes) {
		service.StaticRouteStore.DeleteMultipleObjects(deletedObjs)
	})
}
