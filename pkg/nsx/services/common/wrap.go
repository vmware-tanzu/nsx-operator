package common

import (
	"github.com/openlyinc/pointy"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

// WrapInfra TODO(gran) refactor existing code in other package
func (service *Service) WrapInfra(children []*data.StructValue) (*model.Infra, error) {
	return wrapInfra(children), nil
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
	return WrapChildResourceReference(targetType, org, children)
}

func (service *Service) WrapProject(nsxtProject string, children []*data.StructValue) ([]*data.StructValue, error) {
	targetType := ResourceTypeProject
	return WrapChildResourceReference(targetType, nsxtProject, children)
}

func WrapChildResourceReference(targetType, id string, children []*data.StructValue) ([]*data.StructValue, error) {
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
	dv, err := WrapVPC(vpc)
	if err != nil {
		return nil, err
	}
	return []*data.StructValue{dv}, nil
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

func WrapVpcIpAddressAllocation(allocation *model.VpcIpAddressAllocation) (*data.StructValue, error) {
	allocation.ResourceType = &ResourceTypeIPAddressAllocation
	childAddressAllocation := model.ChildVpcIpAddressAllocation{
		Id:                     allocation.Id,
		MarkedForDelete:        allocation.MarkedForDelete,
		ResourceType:           ResourceTypeChildVpcIPAddressAllocation,
		VpcIpAddressAllocation: allocation,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childAddressAllocation, childAddressAllocation.GetType__())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return dataValue.(*data.StructValue), nil
}

func WrapVpcSubnetPort(port *model.VpcSubnetPort) (*data.StructValue, error) {
	port.ResourceType = &ResourceTypeSubnetPort
	childSubnetPort := model.ChildVpcSubnetPort{
		Id:              port.Id,
		MarkedForDelete: port.MarkedForDelete,
		ResourceType:    ResourceTypeChildVpcSubnetPort,
		VpcSubnetPort:   port,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childSubnetPort, childSubnetPort.GetType__())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return dataValue.(*data.StructValue), nil
}

func WrapSubnetConnectionBindingMap(bindingMap *model.SubnetConnectionBindingMap) (*data.StructValue, error) {
	bindingMap.ResourceType = &ResourceTypeSubnetConnectionBindingMap
	childBindingMap := model.ChildSubnetConnectionBindingMap{
		Id:                         bindingMap.Id,
		MarkedForDelete:            bindingMap.MarkedForDelete,
		ResourceType:               ResourceTypeChildSubnetConnectionBindingMap,
		SubnetConnectionBindingMap: bindingMap,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childBindingMap, childBindingMap.GetType__())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return dataValue.(*data.StructValue), nil
}

func WrapVpcSubnet(subnet *model.VpcSubnet) (*data.StructValue, error) {
	subnet.ResourceType = &ResourceTypeSubnet
	childSubnet := model.ChildVpcSubnet{
		Id:              subnet.Id,
		MarkedForDelete: subnet.MarkedForDelete,
		ResourceType:    ResourceTypeChildVpcSubnet,
		VpcSubnet:       subnet,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childSubnet, childSubnet.GetType__())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return dataValue.(*data.StructValue), nil
}

func WrapStaticRoutes(route *model.StaticRoutes) (*data.StructValue, error) {
	route.ResourceType = &ResourceTypeStaticRoute
	childRoute := model.ChildStaticRoutes{
		Id:              route.Id,
		MarkedForDelete: route.MarkedForDelete,
		ResourceType:    ResourceTypeChildStaticRoutes,
		StaticRoutes:    route,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childRoute, childRoute.GetType__())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return dataValue.(*data.StructValue), nil
}

func WrapSecurityPolicy(sp *model.SecurityPolicy) (*data.StructValue, error) {
	sp.ResourceType = &ResourceTypeSecurityPolicy
	childPolicy := model.ChildSecurityPolicy{
		Id:              sp.Id,
		MarkedForDelete: sp.MarkedForDelete,
		ResourceType:    ResourceTypeChildSecurityPolicy,
		SecurityPolicy:  sp,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childPolicy, childPolicy.GetType__())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return dataValue.(*data.StructValue), nil
}

func WrapRule(rule *model.Rule) (*data.StructValue, error) {
	rule.ResourceType = &ResourceTypeRule
	childRule := model.ChildRule{
		Id:              rule.Id,
		MarkedForDelete: rule.MarkedForDelete,
		ResourceType:    ResourceTypeChildRule,
		Rule:            rule,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childRule, childRule.GetType__())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return dataValue.(*data.StructValue), nil
}

func WrapGroup(group *model.Group) (*data.StructValue, error) {
	group.ResourceType = &ResourceTypeGroup
	childGroup := model.ChildGroup{
		ResourceType:    ResourceTypeChildGroup,
		Id:              group.Id,
		MarkedForDelete: group.MarkedForDelete,
		Group:           group,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childGroup, childGroup.GetType__())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return dataValue.(*data.StructValue), nil
}

func WrapShare(share *model.Share) (*data.StructValue, error) {
	share.ResourceType = &ResourceTypeShare
	childShare := model.ChildShare{
		ResourceType:    ResourceTypeChildShare,
		Id:              share.Id,
		MarkedForDelete: share.MarkedForDelete,
		Share:           share,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childShare, childShare.GetType__())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return dataValue.(*data.StructValue), nil
}

func WrapLBService(lbService *model.LBService) (*data.StructValue, error) {
	lbService.ResourceType = &ResourceTypeLBService
	childLBService := model.ChildLBService{
		ResourceType:    ResourceTypeChildLBService,
		Id:              lbService.Id,
		MarkedForDelete: lbService.MarkedForDelete,
		LbService:       lbService,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childLBService, childLBService.GetType__())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return dataValue.(*data.StructValue), nil
}

func WrapLBVirtualServer(lbVS *model.LBVirtualServer) (*data.StructValue, error) {
	lbVS.ResourceType = &ResourceTypeLBVirtualServer
	childLBVS := model.ChildLBVirtualServer{
		ResourceType:    ResourceTypeChildLBVirtualServer,
		Id:              lbVS.Id,
		MarkedForDelete: lbVS.MarkedForDelete,
		LbVirtualServer: lbVS,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childLBVS, childLBVS.GetType__())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return dataValue.(*data.StructValue), nil
}

func WrapLBPool(lbPool *model.LBPool) (*data.StructValue, error) {
	lbPool.ResourceType = &ResourceTypeLBPool
	childLBPool := model.ChildLBPool{
		ResourceType:    ResourceTypeChildLBPool,
		Id:              lbPool.Id,
		MarkedForDelete: lbPool.MarkedForDelete,
		LbPool:          lbPool,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childLBPool, childLBPool.GetType__())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return dataValue.(*data.StructValue), nil
}

func WrapVPC(vpc *model.Vpc) (*data.StructValue, error) {
	vpc.ResourceType = &ResourceTypeVpc
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
	return dataValue.(*data.StructValue), nil
}

func WrapCertificate(cert *model.TlsCertificate) (*data.StructValue, error) {
	cert.ResourceType = &ResourceTypeTlsCertificate
	childCert := model.ChildTlsCertificate{
		Id:              cert.Id,
		MarkedForDelete: cert.MarkedForDelete,
		ResourceType:    "ChildTlsCertificate",
		TlsCertificate:  cert,
	}
	dataValue, errs := NewConverter().ConvertToVapi(childCert, childCert.GetType__())
	if len(errs) > 0 {
		return nil, errs[0]
	}
	return dataValue.(*data.StructValue), nil
}

func wrapInfra(children []*data.StructValue) *model.Infra {
	// This is the outermost layer of the hierarchy infra client.
	// It doesn't need ID field.
	resourceType := ResourceTypeInfra
	infraObj := model.Infra{
		Children:     children,
		ResourceType: &resourceType,
	}
	return &infraObj
}
