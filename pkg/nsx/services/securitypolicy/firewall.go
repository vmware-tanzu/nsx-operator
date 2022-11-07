package securitypolicy

import (
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"

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
	// The following variables are defined as interface, they should be initialized as concrete type
	securityPolicyStore common.Store
	groupStore          common.Store
	ruleStore           common.Store
)

type SecurityPolicyService struct {
	common.Service
}

// InitializeSecurityPolicy sync NSX resources
func InitializeSecurityPolicy(service common.Service) (*SecurityPolicyService, error) {
	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)

	wg.Add(3)

	securityPolicyService := &SecurityPolicyService{Service: service}

	InitializeStore(securityPolicyService)
	securityPolicyCacheIndexer := securityPolicyService.ResourceCacheMap[ResourceTypeSecurityPolicy]
	ruleCacheIndexer := securityPolicyService.ResourceCacheMap[ResourceTypeRule]
	groupCacheIndexer := securityPolicyService.ResourceCacheMap[ResourceTypeGroup]

	securityPolicyStore = &SecurityPolicyStore{ResourceStore: common.ResourceStore{
		Indexer:           securityPolicyCacheIndexer,
		BindingType:       model.SecurityPolicyBindingType(),
		ResourceAssertion: securityPolicyAssertion,
	}}
	groupStore = &GroupStore{ResourceStore: common.ResourceStore{
		Indexer:           groupCacheIndexer,
		BindingType:       model.GroupBindingType(),
		ResourceAssertion: groupAssertion,
	}}
	ruleStore = &RuleStore{ResourceStore: common.ResourceStore{
		Indexer:           ruleCacheIndexer,
		BindingType:       model.RuleBindingType(),
		ResourceAssertion: ruleAssertion,
	}}

	go securityPolicyService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeSecurityPolicy, securityPolicyStore)
	go securityPolicyService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeGroup, groupStore)
	go securityPolicyService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeRule, ruleStore)

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
	securityPolicyCacheIndexer := service.ResourceCacheMap[ResourceTypeSecurityPolicy]
	ruleCacheIndexer := service.ResourceCacheMap[ResourceTypeRule]
	groupCacheIndexer := service.ResourceCacheMap[ResourceTypeGroup]

	nsxSecurityPolicy, nsxGroups, err := service.buildSecurityPolicy(obj)
	if err != nil {
		log.Error(err, "failed to build SecurityPolicy")
		return err
	}

	if len(nsxSecurityPolicy.Scope) == 0 {
		log.Info("SecurityPolicy has empty policy-level appliedTo")
	}

	existingSecurityPolicy := model.SecurityPolicy{}
	res, exists, err := securityPolicyCacheIndexer.GetByKey(*nsxSecurityPolicy.Id)
	if err != nil {
		log.Error(err, "failed to get security policy", "SecurityPolicy", nsxSecurityPolicy)
	} else if exists {
		existingSecurityPolicy = res.(model.SecurityPolicy)
	}

	indexResults, err := ruleCacheIndexer.ByIndex(common.TagScopeSecurityPolicyCRUID, string(obj.UID))
	if err != nil {
		log.Error(err, "failed to get rules by security policy UID", "SecurityPolicyCR.UID", obj.UID)
		return err
	}
	existingRules := make([]model.Rule, 0)
	for _, rule := range indexResults {
		existingRules = append(existingRules, rule.(model.Rule))
	}

	indexResults, err = groupCacheIndexer.ByIndex(common.TagScopeSecurityPolicyCRUID, string(obj.UID))
	if err != nil {
		log.Error(err, "failed to get groups by security policy UID", "SecurityPolicyCR.UID", obj.UID)
		return err
	}
	existingGroups := make([]model.Group, 0)
	for _, group := range indexResults {
		existingGroups = append(existingGroups, group.(model.Group))
	}

	isChanged := common.CompareResource(SecurityPolicyToComparable(&existingSecurityPolicy), SecurityPolicyToComparable(nsxSecurityPolicy))
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
		finalSecurityPolicy = &existingSecurityPolicy
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
	infraSecurityPolicy, error := service.WrapHierarchySecurityPolicy(finalSecurityPolicy, finalGroups)
	if error != nil {
		return error
	}
	err = service.NSXClient.InfraClient.Patch(*infraSecurityPolicy, &EnforceRevisionCheckParam)
	if err != nil {
		return err
	}

	// The steps below know how to deal with CR, if there is MarkedForDelete, then delete it from store,
	// otherwise add or update it to store.
	if isChanged {
		err = securityPolicyStore.CRUDResource(&finalSecurityPolicyCopy)
		if err != nil {
			return err
		}
	}
	if !(len(changedRules) == 0 && len(staleRules) == 0) {
		err = ruleStore.CRUDResource(&finalSecurityPolicyCopy)
		if err != nil {
			return err
		}
	}
	if !(len(changedGroups) == 0 && len(staleGroups) == 0) {
		err = groupStore.CRUDResource(&finalGroups)
		if err != nil {
			return err
		}
	}
	log.Info("successfully created or updated nsxSecurityPolicy", "nsxSecurityPolicy", finalSecurityPolicyCopy)
	return nil
}

func (service *SecurityPolicyService) DeleteSecurityPolicy(obj interface{}) error {
	securityPolicyCacheIndexer := service.ResourceCacheMap[ResourceTypeSecurityPolicy]
	groupCacheIndexer := service.ResourceCacheMap[ResourceTypeGroup]
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
		indexResults, err := securityPolicyCacheIndexer.ByIndex(common.TagScopeSecurityPolicyCRUID, string(sp))
		if err != nil {
			log.Error(err, "failed to get security policy", "UID", string(sp))
			return err
		}
		if len(indexResults) == 0 {
			log.Info("did not get security policy with index", "UID", string(sp))
			return nil
		}
		t := indexResults[0].(model.SecurityPolicy)
		nsxSecurityPolicy = &t

		indexResults, err = groupCacheIndexer.ByIndex(common.TagScopeSecurityPolicyCRUID, string(sp))
		if err != nil {
			log.Error(err, "failed to get groups", "UID", string(sp))
			return err
		}
		if len(indexResults) == 0 {
			log.Info("did not get groups with index", "UID", string(sp))
		}
		for _, group := range indexResults {
			*nsxGroups = append(*nsxGroups, group.(model.Group))
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
	infraSecurityPolicy, error := service.WrapHierarchySecurityPolicy(nsxSecurityPolicy, *nsxGroups)
	if error != nil {
		return error
	}
	err := service.NSXClient.InfraClient.Patch(*infraSecurityPolicy, &EnforceRevisionCheckParam)
	if err != nil {
		return err
	}
	err = securityPolicyStore.CRUDResource(nsxSecurityPolicy)
	if err != nil {
		return err
	}
	err = groupStore.CRUDResource(nsxGroups)
	if err != nil {
		return err
	}
	err = ruleStore.CRUDResource(&finalSecurityPolicyCopy)
	if err != nil {
		return err
	}
	log.Info("successfully deleted  nsxSecurityPolicy", "nsxSecurityPolicy", nsxSecurityPolicy)
	return nil
}

func (service *SecurityPolicyService) createOrUpdateGroups(nsxGroups []model.Group) error {
	groupCacheIndexer := service.ResourceCacheMap[ResourceTypeGroup]
	for _, group := range nsxGroups {
		err := service.NSXClient.GroupClient.Patch(getDomain(service), *group.Id, group)
		if err != nil {
			return err
		}
		err = groupCacheIndexer.Add(group)
		log.V(2).Info("add group to store", "group", group.Id)
		if err != nil {
			return err
		}
	}
	log.Info("successfully create or update group", "groups", nsxGroups)
	return nil
}

func (service *SecurityPolicyService) ListSecurityPolicyID() sets.String {
	securityPolicyCacheIndexer := service.ResourceCacheMap[ResourceTypeSecurityPolicy]
	groupCacheIndexer := service.ResourceCacheMap[ResourceTypeGroup]
	groups := groupCacheIndexer.ListIndexFuncValues(common.TagScopeSecurityPolicyCRUID)
	groupSet := sets.NewString()
	for _, group := range groups {
		groupSet.Insert(group)
	}
	securityPolicies := securityPolicyCacheIndexer.ListIndexFuncValues(common.TagScopeSecurityPolicyCRUID)
	policySet := sets.NewString()
	for _, policy := range securityPolicies {
		policySet.Insert(policy)
	}
	return groupSet.Union(policySet)
}
