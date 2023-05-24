package securitypolicy

import (
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

var (
	log                        = logger.Log
	MarkedForDelete            = true
	EnforceRevisionCheckParam  = false
	ResourceTypeSecurityPolicy = common.ResourceTypeSecurityPolicy
	ResourceTypeRule           = common.ResourceTypeRule
	ResourceTypeGroup          = common.ResourceTypeGroup
	NewConverter               = common.NewConverter
)

type SecurityPolicyService struct {
	common.Service
	securityPolicyStore *SecurityPolicyStore
	ruleStore           *RuleStore
	groupStore          *GroupStore
}

// InitializeSecurityPolicy sync NSX resources
func InitializeSecurityPolicy(service common.Service) (*SecurityPolicyService, error) {
	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)

	wg.Add(3)

	securityPolicyService := &SecurityPolicyService{Service: service}

	securityPolicyService.securityPolicyStore = &SecurityPolicyStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeSecurityPolicyCRUID: indexFunc}),
		BindingType: model.SecurityPolicyBindingType(),
	}}
	securityPolicyService.groupStore = &GroupStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
			common.TagScopeSecurityPolicyCRUID: indexFunc,
			common.TagScopeRuleID:              indexGroupFunc,
		}),
		BindingType: model.GroupBindingType(),
	}}
	securityPolicyService.ruleStore = &RuleStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeSecurityPolicyCRUID: indexFunc}),
		BindingType: model.RuleBindingType(),
	}}

	go securityPolicyService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeSecurityPolicy, nil, securityPolicyService.securityPolicyStore)
	go securityPolicyService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeGroup, nil, securityPolicyService.groupStore)
	go securityPolicyService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeRule, nil, securityPolicyService.ruleStore)

	go func() {
		wg.Wait()
		close(wgDone)
	}()

	select {
	case <-wgDone:
		break
	case err := <-fatalErrors:
		close(fatalErrors)
		return securityPolicyService, err
	}

	return securityPolicyService, nil
}

func (service *SecurityPolicyService) CreateOrUpdateSecurityPolicy(obj *v1alpha1.SecurityPolicy) error {
	nsxSecurityPolicy, nsxGroups, err := service.buildSecurityPolicy(obj)
	if err != nil {
		log.Error(err, "failed to build SecurityPolicy")
		return err
	}

	if len(nsxSecurityPolicy.Scope) == 0 {
		log.Info("SecurityPolicy has empty policy-level appliedTo")
	}

	existingSecurityPolicy := service.securityPolicyStore.GetByKey(*nsxSecurityPolicy.Id)
	existingRules := service.ruleStore.GetByIndex(common.TagScopeSecurityPolicyCRUID, string(obj.UID))
	existingGroups := service.groupStore.GetByIndex(common.TagScopeSecurityPolicyCRUID, string(obj.UID))

	isChanged := common.CompareResource(SecurityPolicyToComparable(existingSecurityPolicy), SecurityPolicyToComparable(nsxSecurityPolicy))
	changed, stale := common.CompareResources(RulesToComparable(existingRules), RulesToComparable(nsxSecurityPolicy.Rules))
	changedRules, staleRules := ComparableToRules(changed), ComparableToRules(stale)
	changed, stale = common.CompareResources(GroupsToComparable(existingGroups), GroupsToComparable(*nsxGroups))
	changedGroups, staleGroups := ComparableToGroups(changed), ComparableToGroups(stale)

	if !isChanged && len(changedRules) == 0 && len(staleRules) == 0 && len(changedGroups) == 0 && len(staleGroups) == 0 {
		log.Info("security policy, rules and groups are not changed, skip updating them", "nsxSecurityPolicy.Id", nsxSecurityPolicy.Id)
		return nil
	}

	var finalSecurityPolicy *model.SecurityPolicy
	if isChanged {
		finalSecurityPolicy = nsxSecurityPolicy
	} else {
		finalSecurityPolicy = existingSecurityPolicy
	}

	finalRules := make([]model.Rule, 0)
	for i := len(staleRules) - 1; i >= 0; i-- { // Don't use range, it would copy the element
		staleRules[i].MarkedForDelete = &MarkedForDelete // InfraClient need this field to delete the group
	}
	finalRules = append(finalRules, staleRules...)
	finalRules = append(finalRules, changedRules...)
	finalSecurityPolicy.Rules = finalRules

	finalGroups := make([]model.Group, 0)
	for i := len(staleGroups) - 1; i >= 0; i-- { // Don't use range, it would copy the element
		staleGroups[i].MarkedForDelete = &MarkedForDelete // InfraClient need this field to delete the group
	}
	finalGroups = append(finalGroups, staleGroups...)
	finalGroups = append(finalGroups, changedGroups...)

	// WrapHighLevelSecurityPolicy will modify the input security policy, so we need to make a copy for the following store update.
	finalSecurityPolicyCopy := *finalSecurityPolicy
	finalSecurityPolicyCopy.Rules = finalSecurityPolicy.Rules
	infraSecurityPolicy, err := service.WrapHierarchySecurityPolicy(finalSecurityPolicy, finalGroups)
	if err != nil {
		return err
	}
	err = service.NSXClient.InfraClient.Patch(*infraSecurityPolicy, &EnforceRevisionCheckParam)
	if err != nil {
		return err
	}

	// The steps below know how to deal with CR, if there is MarkedForDelete, then delete it from store,
	// otherwise add or update it to store.
	if isChanged {
		err = service.securityPolicyStore.Operate(&finalSecurityPolicyCopy)
		if err != nil {
			return err
		}
	}
	if !(len(changedRules) == 0 && len(staleRules) == 0) {
		err = service.ruleStore.Operate(&finalSecurityPolicyCopy)
		if err != nil {
			return err
		}
	}
	if !(len(changedGroups) == 0 && len(staleGroups) == 0) {
		err = service.groupStore.Operate(&finalGroups)
		if err != nil {
			return err
		}
	}
	log.Info("successfully created or updated nsxSecurityPolicy", "nsxSecurityPolicy", finalSecurityPolicyCopy)
	return nil
}

func (service *SecurityPolicyService) DeleteSecurityPolicy(obj interface{}) error {
	var nsxSecurityPolicy *model.SecurityPolicy
	g := make([]model.Group, 0)
	nsxGroups := &g
	switch sp := obj.(type) {
	case *v1alpha1.SecurityPolicy:
		var err error
		nsxSecurityPolicy, nsxGroups, err = service.buildSecurityPolicy(sp)
		if err != nil {
			log.Error(err, "failed to build SecurityPolicy")
			return err
		}
	case types.UID:
		securityPolicies := service.securityPolicyStore.GetByIndex(common.TagScopeSecurityPolicyCRUID, string(sp))
		if len(securityPolicies) == 0 {
			log.Info("security policy is not found in store, skip deleting it", "securityPolicyUID", sp)
			return nil
		}
		nsxSecurityPolicy = &securityPolicies[0]

		groups := service.groupStore.GetByIndex(common.TagScopeSecurityPolicyCRUID, string(sp))
		if len(groups) == 0 {
			log.Info("did not get groups with index", "UID", string(sp))
		}
		for _, group := range groups {
			*nsxGroups = append(*nsxGroups, group)
		}
	}

	nsxSecurityPolicy.MarkedForDelete = &MarkedForDelete
	for i := len(*nsxGroups) - 1; i >= 0; i-- { // Don't use range, it would copy the element
		(*nsxGroups)[i].MarkedForDelete = &MarkedForDelete
	}
	for i := len(nsxSecurityPolicy.Rules) - 1; i >= 0; i-- { // Don't use range, it would copy the element
		nsxSecurityPolicy.Rules[i].MarkedForDelete = &MarkedForDelete
	}

	// WrapHighLevelSecurityPolicy will modify the input security policy, so we need to make a copy for the following store update.
	finalSecurityPolicyCopy := *nsxSecurityPolicy
	finalSecurityPolicyCopy.Rules = nsxSecurityPolicy.Rules
	infraSecurityPolicy, err := service.WrapHierarchySecurityPolicy(nsxSecurityPolicy, *nsxGroups)
	if err != nil {
		return err
	}
	err = service.NSXClient.InfraClient.Patch(*infraSecurityPolicy, &EnforceRevisionCheckParam)
	if err != nil {
		return err
	}
	err = service.securityPolicyStore.Operate(nsxSecurityPolicy)
	if err != nil {
		return err
	}
	err = service.groupStore.Operate(nsxGroups)
	if err != nil {
		return err
	}
	err = service.ruleStore.Operate(&finalSecurityPolicyCopy)
	if err != nil {
		return err
	}
	log.Info("successfully deleted  nsxSecurityPolicy", "nsxSecurityPolicy", nsxSecurityPolicy)
	return nil
}

func (service *SecurityPolicyService) createOrUpdateGroups(nsxGroups []model.Group) error {
	for _, group := range nsxGroups {
		group.MarkedForDelete = nil
		err := service.NSXClient.GroupClient.Patch(getDomain(service), *group.Id, group)
		if err != nil {
			return err
		}
	}

	err := service.groupStore.Operate(&nsxGroups)
	if err != nil {
		return err
	}
	log.Info("successfully create or update group", "groups", nsxGroups)
	return nil
}

func (service *SecurityPolicyService) ListSecurityPolicyID() sets.String {
	groupSet := service.groupStore.ListIndexFuncValues(common.TagScopeSecurityPolicyCRUID)
	policySet := service.securityPolicyStore.ListIndexFuncValues(common.TagScopeSecurityPolicyCRUID)
	return groupSet.Union(policySet)
}
