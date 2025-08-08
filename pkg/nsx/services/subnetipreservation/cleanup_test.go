package subnetipreservation

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	orgroot_mocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/orgrootclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestCleanupBeforeVPCDeletion(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockRootClient := orgroot_mocks.NewMockOrgRootClient(ctrl)
	mockRootClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil)

	service := createFakeService()
	builder, _ := common.PolicyPathVpcSubnetDynamicIPReservation.NewPolicyTreeBuilder()
	service.builder = builder
	service.NSXClient.OrgRootClient = mockRootClient
	service.IPReservationStore.Apply(&model.DynamicIpAddressReservation{
		Id: common.String("ipr-1"),
	})

	err := service.CleanupBeforeVPCDeletion(context.TODO())
	require.NoError(t, err)
}
