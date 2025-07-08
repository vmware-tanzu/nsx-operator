package subnetport

import (
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type (
	SubnetPort        model.VpcSubnetPort
	DhcpStaticBinding model.DhcpV4StaticBindingConfig
)

type Comparable = common.Comparable

func (sp *SubnetPort) Key() string {
	return *sp.Id
}

func (sp *SubnetPort) Value() data.DataValue {
	s := &SubnetPort{
		Id:          sp.Id,
		DisplayName: sp.DisplayName,
		Tags:        sp.Tags,
		Attachment:  sp.Attachment,
	}
	if sp.Attachment != nil {
		// Ignoring the fields BmsInterfaceConfig, ContextType, EvpnVlans, HyperbusMode
		// because operator doesn't set them.
		s.Attachment = &model.PortAttachment{
			AllocateAddresses: sp.Attachment.AllocateAddresses,
			AppId:             sp.Attachment.AppId,
			ContextId:         sp.Attachment.ContextId,
			Id:                sp.Attachment.Id,
			TrafficTag:        sp.Attachment.TrafficTag,
			Type_:             sp.Attachment.AllocateAddresses,
		}
	}
	if sp.AddressBindings != nil {
		s.AddressBindings = []model.PortAddressBindingEntry{
			{
				IpAddress:  sp.AddressBindings[0].IpAddress,
				MacAddress: sp.AddressBindings[0].MacAddress,
			},
		}
	}
	if sp.ExternalAddressBinding != nil {
		s.ExternalAddressBinding = &model.ExternalAddressBinding{AllocatedExternalIpPath: sp.ExternalAddressBinding.AllocatedExternalIpPath}
	}
	dataValue, _ := ComparableToSubnetPort(s).GetDataValue__()
	return dataValue
}

func SubnetPortToComparable(sp *model.VpcSubnetPort) Comparable {
	return (*SubnetPort)(sp)
}

func ComparableToSubnetPort(sp Comparable) *model.VpcSubnetPort {
	return (*model.VpcSubnetPort)(sp.(*SubnetPort))
}

func (binding *DhcpStaticBinding) Key() string {
	return *binding.Id
}

func (binding *DhcpStaticBinding) Value() data.DataValue {
	s := &DhcpStaticBinding{
		Id:         binding.Id,
		Tags:       binding.Tags,
		IpAddress:  binding.IpAddress,
		MacAddress: binding.MacAddress,
	}

	dataValue, _ := ComparableToDhcpStaticBinding(s).GetDataValue__()
	return dataValue
}

func DhcpStaticBindingToComparable(binding *model.DhcpV4StaticBindingConfig) Comparable {
	return (*DhcpStaticBinding)(binding)
}

func ComparableToDhcpStaticBinding(binding Comparable) *model.DhcpV4StaticBindingConfig {
	return (*model.DhcpV4StaticBindingConfig)(binding.(*DhcpStaticBinding))
}
