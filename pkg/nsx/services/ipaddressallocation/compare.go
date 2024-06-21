package ipaddressallocation

import (
	"github.com/zhengxiexie/vsphere-automation-sdk-go/runtime/data"
	"github.com/zhengxiexie/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type (
	IpAddressAllocation model.VpcIpAddressAllocation
)

type Comparable = common.Comparable

func (iap *IpAddressAllocation) Key() string {
	return *iap.Id
}

func (iap *IpAddressAllocation) Value() data.DataValue {
	s := &IpAddressAllocation{Id: iap.Id, DisplayName: iap.DisplayName, Tags: iap.Tags}
	dataValue, _ := ComparableToIpAddressAllocation(s).GetDataValue__()
	return dataValue
}

func IpAddressAllocationToComparable(iap *model.VpcIpAddressAllocation) Comparable {
	return (*IpAddressAllocation)(iap)
}

func ComparableToIpAddressAllocation(iap Comparable) *model.VpcIpAddressAllocation {
	return (*model.VpcIpAddressAllocation)(iap.(*IpAddressAllocation))
}
