package subnetbinding

import (
	"context"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func (s *BindingService) CleanupBeforeVPCDeletion(ctx context.Context) error {
	allNSXBindings := s.BindingStore.List()
	log.Info("Cleaning up SubnetConnectionBindingMaps", "Count", len(allNSXBindings), "status", "attempting")
	if len(allNSXBindings) == 0 {
		log.Info("No SubnetConnectionBindingMaps found to clean up", "count", 0)
		return nil
	}

	finalBindingMaps := make([]*model.SubnetConnectionBindingMap, len(allNSXBindings))
	for i, obj := range allNSXBindings {
		binding, _ := obj.(*model.SubnetConnectionBindingMap)
		binding.MarkedForDelete = &markedForDelete
		finalBindingMaps[i] = binding
	}
	log.Info("Starting deletion of SubnetConnectionBindingMaps", "count", len(finalBindingMaps))
	err := s.builder.PagingUpdateResources(ctx, finalBindingMaps, servicecommon.DefaultHAPIChildrenCount, s.NSXClient, func(deletedObjs []*model.SubnetConnectionBindingMap) {
		s.BindingStore.DeleteMultipleObjects(deletedObjs)
	})
	if err != nil {
		log.Error(err, "Failed to clean up SubnetConnectionBindingMaps", "count", len(finalBindingMaps), "status", "failed")
		return err
	}
	log.Info("Successfully cleaned up SubnetConnectionBindingMaps", "count", len(finalBindingMaps), "status", "success")
	return nil
}
