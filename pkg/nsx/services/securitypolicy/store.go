package securitypolicy

import (
	"errors"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// keyFunc is used to get the key of a resource, usually, which is the ID of the resource
func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case *model.Group:
		return *v.Id, nil
	case *model.SecurityPolicy:
		return *v.Id, nil
	case *model.Rule:
		return *v.Id, nil
	case *model.Share:
		return *v.Id, nil
	default:
		return "", errors.New("keyFunc doesn't support unknown type")
	}
}

func filterTag(tags []model.Tag, tagScope string) []string {
	var res []string
	for _, tag := range tags {
		if *tag.Scope == tagScope {
			res = append(res, *tag.Tag)
		}
	}
	return res
}

// indexBySecurityPolicyUID is used to get index of a resource, usually, which is the UID of the CR controller reconciles,
// index is used to filter out resources which are related to the CR
func indexBySecurityPolicyUID(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.SecurityPolicy:
		return filterTag(o.Tags, common.TagValueScopeSecurityPolicyUID), nil
	case *model.Group:
		return filterTag(o.Tags, common.TagValueScopeSecurityPolicyUID), nil
	case *model.Rule:
		return filterTag(o.Tags, common.TagValueScopeSecurityPolicyUID), nil
	case *model.Share:
		return filterTag(o.Tags, common.TagValueScopeSecurityPolicyUID), nil
	default:
		return nil, errors.New("indexBySecurityPolicyUID doesn't support unknown type")
	}
}

func indexByNetworkPolicyUID(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.SecurityPolicy:
		return filterTag(o.Tags, common.TagScopeNetworkPolicyUID), nil
	case *model.Group:
		return filterTag(o.Tags, common.TagScopeNetworkPolicyUID), nil
	case *model.Rule:
		return filterTag(o.Tags, common.TagScopeNetworkPolicyUID), nil
	case *model.Share:
		return filterTag(o.Tags, common.TagScopeNetworkPolicyUID), nil
	default:
		return nil, errors.New("indexByNetworkPolicyUID doesn't support unknown type")
	}
}

func indexGroupFunc(obj interface{}) ([]string, error) {
	res := make([]string, 0, 5)
	switch o := obj.(type) {
	case *model.Group:
		return filterRuleTag(o.Tags), nil
	default:
		return res, errors.New("indexGroupFunc doesn't support unknown type")
	}
}

var filterRuleTag = func(v []model.Tag) []string {
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

// ShareStore is a store for project shares referenced by security policy rule
type ShareStore struct {
	common.ResourceStore
}

func (securityPolicyStore *SecurityPolicyStore) Apply(i interface{}) error {
	if i == nil {
		return nil
	}
	sp := i.(*model.SecurityPolicy)
	if sp.MarkedForDelete != nil && *sp.MarkedForDelete {
		err := securityPolicyStore.Delete(sp)
		log.V(1).Info("delete security policy from store", "securitypolicy", sp)
		if err != nil {
			return err
		}
	} else {
		err := securityPolicyStore.Add(sp)
		log.V(1).Info("add security policy to store", "securitypolicy", sp)
		if err != nil {
			return err
		}
	}
	return nil
}

func (securityPolicyStore *SecurityPolicyStore) GetByKey(key string) *model.SecurityPolicy {
	var securityPolicy *model.SecurityPolicy
	obj := securityPolicyStore.ResourceStore.GetByKey(key)
	if obj != nil {
		securityPolicy = obj.(*model.SecurityPolicy)
	}
	return securityPolicy
}

func (securityPolicyStore *SecurityPolicyStore) GetByIndex(key string, value string) []*model.SecurityPolicy {
	securityPolicies := make([]*model.SecurityPolicy, 0)
	objs := securityPolicyStore.ResourceStore.GetByIndex(key, value)
	for _, securityPolicy := range objs {
		securityPolicies = append(securityPolicies, securityPolicy.(*model.SecurityPolicy))
	}
	return securityPolicies
}

func (ruleStore *RuleStore) Apply(i interface{}) error {
	rules := i.(*[]model.Rule)
	for _, rule := range *rules {
		tempRule := rule
		if rule.MarkedForDelete != nil && *rule.MarkedForDelete {
			err := ruleStore.Delete(&tempRule)
			log.V(1).Info("delete rule from store", "rule", tempRule)
			if err != nil {
				return err
			}
		} else {
			err := ruleStore.Add(&tempRule)
			log.V(1).Info("add rule to store", "rule", tempRule)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (ruleStore *RuleStore) GetByIndex(key string, value string) []*model.Rule {
	rules := make([]*model.Rule, 0)
	objs := ruleStore.ResourceStore.GetByIndex(key, value)
	for _, rule := range objs {
		rules = append(rules, rule.(*model.Rule))
	}
	return rules
}

func (groupStore *GroupStore) Apply(i interface{}) error {
	gs := i.(*[]model.Group)
	for _, group := range *gs {
		tempGroup := group
		if group.MarkedForDelete != nil && *group.MarkedForDelete {
			err := groupStore.Delete(&tempGroup)
			log.V(1).Info("delete group from store", "group", tempGroup)
			if err != nil {
				return err
			}
		} else {
			err := groupStore.Add(&tempGroup)
			log.V(1).Info("add group to store", "group", tempGroup)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (groupStore *GroupStore) GetByIndex(key string, value string) []*model.Group {
	groups := make([]*model.Group, 0)
	objs := groupStore.ResourceStore.GetByIndex(key, value)
	for _, group := range objs {
		groups = append(groups, group.(*model.Group))
	}
	return groups
}

func (shareStore *ShareStore) Apply(i interface{}) error {
	shares := i.(*[]model.Share)
	for _, share := range *shares {
		tempShare := share
		if share.MarkedForDelete != nil && *share.MarkedForDelete {
			err := shareStore.Delete(&tempShare)
			log.V(1).Info("delete share from store", "share", tempShare)
			if err != nil {
				return err
			}
		} else {
			err := shareStore.Add(&tempShare)
			log.V(1).Info("add share to store", "share", tempShare)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (shareStore *ShareStore) GetByKey(key string) *model.Share {
	var share *model.Share
	obj := shareStore.ResourceStore.GetByKey(key)
	if obj != nil {
		share = obj.(*model.Share)
	}
	return share
}

func (shareStore *ShareStore) GetByIndex(key string, value string) []*model.Share {
	shares := make([]*model.Share, 0)
	objs := shareStore.ResourceStore.GetByIndex(key, value)
	for _, share := range objs {
		shares = append(shares, share.(*model.Share))
	}
	return shares
}
