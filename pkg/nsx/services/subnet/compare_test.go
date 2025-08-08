package subnet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// Test SubnetToComparable function
func TestSubnetToComparable(t *testing.T) {
	tagScope1 := "fakeTagScope1"
	tagValue1 := "fakeTagValue1"
	tag1 := model.Tag{
		Scope: &tagScope1,
		Tag:   &tagValue1,
	}
	tagScope2 := "fakeTagScope2"
	tagValue2 := "fakeTagValue2"
	tag2 := model.Tag{
		Scope: &tagScope2,
		Tag:   &tagValue2,
	}
	id1 := "fakeSubnetID1"
	id2 := "fakeSubnetID2"
	connectivityStateConnected := "Connected"
	connectivityStateDisconnected := "Disconnected"
	enableVlanTrue := true
	enableVlanFalse := false
	gatewayAddr1 := "192.168.1.1"
	gatewayAddr2 := "192.168.1.2"
	dhcpAddr1 := "192.168.1.100"
	dhcpAddr2 := "192.168.1.200"

	testCases := []struct {
		name           string
		existingSubnet *model.VpcSubnet
		nsxSubnet      *model.VpcSubnet
		expectChanged  bool
	}{
		{
			name:           "Subnet without Tags",
			nsxSubnet:      &model.VpcSubnet{Id: &id1},
			existingSubnet: &model.VpcSubnet{Id: &id2},
			expectChanged:  false,
		},
		{
			name:           "Subnet with the same Tags",
			nsxSubnet:      &model.VpcSubnet{Id: &id1, Tags: []model.Tag{tag1}},
			existingSubnet: &model.VpcSubnet{Id: &id2, Tags: []model.Tag{tag1}},
			expectChanged:  false,
		},
		{
			name:           "Subnet with diff Tags",
			expectChanged:  true,
			nsxSubnet:      &model.VpcSubnet{Tags: []model.Tag{tag2}},
			existingSubnet: &model.VpcSubnet{Tags: []model.Tag{tag1}},
		},
		{
			name: "AdvancedConfig with different ConnectivityState should not cause change",
			nsxSubnet: &model.VpcSubnet{
				Id: &id1,
				AdvancedConfig: &model.SubnetAdvancedConfig{
					ConnectivityState: &connectivityStateConnected,
				},
			},
			existingSubnet: &model.VpcSubnet{
				Id: &id2,
				AdvancedConfig: &model.SubnetAdvancedConfig{
					ConnectivityState: &connectivityStateDisconnected,
				},
			},
			expectChanged: false,
		},
		{
			name: "AdvancedConfig with different EnableVlanExtension should not cause change",
			nsxSubnet: &model.VpcSubnet{
				Id: &id1,
				AdvancedConfig: &model.SubnetAdvancedConfig{
					EnableVlanExtension: &enableVlanTrue,
				},
			},
			existingSubnet: &model.VpcSubnet{
				Id: &id2,
				AdvancedConfig: &model.SubnetAdvancedConfig{
					EnableVlanExtension: &enableVlanFalse,
				},
			},
			expectChanged: false,
		},
		{
			name: "AdvancedConfig with different GatewayAddresses should cause change",
			nsxSubnet: &model.VpcSubnet{
				Id: &id1,
				AdvancedConfig: &model.SubnetAdvancedConfig{
					GatewayAddresses: []string{gatewayAddr1},
				},
			},
			existingSubnet: &model.VpcSubnet{
				Id: &id2,
				AdvancedConfig: &model.SubnetAdvancedConfig{
					GatewayAddresses: []string{gatewayAddr2},
				},
			},
			expectChanged: true,
		},
		{
			name: "AdvancedConfig with different DhcpServerAddresses should cause change",
			nsxSubnet: &model.VpcSubnet{
				Id: &id1,
				AdvancedConfig: &model.SubnetAdvancedConfig{
					DhcpServerAddresses: []string{dhcpAddr1},
				},
			},
			existingSubnet: &model.VpcSubnet{
				Id: &id2,
				AdvancedConfig: &model.SubnetAdvancedConfig{
					DhcpServerAddresses: []string{dhcpAddr2},
				},
			},
			expectChanged: true,
		},
		{
			name: "AdvancedConfig with same GatewayAddresses should not cause change",
			nsxSubnet: &model.VpcSubnet{
				Id: &id1,
				AdvancedConfig: &model.SubnetAdvancedConfig{
					GatewayAddresses: []string{gatewayAddr1},
				},
			},
			existingSubnet: &model.VpcSubnet{
				Id: &id2,
				AdvancedConfig: &model.SubnetAdvancedConfig{
					GatewayAddresses: []string{gatewayAddr1},
				},
			},
			expectChanged: false,
		},
		{
			name: "AdvancedConfig with mixed changes - ConnectivityState different but GatewayAddresses same",
			nsxSubnet: &model.VpcSubnet{
				Id: &id1,
				AdvancedConfig: &model.SubnetAdvancedConfig{
					ConnectivityState:   &connectivityStateConnected,
					GatewayAddresses:    []string{gatewayAddr1},
					EnableVlanExtension: &enableVlanTrue,
				},
			},
			existingSubnet: &model.VpcSubnet{
				Id: &id2,
				AdvancedConfig: &model.SubnetAdvancedConfig{
					ConnectivityState:   &connectivityStateDisconnected,
					GatewayAddresses:    []string{gatewayAddr1},
					EnableVlanExtension: &enableVlanFalse,
				},
			},
			expectChanged: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			changed := common.CompareResource(SubnetToComparable(tc.existingSubnet), SubnetToComparable(tc.nsxSubnet))
			assert.Equal(t, tc.expectChanged, changed)
		})
	}
}
