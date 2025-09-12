package subnetipreservation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestCleanupBeforeVPCDeletion(t *testing.T) {
	service := createFakeService()
	service.IPReservationStore.Apply(&model.DynamicIpAddressReservation{
		Id:   common.String("ipr-1"),
		Path: common.String("/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-1/dynamic-ip-reservations/ipr-1"),
	})

	err := service.CleanupBeforeVPCDeletion(context.TODO())
	require.NoError(t, err)
}
