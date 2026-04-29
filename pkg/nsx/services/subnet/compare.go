package subnet

import (
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type (
	Subnet model.VpcSubnet
)

type Comparable = common.Comparable

func (subnet *Subnet) Key() string {
	return *subnet.Id
}

func (subnet *Subnet) Value() data.DataValue {
	// IPv4SubnetSize/AccessMode/IPAddresses are immutable field,
	// Changes of tags and subnetDHCPConfig are considered as changed.
	// TODO AccessMode may also need to be compared in future.
	var advancedConfig *model.SubnetAdvancedConfig
	if subnet.AdvancedConfig != nil {
		// Compare gateway_addresses, dhcp_server_address, connectivity_state and
		// static_ip_allocation from AdvancedConfig. StaticIpAllocation is
		// included so drift on enabled/pool_ranges triggers a reconcile
		// (required for mixed mode day-2 poolRanges changes).
		advancedConfig = &model.SubnetAdvancedConfig{
			GatewayAddresses:    subnet.AdvancedConfig.GatewayAddresses,
			DhcpServerAddresses: subnet.AdvancedConfig.DhcpServerAddresses,
			ConnectivityState:   subnet.AdvancedConfig.ConnectivityState,
			StaticIpAllocation:  subnet.AdvancedConfig.StaticIpAllocation,
		}
	}
	var subnetDhcpConfig *model.SubnetDhcpConfig
	// Only compare Mode and DhcpServerAdditionalConfig from SubnetDhcpConfig
	if subnet.SubnetDhcpConfig != nil {
		var dhcpServerAdditionalConfig *model.DhcpServerAdditionalConfig
		// Only compare ReservedIpRanges from DhcpServerAdditionalConfig
		if subnet.SubnetDhcpConfig.DhcpServerAdditionalConfig != nil {
			dhcpServerAdditionalConfig = &model.DhcpServerAdditionalConfig{
				ReservedIpRanges: subnet.SubnetDhcpConfig.DhcpServerAdditionalConfig.ReservedIpRanges,
			}
		}
		subnetDhcpConfig = &model.SubnetDhcpConfig{
			Mode:                       subnet.SubnetDhcpConfig.Mode,
			DhcpServerAdditionalConfig: dhcpServerAdditionalConfig,
		}
	}
	s := &Subnet{
		Tags:             subnet.Tags,
		SubnetDhcpConfig: subnetDhcpConfig,
		AdvancedConfig:   advancedConfig,
	}
	dataValue, _ := (*model.VpcSubnet)(s).GetDataValue__()
	return dataValue
}

func SubnetToComparable(subnet *model.VpcSubnet) Comparable {
	return (*Subnet)(subnet)
}
