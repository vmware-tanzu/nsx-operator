package securitypolicy

import (
	"errors"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/client-go/tools/cache"

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

func securityPolicyAssertion(i interface{}) interface{} {
	return i.(model.SecurityPolicy)
}

func groupAssertion(i interface{}) interface{} {
	return i.(model.Group)
}

func ruleAssertion(i interface{}) interface{} {
	return i.(model.Rule)
}

func (securityPolicyStore *SecurityPolicyStore) CRUDResource(i interface{}) error {
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

func (ruleStore *RuleStore) CRUDResource(i interface{}) error {
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

func (groupStore *GroupStore) CRUDResource(i interface{}) error {
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

func InitializeStore(service *SecurityPolicyService) {
	service.ResourceCacheMap = make(map[string]cache.Indexer)
	service.ResourceCacheMap[ResourceTypeSecurityPolicy] = cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeSecurityPolicyCRUID: indexFunc})
	service.ResourceCacheMap[ResourceTypeGroup] = cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeSecurityPolicyCRUID: indexFunc})
	service.ResourceCacheMap[ResourceTypeRule] = cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeSecurityPolicyCRUID: indexFunc})
}
