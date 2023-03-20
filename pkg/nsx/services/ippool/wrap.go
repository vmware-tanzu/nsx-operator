package ippool

import (
	"github.com/openlyinc/pointy"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func (service *IPPoolService) WrapHierarchyIPPool(iap *model.IpAddressPool, iapbs []*model.IpAddressPoolBlockSubnet) (*model.Infra, error) {
	IPSubnetsChildren, err := service.wrapIPSubnets(iapbs)
	if err != nil {
		return nil, err
	}
	iap.Children = IPSubnetsChildren
	IPPoolChildren, err := service.wrapIPPool(iap)
	if err != nil {
		return nil, err
	}
	var infraChildren []*data.StructValue
	infraChildren = append(infraChildren, IPPoolChildren...)

	infra, err := service.wrapInfra(infraChildren)
	if err != nil {
		return nil, err
	}
	return infra, nil
}

func (service *IPPoolService) wrapInfra(children []*data.StructValue) (*model.Infra, error) {
	// This is the outermost layer of the hierarchy security policy.
	// It doesn't need ID field.
	infraType := "Infra"
	infraObj := model.Infra{
		Children:     children,
		ResourceType: &infraType,
	}
	return &infraObj, nil
}

func (service *IPPoolService) wrapIPSubnets(IPSubnets []*model.IpAddressPoolBlockSubnet) ([]*data.StructValue, error) {
	var IPSubnetsChildren []*data.StructValue
	for _, IPSubnet := range IPSubnets {
		IPSubnet.ResourceType = common.ResourceTypeIPPoolBlockSubnet
		dataValue, errs := NewConverter().ConvertToVapi(IPSubnet, model.IpAddressPoolBlockSubnetBindingType())
		if len(errs) > 0 {
			return nil, errs[0]
		}
		childIPSubnet := model.ChildIpAddressPoolSubnet{
			ResourceType:        "ChildIpAddressPoolSubnet",
			Id:                  IPSubnet.Id,
			MarkedForDelete:     IPSubnet.MarkedForDelete,
			IpAddressPoolSubnet: dataValue.(*data.StructValue),
		}
		dataValue, errs = NewConverter().ConvertToVapi(childIPSubnet, model.ChildIpAddressPoolSubnetBindingType())
		if len(errs) > 0 {
			return nil, errs[0]
		}
		IPSubnetsChildren = append(IPSubnetsChildren, dataValue.(*data.StructValue))
	}
	return IPSubnetsChildren, nil
}

func (service *IPPoolService) wrapIPPool(iap *model.IpAddressPool) ([]*data.StructValue, error) {
	var IPPoolChildren []*data.StructValue
	iap.ResourceType = pointy.String(common.ResourceTypeIPPool)
	childIPool := model.ChildIpAddressPool{
		Id:              iap.Id,
		MarkedForDelete: iap.MarkedForDelete,
		ResourceType:    "ChildIpAddressPool",
		IpAddressPool:   iap,
	}
	dataValue, errs := NewConverter().ConvertToVapi(childIPool, model.ChildIpAddressPoolBindingType())
	if len(errs) > 0 {
		return nil, errs[0]
	}
	IPPoolChildren = append(IPPoolChildren, dataValue.(*data.StructValue))
	return IPPoolChildren, nil
}
