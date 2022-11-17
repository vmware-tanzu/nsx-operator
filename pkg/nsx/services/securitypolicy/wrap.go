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

// WrapHierarchySecurityPolicy Wrap the security policy with groups and rules into a hierarchy security policy for InfraClient to patch.
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

	infraChildren, err := service.wrapResourceReference(resourceReferenceChildren)
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
	// This is the outermost layer of the hierarchy security policy.
	// It doesn't need ID field.
	infraType := "Infra"
	infraObj := model.Infra{
		Children:     children,
		ResourceType: &infraType,
	}
	return &infraObj, nil
}

func (service *SecurityPolicyService) wrapResourceReference(children []*data.StructValue) ([]*data.StructValue, error) {
	var resourceReferenceChildren []*data.StructValue
	targetType := "Domain"
	id := getDomain(service)
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
	resourceReferenceChildren = append(resourceReferenceChildren, dataValue.(*data.StructValue))
	return resourceReferenceChildren, nil
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
