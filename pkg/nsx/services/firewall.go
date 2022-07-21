package services

import (
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra/domains"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
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

type SecurityPolicyService struct {
	Client              client.Client
	NSXClient           *nsx.Client
	NSXConfig           *config.NSXOperatorConfig
	GroupStore          cache.Indexer
	SecurityPolicyStore cache.Indexer
	RuleStore           cache.Indexer
}

var log = logf.Log.WithName("service").WithName("firewall")

// InitializeSecurityPolicy sync NSX resources
func InitializeSecurityPolicy(
	nsxClient *nsx.Client,
	cf *config.NSXOperatorConfig,
) (*SecurityPolicyService, error) {
	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)

	wg.Add(3)
	service := &SecurityPolicyService{NSXClient: nsxClient}
	service.GroupStore = cache.NewIndexer(keyFunc, cache.Indexers{
		util.TagScopeNamespace:           namespaceIndexFunc,
		util.TagScopeSecurityPolicyCRUID: securityPolicyCRUIDScopeIndexFunc,
	})
	service.SecurityPolicyStore = cache.NewIndexer(keyFunc, cache.Indexers{
		util.TagScopeSecurityPolicyCRUID: securityPolicyCRUIDScopeIndexFunc,
	})
	service.RuleStore = cache.NewIndexer(keyFunc, cache.Indexers{
		util.TagScopeSecurityPolicyCRUID: securityPolicyCRUIDScopeIndexFunc,
	})
	service.NSXConfig = cf

	go queryGroup(service, &wg, fatalErrors)
	go querySecurityPolicy(service, &wg, fatalErrors)
	go queryRule(service, &wg, fatalErrors)

	go func() {
		wg.Wait()
		close(wgDone)
	}()

	select {
	case <-wgDone:
		break
	case err := <-fatalErrors:
		close(fatalErrors)
		return service, err
	}

	return service, nil
}

func (service *SecurityPolicyService) OperateSecurityPolicy(obj *v1alpha1.SecurityPolicy) error {
	nsxSecurityPolicy, nsxGroups, err := service.buildSecurityPolicy(obj)
	if err != nil {
		return err
	}

	existingGroups, existingSecurityPolicy, existingRules, err := getAll(
		service,
		obj,
		nsxSecurityPolicy,
	)
	if err != nil {
		return err
	}

	if GroupsEqual(existingGroups, *nsxGroups) {
		log.Info("groups not changed, skip", "nsxSecurityPolicy.Id", nsxSecurityPolicy.Id)
	} else {
		err = service.createOrUpdateGroups(*nsxGroups)
		if err != nil {
			return err
		}
	}
	// Caution! Patch can't delete the legacy rules.
	// So we have to delete the legacy rules manually by rulesClient.
	spEqual := SecurityPolicyEqual(existingSecurityPolicy, nsxSecurityPolicy)
	ruleEqual, legacyRules := RulesEqual(existingRules, nsxSecurityPolicy.Rules)
	if spEqual && ruleEqual {
		log.Info(
			"security policy and rules not changed, skip",
			"nsxSecurityPolicy.Id",
			nsxSecurityPolicy.Id,
		)
	} else {
		err := service.updateOrDeleteRules(nsxSecurityPolicy, legacyRules)
		if err != nil {
			return err
		}
		err = service.createOrUpdateSecurityPolicy(nsxSecurityPolicy)
		if err != nil {
			return err
		}
		log.Info("successfully operate", "nsxSecurityPolicy", nsxSecurityPolicy)
	}
	return nil
}

func (service *SecurityPolicyService) createOrUpdateGroups(nsxGroups []model.Group) error {
	for _, group := range nsxGroups {
		err := service.NSXClient.GroupClient.Patch(getDomain(service), *group.Id, group)
		if err != nil {
			return err
		}
		err = service.GroupStore.Add(group)
		if err != nil {
			return err
		}
	}
	return nil
}

func (service *SecurityPolicyService) createOrUpdateSecurityPolicy(sp *model.SecurityPolicy) error {
	err := service.NSXClient.SecurityClient.Patch(getDomain(service), *sp.Id, *sp)
	if err != nil {
		return err
	}
	err = service.SecurityPolicyStore.Add(*sp)
	if err != nil {
		return err
	}
	return nil
}

func (service *SecurityPolicyService) updateOrDeleteRules(
	sp *model.SecurityPolicy,
	legacyRules []model.Rule,
) error {
	for _, rule := range sp.Rules {
		err := service.RuleStore.Add(rule)
		if err != nil {
			return err
		}
	}

	// Delete legacy rules
	for _, rule := range legacyRules {
		err := service.RuleStore.Delete(rule)
		if err != nil {
			return err
		}
		err = service.NSXClient.RuleClient.Delete(getDomain(service), *sp.Id, *rule.Id)
		if err != nil {
			return err
		}
	}
	return nil
}

func (service *SecurityPolicyService) deleteGroup(
	groupsClient domains.GroupsClient,
	nsxGroup *model.Group,
) error {
	failIfSubtreeExistsParam := false
	forceParam := false
	return groupsClient.Delete(
		getDomain(service),
		*nsxGroup.Id,
		&failIfSubtreeExistsParam,
		&forceParam,
	)
}

func (service *SecurityPolicyService) DeleteSecurityPolicy(uID types.UID) error {
	groupsClient := service.NSXClient.GroupClient
	policiesClient := service.NSXClient.SecurityClient

	policy, err := service.SecurityPolicyStore.ByIndex(
		util.TagScopeSecurityPolicyCRUID,
		string(uID),
	)
	if err != nil {
		return err
	}
	if len(policy) == 0 {
		log.V(1).Info("no NSX SecurityPolicy is found", "uID", string(uID))
		return nil
	}
	nsxSecurityPolicy := policy[0].(model.SecurityPolicy)
	if err = policiesClient.Delete(getDomain(service), *nsxSecurityPolicy.Id); err != nil {
		return err
	}
	if err = service.SecurityPolicyStore.Delete(nsxSecurityPolicy); err != nil {
		return err
	}

	rules, err := service.SecurityPolicyStore.ByIndex(util.TagScopeSecurityPolicyCRUID, string(uID))
	if err != nil {
		return err
	}
	for _, rule := range rules {
		err = service.RuleStore.Delete(rule.(model.Rule))
		if err != nil {
			return err
		}
	}

	groups, err := service.GroupStore.ByIndex(util.TagScopeSecurityPolicyCRUID, string(uID))
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		log.V(1).Info("no NSX Group is found", "uID", string(uID))
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
