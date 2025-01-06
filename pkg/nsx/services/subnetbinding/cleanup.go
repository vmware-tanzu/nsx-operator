package subnetbinding

import (
	"context"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func (s *BindingService) CleanupBeforeVPCDeletion(ctx context.Context) error {
	allNSXBindings := s.BindingStore.List()
	log.Info("Cleaning up SubnetConnectionBindingMaps", "Count", len(allNSXBindings))
	if len(allNSXBindings) == 0 {
		return nil
	}

	finalBindingMaps := make([]*model.SubnetConnectionBindingMap, len(allNSXBindings))
	for i, obj := range allNSXBindings {
		binding, _ := obj.(*model.SubnetConnectionBindingMap)
		binding.MarkedForDelete = &markedForDelete
		finalBindingMaps[i] = binding
	}
	return s.builder.PagingDeleteResources(ctx, finalBindingMaps, servicecommon.DefaultHAPIChildrenCount, s.NSXClient, func(deletedObjs []*model.SubnetConnectionBindingMap) {
		s.BindingStore.DeleteMultipleObjects(deletedObjs)
	})
}
