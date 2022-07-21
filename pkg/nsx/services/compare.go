package services

import (
	"encoding/json"
	"sort"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/data/serializers/cleanjson"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

func SecurityPolicyEqual(
	existingSecurityPolicy *model.SecurityPolicy,
	securityPolicy *model.SecurityPolicy,
) bool {
	s1, _ := json.Marshal(simplifySecurityPolicy(existingSecurityPolicy))
	s2, _ := json.Marshal(simplifySecurityPolicy(securityPolicy))
	if string(s1) == string(s2) {
		return true
	}
	log.Info("security policy diff", "nsx sp", simplifySecurityPolicy(existingSecurityPolicy),
		"k8s sp", simplifySecurityPolicy(securityPolicy))
	return false
}

func RulesEqual(existingRules []model.Rule, rules []model.Rule) (bool, []model.Rule) {
	// sort the rules by id, otherwise expandRule may return different results, only the sequence of the
	// rule is different, so sort by port number, and it avoids the needless updates.
	sortRules := func(rules []model.Rule) {
		sort.Slice(rules, func(i, j int) bool {
			return *(rules[i].Id) > *(rules[j].Id)
		})
	}
	sortRules(existingRules)
	sortRules(rules)

	var isEqual bool
	// legacyRules means the rules that are not in the new rules, we should destroy them.
	var legacyRules []model.Rule

	diffIndex := len(existingRules) - len(rules)
	if diffIndex != 0 {
		isEqual = false
		if diffIndex > 0 {
			legacyRules = append(legacyRules, existingRules[diffIndex:]...)
		}
		return isEqual, legacyRules
	}

	isEqual = RulesEqualDetail(existingRules, rules)
	return isEqual, legacyRules
}

func RulesEqualDetail(existingRules []model.Rule, rules []model.Rule) bool {
	isEqual := true
	for i := 0; i < len(rules); i++ {
		r1, _ := simplifyRule(&existingRules[i]).GetDataValue__()
		r2, _ := simplifyRule(&rules[i]).GetDataValue__()
		dataValueToJSONEncoder := cleanjson.NewDataValueToJsonEncoder()
		s1, _ := dataValueToJSONEncoder.Encode(r1)
		s2, _ := dataValueToJSONEncoder.Encode(r2)
		if s1 != s2 {
			log.Info(
				"rule diff",
				"nsx rule",
				simplifyRule(&existingRules[i]),
				"k8s rule",
				simplifyRule(&rules[i]),
			)
			isEqual = false
			break
		}
	}
	return isEqual
}

func GroupsEqual(existingGroups []model.Group, groups []model.Group) bool {
	sortGroups := func(groups []model.Group) {
		sort.Slice(groups, func(i, j int) bool {
			return *(groups[i].Id) > *(groups[j].Id)
		})
	}
	sortGroups(existingGroups)
	sortGroups(groups)

	if len(existingGroups) != len(groups) {
		return false
	}
	for i := 0; i < len(existingGroups); i++ {
		g1, _ := json.Marshal(simplifyGroup(&existingGroups[i]))
		g2, _ := json.Marshal(simplifyGroup(&groups[i]))
		if string(g1) != string(g2) {
			log.Info(
				"group diff",
				"nsx group",
				simplifyGroup(&existingGroups[i]),
				"k8s group",
				simplifyGroup(&groups[i]),
			)
			return false
		}
	}
	return true
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
		DestinationGroups: rule.DestinationGroups,
		SourceGroups:      rule.SourceGroups,
	}
}

func simplifyGroup(group *model.Group) *model.Group {
	return &model.Group{
		Id:          group.Id,
		DisplayName: group.Id,
		Tags:        group.Tags,
	}
}
