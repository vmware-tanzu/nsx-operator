package securitypolicy

import (
	"encoding/json"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/data/serializers/cleanjson"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	util2 "github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func PolicyEqual(existingSecurityPolicy *model.SecurityPolicy, securityPolicy *model.SecurityPolicy) bool {
	s1, _ := json.Marshal(simplifySecurityPolicy(existingSecurityPolicy))
	s2, _ := json.Marshal(simplifySecurityPolicy(securityPolicy))
	if string(s1) == string(s2) {
		return true
	}
	log.V(1).Info(
		"security policies differ",
		"current NSX security policy", simplifySecurityPolicy(existingSecurityPolicy),
		"desired NSX security policy", simplifySecurityPolicy(securityPolicy),
	)
	return false
}

func RulesEqual(existingRules []model.Rule, rules []model.Rule) (bool, []model.Rule) {
	isEqual := true
	var legacyRules []model.Rule
	var newRuleIds []string
	for _, rule := range rules {
		newRuleIds = append(newRuleIds, *rule.Id)
	}

	for _, existingRule := range existingRules {
		if !util2.Contains(newRuleIds, *existingRule.Id) {
			isEqual = false
			legacyRules = append(legacyRules, existingRule)
		}
	}

	if !isEqual || len(existingRules) != len(rules) {
		return false, legacyRules
	}

	isEqual = RulesEqualDetail(existingRules, rules)
	return isEqual, legacyRules
}

func RulesEqualDetail(existingRules []model.Rule, rules []model.Rule) bool {
	isEqual := true
	for i := 0; i < len(rules); i++ {
		r1, _ := simplifyRule(&existingRules[i]).GetDataValue__()
		r2, _ := simplifyRule(&rules[i]).GetDataValue__()
		var dataValueToJSONEncoder = cleanjson.NewDataValueToJsonEncoder()
		s1, _ := dataValueToJSONEncoder.Encode(r1)
		s2, _ := dataValueToJSONEncoder.Encode(r2)
		if s1 != s2 {
			log.V(1).Info(
				"rules differ",
				"current NSX rule", simplifyRule(&existingRules[i]),
				"desired NSX rule", simplifyRule(&rules[i]),
			)
			isEqual = false
			break
		}
	}
	return isEqual
}

func GroupsEqual(existingGroups []model.Group, groups []model.Group) (bool, []model.Group) {
	isEqual := true
	var legacyGroups []model.Group
	var newGroupIds []string
	for _, group := range groups {
		newGroupIds = append(newGroupIds, *group.Id)
	}

	for _, existingGroup := range existingGroups {
		if !util2.Contains(newGroupIds, *existingGroup.Id) {
			isEqual = false
			legacyGroups = append(legacyGroups, existingGroup)
		}
	}

	if !isEqual || len(existingGroups) != len(groups) {
		return false, legacyGroups
	}

	for i := 0; i < len(groups); i++ {
		g1, _ := simplifyGroup(&existingGroups[i]).GetDataValue__()
		g2, _ := simplifyGroup(&groups[i]).GetDataValue__()
		var dataValueToJSONEncoder = cleanjson.NewDataValueToJsonEncoder()
		s1, _ := dataValueToJSONEncoder.Encode(g1)
		s2, _ := dataValueToJSONEncoder.Encode(g2)
		if s1 != s2 {
			log.V(1).Info(
				"groups differ",
				"current NSX group", simplifyGroup(&existingGroups[i]),
				"desired NSX group", simplifyGroup(&groups[i]),
			)
			return false, legacyGroups
		}
	}
	return true, nil
}

// simplifySecurityPolicy is used for abstract the key properties from model.SecurityPolicy, so that
// some unnecessary properties like "CreateTime" can be ignored then we can compare the existing one
// and desired one to determine whether the NSX-T resource should be updated.
func simplifySecurityPolicy(sp *model.SecurityPolicy) *model.SecurityPolicy {
	return &model.SecurityPolicy{
		Id:             sp.Id,
		DisplayName:    sp.DisplayName,
		SequenceNumber: sp.SequenceNumber,
		Scope:          sp.Scope,
		Tags:           sp.Tags,
	}
}

func simplifyRule(rule *model.Rule) *model.Rule {
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

func simplifyGroup(group *model.Group) *model.Group {
	return &model.Group{
		Id:          group.Id,
		DisplayName: group.Id,
		Tags:        group.Tags,
		Expression:  group.Expression,
	}
}
