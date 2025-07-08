package subnetport

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	mock_org_root "github.com/vmware-tanzu/nsx-operator/pkg/mock/orgrootclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestCleanupBeforeVPCDeletion(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	subnetPort := &model.VpcSubnetPort{
		Id:   &subnetPortId1,
		Path: &subnetPortPath1,
	}
	subnetPort2 := &model.VpcSubnetPort{
		Id:   &subnetPortId2,
		Path: &subnetPortPath2,
	}
	staticBinding := &model.DhcpV4StaticBindingConfig{
		Id:         &subnetPortId1,
		IpAddress:  common.String("172.26.0.4"),
		MacAddress: common.String("04:50:56:00:94:00"),
	}

	for _, tc := range []struct {
		name           string
		subnetPorts    []*model.VpcSubnetPort
		staticBindings []*model.DhcpV4StaticBindingConfig
	}{
		{
			name:           "clean up nothing",
			subnetPorts:    nil,
			staticBindings: nil,
		},
		{
			name:           "clean up only SubnetPorts",
			subnetPorts:    []*model.VpcSubnetPort{subnetPort},
			staticBindings: nil,
		},
		{
			name:           "clean up only DhcpStaticBindings",
			subnetPorts:    nil,
			staticBindings: []*model.DhcpV4StaticBindingConfig{staticBinding},
		},
		{
			name:           "clean up with all resources",
			subnetPorts:    []*model.VpcSubnetPort{subnetPort, subnetPort2},
			staticBindings: []*model.DhcpV4StaticBindingConfig{staticBinding},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := createSubnetPortService(t)
			if tc.subnetPorts != nil {
				for _, port := range tc.subnetPorts {
					svc.SubnetPortStore.Add(port)
				}
				assert.Equal(t, len(tc.subnetPorts), len(svc.SubnetPortStore.List()))
			}
			if tc.staticBindings != nil {
				for _, port := range tc.staticBindings {
					svc.DHCPStaticBindingStore.Add(port)
				}
				assert.Equal(t, len(tc.staticBindings), len(svc.DHCPStaticBindingStore.List()))
			}
			orgRootClient := mock_org_root.NewMockOrgRootClient(ctrl)
			orgRootClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			svc.Service.NSXClient.OrgRootClient = orgRootClient

			ctx := context.Background()
			err := svc.CleanupBeforeVPCDeletion(ctx)
			require.NoError(t, err)
			assert.Equal(t, 0, len(svc.SubnetPortStore.List()))
			assert.Equal(t, 0, len(svc.DHCPStaticBindingStore.List()))
		})
	}
}
