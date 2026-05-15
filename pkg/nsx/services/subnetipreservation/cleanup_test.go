package subnetipreservation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/mock/gomock"

	orgroot_mocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/orgrootclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestCleanupDynamicIPReservation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockRootClient := orgroot_mocks.NewMockOrgRootClient(ctrl)
	mockRootClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil)

	service := createFakeService()
	builder, _ := common.PolicyPathVpcSubnetDynamicIPReservation.NewPolicyTreeBuilder()
	service.DynamicIPReservationBuilder = builder
	service.NSXClient.OrgRootClient = mockRootClient
	service.DynamicIPReservationStore.Apply(&model.DynamicIpAddressReservation{
		Id: common.String("ipr-1"),
	})

	err := service.CleanupDynamicIPReservation(context.TODO())
	require.NoError(t, err)
}

func TestCleanupStaticIPReservation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockRootClient := orgroot_mocks.NewMockOrgRootClient(ctrl)
	mockRootClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil)

	service := createFakeService()
	builder, _ := common.PolicyPathVpcSubnetStaticIPReservation.NewPolicyTreeBuilder()
	service.StaticIPReservationBuilder = builder
	service.NSXClient.OrgRootClient = mockRootClient
	service.StaticIPReservationStore.Apply(&model.StaticIpAddressReservation{
		Id: common.String("sipr-1"),
	})

	err := service.CleanupStaticIPReservation(context.TODO())
	require.NoError(t, err)
}

func TestCleanupBeforeVPCDeletion(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockRootClient := orgroot_mocks.NewMockOrgRootClient(ctrl)
	mockRootClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil).Times(2)

	service := createFakeService()
	dynamicBuilder, _ := common.PolicyPathVpcSubnetDynamicIPReservation.NewPolicyTreeBuilder()
	staticBuilder, _ := common.PolicyPathVpcSubnetStaticIPReservation.NewPolicyTreeBuilder()
	service.DynamicIPReservationBuilder = dynamicBuilder
	service.StaticIPReservationBuilder = staticBuilder
	service.NSXClient.OrgRootClient = mockRootClient
	service.DynamicIPReservationStore.Apply(&model.DynamicIpAddressReservation{Id: common.String("ipr-1")})
	service.StaticIPReservationStore.Apply(&model.StaticIpAddressReservation{Id: common.String("sipr-1")})

	err := service.CleanupBeforeVPCDeletion(context.TODO())
	require.NoError(t, err)
	require.Equal(t, 0, len(service.DynamicIPReservationStore.List()))
	require.Equal(t, 0, len(service.StaticIPReservationStore.List()))
}
