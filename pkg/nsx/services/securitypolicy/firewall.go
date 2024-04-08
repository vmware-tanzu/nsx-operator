package securitypolicy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
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
	projectGroupStore   *GroupStore
	shareStore          *ShareStore
	vpcService          common.VPCServiceProvider
}

type ProjectShare struct {
	shareGroup *model.Group
	share      *model.Share
}

var (
	securityService *SecurityPolicyService
	lock            = &sync.Mutex{}
)

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

	wg.Add(5)

	securityPolicyService := &SecurityPolicyService{Service: service}

	if isVpcEnabled(securityPolicyService) {
		common.TagValueScopeSecurityPolicyName = common.TagScopeSecurityPolicyName
		common.TagValueScopeSecurityPolicyUID = common.TagScopeSecurityPolicyUID
	}
	indexScope := common.TagValueScopeSecurityPolicyUID

	securityPolicyService.securityPolicyStore = &SecurityPolicyStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(
			keyFunc, cache.Indexers{
				indexScope:                      indexBySecurityPolicyUID,
				common.TagScopeNetworkPolicyUID: indexByNetworkPolicyUID,
			}),
		BindingType: model.SecurityPolicyBindingType(),
	}}
	securityPolicyService.groupStore = &GroupStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
			indexScope:                      indexBySecurityPolicyUID,
			common.TagScopeNetworkPolicyUID: indexByNetworkPolicyUID,
			common.TagScopeRuleID:           indexGroupFunc,
		}),
		BindingType: model.GroupBindingType(),
	}}
	securityPolicyService.ruleStore = &RuleStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
			indexScope:                      indexBySecurityPolicyUID,
			common.TagScopeNetworkPolicyUID: indexByNetworkPolicyUID,
		}),
		BindingType: model.RuleBindingType(),
	}}

	securityPolicyService.projectGroupStore = &GroupStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
			indexScope:                      indexBySecurityPolicyUID,
			common.TagScopeNetworkPolicyUID: indexByNetworkPolicyUID,
		}),
		BindingType: model.GroupBindingType(),
	}}
	securityPolicyService.shareStore = &ShareStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
			indexScope:                      indexBySecurityPolicyUID,
			common.TagScopeNetworkPolicyUID: indexByNetworkPolicyUID,
		}),
		BindingType: model.ShareBindingType(),
	}}
	securityPolicyService.vpcService = vpcService

	projectGroupShareTag := []model.Tag{
		{
			Scope: String(common.TagScopeProjectGroupShared),
			Tag:   String("true"),
		},
	}
	projectGroupNotShareTag := []model.Tag{
		{
			Scope: String(common.TagScopeProjectGroupShared),
			Tag:   String("false"),
		},
	}
	if isVpcEnabled(securityPolicyService) {
		go securityPolicyService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeGroup, projectGroupNotShareTag, securityPolicyService.groupStore)
	} else {
		go securityPolicyService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeGroup, nil, securityPolicyService.groupStore)
	}

	go securityPolicyService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeGroup, projectGroupShareTag, securityPolicyService.projectGroupStore)
	go securityPolicyService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeShare, nil, securityPolicyService.shareStore)
	go securityPolicyService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeSecurityPolicy, nil, securityPolicyService.securityPolicyStore)
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

func (service *SecurityPolicyService) CreateOrUpdateSecurityPolicy(obj interface{}) error {
	if !nsxutil.IsLicensed(nsxutil.FeatureDFW) {
		log.Info("no DFW license, skip creating SecurityPolicy.")
		return nsxutil.RestrictionError{Desc: "no DFW license"}
	}
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
			spAllow.Spec.Rules = append(spAllow.Spec.Rules, *rule)
		}
	}

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

func (service *SecurityPolicyService) getStores() (*SecurityPolicyStore, *RuleStore, *GroupStore, *GroupStore, *ShareStore) {
	return service.securityPolicyStore, service.ruleStore, service.groupStore, service.projectGroupStore, service.shareStore
}

func (service *SecurityPolicyService) createOrUpdateSecurityPolicy(obj *v1alpha1.SecurityPolicy, createdFor string) error {
	securityPolicyStore, ruleStore, groupStore, projectGroupStore, shareStore := service.getStores()
	nsxSecurityPolicy, nsxGroups, projectShares, err := service.buildSecurityPolicy(obj, createdFor)
	if err != nil {
		log.Error(err, "failed to build SecurityPolicy")
		return err
	}

	if len(nsxSecurityPolicy.Scope) == 0 {
		log.Info("SecurityPolicy has empty policy-level appliedTo")
	}
	indexScope := common.TagValueScopeSecurityPolicyUID
	if createdFor == common.ResourceTypeNetworkPolicy {
		indexScope = common.TagScopeNetworkPolicyUID
	}
	existingSecurityPolicy := securityPolicyStore.GetByKey(*nsxSecurityPolicy.Id)
	existingRules := ruleStore.GetByIndex(indexScope, string(obj.UID))
	existingGroups := groupStore.GetByIndex(indexScope, string(obj.UID))

	isChanged := true
	if existingSecurityPolicy != nil {
		isChanged = common.CompareResource(SecurityPolicyPtrToComparable(existingSecurityPolicy), SecurityPolicyPtrToComparable(nsxSecurityPolicy))
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
		staleRules[i].MarkedForDelete = &MarkedForDelete // nsx clients need this field to delete the rules
	}
	finalRules = append(finalRules, staleRules...)
	finalRules = append(finalRules, changedRules...)
	finalSecurityPolicy.Rules = finalRules

	finalGroups := make([]model.Group, 0)
	for i := len(staleGroups) - 1; i >= 0; i-- { // Don't use range, it would copy the element
		staleGroups[i].MarkedForDelete = &MarkedForDelete // nsx clients need this field to delete the group
	}
	finalGroups = append(finalGroups, staleGroups...)
	finalGroups = append(finalGroups, changedGroups...)

	// WrapHighLevelSecurityPolicy will modify the input security policy, so we need to make a copy for the following store update.
	finalSecurityPolicyCopy := *finalSecurityPolicy
	finalSecurityPolicyCopy.Rules = finalRules

	if isVpcEnabled(service) {
		vpcInfo, err := service.getVpcInfo(obj.ObjectMeta.Namespace)
		if err != nil {
			return err
		}

		finalProjectGroups := make([]model.Group, 0)
		finalProjectShares := make([]model.Share, 0)
		nsxProjectGroups := make([]model.Group, 0)
		nsxProjectShares := make([]model.Share, 0)
		for i := len(*projectShares) - 1; i >= 0; i-- {
			nsxProjectGroups = append(nsxProjectGroups, *((*projectShares)[i].shareGroup))
			nsxProjectShares = append(nsxProjectShares, *((*projectShares)[i].share))
		}

		// Create/Update nsx project shares and nsx project level groups
		existingNsxProjectGroups := projectGroupStore.GetByIndex(indexScope, string(obj.UID))
		changed, stale := common.CompareResources(GroupsPtrToComparable(existingNsxProjectGroups), GroupsToComparable(nsxProjectGroups))
		changedProjectGroups, staleProjectGroups := ComparableToGroups(changed), ComparableToGroups(stale)
		if len(changedProjectGroups) == 0 && len(staleProjectGroups) == 0 {
			log.Info("project groups are not changed, skip updating them")
		}
		for i := len(staleProjectGroups) - 1; i >= 0; i-- {
			staleProjectGroups[i].MarkedForDelete = &MarkedForDelete
		}
		finalProjectGroups = append(finalProjectGroups, staleProjectGroups...)
		finalProjectGroups = append(finalProjectGroups, changedProjectGroups...)

		existingNsxProjectShares := shareStore.GetByIndex(indexScope, string(obj.UID))
		changed, stale = common.CompareResources(SharesPtrToComparable(existingNsxProjectShares), SharesToComparable(nsxProjectShares))
		changedProjectShares, staleProjectShares := ComparableToShares(changed), ComparableToShares(stale)
		if len(changedProjectShares) == 0 && len(staleProjectShares) == 0 {
			log.Info("project shares are not changed, skip updating them")
		}
		for i := len(staleProjectShares) - 1; i >= 0; i-- {
			staleProjectShares[i].MarkedForDelete = &MarkedForDelete
		}
		finalProjectShares = append(finalProjectShares, staleProjectShares...)
		finalProjectShares = append(finalProjectShares, changedProjectShares...)

		// 1.Wrap project groups and shares into project child infra.
		var projectInfra []*data.StructValue
		if len(finalProjectGroups) != 0 || len(finalProjectShares) != 0 {
			projectInfra, err = service.wrapHierarchyProjectResources(finalProjectShares, finalProjectGroups)
			if err != nil {
				log.Error(err, "failed to wrap project groups and shares")
				return err
			}
		}

		// 2.Wrap SecurityPolicy, groups, rules under VPC level together with project groups and shares into one hierarchy resource tree.
		orgRoot, err := service.WrapHierarchyVpcSecurityPolicy(finalSecurityPolicy, finalGroups, projectInfra, vpcInfo)
		if err != nil {
			log.Error(err, "failed to wrap SecurityPolicy in VPC")
			return err
		}

		// 3.Create/update SecurityPolicy together with groups, rules under VPC level and project groups, shares.
		err = service.NSXClient.OrgRootClient.Patch(*orgRoot, &EnforceRevisionCheckParam)
		if err != nil {
			log.Error(err, "failed to create or update SecurityPolicy in VPC")
			return err
		}

		if len(finalProjectGroups) != 0 {
			err = projectGroupStore.Apply(&finalProjectGroups)
			if err != nil {
				log.Error(err, "failed to apply store", "nsxProjectGroups", finalProjectGroups)
				return err
			}
		}

		if len(finalProjectShares) != 0 {
			err = shareStore.Apply(&finalProjectShares)
			if err != nil {
				log.Error(err, "failed to apply store", "nsxProjectShares", finalProjectShares)
				return err
			}
		}
	} else {
		infraSecurityPolicy, err := service.WrapHierarchySecurityPolicy(finalSecurityPolicy, finalGroups)
		if err != nil {
			log.Error(err, "failed to wrap SecurityPolicy")
			return err
		}
		err = service.NSXClient.InfraClient.Patch(*infraSecurityPolicy, &EnforceRevisionCheckParam)
		if err != nil {
			log.Error(err, "failed to create or update SecurityPolicy")
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
			log.Error(err, "failed to apply store", "securityPolicy", finalSecurityPolicyCopy)
			return err
		}
	}
	if !(len(changedRules) == 0 && len(staleRules) == 0) {
		err = ruleStore.Apply(&finalSecurityPolicyCopy)
		if err != nil {
			log.Error(err, "failed to apply store", "nsxRules", finalSecurityPolicyCopy.Rules)
			return err
		}
	}
	if !(len(changedGroups) == 0 && len(staleGroups) == 0) {
		err = groupStore.Apply(&finalGroups)
		if err != nil {
			log.Error(err, "failed to apply store", "nsxGroups", finalGroups)
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
	r := make([]model.Rule, 0)
	nsxRules := &r
	var projectShares *[]ProjectShare
	nsxProjectShares := make([]model.Share, 0)
	nsxProjectGroups := make([]model.Group, 0)
	securityPolicyStore, ruleStore, groupStore, projectGroupStore, shareStore := service.getStores()
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
		// project share and project groups only needed in VPC network.
		for i := len(*projectShares) - 1; i >= 0; i-- {
			nsxProjectGroups = append(nsxProjectGroups, *(*projectShares)[i].shareGroup)
			nsxProjectShares = append(nsxProjectShares, *(*projectShares)[i].share)
		}

	// This case is for SecurityPolicy GC or cleanup process, which means that SecurityPolicy
	// doesn't exist in K8s any more but still has corresponding nsx SecurityPolicy object.
	// Hence, we use SecurityPolicy's UID here from store instead of K8s SecurityPolicy object
	case types.UID:
		indexScope := common.TagValueScopeSecurityPolicyUID
		if createdFor == common.ResourceTypeNetworkPolicy {
			indexScope = common.TagScopeNetworkPolicyUID
		}
		existingSecurityPolices := securityPolicyStore.GetByIndex(indexScope, string(sp))
		if len(existingSecurityPolices) == 0 {
			log.Info("NSX security policy is not found in store, skip deleting it", "nsxSecurityPolicyUID", sp, "createdFor", createdFor)
			return nil
		}
		nsxSecurityPolicy = existingSecurityPolices[0]
		// Get namespace of nsx SecurityPolicy from tags since there is no K8s SecurityPolicy object
		for i := len(nsxSecurityPolicy.Tags) - 1; i >= 0; i-- {
			if *(nsxSecurityPolicy.Tags[i].Scope) == common.TagScopeNamespace {
				spNameSpace = *(nsxSecurityPolicy.Tags[i].Tag)
				log.V(1).Info("get namespace with SecurityPolicy index", "namespace", spNameSpace, "securityPolicyUID", string(sp))
				break
			}
		}

		existingGroups := groupStore.GetByIndex(indexScope, string(sp))
		if len(existingGroups) == 0 {
			log.Info("did not get groups with SecurityPolicy index", "securityPolicyUID", string(sp))
		}
		for _, group := range existingGroups {
			*nsxGroups = append(*nsxGroups, *group)
		}

		// In GC or Cleanup process, there is no nsx rules in the security policy retrieved from securityPolicy store
		// the rules associated the deleting security policy can only be gotten from rule store.
		existingRules := ruleStore.GetByIndex(indexScope, string(sp))
		if len(existingRules) == 0 {
			log.Info("did not get rules with SecurityPolicy index", "securityPolicyUID", string(sp))
		}
		for _, rule := range existingRules {
			*nsxRules = append(*nsxRules, *rule)
		}
		nsxSecurityPolicy.Rules = *nsxRules

		if isVpcEnabled(service) || isVpcCleanup {
			existingNsxProjectGroups := projectGroupStore.GetByIndex(indexScope, string(sp))
			if len(existingNsxProjectGroups) == 0 {
				log.Info("did not get project groups with SecurityPolicy index", "securityPolicyUID", string(sp))
			}
			for _, projectGroup := range existingNsxProjectGroups {
				nsxProjectGroups = append(nsxProjectGroups, *projectGroup)
			}
			existingNsxProjectShares := shareStore.GetByIndex(indexScope, string(sp))
			if len(existingNsxProjectShares) == 0 {
				log.Info("did not get project shares with SecurityPolicy index", "securityPolicyUID", string(sp))
			}
			for _, nsxShare := range existingNsxProjectShares {
				nsxProjectShares = append(nsxProjectShares, *nsxShare)
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
		var vpcInfo *common.VPCResourceInfo
		if isVpcCleanup == false {
			vpcInfo, err = service.getVpcInfo(spNameSpace)
			if err != nil {
				return err
			}
		} else {
			// In cleanup process, vpcInfo should be listed directly from security policy store to avoid calling VPC service.
			// Get orgId, projectId, vpcId from security policy path "/orgs/<orgId>/projects/<projectId>/vpcs/<vpcId>/security-policies/<spId>"
			if nsxSecurityPolicy.Path == nil {
				err = errors.New("nsxSecurityPolicy path is empty")
				log.Error(err, "failed to delete SecurityPolicy in VPC")
				return err
			}

			vpcInfo = new(common.VPCResourceInfo)
			vpcInfo.OrgID, vpcInfo.ProjectID, vpcInfo.VPCID, _ = nsxutil.ParseVPCPath(*(nsxSecurityPolicy.Path))
		}

		for i := len(nsxProjectGroups) - 1; i >= 0; i-- {
			nsxProjectGroups[i].MarkedForDelete = &MarkedForDelete
		}
		for i := len(nsxProjectShares) - 1; i >= 0; i-- {
			nsxProjectShares[i].MarkedForDelete = &MarkedForDelete
		}

		// 1.Wrap project groups and shares into project child infra.
		var projectInfra []*data.StructValue
		if len(nsxProjectShares) != 0 || len(nsxProjectGroups) != 0 {
			projectInfra, err = service.wrapHierarchyProjectResources(nsxProjectShares, nsxProjectGroups)
			if err != nil {
				log.Error(err, "failed to wrap project groups and shares")
				return err
			}
		}

		// 2.Wrap SecurityPolicy, groups, rules under VPC level together with project groups and shares into one hierarchy resource tree.
		orgRoot, err := service.WrapHierarchyVpcSecurityPolicy(nsxSecurityPolicy, *nsxGroups, projectInfra, vpcInfo)
		if err != nil {
			log.Error(err, "failed to wrap SecurityPolicy in VPC")
			return err
		}

		// 3.Create/update SecurityPolicy together with groups, rules under VPC level and project groups, shares.
		err = service.NSXClient.OrgRootClient.Patch(*orgRoot, &EnforceRevisionCheckParam)
		if err != nil {
			log.Error(err, "failed to delete SecurityPolicy in VPC")
			return err
		}

		if len(nsxProjectGroups) != 0 {
			err = projectGroupStore.Apply(&nsxProjectGroups)
			if err != nil {
				log.Error(err, "failed to apply store", "nsxProjectGroups", nsxProjectGroups)
				return err
			}
		}

		if len(nsxProjectShares) != 0 {
			err = shareStore.Apply(&nsxProjectShares)
			if err != nil {
				log.Error(err, "failed to apply store", "nsxProjectShares", nsxProjectShares)
				return err
			}
		}
	} else {
		infraSecurityPolicy, err := service.WrapHierarchySecurityPolicy(nsxSecurityPolicy, *nsxGroups)
		if err != nil {
			log.Error(err, "failed to wrap SecurityPolicy")
			return err
		}
		err = service.NSXClient.InfraClient.Patch(*infraSecurityPolicy, &EnforceRevisionCheckParam)
		if err != nil {
			log.Error(err, "failed to delete SecurityPolicy")
			return err
		}
	}
	if err != nil {
		return err
	}

	err = securityPolicyStore.Apply(&finalSecurityPolicyCopy)
	if err != nil {
		log.Error(err, "failed to apply store", "securityPolicy", finalSecurityPolicyCopy)
		return err
	}
	err = ruleStore.Apply(&finalSecurityPolicyCopy)
	if err != nil {
		log.Error(err, "failed to apply store", "nsxRules", finalSecurityPolicyCopy.Rules)
		return err
	}
	err = groupStore.Apply(nsxGroups)
	if err != nil {
		log.Error(err, "failed to apply store", "nsxGroups", nsxGroups)
		return err
	}

	log.Info("successfully deleted nsx SecurityPolicy", "nsxSecurityPolicy", finalSecurityPolicyCopy)
	return nil
}

func (service *SecurityPolicyService) createOrUpdateGroups(obj *v1alpha1.SecurityPolicy, nsxGroups []*model.Group) error {
	var vpcInfo *common.VPCResourceInfo
	var err error
	finalGroups := make([]model.Group, 0)
	for _, group := range nsxGroups {
		group.MarkedForDelete = nil
		finalGroups = append(finalGroups, *group)
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
	err = service.groupStore.Apply(&finalGroups)
	if err != nil {
		return err
	}
	log.Info("successfully create or update groups", "nsxGroups", finalGroups)
	return nil
}

func (service *SecurityPolicyService) ListSecurityPolicyID() sets.Set[string] {
	indexScope := common.TagValueScopeSecurityPolicyUID

	// List SecurityPolicyID to which groups resources are associated in group store
	groupSet := service.groupStore.ListIndexFuncValues(indexScope)
	// List SecurityPolicyID to which share resources are associated in share store
	shareSet := service.shareStore.ListIndexFuncValues(indexScope)
	policySet := service.securityPolicyStore.ListIndexFuncValues(indexScope)

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

func (service *SecurityPolicyService) Cleanup(ctx context.Context) error {
	// Delete all the security policies in store
	uids := service.ListSecurityPolicyID()
	log.Info("cleaning up security policies created for CR", "count", len(uids))
	for uid := range uids {
		select {
		case <-ctx.Done():
			return errors.Join(nsxutil.TimeoutFailed, ctx.Err())
		default:
			err := service.DeleteSecurityPolicy(types.UID(uid), true, common.ResourceTypeSecurityPolicy)
			if err != nil {
				return err
			}
		}
	}

	// Delete all the security policies created for network policy in store
	uids = service.ListNetworkPolicyID()
	log.Info("cleaning up security policies created for network policy", "count", len(uids))
	for uid := range uids {
		select {
		case <-ctx.Done():
			return errors.Join(nsxutil.TimeoutFailed, ctx.Err())
		default:
			err := service.DeleteSecurityPolicy(types.UID(uid), true, common.ResourceTypeNetworkPolicy)
			if err != nil {
				return err
			}
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
