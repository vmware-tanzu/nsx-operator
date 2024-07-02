package common

import (
	"github.com/openlyinc/pointy"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

// WrapInfra TODO(gran) refactor existing code in other package
func (service *Service) WrapInfra(children []*data.StructValue) (*model.Infra, error) {
	// This is the outermost layer of the hierarchy infra client.
	// It doesn't need ID field.
	resourceType := ResourceTypeInfra
	infraObj := model.Infra{
		Children:     children,
		ResourceType: &resourceType,
	}
	return &infraObj, nil
}

func (service *Service) WrapVPC(vpc *model.Vpc) ([]*data.StructValue, error) {
	var vpcChildren []*data.StructValue
	vpc.ResourceType = pointy.String(ResourceTypeVpc)
	childVpc := model.ChildVpc{
		Id:              vpc.Id,
		MarkedForDelete: vpc.MarkedForDelete,
		ResourceType:    "ChildVpc",
		Vpc:             vpc,
	}
	dataValue, errs := NewConverter().ConvertToVapi(childVpc, childVpc.GetType__())
	if len(errs) > 0 {
		return nil, errs[0]
	}
	vpcChildren = append(vpcChildren, dataValue.(*data.StructValue))
	return vpcChildren, nil
}

func (service *Service) WrapLBS(lbs *model.LBService) ([]*data.StructValue, error) {
	var lbServiceChildren []*data.StructValue
	lbs.ResourceType = pointy.String(ResourceTypeLBService)
	childLBService := model.ChildLBService{
		Id:              lbs.Id,
		MarkedForDelete: lbs.MarkedForDelete,
		ResourceType:    "ChildLBService",
		LbService:       lbs,
	}
	dataValue, errs := NewConverter().ConvertToVapi(childLBService, childLBService.GetType__())
	if len(errs) > 0 {
		return nil, errs[0]
	}
	lbServiceChildren = append(lbServiceChildren, dataValue.(*data.StructValue))
	return lbServiceChildren, nil
}
