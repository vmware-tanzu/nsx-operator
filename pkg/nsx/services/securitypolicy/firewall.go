package securitypolicy

import (
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra/domains"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

const (
	MaxCriteriaExpressions      int = 5
	MaxMixedCriteriaExpressions int = 15
	MaxCriteria                 int = 5
	MaxTotalCriteriaExpressions int = 35
	MaxMatchExpressionInOp      int = 1
	MaxMatchExpressionIn        int = 1
	MaxMatchExpressionInValues  int = 5
	ClusterTagCount             int = 1
	ProjectTagCount             int = 1
)

type Service struct {
	common.Service
	Client              client.Client
	GroupStore          cache.Indexer
	SecurityPolicyStore cache.Indexer
	RuleStore           cache.Indexer
}

var log = common.Log

func (service *Service) OperateSecurityPolicy(obj *v1alpha1.SecurityPolicy) error {
	nsxSecurityPolicy, nsxGroups, err := service.buildSecurityPolicy(obj)
	if err != nil {
		return err
	}

	existingGroups, existingSecurityPolicy, existingRules, err := getAll(service, obj, nsxSecurityPolicy)
	if err != nil {
		return err
	}

	// Caution! createOrUpdate can't delete the legacy groups.
	// So we have to delete the legacy groups firstly by groupClient.
	groupEqual, legacyGroups := GroupsEqual(existingGroups, *nsxGroups)
	if groupEqual {
		log.Info("NSGroups are not changed, skip updating them", "nsxSecurityPolicy.Id", nsxSecurityPolicy.Id)
	} else {
		err = service.createOrUpdateGroups(*nsxGroups)
		if err != nil {
			return err
		}
	}
	// Caution! Patch can't delete the legacy rules.
	// So we have to delete the legacy rules manually by rulesClient.
	spEqual := PolicyEqual(existingSecurityPolicy, nsxSecurityPolicy)
	ruleEqual, legacyRules := RulesEqual(existingRules, nsxSecurityPolicy.Rules)
	if spEqual && ruleEqual {
		log.Info("security policy and rules are not changed, skip updating them", "nsxSecurityPolicy.Id", nsxSecurityPolicy.Id)
	} else {
		err := service.createOrUpdateSecurityPolicy(nsxSecurityPolicy)
		if err != nil {
			return err
		}
		err = service.AddRulesToStore(nsxSecurityPolicy)
		if err != nil {
			return err
		}
		log.Info("successfully operate", "nsxSecurityPolicy", nsxSecurityPolicy)
	}

	if len(legacyRules) > 0 {
		err := service.DeleteRules(nsxSecurityPolicy, legacyRules)
		if err != nil {
			return err
		}
	}

	// The reason why delete legacy groups at last is that some rules may have reference to the legacy groups.
	if len(legacyGroups) > 0 {
		err := service.DeleteGroups(legacyGroups)
		if err != nil {
			return err
		}
	}
	return nil
}

func (service *Service) createOrUpdateGroups(nsxGroups []model.Group) error {
	for _, group := range nsxGroups {
		err := service.NSXClient.GroupClient.Patch(service.NSXConfig.Cluster, *group.Id, group)
		if err != nil {
			return err
		}
		err = service.GroupStore.Add(group)
		log.V(2).Info("add group to store", "group", group.Id)
		if err != nil {
			return err
		}
	}
	log.Info("successfully create or update group", "groups", nsxGroups)
	return nil
}

func (service *Service) createOrUpdateSecurityPolicy(sp *model.SecurityPolicy) error {
	err := service.NSXClient.SecurityClient.Patch(service.NSXConfig.Cluster, *sp.Id, *sp)
	if err != nil {
		return err
	}
	err = service.SecurityPolicyStore.Add(*sp)
	if err != nil {
		return err
	}
	return nil
}

func (service *Service) DeleteRules(sp *model.SecurityPolicy, legacyRules []model.Rule) error {
	// Delete legacy rules
	for _, rule := range legacyRules {
		err := service.NSXClient.RuleClient.Delete(service.NSXConfig.Cluster, *sp.Id, *rule.Id)
		if err != nil {
			return err
		}
		err = service.RuleStore.Delete(rule)
		log.V(1).Info("delete rule from store", "rule", rule)
		if err != nil {
			return err
		}
	}
	return nil
}

func (service *Service) AddRulesToStore(sp *model.SecurityPolicy) error {
	for _, rule := range sp.Rules {
		err := service.RuleStore.Add(rule)
		log.V(1).Info("add rule to store", "rule", rule)
		if err != nil {
			return err
		}
	}
	return nil
}

func (service *Service) DeleteGroups(legacyGroups []model.Group) error {
	// Delete legacy groups
	for _, group := range legacyGroups {
		err := service.deleteGroup(service.NSXClient.GroupClient, &group)
		if err != nil {
			return err
		}
		err = service.GroupStore.Delete(group)
		if err != nil {
			return err
		}
		log.Info("successfully delete group", "group", group)
	}
	return nil
}

func (service *Service) deleteGroup(groupsClient domains.GroupsClient, nsxGroup *model.Group) error {
	failIfSubtreeExistsParam := false
	forceParam := false
	return groupsClient.Delete(service.NSXConfig.Cluster, *nsxGroup.Id, &failIfSubtreeExistsParam, &forceParam)
}

func (service *Service) DeleteSecurityPolicy(UID types.UID) error {
	groupsClient := service.NSXClient.GroupClient
	policiesClient := service.NSXClient.SecurityClient

	policy, err := service.SecurityPolicyStore.ByIndex(util.TagScopeSecurityPolicyCRUID, string(UID))
	if err != nil {
		return err
	}
	if len(policy) == 0 {
		log.V(1).Info("no NSX SecurityPolicy is found", "UID", string(UID))
		return nil
	}
	nsxSecurityPolicy := policy[0].(model.SecurityPolicy)
	if err = policiesClient.Delete(service.NSXConfig.Cluster, *nsxSecurityPolicy.Id); err != nil {
		return err
	}
	if err = service.SecurityPolicyStore.Delete(nsxSecurityPolicy); err != nil {
		return err
	}

	rules, err := service.SecurityPolicyStore.ByIndex(util.TagScopeSecurityPolicyCRUID, string(UID))
	if err != nil {
		return err
	}
	for _, rule := range rules {
		err = service.RuleStore.Delete(rule.(model.Rule))
		if err != nil {
			return err
		}
	}

	groups, err := service.GroupStore.ByIndex(util.TagScopeSecurityPolicyCRUID, string(UID))
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		log.V(1).Info("no NSX Group is found", "UID", string(UID))
		return nil
	}
	for _, group := range groups {
		nsxGroup := group.(model.Group)
		if err = service.deleteGroup(groupsClient, &nsxGroup); err != nil {
			return err
		}
		if err = service.GroupStore.Delete(nsxGroup); err != nil {
			return err
		}
	}

	log.Info("successfully deleted NSX SecurityPolicy", "nsxSecurityPolicy", nsxSecurityPolicy)
	return nil
}
