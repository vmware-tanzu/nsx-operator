package ippool

import (
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type (
	IpAddressPool            model.IpAddressPool
	IpAddressPoolBlockSubnet model.IpAddressPoolBlockSubnet
)

type Comparable = common.Comparable

func (iap *IpAddressPool) Key() string {
	return *iap.Id
}

func (iapbs *IpAddressPoolBlockSubnet) Key() string {
	return *iapbs.Id
}

func (iap *IpAddressPool) Value() data.DataValue {
	s := &IpAddressPool{Id: iap.Id, DisplayName: iap.DisplayName, Tags: iap.Tags}
	dataValue, _ := ComparableToIpAddressPool(s).GetDataValue__()
	return dataValue
}

func (iapbs *IpAddressPoolBlockSubnet) Value() data.DataValue {
	r := &IpAddressPoolBlockSubnet{Id: iapbs.Id, DisplayName: iapbs.Id, Tags: iapbs.Tags}
	dataValue, _ := ComparableToIpAddressPoolBlockSubnet(r).GetDataValue__()
	return dataValue
}

func IpAddressPoolToComparable(iap *model.IpAddressPool) Comparable {
	return (*IpAddressPool)(iap)
}

func IpAddressPoolBlockSubnetsToComparable(iapbs []*model.IpAddressPoolBlockSubnet) []Comparable {
	res := make([]Comparable, 0, len(iapbs))
	for i := range iapbs {
		res = append(res, (*IpAddressPoolBlockSubnet)(iapbs[i]))
	}
	return res
}

func ComparableToIpAddressPool(iap Comparable) *model.IpAddressPool {
	return (*model.IpAddressPool)(iap.(*IpAddressPool))
}

func ComparableToIpAddressPoolBlockSubnets(iapbs []Comparable) []*model.IpAddressPoolBlockSubnet {
	res := make([]*model.IpAddressPoolBlockSubnet, 0, len(iapbs))
	for _, iapb := range iapbs {
		res = append(res, (*model.IpAddressPoolBlockSubnet)(iapb.(*IpAddressPoolBlockSubnet)))
	}
	return res
}

func ComparableToIpAddressPoolBlockSubnet(iapbs Comparable) *model.IpAddressPoolBlockSubnet {
	return (*model.IpAddressPoolBlockSubnet)(iapbs.(*IpAddressPoolBlockSubnet))
}
