package securitypolicy

import (
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// Patch API at infra level can be used in two flavours.
// 1. Like a regular API to update Infra object.
// 2. Hierarchical API: To create/update/delete entire or part of intent hierarchy Hierarchical.
// We use infra patch API in hierarchical mode to create/update/delete entire or part of intent hierarchy,
// for this convenience we can no longer CRUD CR separately, and reduce the number of API calls to NSX-T.

// WrapHierarchySecurityPolicy wrap the security policy with groups and rules into a hierarchy security policy for InfraClient to patch.
func (service *SecurityPolicyService) WrapHierarchySecurityPolicy(sp *model.SecurityPolicy, gs []model.Group) (*model.Infra, error) {
	rulesChildren, err := service.wrapRules(sp.Rules)
	if err != nil {
		return nil, err
	}
	sp.Rules = nil
	sp.Children = rulesChildren
	sp.ResourceType = &common.ResourceTypeSecurityPolicy // InfraClient need this field to identify the resource type

	securityPolicyChildren, err := service.wrapSecurityPolicy(sp)
	if err != nil {
		return nil, err
	}
	var resourceReferenceChildren []*data.StructValue
	resourceReferenceChildren = append(resourceReferenceChildren, securityPolicyChildren...)
	groupsChildren, err := service.wrapGroups(gs)
	if err != nil {
		return nil, err
	}
	resourceReferenceChildren = append(resourceReferenceChildren, groupsChildren...)
	domainId := getDomain(service)
	infraChildren, err := service.wrapDomainResource(resourceReferenceChildren, domainId)
	if err != nil {
		return nil, err
	}
	infra, err := service.wrapInfra(infraChildren)
	if err != nil {
		return nil, err
	}
	return infra, nil
}

func (service *SecurityPolicyService) wrapInfra(children []*data.StructValue) (*model.Infra, error) {
	// This is the outermost layer of the hierarchy infra client.
	// It doesn't need ID field.
	infraType := "Infra"
	infraObj := model.Infra{
		Children:     children,
		ResourceType: &infraType,
	}
	return &infraObj, nil
}

func (service *SecurityPolicyService) wrapDomainResource(children []*data.StructValue, domainId string) ([]*data.StructValue, error) {
	var domainChildren []*data.StructValue
	targetType := "Domain"
	id := domainId
	childDomain := model.ChildResourceReference{
		Id:           &id,
		ResourceType: "ChildResourceReference",
		TargetType:   &targetType,
		Children:     children,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childDomain, model.ChildResourceReferenceBindingType())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	domainChildren = append(domainChildren, dataValue.(*data.StructValue))
	return domainChildren, nil
}

func (service *SecurityPolicyService) wrapRules(rules []model.Rule) ([]*data.StructValue, error) {
	var rulesChildren []*data.StructValue
	for _, rule := range rules {
		rule.ResourceType = &common.ResourceTypeRule // InfraClient need this field to identify the resource type
		childRule := model.ChildRule{                // We need to put child rule's id into upper level, otherwise, NSX-T will not find the child rule
			ResourceType:    "ChildRule", // Children are not allowed for rule, so we don't need to wrap ServiceEntry into Children
			Id:              rule.Id,
			Rule:            &rule,
			MarkedForDelete: rule.MarkedForDelete,
		}
		dataValue, errors := NewConverter().ConvertToVapi(childRule, model.ChildRuleBindingType())
		if len(errors) > 0 {
			return nil, errors[0]
		}
		rulesChildren = append(rulesChildren, dataValue.(*data.StructValue))
	}
	return rulesChildren, nil
}

func (service *SecurityPolicyService) wrapGroups(groups []model.Group) ([]*data.StructValue, error) {
	var groupsChildren []*data.StructValue
	for _, group := range groups {
		group.ResourceType = &common.ResourceTypeGroup // InfraClient need this field to identify the resource type
		childGroup := model.ChildGroup{
			ResourceType:    "ChildGroup",
			Id:              group.Id,
			MarkedForDelete: group.MarkedForDelete,
			Group:           &group,
		}
		dataValue, errors := NewConverter().ConvertToVapi(childGroup, model.ChildGroupBindingType())
		if len(errors) > 0 {
			return nil, errors[0]
		}
		groupsChildren = append(groupsChildren, dataValue.(*data.StructValue))
	}
	return groupsChildren, nil
}

func (service *SecurityPolicyService) wrapSecurityPolicy(sp *model.SecurityPolicy) ([]*data.StructValue, error) {
	var securityPolicyChildren []*data.StructValue
	childPolicy := model.ChildSecurityPolicy{
		Id:              sp.Id,
		MarkedForDelete: sp.MarkedForDelete,
		ResourceType:    "ChildSecurityPolicy",
		SecurityPolicy:  sp,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childPolicy, model.ChildSecurityPolicyBindingType())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	securityPolicyChildren = append(securityPolicyChildren, dataValue.(*data.StructValue))
	return securityPolicyChildren, nil
}

// WrapHierarchyVpcSecurityPolicy wrap the security policy with groups and rules into a hierarchy SecurityPolicy for OrgRootClient to patch.
func (service *SecurityPolicyService) WrapHierarchyVpcSecurityPolicy(sp *model.SecurityPolicy, gs []model.Group,
	vpcInfo *common.VPCResourceInfo,
) (*model.OrgRoot, error) {
	orgID := (*vpcInfo).OrgID
	projectID := (*vpcInfo).ProjectID
	vpcID := (*vpcInfo).VPCID

	if orgRoot, err := service.wrapOrgRoot(sp, gs, orgID, projectID, vpcID); err != nil {
		return nil, err
	} else {
		return orgRoot, nil
	}
}

func (service *SecurityPolicyService) wrapOrgRoot(sp *model.SecurityPolicy, gs []model.Group,
	orgID, projectID, vpcID string,
) (*model.OrgRoot, error) {
	// This is the outermost layer of the hierarchy orgRoot client in VPC mode.
	// It doesn't need ID field.
	resourceType := common.ResourceTypeOrgRoot
	children, err := service.wrapOrg(sp, gs, orgID, projectID, vpcID)
	if err != nil {
		return nil, err
	}
	orgRoot := model.OrgRoot{
		Children:     children,
		ResourceType: &resourceType,
	}
	return &orgRoot, nil
}

func (service *SecurityPolicyService) wrapOrg(sp *model.SecurityPolicy, gs []model.Group,
	orgID, projectID, vpcID string,
) ([]*data.StructValue, error) {
	children, err := service.wrapProject(sp, gs, projectID, vpcID)
	if err != nil {
		return nil, err
	}
	targetType := common.ResourceTypeOrg
	childProject := model.ChildResourceReference{
		Id:           &orgID,
		ResourceType: "ChildResourceReference",
		TargetType:   &targetType,
		Children:     children,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childProject, model.ChildResourceReferenceBindingType())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return []*data.StructValue{dataValue.(*data.StructValue)}, nil
}

func (service *SecurityPolicyService) wrapProject(sp *model.SecurityPolicy, gs []model.Group,
	projectID, vpcID string,
) ([]*data.StructValue, error) {
	children, err := service.wrapVPC(sp, gs, vpcID)
	if err != nil {
		return nil, err
	}
	targetType := common.ResourceTypeProject
	childProject := model.ChildResourceReference{
		Id:           &projectID,
		ResourceType: "ChildResourceReference",
		TargetType:   &targetType,
		Children:     children,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childProject, model.ChildResourceReferenceBindingType())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return []*data.StructValue{dataValue.(*data.StructValue)}, nil
}

func (service *SecurityPolicyService) wrapVPC(sp *model.SecurityPolicy, gs []model.Group,
	vpcID string,
) ([]*data.StructValue, error) {
	rulesChildren, err := service.wrapRules(sp.Rules)
	if err != nil {
		return nil, err
	}
	sp.Rules = nil
	sp.Children = rulesChildren
	sp.ResourceType = &common.ResourceTypeSecurityPolicy

	securityPolicyChildren, err := service.wrapSecurityPolicy(sp)
	if err != nil {
		return nil, err
	}
	var resourceReferenceChildren []*data.StructValue
	resourceReferenceChildren = append(resourceReferenceChildren, securityPolicyChildren...)
	groupsChildren, err := service.wrapGroups(gs)
	if err != nil {
		return nil, err
	}
	resourceReferenceChildren = append(resourceReferenceChildren, groupsChildren...)

	targetType := common.ResourceTypeVpc
	childVPC := model.ChildResourceReference{
		Id:           &vpcID,
		ResourceType: "ChildResourceReference",
		TargetType:   &targetType,
		Children:     resourceReferenceChildren,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childVPC, model.ChildResourceReferenceBindingType())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return []*data.StructValue{dataValue.(*data.StructValue)}, nil
}

func (service *SecurityPolicyService) wrapChildShares(shares []model.Share) ([]*data.StructValue, error) {
	var sharesChildren []*data.StructValue
	resourceType := common.ResourceTypeChildShare

	for _, share := range shares {
		childShare := model.ChildShare{
			Id:              share.Id,
			ResourceType:    resourceType,
			MarkedForDelete: share.MarkedForDelete,
			Share:           &share,
		}

		dataValue, errors := NewConverter().ConvertToVapi(childShare, model.ChildShareBindingType())
		if len(errors) > 0 {
			return nil, errors[0]
		}
		sharesChildren = append(sharesChildren, dataValue.(*data.StructValue))
	}
	return sharesChildren, nil
}

func (service *SecurityPolicyService) WrapHierarchyProjectShares(shares []model.Share) (*model.Infra, error) {
	infraChildren, err := service.wrapChildShares(shares)
	if err != nil {
		return nil, err
	}

	// This is the outermost layer of the hierarchy project infra client in VPC mode.
	// It doesn't need ID field.
	infra, err := service.wrapInfra(infraChildren)
	if err != nil {
		return nil, err
	}
	return infra, nil
}

func (service *SecurityPolicyService) WrapHierarchyProjectGroups(groups []model.Group) (*model.Infra, error) {
	var resourceReferenceChildren []*data.StructValue
	groupsChildren, err := service.wrapGroups(groups)
	if err != nil {
		return nil, err
	}
	resourceReferenceChildren = append(resourceReferenceChildren, groupsChildren...)
	domainId := getVpcProjectDomain()
	infraChildren, err := service.wrapDomainResource(resourceReferenceChildren, domainId)
	if err != nil {
		return nil, err
	}

	// This is the outermost layer of the hierarchy project infra client in VPC mode.
	// It doesn't need ID field.
	infra, err := service.wrapInfra(infraChildren)
	if err != nil {
		return nil, err
	}
	return infra, nil
}
