package subnetipreservation

import (
	"context"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// SetCleanupFilters sets the targetNamespace and targetVPC filters for selective cleanup
func (s *IPReservationService) SetCleanupFilters(targetNamespace, targetVPC string) {
	s.targetNamespace = targetNamespace
	s.targetVPC = targetVPC
}

// shouldCleanResource checks if a resource should be cleaned based on namespace/VPC filtering
func (s *IPReservationService) shouldCleanResource(path *string, tags []model.Tag) bool {
	return common.ShouldCleanResource(s.targetNamespace, s.targetVPC, path, tags)
}

func (s *IPReservationService) CleanupBeforeVPCDeletion(ctx context.Context) error {
	objs := s.IPReservationStore.List()
	log.Info("Cleaning up Subnet IPReservation", "Count", len(objs), "status", "attempting")
	if len(objs) == 0 {
		log.Info("No Subnet IPReservations found to clean up", "count", 0)
		return nil
	}
	// Mark the resources for delete.
	iprs := make([]*model.DynamicIpAddressReservation, 0)
	for _, obj := range objs {
		ipr := obj.(*model.DynamicIpAddressReservation)
		if s.shouldCleanResource(ipr.Path, ipr.Tags) {
			ipr.MarkedForDelete = &MarkedForDelete
			iprs = append(iprs, ipr)
			if s.targetNamespace != "" || s.targetVPC != "" {
				log.Info("Marking IP reservation for deletion", "path", *ipr.Path, "name", *ipr.DisplayName)
			}
		} else {
			log.Info("Skipping IP reservation (not in target)", "path", *ipr.Path, "targetNamespace", s.targetNamespace, "targetVPC", s.targetVPC)
		}
	}
	log.Info("Starting deletion of Subnet IPReservations", "count", len(iprs))
	err := s.builder.PagingUpdateResources(ctx, iprs, common.DefaultHAPIChildrenCount, s.NSXClient, func(delObjs []*model.DynamicIpAddressReservation) {
		s.IPReservationStore.DeleteMultipleObjects(delObjs)
	})
	if err != nil {
		log.Error(err, "Failed to clean up Subnet IPReservations", "count", len(iprs), "status", "failed")
		return err
	}
	log.Info("Successfully cleaned up Subnet IPReservations", "count", len(iprs), "status", "success")
	return nil
}
