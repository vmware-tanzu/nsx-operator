package securitypolicy

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
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
	vpcService          common.VPCServiceProvider
}

type ProjectShare struct {
	shareGroup *model.Group
	share      *model.Share
}

var securityService *SecurityPolicyService
var lock = &sync.Mutex{}

// GetSecurityService get singleton SecurityPolicyService instance, networkpolicy/securitypolicy controller share the same instance.
func GetSecurityService(service common.Service, vpcService common.VPCServiceProvider) *SecurityPolicyService {
	if securityService == nil {
		lock.Lock()
		defer lock.Unlock()
		if securityService == nil {
			var err error
			if securityService, err = InitializeSecurityPolicy(service, vpcService); err != nil {
				log.Error(err, "failed to initialize subnet commonService")
				os.Exit(1)
			}
		}
	}
	return securityService
}

// InitializeSecurityPolicy sync NSX resources
func InitializeSecurityPolicy(service common.Service, vpcService common.VPCServiceProvider) (*SecurityPolicyService, error) {
	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)

	wg.Add(4)

	securityPolicyService := &SecurityPolicyService{Service: service}

	securityPolicyService.securityPolicyStore = &SecurityPolicyStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(
			keyFunc, cache.Indexers{
				common.TagScopeSecurityPolicyCRUID: indexBySecurityPolicyCRUID,
				common.TagScopeNetworkPolicyUID:    indexByNetworkPolicyUID,
			}),
		BindingType: model.SecurityPolicyBindingType(),
	}}
	securityPolicyService.groupStore = &GroupStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
			common.TagScopeSecurityPolicyCRUID: indexBySecurityPolicyCRUID,
			common.TagScopeNetworkPolicyUID:    indexByNetworkPolicyUID,
			common.TagScopeRuleID:              indexGroupFunc,
		}),
		BindingType: model.GroupBindingType(),
	}}
	securityPolicyService.ruleStore = &RuleStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
			common.TagScopeSecurityPolicyCRUID: indexBySecurityPolicyCRUID,
			common.TagScopeNetworkPolicyUID:    indexByNetworkPolicyUID,
		}),
		BindingType: model.RuleBindingType(),
	}}
	securityPolicyService.shareStore = &ShareStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
			common.TagScopeSecurityPolicyCRUID: indexBySecurityPolicyCRUID,
			common.TagScopeNetworkPolicyUID:    indexByNetworkPolicyUID,
		}),
		BindingType: model.ShareBindingType(),
	}}
	securityPolicyService.vpcService = vpcService

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

func (service *SecurityPolicyService) CreateOrUpdateSecurityPolicy(obj interface{}) error {
	var err error
	switch obj.(type) {
	case *networkingv1.NetworkPolicy:
		internalSecurityPolicies, err := service.convertNetworkPolicyToInternalSecurityPolicies(obj.(*networkingv1.NetworkPolicy))
		if err != nil {
			return err
		}
		for _, internalSecurityPolicy := range internalSecurityPolicies {
			err = service.createOrUpdateSecurityPolicy(internalSecurityPolicy, common.ResourceTypeNetworkPolicy)
			if err != nil {
				return err
			}
		}
	case *v1alpha1.SecurityPolicy:
		err = service.createOrUpdateSecurityPolicy(obj.(*v1alpha1.SecurityPolicy), common.ResourceTypeSecurityPolicy)
	}
	return err
}

func (service *SecurityPolicyService) convertNetworkPolicyToInternalSecurityPolicies(networkPolicy *networkingv1.NetworkPolicy) ([]*v1alpha1.SecurityPolicy, error) {
	securityPolicies := []*v1alpha1.SecurityPolicy{}
	actionAllow := v1alpha1.RuleActionAllow
	actionDrop := v1alpha1.RuleActionDrop
	directionIn := v1alpha1.RuleDirectionIn
	directionOut := v1alpha1.RuleDirectionOut
	spAllow := &v1alpha1.SecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: networkPolicy.Namespace,
			Name:      service.BuildNetworkPolicyAllowPolicyName(networkPolicy.Name),
			UID:       types.UID(service.BuildNetworkPolicyAllowPolicyID(string(networkPolicy.UID))),
		},
		Spec: v1alpha1.SecurityPolicySpec{
			Priority: common.PriorityNetworkPolicyAllowRule,
			AppliedTo: []v1alpha1.SecurityPolicyTarget{
				{
					PodSelector: &networkPolicy.Spec.PodSelector,
				},
			},
		},
	}
	spIsolation := &v1alpha1.SecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: networkPolicy.Namespace,
			Name:      service.BuildNetworkPolicyIsolationPolicyName(networkPolicy.Name),
			UID:       types.UID(service.BuildNetworkPolicyIsolationPolicyID(string(networkPolicy.UID))),
		},
		Spec: v1alpha1.SecurityPolicySpec{
			Priority: common.PriorityNetworkPolicyIsolationRule,
			AppliedTo: []v1alpha1.SecurityPolicyTarget{
				{
					PodSelector: &networkPolicy.Spec.PodSelector,
				},
			},
		},
	}

	if len(networkPolicy.Spec.Ingress) > 0 {
		spIsolation.Spec.Rules = []v1alpha1.SecurityPolicyRule{
			{
				Action:    &actionDrop,
				Direction: &directionIn,
				Name:      "ingress-isolation",
			},
		}
		for _, ingress := range networkPolicy.Spec.Ingress {
			rule := &v1alpha1.SecurityPolicyRule{
				Action:    &actionAllow,
				Direction: &directionIn,
				Sources:   []v1alpha1.SecurityPolicyPeer{},
			}
			for _, p := range ingress.From {
				npPeer := p
				spPeer, err := service.convertNetworkPolicyPeerToSecurityPolicyPeer(&npPeer)
				if err != nil {
					return securityPolicies, err
				}
				rule.Sources = append(rule.Sources, *spPeer)
			}
			for _, p := range ingress.Ports {
				npPort := p
				spPort, err := service.convertNetworkPolicyPortToSecurityPolicyPort(&npPort)
				if err != nil {
					return securityPolicies, err
				}
				rule.Ports = append(rule.Ports, *spPort)
			}
			rule.Name = service.buildRulePortsString(&rule.Ports, "ingress")
			spAllow.Spec.Rules = append(spAllow.Spec.Rules, *rule)
		}
	}
	securityPolicies = append(securityPolicies, spAllow, spIsolation)

	if len(networkPolicy.Spec.Egress) > 0 {
		spIsolation.Spec.Rules = append(spIsolation.Spec.Rules, v1alpha1.SecurityPolicyRule{
			Action:    &actionDrop,
			Direction: &directionOut,
			Name:      "egress-isolation",
		})
		for _, egress := range networkPolicy.Spec.Egress {
			rule := &v1alpha1.SecurityPolicyRule{
				Action:       &actionAllow,
				Direction:    &directionOut,
				Destinations: []v1alpha1.SecurityPolicyPeer{},
			}
			for _, p := range egress.To {
				npPeer := p
				spPeer, err := service.convertNetworkPolicyPeerToSecurityPolicyPeer(&npPeer)
				if err != nil {
					return securityPolicies, err
				}
				rule.Destinations = append(rule.Destinations, *spPeer)
			}
			for _, p := range egress.Ports {
				npPort := p
				spPort, err := service.convertNetworkPolicyPortToSecurityPolicyPort(&npPort)
				if err != nil {
					return securityPolicies, err
				}
				rule.Ports = append(rule.Ports, *spPort)
			}
			rule.Name = service.buildRulePortsString(&rule.Ports, "egress")
			spAllow.Spec.Rules = append(spAllow.Spec.Rules, *rule)
		}
	}
	securityPolicies = append(securityPolicies, spAllow, spIsolation)
	log.V(1).Info("converted network policy to security policies", "securityPolicies", securityPolicies)
	return securityPolicies, nil
}

func (service *SecurityPolicyService) convertNetworkPolicyPeerToSecurityPolicyPeer(npPeer *networkingv1.NetworkPolicyPeer) (*v1alpha1.SecurityPolicyPeer, error) {
	if npPeer.PodSelector != nil && npPeer.NamespaceSelector == nil && npPeer.IPBlock == nil {
		return &v1alpha1.SecurityPolicyPeer{
			PodSelector: npPeer.PodSelector,
		}, nil
	} else if npPeer.PodSelector == nil && npPeer.NamespaceSelector != nil && npPeer.IPBlock == nil {
		return &v1alpha1.SecurityPolicyPeer{
			PodSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{},
			},
			NamespaceSelector: npPeer.NamespaceSelector,
		}, nil
	} else if npPeer.PodSelector != nil && npPeer.NamespaceSelector != nil && npPeer.IPBlock == nil {
		return &v1alpha1.SecurityPolicyPeer{
			PodSelector:       npPeer.PodSelector,
			NamespaceSelector: npPeer.NamespaceSelector,
		}, nil
	} else if npPeer.PodSelector == nil && npPeer.NamespaceSelector == nil && npPeer.IPBlock != nil {
		var ipBlocks []v1alpha1.IPBlock
		cidr := npPeer.IPBlock.CIDR
		if npPeer.IPBlock.Except == nil {
			ipBlocks = append(ipBlocks, v1alpha1.IPBlock{CIDR: cidr})
		} else {
			ranges, err := util.GetCIDRRangesWithExcept(cidr, npPeer.IPBlock.Except)
			if err != nil {
				return nil, err
			}
			for _, rng := range ranges {
				ipBlocks = append(ipBlocks, v1alpha1.IPBlock{CIDR: rng})
			}
		}
		return &v1alpha1.SecurityPolicyPeer{
			IPBlocks: ipBlocks,
		}, nil
	}
	err := fmt.Errorf("unsupported NetworkPolicyPeer: %s", npPeer)
	return nil, err
}

func (service *SecurityPolicyService) convertNetworkPolicyPortToSecurityPolicyPort(npPort *networkingv1.NetworkPolicyPort) (*v1alpha1.SecurityPolicyPort, error) {
	spPort := &v1alpha1.SecurityPolicyPort{
		Protocol: *npPort.Protocol,
		Port:     *npPort.Port,
	}
	if npPort.EndPort != nil {
		spPort.EndPort = int(*npPort.EndPort)
	}
	return spPort, nil
}

func (service *SecurityPolicyService) getStoresByCreatedFor(createdFor string) (*SecurityPolicyStore, *RuleStore, *GroupStore, *ShareStore) {
	return service.securityPolicyStore, service.ruleStore, service.groupStore, service.shareStore
}

func (service *SecurityPolicyService) createOrUpdateSecurityPolicy(obj *v1alpha1.SecurityPolicy, createdFor string) error {
	securityPolicyStore, ruleStore, groupStore, _ := service.getStoresByCreatedFor(createdFor)
	nsxSecurityPolicy, nsxGroups, nsxProjectShares, err := service.buildSecurityPolicy(obj, createdFor)
	if err != nil {
		log.Error(err, "failed to build SecurityPolicy")
		return err
	}

	if len(nsxSecurityPolicy.Scope) == 0 {
		log.Info("SecurityPolicy has empty policy-level appliedTo")
	}
	indexScope := common.TagScopeSecurityPolicyCRUID
	if createdFor == common.ResourceTypeNetworkPolicy {
		indexScope = common.TagScopeNetworkPolicyUID
	}
	existingSecurityPolicy := securityPolicyStore.GetByKey(*nsxSecurityPolicy.Id)
	existingRules := ruleStore.GetByIndex(indexScope, string(obj.UID))
	existingGroups := groupStore.GetByIndex(indexScope, string(obj.UID))

	isChanged := true
	if existingSecurityPolicy != nil {
		isChanged = common.CompareResource(SecurityPolicyToComparable(existingSecurityPolicy), SecurityPolicyToComparable(nsxSecurityPolicy))
	}

	changed, stale := common.CompareResources(RulesPtrToComparable(existingRules), RulesToComparable(nsxSecurityPolicy.Rules))
	changedRules, staleRules := ComparableToRules(changed), ComparableToRules(stale)
	changed, stale = common.CompareResources(GroupsPtrToComparable(existingGroups), GroupsToComparable(*nsxGroups))
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
		vpcInfo, err := service.getVpcInfo(obj.ObjectMeta.Namespace)
		if err != nil {
			return err
		}

		var finalProjectGroups *[]model.Group
		var finalProjectShares *[]model.Share

		if len(*nsxProjectShares) != 0 {
			projectGroups := make([]model.Group, 0)
			projectShares := make([]model.Share, 0)
			// create/update nsx project shares and nsx project level groups
			for i := len(*nsxProjectShares) - 1; i >= 0; i-- {
				projectGroups = append(projectGroups, *((*nsxProjectShares)[i].shareGroup))
				projectShares = append(projectShares, *((*nsxProjectShares)[i].share))
			}

			// 1.Create/update project level groups
			finalProjectGroups, err = service.createOrUpdateProjectGroups(obj, projectGroups, createdFor)
			if err != nil {
				log.Error(err, "failed to create or update project level groups")
				return err
			}

			// 2.Create/update project shares
			finalProjectShares, err = service.createOrUpdateProjectShares(obj, projectShares, createdFor)
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
			err = groupStore.Apply(finalProjectGroups)
			if err != nil {
				log.Error(err, "failed to apply store", "group", finalProjectGroups)
				return err
			}
		}

		if (finalProjectShares != nil) && len(*finalProjectShares) != 0 {
			if err = service.shareStore.Apply(finalProjectShares); err != nil {
				log.Error(err, "failed to apply store", "share", finalProjectShares)
				return err
			}
		}
	} else {
		infraSecurityPolicy, err := service.WrapHierarchySecurityPolicy(finalSecurityPolicy, finalGroups)
		if err != nil {
			log.Error(err, "failed to wrap SecurityPolicy")
			return err
		}
		if err = service.NSXClient.InfraClient.Patch(*infraSecurityPolicy, &EnforceRevisionCheckParam); err != nil {
			log.Error(err, "failed to update SecurityPolicy")
			return err
		}
	}
	if err != nil {
		return err
	}

	// The steps below know how to deal with NSX resources, if there is MarkedForDelete, then delete it from store,
	// otherwise add or update it to store.
	if isChanged {
		err = securityPolicyStore.Apply(&finalSecurityPolicyCopy)
		if err != nil {
			return err
		}
	}
	if !(len(changedRules) == 0 && len(staleRules) == 0) {
		err = ruleStore.Apply(&finalSecurityPolicyCopy)
		if err != nil {
			return err
		}
	}
	if !(len(changedGroups) == 0 && len(staleGroups) == 0) {
		err = groupStore.Apply(&finalGroups)
		if err != nil {
			return err
		}
	}
	log.Info("successfully created or updated nsx SecurityPolicy", "nsxSecurityPolicy", finalSecurityPolicyCopy)
	return nil
}

func (service *SecurityPolicyService) DeleteSecurityPolicy(obj interface{}, isVpcCleanup bool, createdFor string) error {
	var err error
	switch obj.(type) {
	case *networkingv1.NetworkPolicy:
		internalSecurityPolicies, err := service.convertNetworkPolicyToInternalSecurityPolicies(obj.(*networkingv1.NetworkPolicy))
		if err != nil {
			return err
		}
		for _, internalSecurityPolicy := range internalSecurityPolicies {
			err = service.deleteSecurityPolicy(internalSecurityPolicy, isVpcCleanup, createdFor)
			if err != nil {
				return err
			}
		}
	case *v1alpha1.SecurityPolicy:
		err = service.deleteSecurityPolicy(obj, isVpcCleanup, createdFor)
	case types.UID:
		err = service.deleteSecurityPolicy(obj, isVpcCleanup, createdFor)
	}
	return err
}

func (service *SecurityPolicyService) deleteSecurityPolicy(obj interface{}, isVpcCleanup bool, createdFor string) error {
	var nsxSecurityPolicy *model.SecurityPolicy
	var spNameSpace string
	var err error
	g := make([]model.Group, 0)
	nsxGroups := &g
	var projectShares *[]ProjectShare
	nsxProjectShares := make([]model.Share, 0)
	nsxProjectGroups := make([]model.Group, 0)
	securityPolicyStore, ruleStore, groupStore, shareStore := service.getStoresByCreatedFor(createdFor)
	switch sp := obj.(type) {
	// This case is for normal SecurityPolicy deletion process, which means that SecurityPolicy
	// has corresponding nsx SecurityPolicy object
	case *v1alpha1.SecurityPolicy:
		nsxSecurityPolicy, nsxGroups, projectShares, err = service.buildSecurityPolicy(sp, createdFor)
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
		indexScope := common.TagScopeSecurityPolicyCRUID
		if createdFor == common.ResourceTypeNetworkPolicy {
			indexScope = common.TagScopeNetworkPolicyUID
		}
		securityPolicies := securityPolicyStore.GetByIndex(indexScope, string(sp))
		if len(securityPolicies) == 0 {
			log.Info("NSX security policy is not found in store, skip deleting it", "nsxSecurityPolicyUID", sp, "createdFor", createdFor)
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

		groups := groupStore.GetByIndex(indexScope, string(sp))
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
			shares := shareStore.GetByIndex(indexScope, string(sp))
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
		vpcInfo, err := service.getVpcInfo(spNameSpace)
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
				log.Error(err, "failed to delete project level groups")
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
			if err = service.groupStore.Apply(&nsxProjectGroups); err != nil {
				return err
			}
		}
	} else {
		infraSecurityPolicy, err := service.WrapHierarchySecurityPolicy(nsxSecurityPolicy, *nsxGroups)
		if err != nil {
			log.Error(err, "failed to wrap SecurityPolicy")
			return err
		}
		if err = service.NSXClient.InfraClient.Patch(*infraSecurityPolicy, &EnforceRevisionCheckParam); err != nil {
			log.Error(err, "failed to delete SecurityPolicy")
			return err
		}
	}

	err = securityPolicyStore.Apply(&finalSecurityPolicyCopy)
	if err != nil {
		return err
	}
	err = groupStore.Apply(nsxGroups)
	if err != nil {
		return err
	}
	err = ruleStore.Apply(&finalSecurityPolicyCopy)
	if err != nil {
		return err
	}
	log.Info("successfully deleted nsx SecurityPolicy", "nsxSecurityPolicy", finalSecurityPolicyCopy)
	return nil
}

func (service *SecurityPolicyService) createOrUpdateGroups(obj *v1alpha1.SecurityPolicy, nsxGroups []*model.Group) error {
	var vpcInfo *common.VPCResourceInfo
	var err error
	for _, group := range nsxGroups {
		group.MarkedForDelete = nil
		if isVpcEnabled(service) {
			vpcInfo, err = service.getVpcInfo(obj.ObjectMeta.Namespace)
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
func (service *SecurityPolicyService) createOrUpdateProjectShares(obj *v1alpha1.SecurityPolicy, projectShares []model.Share, createdFor string) (*[]model.Share, error) {
	_, _, _, shareStore := service.getStoresByCreatedFor(createdFor)
	finalShares := make([]model.Share, 0)

	indexScope := common.TagScopeSecurityPolicyCRUID
	if createdFor == common.ResourceTypeNetworkPolicy {
		indexScope = common.TagScopeNetworkPolicyUID
	}
	existingShares := shareStore.GetByIndex(indexScope, string(obj.UID))

	changed, stale := common.CompareResources(SharesPtrToComparable(existingShares), SharesToComparable(projectShares))
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

	vpcInfo, err := service.getVpcInfo(obj.ObjectMeta.Namespace)
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

func (service *SecurityPolicyService) createOrUpdateProjectGroups(obj *v1alpha1.SecurityPolicy, groups []model.Group, createdFor string) (*[]model.Group, error) {
	_, _, groupStore, _ := service.getStoresByCreatedFor(createdFor)
	finalGroups := make([]model.Group, 0)

	indexScope := common.TagScopeSecurityPolicyCRUID
	if createdFor == common.ResourceTypeNetworkPolicy {
		indexScope = common.TagScopeNetworkPolicyUID
	}
	tmpExistingGroups := groupStore.GetByIndex(indexScope, string(obj.UID))
	existingGroups := make([]model.Group, len(tmpExistingGroups))
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

	vpcInfo, err := service.getVpcInfo(obj.ObjectMeta.Namespace)
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

func (service *SecurityPolicyService) ListSecurityPolicyID() sets.Set[string] {
	// List SeurityPolicyID to which groups resources are associated in group store
	groupSet := service.groupStore.ListIndexFuncValues(common.TagScopeSecurityPolicyCRUID)
	// List SeurityPolicyID to which share resources are associated in share store
	shareSet := service.shareStore.ListIndexFuncValues(common.TagScopeSecurityPolicyCRUID)
	policySet := service.securityPolicyStore.ListIndexFuncValues(common.TagScopeSecurityPolicyCRUID)

	return groupSet.Union(policySet).Union(shareSet)
}

func (service *SecurityPolicyService) ListNetworkPolicyID() sets.Set[string] {
	// List ListNetworkPolicyID to which groups resources are associated in group store
	groupSet := service.groupStore.ListIndexFuncValues(common.TagScopeNetworkPolicyUID)
	// List service to which share resources are associated in share store
	shareSet := service.shareStore.ListIndexFuncValues(common.TagScopeNetworkPolicyUID)
	policySet := service.securityPolicyStore.ListIndexFuncValues(common.TagScopeNetworkPolicyUID)

	return groupSet.Union(policySet).Union(shareSet)
}

func (service *SecurityPolicyService) Cleanup() error {
	// Delete all the security policies in store
	uids := service.ListSecurityPolicyID()
	log.Info("cleaning up security policies created for CR", "count", len(uids))
	for uid := range uids {
		err := service.DeleteSecurityPolicy(types.UID(uid), true, common.ResourceTypeSecurityPolicy)
		if err != nil {
			return err
		}
	}
	uids = service.ListNetworkPolicyID()
	log.Info("cleaning up security policies created for network policy", "count", len(uids))
	for uid := range uids {
		err := service.DeleteSecurityPolicy(types.UID(uid), true, common.ResourceTypeNetworkPolicy)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *SecurityPolicyService) getVpcInfo(spNameSpace string) (*common.VPCResourceInfo, error) {
	VPCInfo := s.vpcService.ListVPCInfo(spNameSpace)
	if len(VPCInfo) == 0 {
		errorMsg := fmt.Sprintf("there is no VPC info found for namespace %s", spNameSpace)
		err := errors.New(errorMsg)
		return nil, err
	}
	return &VPCInfo[0], nil
}
