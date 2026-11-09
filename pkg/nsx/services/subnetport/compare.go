package subnetport

import (
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type (
	SubnetPort model.VpcSubnetPort
)

type Comparable = common.Comparable

func (sp *SubnetPort) Key() string {
	return *sp.Id
}

func (sp *SubnetPort) Value() data.DataValue {
	var dnsHostname *string
	if sp.DnsHostname != nil {
		normalized := strings.ToLower(strings.TrimSpace(*sp.DnsHostname))
		dnsHostname = &normalized
	}
	s := &SubnetPort{
		Id:                     sp.Id,
		DisplayName:            sp.DisplayName,
		Tags:                   sp.Tags,
		DnsHostname:            dnsHostname,
		Attachment:             sp.Attachment,
		StaticIpAllocationType: sp.StaticIpAllocationType,
		PortSettingPath:        sp.PortSettingPath,
	}
	// NSX returns the subnet's default setting when the request omits it.
	// Normalize only the comparable copy, preserving the request and cached response.
	if sp.ParentPath != nil && *sp.ParentPath != "" && s.PortSettingPath != nil &&
		*s.PortSettingPath == *sp.ParentPath+"/port-settings/default" {
		s.PortSettingPath = nil
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
		s.AddressBindings = make([]model.PortAddressBindingEntry, len(sp.AddressBindings))
		for i, binding := range sp.AddressBindings {
			s.AddressBindings[i] = model.PortAddressBindingEntry{
				IpAddress:  binding.IpAddress,
				MacAddress: binding.MacAddress,
			}
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
