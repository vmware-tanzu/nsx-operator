package services

import (
	"encoding/json"
	"sort"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/data/serializers/cleanjson"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

func SecurityPolicyEqual(existingSecurityPolicy *model.SecurityPolicy, securityPolicy *model.SecurityPolicy) bool {
	s1, _ := json.Marshal(simplifySecurityPolicy(existingSecurityPolicy))
	s2, _ := json.Marshal(simplifySecurityPolicy(securityPolicy))
	if string(s1) == string(s2) {
		return true
	}
	log.Info("security policy diff", "nsx sp", s1, "k8s sp", s2)
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

	isEqual := true
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
		// if we use dataValueToJSONEncoder to marshal the whole rule, it will lose the "ServiceEntries" property,
		// it shows that the result is "service_entries":[]" or even nothing, so everytime it shows not equal and
		// operate nsx-t repeatedly, perhaps the sdk failed to marshal the nested property which is a list of
		// *data.StructValue. We suppose *data.ListValue is the right way to do it.
		// however, the ServiceEntries which model.Rule contains is []*data.StructValue, rather than *data.ListValue,
		// we don't know why sdk design like this, We leave it for follow-up observation.
		// now we fix this problem by comparing them separately.
		r1, _ := json.Marshal(simplifyRule(&existingRules[i]))
		r2, _ := json.Marshal(simplifyRule(&rules[i]))
		if string(r1) != string(r2) {
			log.Info("rule diff", "nsx rule", simplifyRule(&existingRules[i]), "k8s rule", simplifyRule(&rules[i]))
			isEqual = false
			break
		}

		se1 := existingRules[i].ServiceEntries
		se2 := rules[i].ServiceEntries
		if len(se1) != len(se2) {
			log.Info("service entry len diff", "nsx service entry len", len(se1), "k8s service entry len", len(se2))
			isEqual = false
			break
		}

		dataValueToJSONEncoder := cleanjson.NewDataValueToJsonEncoder()
		for i := 0; i < len(se1); i++ {
			je1, _ := dataValueToJSONEncoder.Encode(se1[i])
			je2, _ := dataValueToJSONEncoder.Encode(se2[i])
			if je1 != je2 {
				log.Info("service entry diff", "nsx service entry", je1, "k8s service entry", je2)
				isEqual = false
				break
			}
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
			log.Info("group diff", "nsx group", simplifyGroup(&existingGroups[i]), "k8s group", simplifyGroup(&groups[i]))
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
