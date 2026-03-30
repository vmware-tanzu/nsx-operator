package subnetipreservation

import (
	"context"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func (s *IPReservationService) CleanupBeforeVPCDeletion(ctx context.Context) error {
	if err := s.CleanupDynamicIPReservation(ctx); err != nil {
		return err
	}
	return s.CleanupStaticIPReservation(ctx)
}

func (s *IPReservationService) CleanupDynamicIPReservation(ctx context.Context) error {
	objs := s.DynamicIPReservationStore.List()
	log.Info("Cleaning up Subnet DynamicIPReservation", "Count", len(objs), "status", "attempting")
	if len(objs) == 0 {
		log.Info("No Subnet DynamicIPReservation found to clean up", "count", 0)
		return nil
	}
	// Mark the resources for delete.
	iprs := make([]*model.DynamicIpAddressReservation, len(objs))
	for i, obj := range objs {
		ipr := obj.(*model.DynamicIpAddressReservation)
		ipr.MarkedForDelete = &MarkedForDelete
		iprs[i] = ipr
	}
	log.Info("Starting deletion of Subnet DynamicIPReservation", "count", len(iprs))
	err := s.DynamicIPReservationBuilder.PagingUpdateResources(ctx, iprs, common.DefaultHAPIChildrenCount, s.NSXClient, func(delObjs []*model.DynamicIpAddressReservation) {
		s.DynamicIPReservationStore.DeleteMultipleObjects(delObjs)
	})
	if err != nil {
		log.Error(err, "Failed to clean up Subnet DynamicIPReservation", "count", len(iprs), "status", "failed")
		return err
	}
	log.Info("Successfully cleaned up Subnet DynamicIPReservation", "count", len(iprs), "status", "success")
	return nil
}

func (s *IPReservationService) CleanupStaticIPReservation(ctx context.Context) error {
	objs := s.StaticIPReservationStore.List()
	log.Info("Cleaning up Subnet StaticIPReservation", "Count", len(objs), "status", "attempting")
	if len(objs) == 0 {
		log.Info("No Subnet StaticIPReservation found to clean up", "count", 0)
		return nil
	}
	// Mark the resources for delete.
	iprs := make([]*model.StaticIpAddressReservation, len(objs))
	for i, obj := range objs {
		ipr := obj.(*model.StaticIpAddressReservation)
		ipr.MarkedForDelete = &MarkedForDelete
		iprs[i] = ipr
	}
	log.Info("Starting deletion of Subnet StaticIPReservation", "count", len(iprs))
	err := s.StaticIPReservationBuilder.PagingUpdateResources(ctx, iprs, common.DefaultHAPIChildrenCount, s.NSXClient, func(delObjs []*model.StaticIpAddressReservation) {
		s.StaticIPReservationStore.DeleteMultipleObjects(delObjs)
	})
	if err != nil {
		log.Error(err, "Failed to clean up Subnet StaticIPReservation", "count", len(iprs), "status", "failed")
		return err
	}
	log.Info("Successfully cleaned up Subnet StaticIPReservation", "count", len(iprs), "status", "success")
	return nil
}
