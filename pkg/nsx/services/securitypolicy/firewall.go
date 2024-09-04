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

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	log                        = &logger.Log
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

	securityPolicyService.setUpStore(indexScope)
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

func (s *SecurityPolicyService) setUpStore(indexScope string) {
	s.securityPolicyStore = &SecurityPolicyStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(
			keyFunc, cache.Indexers{
				indexScope:                      indexBySecurityPolicyUID,
				common.TagScopeNetworkPolicyUID: indexByNetworkPolicyUID,
			}),
		BindingType: model.SecurityPolicyBindingType(),
	}}
	s.groupStore = &GroupStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
			indexScope:                      indexBySecurityPolicyUID,
			common.TagScopeNetworkPolicyUID: indexByNetworkPolicyUID,
			common.TagScopeRuleID:           indexGroupFunc,
		}),
		BindingType: model.GroupBindingType(),
	}}
	s.ruleStore = &RuleStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
			indexScope:                      indexBySecurityPolicyUID,
			common.TagScopeNetworkPolicyUID: indexByNetworkPolicyUID,
		}),
		BindingType: model.RuleBindingType(),
	}}

	s.projectGroupStore = &GroupStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
			indexScope:                      indexBySecurityPolicyUID,
			common.TagScopeNetworkPolicyUID: indexByNetworkPolicyUID,
		}),
		BindingType: model.GroupBindingType(),
	}}
	s.shareStore = &ShareStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
			indexScope:                      indexBySecurityPolicyUID,
			common.TagScopeNetworkPolicyUID: indexByNetworkPolicyUID,
		}),
		BindingType: model.ShareBindingType(),
	}}
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
			err = service.createOrUpdateVPCSecurityPolicy(internalSecurityPolicy, common.ResourceTypeNetworkPolicy)
			if err != nil {
				return err
			}
		}
	case *v1alpha1.SecurityPolicy:
		if isVpcEnabled(service) {
			err = service.createOrUpdateVPCSecurityPolicy(obj.(*v1alpha1.SecurityPolicy), common.ResourceTypeSecurityPolicy)
		} else {
			// For T1 network SecurityPolicy create/update
			err = service.createOrUpdateSecurityPolicy(obj.(*v1alpha1.SecurityPolicy), common.ResourceTypeSecurityPolicy)
		}
	}
	return err
}

func (service *SecurityPolicyService) populateRulesForAllowSection(spAllow *v1alpha1.SecurityPolicy, networkPolicy *networkingv1.NetworkPolicy) error {
	actionAllow := v1alpha1.RuleActionAllow
	directionIn := v1alpha1.RuleDirectionIn
	directionOut := v1alpha1.RuleDirectionOut
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
				return err
			}
			rule.Sources = append(rule.Sources, *spPeer)
		}
		for _, p := range ingress.Ports {
			npPort := p
			spPort, err := service.convertNetworkPolicyPortToSecurityPolicyPort(&npPort)
			if err != nil {
				return err
			}
			rule.Ports = append(rule.Ports, *spPort)
		}
		spAllow.Spec.Rules = append(spAllow.Spec.Rules, *rule)
	}

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
				return err
			}
			rule.Destinations = append(rule.Destinations, *spPeer)
		}
		for _, p := range egress.Ports {
			npPort := p
			spPort, err := service.convertNetworkPolicyPortToSecurityPolicyPort(&npPort)
			if err != nil {
				return err
			}
			rule.Ports = append(rule.Ports, *spPort)
		}
		spAllow.Spec.Rules = append(spAllow.Spec.Rules, *rule)
	}
	return nil
}

func (service *SecurityPolicyService) populateRulesForIsolationSection(spIsolation *v1alpha1.SecurityPolicy, networkPolicy *networkingv1.NetworkPolicy) error {
	actionDrop := v1alpha1.RuleActionDrop
	directionIn := v1alpha1.RuleDirectionIn
	directionOut := v1alpha1.RuleDirectionOut
	for _, policyType := range networkPolicy.Spec.PolicyTypes {
		if policyType == networkingv1.PolicyTypeIngress {
			// Generating ingress deny rule in isolation section.
			spIsolation.Spec.Rules = append(spIsolation.Spec.Rules, v1alpha1.SecurityPolicyRule{
				Action:    &actionDrop,
				Direction: &directionIn,
				Name:      "ingress-isolation",
			})
		} else if policyType == networkingv1.PolicyTypeEgress {
			// Generating egress deny rule in isolation section.
			spIsolation.Spec.Rules = append(spIsolation.Spec.Rules, v1alpha1.SecurityPolicyRule{
				Action:    &actionDrop,
				Direction: &directionOut,
				Name:      "egress-isolation",
			})
		} else {
			// This logic branch is impossible, leave it just for following the coding rules.
			return fmt.Errorf("invalid network policy type %s", policyType)
		}
	}
	return nil
}

func (service *SecurityPolicyService) generateSectionForNetworkPolicy(networkPolicy *networkingv1.NetworkPolicy, sectionType string) (*v1alpha1.SecurityPolicy, error) {
	name := service.BuildNetworkPolicyAllowPolicyName(networkPolicy.Name)
	uid := types.UID(service.BuildNetworkPolicyAllowPolicyID(string(networkPolicy.UID)))
	priority := common.PriorityNetworkPolicyAllowRule
	if sectionType == "isolation" {
		name = service.BuildNetworkPolicyIsolationPolicyName(networkPolicy.Name)
		uid = types.UID(service.BuildNetworkPolicyIsolationPolicyID(string(networkPolicy.UID)))
		priority = common.PriorityNetworkPolicyIsolationRule
	}
	section := &v1alpha1.SecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: networkPolicy.Namespace,
			Name:      name,
			UID:       uid,
		},
		Spec: v1alpha1.SecurityPolicySpec{
			Priority: priority,
			AppliedTo: []v1alpha1.SecurityPolicyTarget{
				{
					PodSelector: &networkPolicy.Spec.PodSelector,
				},
			},
		},
	}
	return section, nil
}

func (service *SecurityPolicyService) convertNetworkPolicyToInternalSecurityPolicies(networkPolicy *networkingv1.NetworkPolicy) ([]*v1alpha1.SecurityPolicy, error) {
	securityPolicies := []*v1alpha1.SecurityPolicy{}

	// Generating allow section.
	spAllow, err := service.generateSectionForNetworkPolicy(networkPolicy, "allow")
	if err != nil {
		return nil, err
	}
	err = service.populateRulesForAllowSection(spAllow, networkPolicy)
	if err != nil {
		return nil, err
	}

	// Generating isolation section.
	spIsolation, err := service.generateSectionForNetworkPolicy(networkPolicy, "isolation")
	if err != nil {
		return nil, err
	}
	err = service.populateRulesForIsolationSection(spIsolation, networkPolicy)
	if err != nil {
		return nil, err
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

func (service *SecurityPolicyService) getFinalSecurityPolicyResource(obj *v1alpha1.SecurityPolicy, createdFor string) (*model.SecurityPolicy, []model.Group, *[]ProjectShare, bool, error) {
	securityPolicyStore, ruleStore, groupStore, _, _ := service.getStores()

	nsxSecurityPolicy, nsxGroups, projectShares, err := service.buildSecurityPolicy(obj, createdFor)
	if err != nil {
		log.Error(err, "failed to build SecurityPolicy from CR", "ID", obj.UID)
		return nil, nil, nil, false, err
	}

	if len(nsxSecurityPolicy.Scope) == 0 {
		log.Info("SecurityPolicy has empty policy-level appliedTo")
	}
	indexScope := common.TagValueScopeSecurityPolicyUID
	if createdFor == common.ResourceTypeNetworkPolicy {
		indexScope = common.TagScopeNetworkPolicyUID
	}

	existingSecurityPolicy := securityPolicyStore.GetByKey(*nsxSecurityPolicy.Id)
	isChanged := true
	if existingSecurityPolicy != nil {
		isChanged = common.CompareResource(SecurityPolicyPtrToComparable(existingSecurityPolicy), SecurityPolicyPtrToComparable(nsxSecurityPolicy))
	}
	var finalSecurityPolicy *model.SecurityPolicy
	if isChanged {
		finalSecurityPolicy = nsxSecurityPolicy
	} else {
		finalSecurityPolicy = existingSecurityPolicy
	}

	existingRules := ruleStore.GetByIndex(indexScope, string(obj.UID))
	finalRules := service.updateRules(existingRules, nsxSecurityPolicy.Rules)
	finalSecurityPolicy.Rules = finalRules

	existingGroups := groupStore.GetByIndex(indexScope, string(obj.UID))
	finalGroups := service.updateGroups(existingGroups, *nsxGroups)

	if isVpcEnabled(service) {
		return finalSecurityPolicy, finalGroups, projectShares, isChanged, nil
	} else {
		return finalSecurityPolicy, finalGroups, nil, isChanged, nil
	}
}

func (service *SecurityPolicyService) createOrUpdateSecurityPolicy(obj *v1alpha1.SecurityPolicy, createdFor string) error {
	finalSecurityPolicy, finalGroups, _, isChanged, err := service.getFinalSecurityPolicyResource(obj, createdFor)
	if err != nil {
		log.Error(err, "failed to get FinalSecurityPolicy from CR", "ID", obj.UID)
		return err
	}

	// WrapHighLevelSecurityPolicy will modify the input security policy, so we need to make a copy for the following store update.
	finalSecurityPolicyCopy := *finalSecurityPolicy

	if !isChanged && len(finalSecurityPolicy.Rules) == 0 && len(finalGroups) == 0 {
		log.Info("securityPolicy, rules, groups are not changed, skip updating them", "nsxSecurityPolicyId", finalSecurityPolicy.Id)
		return nil
	}

	infraSecurityPolicy, err := service.WrapHierarchySecurityPolicy(finalSecurityPolicy, finalGroups)
	if err != nil {
		log.Error(err, "failed to wrap SecurityPolicy", "nsxSecurityPolicyId", finalSecurityPolicy.Id)
		return err
	}
	err = service.NSXClient.InfraClient.Patch(*infraSecurityPolicy, &EnforceRevisionCheckParam)
	err = nsxutil.NSXApiError(err)
	if err != nil {
		log.Error(err, "failed to create or update SecurityPolicy", "nsxSecurityPolicyId", finalSecurityPolicy.Id)
		return err
	}
	// Get SecurityPolicy from NSX after HAPI call as NSX renders several fields like `path`/`parent_path`.
	finalSecurityPolicyCopy, err = service.NSXClient.SecurityClient.Get(getDomain(service), *finalSecurityPolicy.Id)
	err = nsxutil.NSXApiError(err)
	if err != nil {
		log.Error(err, "failed to get SecurityPolicy", "nsxSecurityPolicyId", finalSecurityPolicy.Id)
		return err
	}

	securityPolicyStore, ruleStore, groupStore, _, _ := service.getStores()
	// The steps below know how to deal with NSX resources, if there is MarkedForDelete, then delete it from store,
	// otherwise add or update it to store.
	if isChanged {
		err = securityPolicyStore.Apply(&finalSecurityPolicyCopy)
		if err != nil {
			log.Error(err, "failed to apply store", "securityPolicy", finalSecurityPolicyCopy)
			return err
		}
	}
	err = ruleStore.Apply(&finalSecurityPolicyCopy)
	if err != nil {
		log.Error(err, "failed to apply store", "nsxRules", finalSecurityPolicyCopy.Rules)
		return err
	}
	err = groupStore.Apply(&finalGroups)
	if err != nil {
		log.Error(err, "failed to apply store", "nsxGroups", finalGroups)
		return err
	}
	log.Info("successfully created or updated NSX SecurityPolicy", "nsxSecurityPolicy", finalSecurityPolicyCopy)
	return nil
}

func (service *SecurityPolicyService) createOrUpdateVPCSecurityPolicy(obj *v1alpha1.SecurityPolicy, createdFor string) error {
	vpcInfo, err := service.getVpcInfo(obj.ObjectMeta.Namespace)
	if err != nil {
		return err
	}

	finalSecurityPolicy, finalGroups, projectShares, isChanged, err := service.getFinalSecurityPolicyResource(obj, createdFor)
	if err != nil {
		log.Error(err, "failed to get FinalSecurityPolicy from CR", "ID", obj.UID)
		return err
	}

	securityPolicyStore, ruleStore, groupStore, projectGroupStore, shareStore := service.getStores()
	indexScope := common.TagValueScopeSecurityPolicyUID
	if createdFor == common.ResourceTypeNetworkPolicy {
		indexScope = common.TagScopeNetworkPolicyUID
	}

	// WrapHierarchyVpcSecurityPolicy will modify the input security policy, so we need to make a copy for the following store update.
	finalSecurityPolicyCopy := *finalSecurityPolicy

	nsxProjectGroups := make([]model.Group, 0)
	nsxProjectShares := make([]model.Share, 0)
	for i := len(*projectShares) - 1; i >= 0; i-- {
		nsxProjectGroups = append(nsxProjectGroups, *((*projectShares)[i].shareGroup))
		nsxProjectShares = append(nsxProjectShares, *((*projectShares)[i].share))
	}

	// Create/Update nsx project shares and nsx project level groups
	existingNsxProjectGroups := projectGroupStore.GetByIndex(indexScope, string(obj.UID))
	finalProjectGroups := service.updateGroups(existingNsxProjectGroups, nsxProjectGroups)

	existingNsxProjectShares := shareStore.GetByIndex(indexScope, string(obj.UID))
	finalProjectShares := service.updateShares(existingNsxProjectShares, nsxProjectShares)

	if !isChanged && len(finalSecurityPolicy.Rules) == 0 && len(finalGroups) == 0 && len(finalProjectGroups) == 0 && len(finalProjectShares) == 0 {
		log.Info("securityPolicy, rules, groups and shares are not changed, skip updating them", "nsxSecurityPolicyId", finalSecurityPolicy.Id)
		return nil
	}

	// TODO: Simplify resource wrap and patch for both create/delete.
	// 1.Wrap project groups and shares into project child infra.
	var projectInfra []*data.StructValue
	if len(finalProjectGroups) != 0 || len(finalProjectShares) != 0 {
		projectInfra, err = service.wrapHierarchyProjectResources(finalProjectShares, finalProjectGroups)
		if err != nil {
			log.Error(err, "failed to wrap project groups and shares", "nsxSecurityPolicyId", finalSecurityPolicy.Id)
			return err
		}
	}

	// 2.Wrap SecurityPolicy, groups, rules under VPC level together with project groups and shares into one hierarchy resource tree.
	orgRoot, err := service.WrapHierarchyVpcSecurityPolicy(&finalSecurityPolicyCopy, finalGroups, projectInfra, vpcInfo)
	if err != nil {
		log.Error(err, "failed to wrap SecurityPolicy in VPC", "nsxSecurityPolicyId", finalSecurityPolicy.Id)
		return err
	}

	// 3.Create/update SecurityPolicy together with groups, rules under VPC level and project groups, shares.
	err = service.NSXClient.OrgRootClient.Patch(*orgRoot, &EnforceRevisionCheckParam)
	err = nsxutil.NSXApiError(err)
	if err != nil {
		log.Error(err, "failed to create or update SecurityPolicy in VPC", "nsxSecurityPolicyId", finalSecurityPolicy.Id)
		return err
	}

	// Get SecurityPolicy from NSX after HAPI call as NSX renders several fields like `path`/`parent_path`.
	finalSecurityPolicyCopy, err = service.NSXClient.VPCSecurityClient.Get(vpcInfo.OrgID, vpcInfo.ProjectID, vpcInfo.VPCID, *finalSecurityPolicyCopy.Id)
	err = nsxutil.NSXApiError(err)
	if err != nil {
		log.Error(err, "failed to get SecurityPolicy in VPC", "nsxSecurityPolicyId", finalSecurityPolicy.Id)
		return err
	}

	// TODO: Simplify resource store update for both create/delete.
	// The steps below know how to deal with NSX resources, if there is MarkedForDelete, then delete it from store,
	// otherwise add or update it to store.
	if isChanged {
		err = securityPolicyStore.Apply(&finalSecurityPolicyCopy)
		if err != nil {
			log.Error(err, "failed to apply store", "securityPolicy", finalSecurityPolicyCopy)
			return err
		}
	}

	err = ruleStore.Apply(&finalSecurityPolicyCopy)
	if err != nil {
		log.Error(err, "failed to apply store", "nsxRules", finalSecurityPolicyCopy.Rules)
		return err
	}
	err = groupStore.Apply(&finalGroups)
	if err != nil {
		log.Error(err, "failed to apply store", "nsxGroups", finalGroups)
		return err
	}
	err = projectGroupStore.Apply(&finalProjectGroups)
	if err != nil {
		log.Error(err, "failed to apply store", "nsxProjectGroups", finalProjectGroups)
		return err
	}
	err = shareStore.Apply(&finalProjectShares)
	if err != nil {
		log.Error(err, "failed to apply store", "nsxProjectShares", finalProjectShares)
		return err
	}

	log.Info("successfully created or updated NSX SecurityPolicy in VPC", "nsxSecurityPolicy", finalSecurityPolicyCopy)
	return nil
}

func (service *SecurityPolicyService) DeleteSecurityPolicy(obj interface{}, isVpcCleanup bool, createdFor string) error {
	var err error
	switch sp := obj.(type) {
	case *networkingv1.NetworkPolicy:
		CRPolicySet := sets.New[string]()
		CRPolicySet.Insert(service.BuildNetworkPolicyAllowPolicyID(string(sp.UID)))
		CRPolicySet.Insert(service.BuildNetworkPolicyIsolationPolicyID(string(sp.UID)))
		for elem := range CRPolicySet {
			err = service.deleteVPCSecurityPolicy(types.UID(elem), createdFor)
		}
	case types.UID:
		if isVpcEnabled(service) || isVpcCleanup {
			err = service.deleteVPCSecurityPolicy(sp, createdFor)
		} else {
			// For T1 network SecurityPolicy deletion
			err = service.deleteSecurityPolicy(sp)
		}
	}
	return err
}

func (service *SecurityPolicyService) deleteSecurityPolicy(sp types.UID) error {
	var nsxSecurityPolicy *model.SecurityPolicy
	var err error
	g := make([]model.Group, 0)
	nsxGroups := &g
	r := make([]model.Rule, 0)
	nsxRules := &r
	securityPolicyStore, ruleStore, groupStore, _, _ := service.getStores()

	// For normal SecurityPolicy deletion process, which means that SecurityPolicy has corresponding nsx SecurityPolicy object
	// And for SecurityPolicy GC or cleanup process, which means that SecurityPolicy doesn't exist in K8s any more
	// but still has corresponding nsx SecurityPolicy object.
	// We use SecurityPolicy's UID from store to get NSX SecurityPolicy object
	indexScope := common.TagValueScopeSecurityPolicyUID
	existingSecurityPolices := securityPolicyStore.GetByIndex(indexScope, string(sp))
	if len(existingSecurityPolices) == 0 {
		log.Info("NSX security policy is not found in store, skip deleting it", "nsxSecurityPolicyUID", sp)
		return nil
	}
	nsxSecurityPolicy = existingSecurityPolices[0]
	if nsxSecurityPolicy.Path == nil {
		err = errors.New("nsxSecurityPolicy path is empty")
		log.Error(err, "failed to delete SecurityPolicy", "nsxSecurityPolicyUID", sp)
		return err
	}

	nsxSecurityPolicy.MarkedForDelete = &MarkedForDelete

	// There is no nsx groups/rules in the security policy retrieved from securityPolicy store.
	// The groups/rules associated the deleting security policy can only be gotten from group/rule store.
	existingGroups := groupStore.GetByIndex(indexScope, string(sp))
	service.markDeleteGroups(existingGroups, nsxGroups, sp)

	existingRules := ruleStore.GetByIndex(indexScope, string(sp))
	service.markDeleteRules(existingRules, nsxRules, sp)
	nsxSecurityPolicy.Rules = *nsxRules

	// WrapHighLevelSecurityPolicy will modify the input security policy, so we need to make a copy for the following store update.
	finalSecurityPolicyCopy := *nsxSecurityPolicy
	finalSecurityPolicyCopy.Rules = nsxSecurityPolicy.Rules

	infraSecurityPolicy, err := service.WrapHierarchySecurityPolicy(nsxSecurityPolicy, *nsxGroups)
	if err != nil {
		log.Error(err, "failed to wrap SecurityPolicy", "nsxSecurityPolicyId", nsxSecurityPolicy.Id)
		return err
	}
	err = service.NSXClient.InfraClient.Patch(*infraSecurityPolicy, &EnforceRevisionCheckParam)
	err = nsxutil.NSXApiError(err)
	if err != nil {
		log.Error(err, "failed to delete SecurityPolicy", "nsxSecurityPolicyId", nsxSecurityPolicy.Id)
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

	log.Info("successfully deleted NSX SecurityPolicy", "nsxSecurityPolicy", finalSecurityPolicyCopy)
	return nil
}

func (service *SecurityPolicyService) deleteVPCSecurityPolicy(sp types.UID, createdFor string) error {
	var nsxSecurityPolicy *model.SecurityPolicy
	var err error
	g := make([]model.Group, 0)
	nsxGroups := &g
	r := make([]model.Rule, 0)
	nsxRules := &r
	g1 := make([]model.Group, 0)
	s := make([]model.Share, 0)
	nsxProjectGroups := &g1
	nsxProjectShares := &s
	securityPolicyStore, ruleStore, groupStore, projectGroupStore, shareStore := service.getStores()

	// For normal SecurityPolicy deletion process, which means that SecurityPolicy has corresponding nsx SecurityPolicy object
	// And for SecurityPolicy GC or cleanup process, which means that SecurityPolicy doesn't exist in K8s any more
	// but still has corresponding nsx SecurityPolicy object.
	// We use SecurityPolicy's UID from store to get NSX SecurityPolicy object
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

	// vpcInfo should be listed directly from security policy store to avoid calling VPC service.
	// Get orgId, projectId, vpcId from security policy path "/orgs/<orgId>/projects/<projectId>/vpcs/<vpcId>/security-policies/<spId>"
	if nsxSecurityPolicy.Path == nil {
		err = errors.New("nsxSecurityPolicy path is empty")
		log.Error(err, "failed to delete SecurityPolicy in VPC", "nsxSecurityPolicyUID", sp)
		return err
	}
	vpcInfo, _ := common.ParseVPCResourcePath(*(nsxSecurityPolicy.Path))

	nsxSecurityPolicy.MarkedForDelete = &MarkedForDelete

	// There is no nsx groups/rules in the security policy retrieved from securityPolicy store.
	// The groups/rules associated the deleting security policy can only be gotten from group/rule store.
	existingGroups := groupStore.GetByIndex(indexScope, string(sp))
	service.markDeleteGroups(existingGroups, nsxGroups, sp)

	existingRules := ruleStore.GetByIndex(indexScope, string(sp))
	service.markDeleteRules(existingRules, nsxRules, sp)
	nsxSecurityPolicy.Rules = *nsxRules

	existingNsxProjectGroups := projectGroupStore.GetByIndex(indexScope, string(sp))
	service.markDeleteGroups(existingNsxProjectGroups, nsxProjectGroups, sp)

	existingNsxProjectShares := shareStore.GetByIndex(indexScope, string(sp))
	service.markDeleteShares(existingNsxProjectShares, nsxProjectShares, sp)

	// WrapHierarchyVpcSecurityPolicy will modify the input security policy, so we need to make a copy for the following store update.
	finalSecurityPolicyCopy := *nsxSecurityPolicy
	finalSecurityPolicyCopy.Rules = nsxSecurityPolicy.Rules

	// 1.Wrap project groups and shares into project child infra.
	var projectInfra []*data.StructValue
	if len(*nsxProjectShares) != 0 || len(*nsxProjectGroups) != 0 {
		projectInfra, err = service.wrapHierarchyProjectResources(*nsxProjectShares, *nsxProjectGroups)
		if err != nil {
			log.Error(err, "failed to wrap project groups and shares", "nsxSecurityPolicyId", nsxSecurityPolicy.Id)
			return err
		}
	}

	// 2.Wrap SecurityPolicy, groups, rules under VPC level together with project groups and shares into one hierarchy resource tree.
	orgRoot, err := service.WrapHierarchyVpcSecurityPolicy(nsxSecurityPolicy, *nsxGroups, projectInfra, &vpcInfo)
	if err != nil {
		log.Error(err, "failed to wrap SecurityPolicy in VPC", "nsxSecurityPolicyId", nsxSecurityPolicy.Id)
		return err
	}

	// 3.Create/update SecurityPolicy together with groups, rules under VPC level and project groups, shares.
	err = service.NSXClient.OrgRootClient.Patch(*orgRoot, &EnforceRevisionCheckParam)
	err = nsxutil.NSXApiError(err)
	if err != nil {
		log.Error(err, "failed to delete SecurityPolicy in VPC", "nsxSecurityPolicyId", nsxSecurityPolicy.Id)
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
	err = projectGroupStore.Apply(nsxProjectGroups)
	if err != nil {
		log.Error(err, "failed to apply store", "nsxProjectGroups", nsxProjectGroups)
		return err
	}
	err = shareStore.Apply(nsxProjectShares)
	if err != nil {
		log.Error(err, "failed to apply store", "nsxProjectShares", nsxProjectShares)
		return err
	}

	log.Info("successfully deleted NSX SecurityPolicy in VPC", "nsxSecurityPolicy", finalSecurityPolicyCopy)
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
			err = nsxutil.NSXApiError(err)
		} else {
			err = service.NSXClient.GroupClient.Patch(getDomain(service), *group.Id, *group)
			err = nsxutil.NSXApiError(err)
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

func (service *SecurityPolicyService) updateRules(existingRules []*model.Rule, expectedRules []model.Rule) []model.Rule {
	changed, stale := common.CompareResources(RulesPtrToComparable(existingRules), RulesToComparable(expectedRules))
	changedRules, staleRules := ComparableToRules(changed), ComparableToRules(stale)
	finalRules := make([]model.Rule, 0)
	for i := len(staleRules) - 1; i >= 0; i-- { // Don't use range, it would copy the element
		staleRules[i].MarkedForDelete = &MarkedForDelete // nsx clients need this field to delete the rules
	}
	finalRules = append(finalRules, staleRules...)
	finalRules = append(finalRules, changedRules...)
	return finalRules
}

func (service *SecurityPolicyService) updateGroups(existingGroups []*model.Group, expectedGroups []model.Group) []model.Group {
	changed, stale := common.CompareResources(GroupsPtrToComparable(existingGroups), GroupsToComparable(expectedGroups))
	changedGroups, staleGroups := ComparableToGroups(changed), ComparableToGroups(stale)
	finalGroups := make([]model.Group, 0)
	for i := len(staleGroups) - 1; i >= 0; i-- {
		staleGroups[i].MarkedForDelete = &MarkedForDelete
	}
	finalGroups = append(finalGroups, staleGroups...)
	finalGroups = append(finalGroups, changedGroups...)
	return finalGroups
}

func (service *SecurityPolicyService) updateShares(existingShares []*model.Share, expectedShares []model.Share) []model.Share {
	changed, stale := common.CompareResources(SharesPtrToComparable(existingShares), SharesToComparable(expectedShares))
	changedShares, staleShares := ComparableToShares(changed), ComparableToShares(stale)
	for i := len(staleShares) - 1; i >= 0; i-- {
		staleShares[i].MarkedForDelete = &MarkedForDelete
	}
	finalShares := make([]model.Share, 0)
	finalShares = append(finalShares, staleShares...)
	finalShares = append(finalShares, changedShares...)
	return finalShares
}

func (service *SecurityPolicyService) markDeleteGroups(existingGroups []*model.Group, deleteGroups *[]model.Group, sp types.UID) {
	if len(existingGroups) == 0 {
		log.Info("did not get groups with SecurityPolicy index", "securityPolicyUID", string(sp))
		return
	}
	for _, group := range existingGroups {
		*deleteGroups = append(*deleteGroups, *group)
	}
	for i := len(*deleteGroups) - 1; i >= 0; i-- {
		(*deleteGroups)[i].MarkedForDelete = &MarkedForDelete
	}
}

func (service *SecurityPolicyService) markDeleteRules(existingRules []*model.Rule, deleteRules *[]model.Rule, sp types.UID) {
	if len(existingRules) == 0 {
		log.Info("did not get rules with SecurityPolicy index", "securityPolicyUID", string(sp))
		return
	}
	for _, rule := range existingRules {
		*deleteRules = append(*deleteRules, *rule)
	}
	for i := len(*deleteRules) - 1; i >= 0; i-- {
		(*deleteRules)[i].MarkedForDelete = &MarkedForDelete
	}
}

func (service *SecurityPolicyService) markDeleteShares(existingShares []*model.Share, deleteShares *[]model.Share, sp types.UID) {
	if len(existingShares) == 0 {
		log.Info("did not get shares with SecurityPolicy index", "securityPolicyUID", string(sp))
		return
	}
	for _, share := range existingShares {
		*deleteShares = append(*deleteShares, *share)
	}
	for i := len(*deleteShares) - 1; i >= 0; i-- {
		(*deleteShares)[i].MarkedForDelete = &MarkedForDelete
	}
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
