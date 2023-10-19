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
	ResourceTypeShare          = common.ResourceTypeShare
	NewConverter               = common.NewConverter
)

type SecurityPolicyService struct {
	common.Service
	securityPolicyStore *SecurityPolicyStore
	ruleStore           *RuleStore
	groupStore          *GroupStore
	shareStore          *ShareStore
}

type ProjectShare struct {
	shareGroup *model.Group
	share      *model.Share
}

// InitializeSecurityPolicy sync NSX resources
func InitializeSecurityPolicy(service common.Service) (*SecurityPolicyService, error) {
	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)

	wg.Add(4)

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
	securityPolicyService.shareStore = &ShareStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeSecurityPolicyCRUID: indexFunc}),
		BindingType: model.ShareBindingType(),
	}}

	go securityPolicyService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeSecurityPolicy, nil, securityPolicyService.securityPolicyStore)
	go securityPolicyService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeGroup, nil, securityPolicyService.groupStore)
	go securityPolicyService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeRule, nil, securityPolicyService.ruleStore)
	go securityPolicyService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeShare, nil, securityPolicyService.shareStore)

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
	nsxSecurityPolicy, nsxGroups, nsxProjectShares, err := service.buildSecurityPolicy(obj)
	if err != nil {
		log.Error(err, "failed to build SecurityPolicy")
		return err
	}

	if len(nsxSecurityPolicy.Scope) == 0 {
		log.Info("SecurityPolicy has empty policy-level appliedTo")
	}

	existingSecurityPolicy := service.securityPolicyStore.GetByKey(*nsxSecurityPolicy.Id)
	tmpExistingRules := service.ruleStore.GetByIndex(common.TagScopeSecurityPolicyCRUID, string(obj.UID))
	tmpExistingGroups := service.groupStore.GetByIndex(common.TagScopeSecurityPolicyCRUID, string(obj.UID))
	existingRules := []model.Rule{}
	for i, rule := range tmpExistingRules {
		existingRules[i] = *rule
	}
	existingGroups := []model.Group{}
	for i, group := range tmpExistingGroups {
		existingGroups[i] = *group
	}
	isChanged := common.CompareResource(SecurityPolicyToComparable(existingSecurityPolicy), SecurityPolicyToComparable(nsxSecurityPolicy))

	changed, stale := common.CompareResources(RulesToComparable(existingRules), RulesToComparable(nsxSecurityPolicy.Rules))
	changedRules, staleRules := ComparableToRules(changed), ComparableToRules(stale)
	changed, stale = common.CompareResources(GroupsToComparable(existingGroups), GroupsToComparable(*nsxGroups))
	changedGroups, staleGroups := ComparableToGroups(changed), ComparableToGroups(stale)

	if !isChanged && len(changedRules) == 0 && len(staleRules) == 0 && len(changedGroups) == 0 && len(staleGroups) == 0 {
		log.Info("securityPolicy, rules and groups are not changed, skip updating them", "nsxSecurityPolicy.Id", nsxSecurityPolicy.Id)
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

	if isVpcEnabled(service) {
		vpcInfo, err := getVpcInfo(obj.ObjectMeta.Namespace)
		if err != nil {
			return err
		}

		var finalProjectGroups *[]model.Group = nil
		var finalProjectShares *[]model.Share = nil

		if len(*nsxProjectShares) != 0 {
			projectGroups := make([]model.Group, 0)
			projectShares := make([]model.Share, 0)
			// create/update nsx project shares and nsx project level groups
			for i := len(*nsxProjectShares) - 1; i >= 0; i-- {
				projectGroups = append(projectGroups, *((*nsxProjectShares)[i].shareGroup))
				projectShares = append(projectShares, *((*nsxProjectShares)[i].share))
			}

			// 1.Create/update project level groups
			finalProjectGroups, err = service.createOrUpdateProjectGroups(obj, projectGroups)
			if err != nil {
				log.Error(err, "failed to create or update project level groups")
				return err
			}

			// 2.Create/update project shares
			finalProjectShares, err = service.createOrUpdateProjectShares(obj, projectShares)
			if err != nil {
				log.Error(err, "failed to create or update project share")
				return err
			}
		}

		orgRoot, err := service.WrapHierarchyVpcSecurityPolicy(finalSecurityPolicy, finalGroups, vpcInfo)
		if err != nil {
			log.Error(err, "failed to wrap SecurityPolicy in VPC")
			return err
		}
		// 3.Create/update SecurityPolicy, groups and rules under VPC path
		err = service.NSXClient.OrgRootClient.Patch(*orgRoot, &EnforceRevisionCheckParam)
		if err != nil {
			log.Error(err, "failed to create or update SecurityPolicy")
			return err
		}

		if (finalProjectGroups != nil) && len(*finalProjectGroups) != 0 {
			err = service.groupStore.Apply(finalProjectGroups)
			if err != nil {
				return err
			}
		}

		if (finalProjectShares != nil) && len(*finalProjectShares) != 0 {
			err = service.shareStore.Apply(finalProjectShares)
		}
	} else {
		infraSecurityPolicy, err := service.WrapHierarchySecurityPolicy(finalSecurityPolicy, finalGroups)
		if err != nil {
			log.Error(err, "failed to wrap SecurityPolicy")
			return err
		}
		err = service.NSXClient.InfraClient.Patch(*infraSecurityPolicy, &EnforceRevisionCheckParam)
	}
	if err != nil {
		return err
	}

	// The steps below know how to deal with CR, if there is MarkedForDelete, then delete it from store,
	// otherwise add or update it to store.
	if isChanged {
		err = service.securityPolicyStore.Apply(&finalSecurityPolicyCopy)
		if err != nil {
			return err
		}
	}
	if !(len(changedRules) == 0 && len(staleRules) == 0) {
		err = service.ruleStore.Apply(&finalSecurityPolicyCopy)
		if err != nil {
			return err
		}
	}
	if !(len(changedGroups) == 0 && len(staleGroups) == 0) {
		err = service.groupStore.Apply(&finalGroups)
		if err != nil {
			return err
		}
	}
	log.Info("successfully created or updated nsx SecurityPolicy", "nsxSecurityPolicy", finalSecurityPolicyCopy)
	return nil
}

func (service *SecurityPolicyService) DeleteSecurityPolicy(obj interface{}, isVpcCleanup bool) error {
	var nsxSecurityPolicy *model.SecurityPolicy
	var spNameSpace string
	var err error
	g := make([]model.Group, 0)
	nsxGroups := &g
	var projectShares *[]ProjectShare
	nsxProjectShares := make([]model.Share, 0)
	nsxProjectGroups := make([]model.Group, 0)
	switch sp := obj.(type) {
	// This case is for normal SecurityPolicy deletion process, which means that SecurityPolicy
	// has corresponding nsx SecurityPolicy object
	case *v1alpha1.SecurityPolicy:
		nsxSecurityPolicy, nsxGroups, projectShares, err = service.buildSecurityPolicy(sp)
		spNameSpace = sp.ObjectMeta.Namespace
		if err != nil {
			log.Error(err, "failed to build nsx SecurityPolicy in deleting")
			return err
		}

		// Collect project share and project level groups that need to be removed from nsx
		// project share and project groups only aviable in VPC network.
		for i := len(*projectShares) - 1; i >= 0; i-- {
			nsxProjectGroups = append(nsxProjectGroups, *(*projectShares)[i].shareGroup)
			nsxProjectShares = append(nsxProjectShares, *(*projectShares)[i].share)
		}

	// This case is for SecurityPolicy GC process, which means that SecurityPolicy
	// doesn't exist in K8s any more but still has corresponding nsx SecurityPolicy object.
	// Hence, we use SecurityPolicy's UID here from store instead of K8s SecurityPolicy object
	case types.UID:
		securityPolicies := service.securityPolicyStore.GetByIndex(common.TagScopeSecurityPolicyCRUID, string(sp))
		if len(securityPolicies) == 0 {
			log.Info("security policy is not found in store, skip deleting it", "securityPolicyUID", sp)
			return nil
		}
		nsxSecurityPolicy = securityPolicies[0]
		// Get namespace of nsx SecurityPolicy from tags since there is no K8s SecurityPolicy object
		for i := len(nsxSecurityPolicy.Tags) - 1; i >= 0; i-- {
			if *(nsxSecurityPolicy.Tags[i].Scope) == common.TagScopeNamespace {
				spNameSpace = *(nsxSecurityPolicy.Tags[i].Tag)
				log.V(1).Info("get namespace with SecurityPolicy index", "namespace", spNameSpace, "UID", string(sp))
				break
			}
		}

		groups := service.groupStore.GetByIndex(common.TagScopeSecurityPolicyCRUID, string(sp))
		if len(groups) == 0 {
			log.Info("did not get groups with SecurityPolicy index", "UID", string(sp))
		}

		if isVpcEnabled(service) || isVpcCleanup {
			for i := len(groups) - 1; i >= 0; i-- {
				for j := len(groups[i].Tags) - 1; j >= 0; j-- {
					if *(groups[i].Tags[j].Scope) == common.TagScopeProjectGroupShared {
						if *(groups[i].Tags[j].Tag) == "true" {
							nsxProjectGroups = append(nsxProjectGroups, *groups[i])
						} else {
							*nsxGroups = append(*nsxGroups, *groups[i])
						}
						break
					}
				}
			}
			shares := service.shareStore.GetByIndex(common.TagScopeSecurityPolicyCRUID, string(sp))
			if len(shares) == 0 {
				log.Info("did not get shares with SecurityPolicy index", "UID", string(sp))
			}
			for _, share := range shares {
				nsxProjectShares = append(nsxProjectShares, *share)
			}
		} else {
			for _, group := range groups {
				*nsxGroups = append(*nsxGroups, *group)
			}
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

	if isVpcEnabled(service) || isVpcCleanup {
		vpcInfo, err := getVpcInfo(spNameSpace)
		if err != nil {
			return err
		}
		orgId := (*vpcInfo).OrgID
		projectId := (*vpcInfo).ProjectID

		for i := len(nsxProjectGroups) - 1; i >= 0; i-- {
			nsxProjectGroups[i].MarkedForDelete = &MarkedForDelete
		}
		for i := len(nsxProjectShares) - 1; i >= 0; i-- {
			nsxProjectShares[i].MarkedForDelete = &MarkedForDelete
		}

		// 1.Delete SecurityPolicy, groups and rules under VPC path
		orgRoot, err := service.WrapHierarchyVpcSecurityPolicy(nsxSecurityPolicy, *nsxGroups, vpcInfo)
		if err != nil {
			log.Error(err, "failed to wrap SecurityPolicy in VPC")
			return err
		}
		err = service.NSXClient.OrgRootClient.Patch(*orgRoot, &EnforceRevisionCheckParam)
		if err != nil {
			log.Error(err, "failed to delete SecurityPolicy")
			return err
		}

		// 2.Delete nsx project share under project level
		if len(nsxProjectShares) != 0 {
			projectInfra, err := service.WrapHierarchyProjectShares(nsxProjectShares)
			if err != nil {
				log.Error(err, "failed to wrap project share")
				return err
			}

			err = service.NSXClient.ProjectInfraClient.Patch(orgId, projectId, *projectInfra, &EnforceRevisionCheckParam)
			if err != nil {
				log.Error(err, "failed to delete project shares")
				return err
			}
		}

		// 3.Delete nsx project groups under project level
		if len(nsxProjectGroups) != 0 {
			projectInfra1, err := service.WrapHierarchyProjectGroups(nsxProjectGroups)
			if err != nil {
				log.Error(err, "failed to wrap project level groups")
				return err
			}

			err = service.NSXClient.ProjectInfraClient.Patch(orgId, projectId, *projectInfra1, &EnforceRevisionCheckParam)
			if err != nil {
				log.Error(err, "failed to delte project level groups")
				return err
			}
		}

		if len(nsxProjectShares) != 0 {
			err = service.shareStore.Apply(&nsxProjectShares)
			if err != nil {
				return err
			}
		}
		if len(nsxProjectGroups) != 0 {
			err = service.groupStore.Apply(&nsxProjectGroups)
		}
	} else {
		infraSecurityPolicy, err := service.WrapHierarchySecurityPolicy(nsxSecurityPolicy, *nsxGroups)
		if err != nil {
			log.Error(err, "failed to wrap SecurityPolicy")
			return err
		}
		err = service.NSXClient.InfraClient.Patch(*infraSecurityPolicy, &EnforceRevisionCheckParam)
	}
	if err != nil {
		return err
	}

	err = service.securityPolicyStore.Apply(&finalSecurityPolicyCopy)
	if err != nil {
		return err
	}
	err = service.groupStore.Apply(nsxGroups)
	if err != nil {
		return err
	}
	err = service.ruleStore.Apply(&finalSecurityPolicyCopy)
	if err != nil {
		return err
	}
	log.Info("successfully deleted nsx SecurityPolicy", "nsxSecurityPolicy", finalSecurityPolicyCopy)
	return nil
}

func (service *SecurityPolicyService) createOrUpdateGroups(obj *v1alpha1.SecurityPolicy, nsxGroups []*model.Group) error {
	var err error = nil
	for _, group := range nsxGroups {
		group.MarkedForDelete = nil
		if isVpcEnabled(service) {
			vpcInfo, err := getVpcInfo(obj.ObjectMeta.Namespace)
			if err != nil {
				return err
			}
			orgId := (*vpcInfo).OrgID
			projectId := (*vpcInfo).ProjectID
			vpcId := (*vpcInfo).VPCID

			err = service.NSXClient.VpcGroupClient.Patch(orgId, projectId, vpcId, *group.Id, *group)
		} else {
			err = service.NSXClient.GroupClient.Patch(getDomain(service), *group.Id, *group)
		}
	}

	if err != nil {
		return err
	}
	err = service.groupStore.Apply(&nsxGroups)
	if err != nil {
		return err
	}
	log.Info("successfully create or update groups", "groups", nsxGroups)
	return nil
}

// Create a project group share to share the group with vpc in which SecurityPolicy is
func (service *SecurityPolicyService) createOrUpdateProjectShares(obj *v1alpha1.SecurityPolicy, projectShares []model.Share) (*[]model.Share, error) {
	finalShares := make([]model.Share, 0)

	tmpExistingShares := service.shareStore.GetByIndex(common.TagScopeSecurityPolicyCRUID, string(obj.UID))
	existingShares := []model.Share{}
	for i, share := range tmpExistingShares {
		existingShares[i] = *share
	}
	changed, stale := common.CompareResources(SharesToComparable(existingShares), SharesToComparable(projectShares))
	changedShares, staleShares := ComparableToShares(changed), ComparableToShares(stale)

	if len(changedShares) == 0 && len(staleShares) == 0 {
		log.Info("project shares are not changed, skip updating them")
		return &finalShares, nil
	}

	for i := len(staleShares) - 1; i >= 0; i-- { // Don't use range, it would copy the element
		staleShares[i].MarkedForDelete = &MarkedForDelete
	}
	finalShares = append(finalShares, staleShares...)
	finalShares = append(finalShares, changedShares...)

	vpcInfo, err := getVpcInfo(obj.ObjectMeta.Namespace)
	if err != nil {
		return nil, err
	}
	orgId := (*vpcInfo).OrgID
	projectId := (*vpcInfo).ProjectID

	projectInfra, err := service.WrapHierarchyProjectShares(projectShares)
	if err != nil {
		log.Error(err, "failed to wrap project shares")
		return nil, err
	}

	err = service.NSXClient.ProjectInfraClient.Patch(orgId, projectId, *projectInfra, &EnforceRevisionCheckParam)
	if err != nil {
		return nil, err
	}

	return &finalShares, nil
}

func (service *SecurityPolicyService) createOrUpdateProjectGroups(obj *v1alpha1.SecurityPolicy, groups []model.Group) (*[]model.Group, error) {
	finalGroups := make([]model.Group, 0)

	tmpExistingGroups := service.groupStore.GetByIndex(common.TagScopeSecurityPolicyCRUID, string(obj.UID))
	existingGroups := []model.Group{}
	for i, group := range tmpExistingGroups {
		existingGroups[i] = *group
	}
	changed, stale := common.CompareResources(GroupsToComparable(existingGroups), GroupsToComparable(groups))
	changedGroups, staleGroups := ComparableToGroups(changed), ComparableToGroups(stale)

	if len(changedGroups) == 0 && len(staleGroups) == 0 {
		log.Info("project groups are not changed, skip updating them")
		return nil, nil
	}

	for i := len(staleGroups) - 1; i >= 0; i-- { // Don't use range, it would copy the element
		staleGroups[i].MarkedForDelete = &MarkedForDelete
	}
	finalGroups = append(finalGroups, staleGroups...)
	finalGroups = append(finalGroups, changedGroups...)

	vpcInfo, err := getVpcInfo(obj.ObjectMeta.Namespace)
	if err != nil {
		return nil, err
	}
	orgId := (*vpcInfo).OrgID
	projectId := (*vpcInfo).ProjectID

	projectInfra, err := service.WrapHierarchyProjectGroups(finalGroups)
	if err != nil {
		log.Error(err, "failed to wrap project level groups")
		return nil, err
	}

	err = service.NSXClient.ProjectInfraClient.Patch(orgId, projectId, *projectInfra, &EnforceRevisionCheckParam)
	if err != nil {
		return nil, err
	}

	return &finalGroups, nil
}

func (service *SecurityPolicyService) ListSecurityPolicyID() sets.String {
	// List SeurityPolicyID to which groups resources are associated in group store
	groupSet := service.groupStore.ListIndexFuncValues(common.TagScopeSecurityPolicyCRUID)
	// List SeurityPolicyID to which share resources are associated in share store
	shareSet := service.shareStore.ListIndexFuncValues(common.TagScopeSecurityPolicyCRUID)
	policySet := service.securityPolicyStore.ListIndexFuncValues(common.TagScopeSecurityPolicyCRUID)

	return groupSet.Union(policySet).Union(shareSet)
}

func (service *SecurityPolicyService) Cleanup() error {
	// Delete all the security policies in store
	uids := service.ListSecurityPolicyID()
	log.Info("cleaning up security policies", "count", len(uids))
	for uid := range uids {
		err := service.DeleteSecurityPolicy(types.UID(uid), true)
		if err != nil {
			return err
		}
	}
	return nil
}
