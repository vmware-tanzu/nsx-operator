package securitypolicy

import (
	"encoding/json"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/data/serializers/cleanjson"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

func (service *SecurityPolicyService) securityPolicyCompare(existingSecurityPolicy *model.SecurityPolicy, securityPolicy *model.SecurityPolicy) *model.SecurityPolicy {
	v1, _ := json.Marshal(service.simplifySecurityPolicy(existingSecurityPolicy))
	v2, _ := json.Marshal(service.simplifySecurityPolicy(securityPolicy))
	if string(v1) == string(v2) {
		return nil
	}
	log.V(1).Info("security policies differ", "current NSX security policy", service.simplifySecurityPolicy(existingSecurityPolicy),
		"desired NSX security policy", service.simplifySecurityPolicy(securityPolicy))
	return securityPolicy
}

func (service *SecurityPolicyService) rulesCompare(existingRules []model.Rule, rules []model.Rule) ([]model.Rule, []model.Rule) {
	staleRules := make([]model.Rule, 0)
	changedRules := make([]model.Rule, 0)

	rulesMap := make(map[string]model.Rule)
	for _, rule := range rules {
		rulesMap[*rule.Id] = rule
	}
	existingRulesMap := make(map[string]model.Rule)
	for _, rule := range existingRules {
		existingRulesMap[*rule.Id] = rule
	}

	for ruleId, rule := range rulesMap {
		if existingRule, ok := existingRulesMap[ruleId]; ok {
			if service.ruleCompareDetail(existingRule, rule) {
				continue
			}
		}
		changedRules = append(changedRules, rule)
	}
	for ruleId, existingRule := range existingRulesMap {
		if _, ok := rulesMap[ruleId]; !ok {
			staleRules = append(staleRules, existingRule)
		}
	}
	log.V(1).Info("rules differ", "stale rules", staleRules, "changed rules", changedRules)
	return changedRules, staleRules
}

func (service *SecurityPolicyService) ruleCompareDetail(existingRule model.Rule, rule model.Rule) bool {
	r1, _ := service.simplifyRule(&existingRule).GetDataValue__()
	r2, _ := service.simplifyRule(&rule).GetDataValue__()
	var dataValueToJSONEncoder = cleanjson.NewDataValueToJsonEncoder()
	s1, _ := dataValueToJSONEncoder.Encode(r1)
	s2, _ := dataValueToJSONEncoder.Encode(r2)
	if s1 != s2 {
		log.Info("rules differ", "current NSX rule", s1, "desired NSX rule", s2)
		return false
	}
	return true
}

func (service *SecurityPolicyService) groupsCompare(existingGroups []model.Group, groups []model.Group) ([]model.Group, []model.Group) {
	staleGroups := make([]model.Group, 0)
	changedGroups := make([]model.Group, 0)

	groupsMap := make(map[string]model.Group)
	for _, group := range groups {
		groupsMap[*group.Id] = group
	}
	existingGroupsMap := make(map[string]model.Group)
	for _, group := range existingGroups {
		existingGroupsMap[*group.Id] = group
	}

	for groupId, group := range groupsMap {
		if existingGroup, ok := existingGroupsMap[groupId]; ok {
			if service.groupCompareDetail(existingGroup, group) {
				continue
			}
		}
		changedGroups = append(changedGroups, group)
	}
	for groupId, existingGroup := range existingGroupsMap {
		if _, ok := groupsMap[groupId]; !ok {
			staleGroups = append(staleGroups, existingGroup)
		}
	}
	log.V(1).Info("groups differ", "stale groups", staleGroups, "changed groups", changedGroups)
	return changedGroups, staleGroups
}

func (service *SecurityPolicyService) groupCompareDetail(existingGroup model.Group, group model.Group) bool {
	g1, _ := json.Marshal(service.simplifyGroup(&existingGroup))
	g2, _ := json.Marshal(service.simplifyGroup(&group))
	if string(g1) != string(g2) {
		log.V(1).Info("groups differ", "current NSX group", service.simplifyGroup(&existingGroup), "desired NSX group", service.simplifyGroup(&group))
		return false
	}
	return true
}

// simplifySecurityPolicy is used for abstract the key properties from model.SecurityPolicy, so that
// some unnecessary properties like "CreateTime" can be ignored then we can compare the existing one
// and desired one to determine whether the NSX-T resource should be updated.
func (service *SecurityPolicyService) simplifySecurityPolicy(sp *model.SecurityPolicy) *model.SecurityPolicy {
	return &model.SecurityPolicy{
		Id:             sp.Id,
		DisplayName:    sp.DisplayName,
		SequenceNumber: sp.SequenceNumber,
		Scope:          sp.Scope,
		Tags:           sp.Tags,
	}
}

func (service *SecurityPolicyService) simplifyRule(rule *model.Rule) *model.Rule {
	return &model.Rule{
		DisplayName:       rule.DisplayName,
		Id:                rule.Id,
		Tags:              rule.Tags,
		Direction:         rule.Direction,
		Scope:             rule.Scope,
		SequenceNumber:    rule.SequenceNumber,
		Action:            rule.Action,
		Services:          rule.Services,
		ServiceEntries:    rule.ServiceEntries,
		DestinationGroups: rule.DestinationGroups,
		SourceGroups:      rule.SourceGroups,
	}
}

func (service *SecurityPolicyService) simplifyGroup(group *model.Group) *model.Group {
	return &model.Group{
		Id:          group.Id,
		DisplayName: group.Id,
		Tags:        group.Tags,
		Expression:  group.Expression,
	}
}
