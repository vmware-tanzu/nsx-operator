package securitypolicy

import (
	"errors"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// keyFunc is used to get the key of a resource, usually, which is the ID of the resource
func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case model.Group:
		return *v.Id, nil
	case model.SecurityPolicy:
		return *v.Id, nil
	case model.Rule:
		return *v.Id, nil
	default:
		return "", errors.New("keyFunc doesn't support unknown type")
	}
}

// indexFunc is used to get index of a resource, usually, which is the UID of the CR controller reconciles,
// index is used to filter out resources which are related to the CR
func indexFunc(obj interface{}) ([]string, error) {
	res := make([]string, 0, 5)
	switch o := obj.(type) {
	case model.SecurityPolicy:
		return filterTag(o.Tags), nil
	case model.Group:
		return filterTag(o.Tags), nil
	case model.Rule:
		return filterTag(o.Tags), nil
	default:
		return res, errors.New("indexFunc doesn't support unknown type")
	}
}

var filterTag = func(v []model.Tag) []string {
	res := make([]string, 0, 5)
	for _, tag := range v {
		if *tag.Scope == common.TagScopeSecurityPolicyCRUID {
			res = append(res, *tag.Tag)
		}
	}
	return res
}

func indexGroupFunc(obj interface{}) ([]string, error) {
	res := make([]string, 0, 5)
	switch o := obj.(type) {
	case model.Group:
		return filterGroupTag(o.Tags), nil
	default:
		return res, errors.New("indexGroupFunc doesn't support unknown type")
	}
}

var filterGroupTag = func(v []model.Tag) []string {
	res := make([]string, 0, 5)
	for _, tag := range v {
		if *tag.Scope == common.TagScopeRuleID {
			res = append(res, *tag.Tag)
		}
	}
	return res
}

// SecurityPolicyStore is a store for security policy
type SecurityPolicyStore struct {
	common.ResourceStore
}

// RuleStore is a store for rules of security policy
type RuleStore struct {
	common.ResourceStore
}

// GroupStore is a store for groups referenced by security policy or rule
type GroupStore struct {
	common.ResourceStore
}

func (securityPolicyStore *SecurityPolicyStore) Operate(i interface{}) error {
	if i == nil {
		return nil
	}
	sp := i.(*model.SecurityPolicy)
	if sp.MarkedForDelete != nil && *sp.MarkedForDelete {
		err := securityPolicyStore.Delete(*sp) // Pass in the object to be deleted, not the pointer
		log.V(1).Info("delete security policy from store", "securitypolicy", sp)
		if err != nil {
			return err
		}
	} else {
		err := securityPolicyStore.Add(*sp)
		log.V(1).Info("add security policy to store", "securitypolicy", sp)
		if err != nil {
			return err
		}
	}
	return nil
}

func (securityPolicyStore *SecurityPolicyStore) GetByKey(key string) *model.SecurityPolicy {
	var securityPolicy model.SecurityPolicy
	obj := securityPolicyStore.ResourceStore.GetByKey(key)
	if obj != nil {
		securityPolicy = obj.(model.SecurityPolicy)
	}
	return &securityPolicy
}

func (securityPolicyStore *SecurityPolicyStore) GetByIndex(key string, value string) []model.SecurityPolicy {
	securityPolicies := make([]model.SecurityPolicy, 0)
	objs := securityPolicyStore.ResourceStore.GetByIndex(key, value)
	for _, securityPolicy := range objs {
		securityPolicies = append(securityPolicies, securityPolicy.(model.SecurityPolicy))
	}
	return securityPolicies
}

func (ruleStore *RuleStore) Operate(i interface{}) error {
	sp := i.(*model.SecurityPolicy)
	for _, rule := range sp.Rules {
		if rule.MarkedForDelete != nil && *rule.MarkedForDelete {
			err := ruleStore.Delete(rule)
			log.V(1).Info("delete rule from store", "rule", rule)
			if err != nil {
				return err
			}
		} else {
			err := ruleStore.Add(rule)
			log.V(1).Info("add rule to store", "rule", rule)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (ruleStore *RuleStore) GetByIndex(key string, value string) []model.Rule {
	rules := make([]model.Rule, 0)
	objs := ruleStore.ResourceStore.GetByIndex(key, value)
	for _, rule := range objs {
		rules = append(rules, rule.(model.Rule))
	}
	return rules
}

func (groupStore *GroupStore) Operate(i interface{}) error {
	gs := i.(*[]model.Group)
	for _, group := range *gs {
		if group.MarkedForDelete != nil && *group.MarkedForDelete {
			err := groupStore.Delete(group)
			log.V(1).Info("delete group from store", "group", group)
			if err != nil {
				return err
			}
		} else {
			err := groupStore.Add(group)
			log.V(1).Info("add group to store", "group", group)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (groupStore *GroupStore) GetByIndex(key string, value string) []model.Group {
	groups := make([]model.Group, 0)
	objs := groupStore.ResourceStore.GetByIndex(key, value)
	for _, group := range objs {
		groups = append(groups, group.(model.Group))
	}
	return groups
}
