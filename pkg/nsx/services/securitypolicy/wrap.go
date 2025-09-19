/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

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
	resourceType := common.ResourceTypeInfra
	infraObj := model.Infra{
		Children:     children,
		ResourceType: &resourceType,
	}
	return &infraObj, nil
}

func (service *SecurityPolicyService) wrapDomainResource(children []*data.StructValue, domainId string) ([]*data.StructValue, error) {
	var domainChildren []*data.StructValue
	targetType := common.ResourceTypeDomain
	resourceType := common.ResourceTypeChildResourceReference
	id := domainId

	childDomain := model.ChildResourceReference{
		Id:           &id,
		ResourceType: resourceType,
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
	resourceType := common.ResourceTypeChildRule

	for _, r := range rules {
		rule := r
		rule.ResourceType = &common.ResourceTypeRule // need this field to identify the resource type
		childRule := model.ChildRule{                // We need to put child rule's id into upper level, otherwise, NSX-T will not find the child rule
			ResourceType:    resourceType, // Children are not allowed for rule, so we don't need to wrap ServiceEntry into Children
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
	resourceType := common.ResourceTypeChildGroup

	for _, g := range groups {
		group := g
		group.ResourceType = &common.ResourceTypeGroup // need this field to identify the resource type
		childGroup := model.ChildGroup{
			ResourceType:    resourceType,
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
	resourceType := common.ResourceTypeChildSecurityPolicy

	childPolicy := model.ChildSecurityPolicy{
		Id:              sp.Id,
		MarkedForDelete: sp.MarkedForDelete,
		ResourceType:    resourceType,
		SecurityPolicy:  sp,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childPolicy, model.ChildSecurityPolicyBindingType())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	securityPolicyChildren = append(securityPolicyChildren, dataValue.(*data.StructValue))
	return securityPolicyChildren, nil
}

// wrapHierarchyVpcSecurityPolicy wrap the security policy with groups and rules in VPC level and associated project infra children including project shares and groups
// into one hierarchy resource tree for OrgRootClient to patch.
func (service *SecurityPolicyService) wrapHierarchyVpcSecurityPolicy(sp *model.SecurityPolicy, vpcGroups []model.Group, projectInfraChildren []*data.StructValue,
	vpcInfo *common.VPCResourceInfo,
) (*model.OrgRoot, error) {
	orgID := (*vpcInfo).OrgID
	projectID := (*vpcInfo).ProjectID
	vpcID := (*vpcInfo).VPCID

	if orgRoot, err := service.wrapOrgRoot(sp, vpcGroups, projectInfraChildren, orgID, projectID, vpcID); err != nil {
		return nil, err
	} else {
		return orgRoot, nil
	}
}

func (service *SecurityPolicyService) wrapOrgRoot(sp *model.SecurityPolicy, vpcGroups []model.Group, projectInfraChildren []*data.StructValue,
	orgID, projectID, vpcID string,
) (*model.OrgRoot, error) {
	// This is the outermost layer of the hierarchy orgRoot client in VPC mode.
	// It doesn't need ID field.
	resourceType := common.ResourceTypeOrgRoot
	children, err := service.wrapOrg(sp, vpcGroups, projectInfraChildren, orgID, projectID, vpcID)
	if err != nil {
		return nil, err
	}
	orgRoot := model.OrgRoot{
		Children:     children,
		ResourceType: &resourceType,
	}
	return &orgRoot, nil
}

func (service *SecurityPolicyService) wrapOrg(sp *model.SecurityPolicy, vpcGroups []model.Group, projectInfraChildren []*data.StructValue,
	orgID, projectID, vpcID string,
) ([]*data.StructValue, error) {
	children, err := service.wrapProject(sp, vpcGroups, projectInfraChildren, projectID, vpcID)
	if err != nil {
		return nil, err
	}
	targetType := common.ResourceTypeOrg
	resourceType := common.ResourceTypeChildResourceReference

	childProject := model.ChildResourceReference{
		Id:           &orgID,
		ResourceType: resourceType,
		TargetType:   &targetType,
		Children:     children,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childProject, model.ChildResourceReferenceBindingType())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return []*data.StructValue{dataValue.(*data.StructValue)}, nil
}

func (service *SecurityPolicyService) wrapProject(sp *model.SecurityPolicy, vpcGroups []model.Group, projectInfraChildren []*data.StructValue,
	projectID, vpcID string,
) ([]*data.StructValue, error) {
	vpcChildren, err := service.wrapVPC(sp, vpcGroups, vpcID)
	if err != nil {
		return nil, err
	}

	var resourceReferenceChildren []*data.StructValue
	resourceReferenceChildren = append(resourceReferenceChildren, projectInfraChildren...)
	resourceReferenceChildren = append(resourceReferenceChildren, vpcChildren...)

	targetType := common.ResourceTypeProject
	resourceType := common.ResourceTypeChildResourceReference

	childProject := model.ChildResourceReference{
		Id:           &projectID,
		ResourceType: resourceType,
		TargetType:   &targetType,
		Children:     resourceReferenceChildren,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childProject, model.ChildResourceReferenceBindingType())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return []*data.StructValue{dataValue.(*data.StructValue)}, nil
}

func (service *SecurityPolicyService) wrapVPC(sp *model.SecurityPolicy, vpcGroups []model.Group, vpcID string) ([]*data.StructValue, error) {
	var resourceReferenceChildren []*data.StructValue
	if sp != nil {
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
		resourceReferenceChildren = append(resourceReferenceChildren, securityPolicyChildren...)
	}

	groupsChildren, err := service.wrapGroups(vpcGroups)
	if err != nil {
		return nil, err
	}
	resourceReferenceChildren = append(resourceReferenceChildren, groupsChildren...)

	targetType := common.ResourceTypeVpc
	resourceType := common.ResourceTypeChildResourceReference
	childVPC := model.ChildResourceReference{
		Id:           &vpcID,
		ResourceType: resourceType,
		TargetType:   &targetType,
		Children:     resourceReferenceChildren,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childVPC, model.ChildResourceReferenceBindingType())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return []*data.StructValue{dataValue.(*data.StructValue)}, nil
}

func (service *SecurityPolicyService) wrapShares(shares []model.Share) ([]*data.StructValue, error) {
	var sharesChildren []*data.StructValue
	resourceType := common.ResourceTypeChildShare

	for _, s := range shares {
		share := s
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

func (service *SecurityPolicyService) wrapChildTargetInfra(children []*data.StructValue) ([]*data.StructValue, error) {
	var infraChildren []*data.StructValue
	targetType := common.ResourceTypeInfra
	resourceType := common.ResourceTypeChildResourceReference

	// This is the outermost layer of the hierarchy project child infra in VPC mode.
	// It doesn't need ID field.
	childInfra := model.ChildResourceReference{
		ResourceType: resourceType,
		TargetType:   &targetType,
		Children:     children,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childInfra, model.ChildResourceReferenceBindingType())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	infraChildren = append(infraChildren, dataValue.(*data.StructValue))
	return infraChildren, nil
}

// wrapHierarchyProjectResources wrap the project shares and groups into a project infra children in VPC mode.
func (service *SecurityPolicyService) wrapHierarchyProjectResources(shares []model.Share, groups []model.Group) ([]*data.StructValue, error) {
	var domainReferenceChildren []*data.StructValue
	var infraChildren []*data.StructValue

	shareChildren, err := service.wrapShares(shares)
	if err != nil {
		return nil, err
	}
	infraChildren = append(infraChildren, shareChildren...)

	groupsChildren, err := service.wrapGroups(groups)
	if err != nil {
		return nil, err
	}
	domainReferenceChildren = append(domainReferenceChildren, groupsChildren...)
	domainId := getVPCProjectDomain()
	domainTargetChildren, err := service.wrapDomainResource(domainReferenceChildren, domainId)
	if err != nil {
		return nil, err
	}
	infraChildren = append(infraChildren, domainTargetChildren...)

	wrapProjInfraChildren, err := service.wrapChildTargetInfra(infraChildren)
	if err != nil {
		return nil, err
	}
	return wrapProjInfraChildren, nil
}

// wrapHierarchyInfraResources wrap the infra shares and groups into a infra children in VPC mode.
func (service *SecurityPolicyService) wrapHierarchyInfraResources(shares []model.Share, groups []model.Group) (*model.Infra, error) {
	var domainReferenceChildren []*data.StructValue
	var infraChildren []*data.StructValue

	shareChildren, err := service.wrapShares(shares)
	if err != nil {
		return nil, err
	}
	infraChildren = append(infraChildren, shareChildren...)

	groupsChildren, err := service.wrapGroups(groups)
	if err != nil {
		return nil, err
	}
	domainReferenceChildren = append(domainReferenceChildren, groupsChildren...)
	domainId := getDefaultProjectDomain()
	domainTargetChildren, err := service.wrapDomainResource(domainReferenceChildren, domainId)
	if err != nil {
		return nil, err
	}
	infraChildren = append(infraChildren, domainTargetChildren...)

	wrapInfra, err := service.wrapInfra(infraChildren)
	if err != nil {
		return nil, err
	}
	return wrapInfra, nil
}
