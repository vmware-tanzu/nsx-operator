package subnetipreservation

import (
	"context"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func (s *IPReservationService) CleanupBeforeVPCDeletion(ctx context.Context) error {
	objs := s.IPReservationStore.List()
	log.Info("Cleaning up Subnet IPReservation", "Count", len(objs))
	if len(objs) == 0 {
		return nil
	}
	// Mark the resources for delete.
	iprs := make([]*model.DynamicIpAddressReservation, len(objs))
	for i, obj := range objs {
		ipr := obj.(*model.DynamicIpAddressReservation)
		ipr.MarkedForDelete = &MarkedForDelete
		iprs[i] = ipr
	}
	return s.builder.PagingUpdateResources(ctx, iprs, common.DefaultHAPIChildrenCount, s.NSXClient, func(delObjs []*model.DynamicIpAddressReservation) {
		s.IPReservationStore.DeleteMultipleObjects(delObjs)
	})
}
