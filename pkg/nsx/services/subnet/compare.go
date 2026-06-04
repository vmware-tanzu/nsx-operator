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
	// Changes of tags, subnetDHCPConfig, subnetDHCPv6Config, and IPAddressType are considered as changed.
	// TODO AccessMode may also need to be compared in future.
	var advancedConfig *model.SubnetAdvancedConfig
	if subnet.AdvancedConfig != nil {
		// Only compare gateway_addresses, dhcp_server_address, and connectivity_state from AdvancedConfig
		advancedConfig = &model.SubnetAdvancedConfig{
			GatewayAddresses:    subnet.AdvancedConfig.GatewayAddresses,
			DhcpServerAddresses: subnet.AdvancedConfig.DhcpServerAddresses,
			ConnectivityState:   subnet.AdvancedConfig.ConnectivityState,
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
	var subnetDhcpv6Config *model.SubnetDhcpv6Config
	// Only compare Mode and Dhcpv6ServerAdditionalConfig from SubnetDhcpv6Config
	if subnet.SubnetDhcpv6Config != nil {
		var dhcpv6ServerAdditionalConfig *model.DhcpV6ServerAdditionalConfig
		// Only compare ReservedIpRanges from DhcpV6ServerAdditionalConfig
		if subnet.SubnetDhcpv6Config.Dhcpv6ServerAdditionalConfig != nil {
			dhcpv6ServerAdditionalConfig = &model.DhcpV6ServerAdditionalConfig{
				ReservedIpRanges: subnet.SubnetDhcpv6Config.Dhcpv6ServerAdditionalConfig.ReservedIpRanges,
			}
		}
		subnetDhcpv6Config = &model.SubnetDhcpv6Config{
			Mode:                         subnet.SubnetDhcpv6Config.Mode,
			Dhcpv6ServerAdditionalConfig: dhcpv6ServerAdditionalConfig,
		}
	}
	s := &Subnet{
		Tags:               subnet.Tags,
		SubnetDhcpConfig:   subnetDhcpConfig,
		SubnetDhcpv6Config: subnetDhcpv6Config,
		IpAddressType:      subnet.IpAddressType,
		AdvancedConfig:     advancedConfig,
	}
	dataValue, _ := (*model.VpcSubnet)(s).GetDataValue__()
	return dataValue
}

func SubnetToComparable(subnet *model.VpcSubnet) Comparable {
	return (*Subnet)(subnet)
}
