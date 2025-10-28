package subnetbinding

import (
	"context"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// SetCleanupFilters sets the targetNamespace and targetVPC filters for selective cleanup
func (s *BindingService) SetCleanupFilters(targetNamespace, targetVPC string) {
	s.targetNamespace = targetNamespace
	s.targetVPC = targetVPC
}

// shouldCleanResource checks if a resource should be cleaned based on namespace/VPC filtering
func (s *BindingService) shouldCleanResource(path *string, tags []model.Tag) bool {
	return servicecommon.ShouldCleanResource(s.targetNamespace, s.targetVPC, path, tags)
}

func (s *BindingService) CleanupBeforeVPCDeletion(ctx context.Context) error {
	allNSXBindings := s.BindingStore.List()
	log.Info("Cleaning up SubnetConnectionBindingMaps", "Count", len(allNSXBindings), "status", "attempting")
	if len(allNSXBindings) == 0 {
		log.Info("No SubnetConnectionBindingMaps found to clean up", "count", 0)
		return nil
	}

	finalBindingMaps := make([]*model.SubnetConnectionBindingMap, 0)
	for _, obj := range allNSXBindings {
		binding, _ := obj.(*model.SubnetConnectionBindingMap)
		if s.shouldCleanResource(binding.Path, binding.Tags) {
			binding.MarkedForDelete = &markedForDelete
			finalBindingMaps = append(finalBindingMaps, binding)
			if s.targetNamespace != "" || s.targetVPC != "" {
				log.Info("Marking subnet binding for deletion", "path", *binding.Path, "name", *binding.DisplayName)
			}
		} else {
			log.Info("Skipping subnet binding (not in target)", "path", *binding.Path, "targetNamespace", s.targetNamespace, "targetVPC", s.targetVPC)
		}
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
