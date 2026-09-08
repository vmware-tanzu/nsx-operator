package ipaddressallocation

import (
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type (
	IpAddressAllocation model.VpcIpAddressAllocation
)

type Comparable = common.Comparable

func (ipa *IpAddressAllocation) Key() string {
	return *ipa.Id
}

func normalizeDNSHostname(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}
	return s
}

func (ipa *IpAddressAllocation) Value() data.DataValue {
	if ipa == nil {
		return nil
	}
	s := &IpAddressAllocation{
		Id:                         ipa.Id,
		DisplayName:                ipa.DisplayName,
		Tags:                       ipa.Tags,
		AllocationSize:             ipa.AllocationSize,
		IpAddressBlockVisibility:   ipa.IpAddressBlockVisibility,
		IpAddressType:              ipa.IpAddressType,
		Ipv6AllocationPrefixLength: ipa.Ipv6AllocationPrefixLength,
		DnsHostname:                normalizeDNSHostname(ipa.DnsHostname),
	}
	dataValue, _ := ComparableToIpAddressAllocation(s).GetDataValue__()
	return dataValue
}

func IpAddressAllocationToComparable(iap *model.VpcIpAddressAllocation) Comparable {
	return (*IpAddressAllocation)(iap)
}

func ComparableToIpAddressAllocation(iap Comparable) *model.VpcIpAddressAllocation {
	return (*model.VpcIpAddressAllocation)(iap.(*IpAddressAllocation))
}
