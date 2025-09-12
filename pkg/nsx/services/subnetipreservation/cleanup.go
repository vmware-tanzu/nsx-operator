package subnetipreservation

import (
	"context"
	"errors"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

func (s *IPReservationService) CleanupBeforeVPCDeletion(ctx context.Context) error {
	objs := s.IPReservationStore.List()
	log.Info("Cleaning up Subnet IPReservation", "Count", len(objs))
	if len(objs) == 0 {
		return nil
	}
	// TODO: NSX HAPI has bug to delete the IPReservation, use the plain API for now
	var errList []error
	for _, obj := range objs {
		ipr := obj.(*model.DynamicIpAddressReservation)
		err := s.DeleteIPReservation(ipr)
		if err != nil {
			log.Error(err, "Failed to delete Subnet IPReservation", "IPReservation", *ipr.Path)
			errList = append(errList, err)
		}
	}
	if len(errList) > 0 {
		return errors.Join(errList...)
	}
	return nil
}
