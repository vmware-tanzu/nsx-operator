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

func (service *Service) WrapOrgRoot(children []*data.StructValue) (*model.OrgRoot, error) {
	resourceType := ResourceTypeOrgRoot
	orgRootObj := model.OrgRoot{
		Children:     children,
		ResourceType: &resourceType,
	}
	return &orgRootObj, nil
}

func (service *Service) WrapOrg(org string, children []*data.StructValue) ([]*data.StructValue, error) {
	targetType := ResourceTypeOrg
	return wrapChildResourceReference(targetType, org, children)
}

func (service *Service) WrapProject(nsxtProject string, children []*data.StructValue) ([]*data.StructValue, error) {
	targetType := ResourceTypeProject
	return wrapChildResourceReference(targetType, nsxtProject, children)
}

func wrapChildResourceReference(targetType, id string, children []*data.StructValue) ([]*data.StructValue, error) {
	resourceType := ResourceTypeChildResourceReference
	childProject := model.ChildResourceReference{
		Id:           &id,
		ResourceType: resourceType,
		TargetType:   &targetType,
		Children:     children,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childProject, childProject.GetType__())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return []*data.StructValue{dataValue.(*data.StructValue)}, nil

}

func (service *Service) WrapVPC(vpc *model.Vpc) ([]*data.StructValue, error) {
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
	return []*data.StructValue{dataValue.(*data.StructValue)}, nil
}

func (service *Service) WrapLBS(lbs *model.LBService) ([]*data.StructValue, error) {
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
	return []*data.StructValue{dataValue.(*data.StructValue)}, nil
}

func (service *Service) WrapAttachment(attachment *model.VpcAttachment) ([]*data.StructValue, error) {
	attachment.ResourceType = pointy.String(ResourceTypeVpcAttachment)
	childVpcAttachment := model.ChildVpcAttachment{
		Id:              attachment.Id,
		MarkedForDelete: attachment.MarkedForDelete,
		ResourceType:    ResourceTypeChildVpcAttachment,
		VpcAttachment:   attachment,
	}
	dataValue, errs := NewConverter().ConvertToVapi(childVpcAttachment, childVpcAttachment.GetType__())
	if len(errs) > 0 {
		return nil, errs[0]
	}
	return []*data.StructValue{dataValue.(*data.StructValue)}, nil
}
