package securitypolicy

import (
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type (
	SecurityPolicy model.SecurityPolicy
	Rule           model.Rule
	Group          model.Group
	Share          model.Share
)

type Comparable = common.Comparable

func (sp *SecurityPolicy) Key() string {
	return *sp.Id
}

func (group *Group) Key() string {
	return *group.Id
}

func (rule *Rule) Key() string {
	return *rule.Id
}

func (share *Share) Key() string {
	return *share.Id
}

func (sp *SecurityPolicy) Value() data.DataValue {
	s := &SecurityPolicy{
		Id:             sp.Id,
		DisplayName:    sp.DisplayName,
		SequenceNumber: sp.SequenceNumber,
		Scope:          sp.Scope,
		Tags:           sp.Tags,
	}
	dataValue, _ := ComparableToSecurityPolicy(s).GetDataValue__()
	return dataValue
}

func (rule *Rule) Value() data.DataValue {
	r := &Rule{
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
	dataValue, _ := ComparableToRule(r).GetDataValue__()
	return dataValue
}

func (group *Group) Value() data.DataValue {
	g := &Group{
		Id:          group.Id,
		DisplayName: group.Id,
		Tags:        group.Tags,
		Expression:  group.Expression,
	}
	dataValue, _ := ComparableToGroup(g).GetDataValue__()
	return dataValue
}

func (share *Share) Value() data.DataValue {
	s := &Share{
		Id:          share.Id,
		DisplayName: share.DisplayName,
		Tags:        share.Tags,
		SharedWith:  share.SharedWith,
		Children:    share.Children,
	}
	dataValue, _ := ComparableToShare(s).GetDataValue__()
	return dataValue
}

func SecurityPolicyPtrToComparable(sp *model.SecurityPolicy) Comparable {
	return (*SecurityPolicy)(sp)
}

func SecurityPolicyToComparable(sp model.SecurityPolicy) Comparable {
	return (*SecurityPolicy)(&sp)
}

func RulesPtrToComparable(rules []*model.Rule) []Comparable {
	res := make([]Comparable, 0, len(rules))
	for i := range rules {
		res = append(res, (*Rule)((rules[i])))
	}
	return res
}

func RulesToComparable(rules []model.Rule) []Comparable {
	res := make([]Comparable, 0, len(rules))
	for i := range rules {
		res = append(res, (*Rule)((&rules[i])))
	}
	return res
}

func GroupsPtrToComparable(groups []*model.Group) []Comparable {
	res := make([]Comparable, 0, len(groups))
	for i := range groups {
		res = append(res, (*Group)((groups[i])))
	}
	return res
}

func GroupsToComparable(groups []model.Group) []Comparable {
	res := make([]Comparable, 0, len(groups))
	for i := range groups {
		res = append(res, (*Group)((&groups[i])))
	}
	return res
}

func SharesPtrToComparable(shares []*model.Share) []Comparable {
	res := make([]Comparable, 0, len(shares))
	for i := range shares {
		res = append(res, (*Share)((shares[i])))
	}
	return res
}

func SharesToComparable(shares []model.Share) []Comparable {
	res := make([]Comparable, 0, len(shares))
	for i := range shares {
		res = append(res, (*Share)(&(shares[i])))
	}
	return res
}

func ComparableToSecurityPolicy(sp Comparable) *model.SecurityPolicy {
	return (*model.SecurityPolicy)(sp.(*SecurityPolicy))
}

func ComparableToRules(rules []Comparable) []model.Rule {
	res := make([]model.Rule, 0, len(rules))
	for _, rule := range rules {
		res = append(res, (model.Rule)(*rule.(*Rule)))
	}
	return res
}

func ComparableToRule(rule Comparable) *model.Rule {
	return (*model.Rule)(rule.(*Rule))
}

func ComparableToGroups(groups []Comparable) []model.Group {
	res := make([]model.Group, 0, len(groups))
	for _, group := range groups {
		res = append(res, (model.Group)(*(group.(*Group))))
	}
	return res
}

func ComparableToGroup(group Comparable) *model.Group {
	return (*model.Group)(group.(*Group))
}

func ComparableToShares(shares []Comparable) []model.Share {
	res := make([]model.Share, 0, len(shares))
	for _, share := range shares {
		res = append(res, (model.Share)(*(share.(*Share))))
	}
	return res
}

func ComparableToShare(share Comparable) *model.Share {
	return (*model.Share)(share.(*Share))
}
