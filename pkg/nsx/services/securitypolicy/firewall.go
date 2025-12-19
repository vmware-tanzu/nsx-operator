/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package securitypolicy

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/realizestate"
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
	infraGroupStore     *GroupStore
	infraShareStore     *ShareStore
	projectGroupStore   *GroupStore
	projectShareStore   *ShareStore
	vpcService          common.VPCServiceProvider

	securityPolicyBuilder *common.PolicyTreeBuilder[*model.SecurityPolicy]
	ruleBuilder           *common.PolicyTreeBuilder[*model.Rule]
	groupBuilder          *common.PolicyTreeBuilder[*model.Group]
	infraGroupBuilder     *common.PolicyTreeBuilder[*model.Group]
	projectGroupBuilder   *common.PolicyTreeBuilder[*model.Group]
	infraShareBuilder     *common.PolicyTreeBuilder[*model.Share]
	projectShareBuilder   *common.PolicyTreeBuilder[*model.Share]
}

type GroupShare struct {
	shareGroup *model.Group
	share      *model.Share
}

var (
	securityService *SecurityPolicyService
	lock            = &sync.Mutex{}
)

// GetSecurityService get singleton SecurityPolicy Service instance, NetworkPolicy/SecurityPolicy controller share the same instance.
func GetSecurityService(service common.Service, vpcService common.VPCServiceProvider) *SecurityPolicyService {
	if securityService == nil {
		lock.Lock()
		defer lock.Unlock()
		if securityService == nil {
			var err error
			if securityService, err = InitializeSecurityPolicy(service, vpcService, false); err != nil {
				log.Error(err, "Failed to initialize SecurityPolicy service")
				os.Exit(1)
			}
		}
	}
	return securityService
}

// InitializeSecurityPolicy sync NSX resources
func InitializeSecurityPolicy(service common.Service, vpcService common.VPCServiceProvider, forCleanUp bool) (*SecurityPolicyService, error) {
	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)

	wg.Add(7)

	securityPolicyService := &SecurityPolicyService{
		Service: service,
	}

	if forCleanUp {
		securityPolicyService.securityPolicyBuilder, _ = common.PolicyPathVpcSecurityPolicy.NewPolicyTreeBuilder()
		securityPolicyService.ruleBuilder, _ = common.PolicyPathVpcSecurityPolicyRule.NewPolicyTreeBuilder()
		securityPolicyService.groupBuilder, _ = common.PolicyPathVpcGroup.NewPolicyTreeBuilder()
		securityPolicyService.infraShareBuilder, _ = common.PolicyPathInfraShare.NewPolicyTreeBuilder()
		securityPolicyService.projectShareBuilder, _ = common.PolicyPathProjectShare.NewPolicyTreeBuilder()
		securityPolicyService.projectGroupBuilder, _ = common.PolicyPathProjectGroup.NewPolicyTreeBuilder()
		securityPolicyService.infraGroupBuilder, _ = common.PolicyPathInfraGroup.NewPolicyTreeBuilder()
	}

	if IsVPCEnabled(securityPolicyService) {
		common.TagValueScopeSecurityPolicyName = common.TagScopeSecurityPolicyName
		common.TagValueScopeSecurityPolicyUID = common.TagScopeSecurityPolicyUID
	}
	indexScope := common.TagValueScopeSecurityPolicyUID
	securityPolicyService.setUpStore(indexScope, forCleanUp)
	securityPolicyService.vpcService = vpcService

	infraShareTag := []model.Tag{
		{
			Scope: String(common.TagScopeNSXShareCreatedFor),
			Tag:   String(common.TagValueShareCreatedForInfra),
		},
	}
	projectShareTag := []model.Tag{
		{
			Scope: String(common.TagScopeNSXShareCreatedFor),
			Tag:   String(common.TagValueShareCreatedForProject),
		},
	}
	notShareTag := []model.Tag{
		{
			Scope: String(common.TagScopeNSXShareCreatedFor),
			Tag:   String(common.TagValueShareNotCreated),
		},
	}

	go securityPolicyService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeGroup, infraShareTag, securityPolicyService.infraGroupStore)
	go securityPolicyService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeShare, infraShareTag, securityPolicyService.infraShareStore)
	go securityPolicyService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeGroup, projectShareTag, securityPolicyService.projectGroupStore)
	go securityPolicyService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeShare, projectShareTag, securityPolicyService.projectShareStore)

	if IsVPCEnabled(securityPolicyService) {
		go securityPolicyService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeGroup, notShareTag, securityPolicyService.groupStore)
	} else {
		go securityPolicyService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeGroup, nil, securityPolicyService.groupStore)
	}
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

func (s *SecurityPolicyService) setUpStore(indexScope string, indexWithVPCPath bool) {
	vpcResourceIndexWrapper := func(indexers cache.Indexers) cache.Indexers {
		indexers[indexScope] = indexBySecurityPolicyUID
		indexers[common.TagScopeNetworkPolicyUID] = indexByNetworkPolicyUID
		// Note: we can't use indexer `common.IndexByVPCPathFuncKey` with group/rule stores by default because the
		// caller may not use the object read from NSX to apply on the store which is possibly not set with path or
		// the parent path. But for cleanup logic, indexWithVPCPath is always set true and the store is re-built from
		// the NSX resources but not from nsx-operator local calculation.
		if indexWithVPCPath {
			indexers[common.IndexByVPCPathFuncKey] = common.IndexByVPCFunc
		}
		return indexers
	}

	s.securityPolicyStore = &SecurityPolicyStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(
			keyFunc, vpcResourceIndexWrapper(cache.Indexers{
				common.TagScopeNamespace: indexBySecurityPolicyNamespace,
			})),
		BindingType: model.SecurityPolicyBindingType(),
	}}
	s.groupStore = &GroupStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, vpcResourceIndexWrapper(cache.Indexers{
			common.TagScopeRuleID: indexGroupFunc,
		})),
		BindingType: model.GroupBindingType(),
	}}
	s.ruleStore = &RuleStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, vpcResourceIndexWrapper(cache.Indexers{
			SPIndexByUUIDAndRuleHashFuncKey: indexSPByUUIDAndRuleHash,
			NPIndexByUUIDAndRuleHashFuncKey: indexNPByUUIDAndRuleHash,
			common.TagScopeRuleID:           indexRuleFunc,
		})),
		BindingType: model.RuleBindingType(),
	}}
	s.infraGroupStore = &GroupStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
			indexScope:                      indexBySecurityPolicyUID,
			common.TagScopeNetworkPolicyUID: indexByNetworkPolicyUID,
			common.TagScopeRuleID:           indexGroupFunc,
		}),
		BindingType: model.GroupBindingType(),
	}}
	s.infraShareStore = &ShareStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
			indexScope:                      indexBySecurityPolicyUID,
			common.TagScopeNetworkPolicyUID: indexByNetworkPolicyUID,
		}),
		BindingType: model.ShareBindingType(),
	}}
	s.projectGroupStore = &GroupStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
			indexScope:                      indexBySecurityPolicyUID,
			common.TagScopeNetworkPolicyUID: indexByNetworkPolicyUID,
			common.TagScopeRuleID:           indexGroupFunc,
		}),
		BindingType: model.GroupBindingType(),
	}}
	s.projectShareStore = &ShareStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
			indexScope:                      indexBySecurityPolicyUID,
			common.TagScopeNetworkPolicyUID: indexByNetworkPolicyUID,
		}),
		BindingType: model.ShareBindingType(),
	}}
}

func (service *SecurityPolicyService) CreateOrUpdateSecurityPolicy(obj interface{}) error {
	if !nsxutil.GetDFWLicense() {
		log.Warn("No DFW license, skip creating SecurityPolicy.")
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
		if IsVPCEnabled(service) {
			err = service.createOrUpdateVPCSecurityPolicy(obj.(*v1alpha1.SecurityPolicy), common.ResourceTypeSecurityPolicy)
		} else {
			// For T1 network SecurityPolicy create/update
			err = service.createOrUpdateT1SecurityPolicy(obj.(*v1alpha1.SecurityPolicy), common.ResourceTypeSecurityPolicy)
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
				Name:      strings.Join([]string{common.RuleIngress, common.RuleActionDrop}, common.ConnectorUnderline),
			})
		} else if policyType == networkingv1.PolicyTypeEgress {
			// Generating egress deny rule in isolation section.
			spIsolation.Spec.Rules = append(spIsolation.Spec.Rules, v1alpha1.SecurityPolicyRule{
				Action:    &actionDrop,
				Direction: &directionOut,
				Name:      strings.Join([]string{common.RuleEgress, common.RuleActionDrop}, common.ConnectorUnderline),
			})
		} else {
			// This logic branch is impossible, leave it just for following the coding rules.
			return &nsxutil.ValidationError{Desc: fmt.Sprintf("invalid network policy type %s", policyType)}
		}
	}
	return nil
}

func (service *SecurityPolicyService) generateSectionForNetworkPolicy(networkPolicy *networkingv1.NetworkPolicy, sectionType string) (*v1alpha1.SecurityPolicy, error) {
	name := networkPolicy.Name
	uid := types.UID(service.BuildNetworkPolicyAllowPolicyID(string(networkPolicy.UID)))
	priority := common.PriorityNetworkPolicyAllowRule
	if sectionType == common.RuleActionDrop {
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
	spAllow, err := service.generateSectionForNetworkPolicy(networkPolicy, common.RuleActionAllow)
	if err != nil {
		return nil, err
	}
	err = service.populateRulesForAllowSection(spAllow, networkPolicy)
	if err != nil {
		return nil, err
	}

	// Generating isolation section.
	spIsolation, err := service.generateSectionForNetworkPolicy(networkPolicy, common.RuleActionDrop)
	if err != nil {
		return nil, err
	}
	err = service.populateRulesForIsolationSection(spIsolation, networkPolicy)
	if err != nil {
		return nil, err
	}

	securityPolicies = append(securityPolicies, spAllow, spIsolation)
	log.Debug("Converted network policy to security policies", "securityPolicies", securityPolicies)
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
	err := &nsxutil.ValidationError{Desc: fmt.Sprintf("unsupported NetworkPolicyPeer: %s", npPeer)}
	return nil, err
}

func (service *SecurityPolicyService) convertNetworkPolicyPortToSecurityPolicyPort(npPort *networkingv1.NetworkPolicyPort) (*v1alpha1.SecurityPolicyPort, error) {
	spPort := &v1alpha1.SecurityPolicyPort{}
	if npPort.Protocol != nil {
		spPort.Protocol = *npPort.Protocol
	}

	if npPort.Port != nil {
		spPort.Port = *npPort.Port
	}
	if npPort.EndPort != nil {
		spPort.EndPort = int(*npPort.EndPort)
	}
	return spPort, nil
}

func (service *SecurityPolicyService) getSecurityPolicyResourceStores() (*SecurityPolicyStore, *RuleStore, *GroupStore) {
	return service.securityPolicyStore, service.ruleStore, service.groupStore
}

func (service *SecurityPolicyService) getVPCShareResourceStores() (*GroupStore, *ShareStore, *GroupStore, *ShareStore) {
	return service.infraGroupStore, service.infraShareStore, service.projectGroupStore, service.projectShareStore
}

func (service *SecurityPolicyService) getFinalVPCShareResources(obj *v1alpha1.SecurityPolicy, indexScope string, nsxGroupShares *[]GroupShare, isDefaultProject bool) ([]model.Share, []model.Group) {
	var finalShares []model.Share
	var finalShareGroups []model.Group
	nsxShares := make([]model.Share, 0)
	nsxShareGroups := make([]model.Group, 0)

	infraGroupStore, infraShareStore, projectGroupStore, projectShareStore := service.getVPCShareResourceStores()

	// Create/Update NSX shares and NSX share groups
	for i := len(*nsxGroupShares) - 1; i >= 0; i-- {
		nsxShareGroups = append(nsxShareGroups, *((*nsxGroupShares)[i].shareGroup))
		nsxShares = append(nsxShares, *((*nsxGroupShares)[i].share))
	}
	if isDefaultProject {
		existingNsxShareGroups := infraGroupStore.GetByIndex(indexScope, string(obj.UID))
		finalShareGroups = service.getUpdateGroups(existingNsxShareGroups, nsxShareGroups)

		existingNsxShares := infraShareStore.GetByIndex(indexScope, string(obj.UID))
		finalShares = service.getUpdateShares(existingNsxShares, nsxShares)
	} else {
		existingNsxShareGroups := projectGroupStore.GetByIndex(indexScope, string(obj.UID))
		finalShareGroups = service.getUpdateGroups(existingNsxShareGroups, nsxShareGroups)

		existingNsxShares := projectShareStore.GetByIndex(indexScope, string(obj.UID))
		finalShares = service.getUpdateShares(existingNsxShares, nsxShares)
	}

	return finalShares, finalShareGroups
}

func (service *SecurityPolicyService) getFinalSecurityPolicyResource(obj *v1alpha1.SecurityPolicy, createdFor string, vpcInfo *common.VPCResourceInfo, isDefaultProject bool) (*model.SecurityPolicy, []model.Group, []model.Share, []model.Group, bool, error) {
	securityPolicyStore, ruleStore, groupStore := service.getSecurityPolicyResourceStores()

	nsxSecurityPolicy, nsxGroups, nsxGroupShares, err := service.buildSecurityPolicy(obj, createdFor, vpcInfo, isDefaultProject)
	if err != nil {
		log.Error(err, "Failed to build SecurityPolicy from CR", "securityPolicyUID", obj.UID)
		return nil, nil, nil, nil, false, err
	}

	if len(nsxSecurityPolicy.Scope) == 0 {
		log.Info("SecurityPolicy has empty policy-level appliedTo field")
	}
	indexScope := common.TagValueScopeSecurityPolicyUID
	if createdFor == common.ResourceTypeNetworkPolicy {
		indexScope = common.TagScopeNetworkPolicyUID
	}

	existingSecurityPolicies := securityPolicyStore.GetByIndex(indexScope, string(obj.GetUID()))
	isChanged := true
	finalSecurityPolicy := nsxSecurityPolicy
	if len(existingSecurityPolicies) > 0 {
		existingSecurityPolicy := existingSecurityPolicies[0]
		isChanged = common.CompareResource(SecurityPolicyPtrToComparable(existingSecurityPolicy), SecurityPolicyPtrToComparable(nsxSecurityPolicy))
		if !isChanged {
			finalSecurityPolicy = existingSecurityPolicy
		}
	}

	existingRules := ruleStore.GetByIndex(indexScope, string(obj.UID))
	finalRules := service.getUpdateRules(existingRules, nsxSecurityPolicy.Rules)
	finalSecurityPolicy.Rules = finalRules

	existingGroups := groupStore.GetByIndex(indexScope, string(obj.UID))
	finalGroups := service.getUpdateGroups(existingGroups, *nsxGroups)

	if IsVPCEnabled(service) {
		finalShares, finalShareGroups := service.getFinalVPCShareResources(obj, indexScope, nsxGroupShares, isDefaultProject)
		return finalSecurityPolicy, finalGroups, finalShares, finalShareGroups, isChanged, nil
	} else {
		return finalSecurityPolicy, finalGroups, nil, nil, isChanged, nil
	}
}

func (service *SecurityPolicyService) createOrUpdateT1SecurityPolicy(obj *v1alpha1.SecurityPolicy, createdFor string) error {
	finalSecurityPolicy, finalGroups, _, _, isChanged, err := service.getFinalSecurityPolicyResource(obj, createdFor, nil, false)
	if err != nil {
		log.Error(err, "Failed to get SecurityPolicy resources from CR", "securityPolicyUID", obj.UID)
		return err
	}

	// WrapHierarchySecurityPolicy will modify the input security policy rules and move the rules to Children fields for HAPI wrap,
	// so we need to make a copy for the rules store update.
	finalRules := finalSecurityPolicy.Rules

	if !isChanged && len(finalSecurityPolicy.Rules) == 0 && len(finalGroups) == 0 {
		log.Info("SecurityPolicy, rules, groups are not changed, skip updating them", "nsxSecurityPolicyId", finalSecurityPolicy.Id)
		return nil
	}

	infraSecurityPolicy, err := service.WrapHierarchySecurityPolicy(finalSecurityPolicy, finalGroups)
	if err != nil {
		log.Error(err, "Failed to wrap SecurityPolicy", "nsxSecurityPolicyId", finalSecurityPolicy.Id)
		return err
	}
	err = service.NSXClient.InfraClient.Patch(*infraSecurityPolicy, &EnforceRevisionCheckParam)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "Failed to create or update SecurityPolicy", "nsxSecurityPolicyId", finalSecurityPolicy.Id)
		return err
	}
	// Get SecurityPolicy from NSX after HAPI call as NSX renders several fields like `path`/`parent_path`.
	finalGetNSXSecurityPolicy, err := service.NSXClient.SecurityClient.Get(getDomain(service), *finalSecurityPolicy.Id)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "Failed to get SecurityPolicy", "nsxSecurityPolicyId", finalSecurityPolicy.Id)
		return err
	}

	securityPolicyStore, ruleStore, groupStore := service.getSecurityPolicyResourceStores()
	// The steps below know how to deal with NSX resources, if there is MarkedForDelete, then delete it from store,
	// otherwise add or update it to store.
	if isChanged {
		err = securityPolicyStore.Apply(&finalGetNSXSecurityPolicy)
		if err != nil {
			log.Error(err, "Failed to apply store", "securityPolicy", finalGetNSXSecurityPolicy)
			return err
		}
	}
	err = ruleStore.Apply(&finalRules)
	if err != nil {
		log.Error(err, "Failed to apply store", "nsxRules", finalRules)
		return err
	}
	err = groupStore.Apply(&finalGroups)
	if err != nil {
		log.Error(err, "Failed to apply store", "nsxGroups", finalGroups)
		return err
	}
	log.Info("Successfully created or updated NSX SecurityPolicy", "nsxSecurityPolicy", finalGetNSXSecurityPolicy)
	return nil
}

func (service *SecurityPolicyService) createOrUpdateVPCSecurityPolicy(obj *v1alpha1.SecurityPolicy, createdFor string) error {
	var err error
	var finalGetNSXSecurityPolicy *model.SecurityPolicy

	vpcInfo, err := service.getVPCInfo(obj.ObjectMeta.Namespace)
	if err != nil {
		return err
	}
	isDefaultProject, err := service.vpcService.IsDefaultNSXProject(vpcInfo.OrgID, vpcInfo.ProjectID)
	if err != nil {
		log.Error(err, "Failed to check if NSX Project is default", "nsxProjectID", vpcInfo.ProjectID)
		return err
	}

	finalSecurityPolicy, finalGroups, finalShares, finalShareGroups, isChanged, err := service.getFinalSecurityPolicyResource(obj, createdFor, vpcInfo, isDefaultProject)
	if err != nil {
		log.Error(err, "Failed to get SecurityPolicy resources from CR", "securityPolicyUID", obj.UID)
		return err
	}

	// WrapHierarchyVpcSecurityPolicy will modify the input security policy rules and move the rules to Children fields for HAPI wrap,
	// so we need to make a copy for the rules store update.
	finalRules := finalSecurityPolicy.Rules

	if !isChanged && len(finalSecurityPolicy.Rules) == 0 && len(finalGroups) == 0 && len(finalShares) == 0 {
		log.Info("SecurityPolicy, rules, groups and shares are not changed, skip updating them", "nsxSecurityPolicyId", finalSecurityPolicy.Id)
		return nil
	}
	if !isDefaultProject {
		finalGetNSXSecurityPolicy, err = service.createOrUpdateNSXSecurityPolicy(finalSecurityPolicy, finalGroups, finalShares, finalShareGroups, vpcInfo)
	} else {
		finalGetNSXSecurityPolicy, err = service.createOrUpdateNSXSecurityPolicyForDefaultProject(finalSecurityPolicy, finalGroups, finalShares, finalShareGroups, vpcInfo)
	}
	if err != nil {
		return err
	}

	err = service.applySecurityPolicyStore(finalGetNSXSecurityPolicy, finalRules, isChanged)
	if err != nil {
		return err
	}
	err = service.applyVPCGroupShareStore(finalGroups, finalShares, finalShareGroups, isDefaultProject)
	if err != nil {
		return err
	}

	log.Info("Successfully created or updated NSX SecurityPolicy resources in VPC", "nsxSecurityPolicy", *finalGetNSXSecurityPolicy)
	return nil
}

func (service *SecurityPolicyService) DeleteSecurityPolicy(spUid types.UID, isGC bool, createdFor string) error {
	var err error
	// For VPC network, SecurityPolicy normal deletion, GC deletion and cleanup
	if IsVPCEnabled(service) {
		err = service.deleteVPCSecurityPolicy(spUid, isGC, createdFor)
	} else {
		// For T1 network, SecurityPolicy normal deletion and GC deletion
		err = service.deleteT1SecurityPolicy(spUid)
	}
	return err
}

func (service *SecurityPolicyService) deleteT1SecurityPolicy(spUid types.UID) error {
	var nsxSecurityPolicy *model.SecurityPolicy
	var err error

	securityPolicyStore, ruleStore, groupStore := service.getSecurityPolicyResourceStores()

	// For normal SecurityPolicy deletion process, which means that SecurityPolicy has corresponding NSX SecurityPolicy object.
	// And for SecurityPolicy GC or cleanup process, which means that SecurityPolicy doesn't exist in K8s any more,
	// but still has corresponding NSX SecurityPolicy object.
	// Using SecurityPolicy's UID from store to get NSX SecurityPolicy object
	indexScope := common.TagValueScopeSecurityPolicyUID
	existingSecurityPolices := securityPolicyStore.GetByIndex(indexScope, string(spUid))
	if len(existingSecurityPolices) == 0 {
		log.Info("NSX SecurityPolicy is not found in store, skip deleting it", "nsxSecurityPolicyUID", spUid)
		return nil
	}
	nsxSecurityPolicy = existingSecurityPolices[0]
	if nsxSecurityPolicy.Path == nil {
		err = errors.New("nsxSecurityPolicy path is empty")
		log.Error(err, "Failed to delete SecurityPolicy", "nsxSecurityPolicyUID", spUid)
		return err
	}
	nsxSecurityPolicy.MarkedForDelete = &MarkedForDelete

	// There is no NSX groups/rules in the security policy retrieved from securityPolicy store.
	// The groups/rules associated the deleting security policy can only be gotten from group/rule store.
	existingGroups := groupStore.GetByIndex(indexScope, string(spUid))
	nsxGroups := service.getMarkDeleteGroups(existingGroups, spUid)

	existingRules := ruleStore.GetByIndex(indexScope, string(spUid))
	nsxRules := service.getMarkDeleteRules(existingRules, spUid)
	nsxSecurityPolicy.Rules = nsxRules

	// WrapHierarchySecurityPolicy will modify the input security policy, so we need to make a copy for the following store update.
	finalSecurityPolicyCopy := *nsxSecurityPolicy
	finalSecurityPolicyCopy.Rules = nsxSecurityPolicy.Rules

	infraSecurityPolicy, err := service.WrapHierarchySecurityPolicy(nsxSecurityPolicy, nsxGroups)
	if err != nil {
		log.Error(err, "Failed to wrap SecurityPolicy", "nsxSecurityPolicyId", nsxSecurityPolicy.Id)
		return err
	}
	err = service.NSXClient.InfraClient.Patch(*infraSecurityPolicy, &EnforceRevisionCheckParam)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "Failed to delete SecurityPolicy", "nsxSecurityPolicyId", nsxSecurityPolicy.Id)
		return err
	}

	err = securityPolicyStore.Apply(&finalSecurityPolicyCopy)
	if err != nil {
		log.Error(err, "Failed to apply store", "securityPolicy", finalSecurityPolicyCopy)
		return err
	}
	err = ruleStore.Apply(&finalSecurityPolicyCopy.Rules)
	if err != nil {
		log.Error(err, "Failed to apply store", "nsxRules", finalSecurityPolicyCopy.Rules)
		return err
	}
	err = groupStore.Apply(&nsxGroups)
	if err != nil {
		log.Error(err, "Failed to apply store", "nsxGroups", nsxGroups)
		return err
	}

	log.Info("Successfully deleted NSX SecurityPolicy", "nsxSecurityPolicy", finalSecurityPolicyCopy)
	return nil
}

func (service *SecurityPolicyService) deleteVPCSecurityPolicy(spUID types.UID, isGC bool, createdFor string) error {
	indexScope := common.TagValueScopeSecurityPolicyUID
	if createdFor == common.ResourceTypeNetworkPolicy {
		indexScope = common.TagScopeNetworkPolicyUID
	}

	// For normal SecurityPolicy deletion process, which means that SecurityPolicy has a corresponding NSX SecurityPolicy object.
	// And for SecurityPolicy GC or cleanup process, which means that SecurityPolicy doesn't exist in K8s any more,
	// but still has a corresponding NSX SecurityPolicy object.
	// Using SecurityPolicy's UID from store to get NSX SecurityPolicy object.
	nsxSecurityPolicy, nsxGroups, nsxInfraShares, nsxInfraShareGroups,
		nsxProjectShares, nsxProjectShareGroups, vpcInfo, err := service.markSecurityPolicyResourcesDelete(indexScope, spUID)
	if err != nil {
		log.Error(err, "Failed to mark SecurityPolicy resources delete in VPC", "nsxSecurityPolicyUID", spUID)
		return err
	}

	isDefaultProject := false
	// For GC case, it usually will follow the normal deletion process.
	// Infra shares and groups also could be GC with NSX security policy together if security policy is found in store.
	// However, there is a specific case for Default Project that needs to be handle in GC process, that is,
	// When the NSX security policy, rules, and groups at the VPC level are deleted,
	// The following infra API call to delete infra share resources fail or NSX Operator restarts suddenly.
	// So, there are no more NSX security policy but the related NSX infra share resources became stale.
	if isGC && (len(nsxInfraShares) != 0 || len(nsxInfraShareGroups) != 0) {
		log.Info("There are stale NSX infra share resource to be GC", "nsxSecurityPolicyUID", spUID, "createdFor", createdFor)
		isDefaultProject = true
	} else if vpcInfo.VPCID == "" {
		err = errors.New("vpcID is empty")
		log.Error(err, "Failed to delete SecurityPolicy in VPC", "nsxSecurityPolicyUID", spUID)
		return err
	}

	// Check if it's Default Project from vpcInfo.
	// This check is necessary for normal deletion or GC process.
	if !isDefaultProject {
		isDefaultProject, err = service.vpcService.IsDefaultNSXProject(vpcInfo.OrgID, vpcInfo.ProjectID)
		if err != nil {
			log.Error(err, "Failed to check if NSX Project is default", "nsxProjectID", vpcInfo.ProjectID)
			return err
		}
	}

	if nsxSecurityPolicy != nil {
		err = service.deleteNSXSecurityPolicy(nsxSecurityPolicy, &vpcInfo)
		if err != nil {
			log.Error(err, "Failed to delete NSX SecurityPolicy and rules in VPC", "nsxSecurityPolicyUID", spUID)
			return err
		}
		err = service.applySecurityPolicyStore(nsxSecurityPolicy, nsxSecurityPolicy.Rules, true)
		if err != nil {
			return err
		}
		log.Info("Successfully deleted NSX SecurityPolicy and rules only", "nsxSecurityPolicyUID", spUID)
	}

	if !isDefaultProject {
		err = service.deleteNSXSecurityPolicyGroupShare(nsxGroups, nsxProjectShares, nsxProjectShareGroups, &vpcInfo)
	} else {
		err = service.deleteNSXSecurityPolicyGroupShareForDefaultProject(nsxGroups, nsxInfraShares, nsxInfraShareGroups, &vpcInfo)
	}
	// Ignore error here to make groups/shares to be deleted in GC.
	// Because NSX SecurityPolicy is deleted, it's unable to get SecurityPolicyUID from SecurityPolicyStore for fetching groups/shares even by requeuing the error.
	// Also Ignore error that happens during GC process, because GC will be run in the next round, there is no need to return error to requeue.
	if err != nil {
		log.Error(err, "Failed to delete NSX groups and shares after NSX SecurityPolicy is deleted, and the groups and shares will be GC", "nsxSecurityPolicyUID", spUID)
		return nil
	}

	if !isDefaultProject {
		err = service.applyVPCGroupShareStore(nsxGroups, nsxProjectShares, nsxProjectShareGroups, isDefaultProject)
	} else {
		err = service.applyVPCGroupShareStore(nsxGroups, nsxInfraShares, nsxInfraShareGroups, isDefaultProject)
	}
	if err != nil {
		return err
	}

	if isGC {
		log.Info("Successfully GC NSX SecurityPolicy, rules, groups and shares in VPC", "nsxSecurityPolicyUID", spUID)
	} else {
		log.Info("Successfully deleted NSX SecurityPolicy, rules, groups and shares in VPC", "nsxSecurityPolicyUID", spUID)
	}

	return nil
}

func (service *SecurityPolicyService) createOrUpdateGroups(obj *v1alpha1.SecurityPolicy, nsxGroups []*model.Group) error {
	var vpcInfo *common.VPCResourceInfo
	var err error
	finalGroups := make([]model.Group, 0)
	for _, group := range nsxGroups {
		group.MarkedForDelete = nil
		finalGroups = append(finalGroups, *group)
		if IsVPCEnabled(service) {
			vpcInfo, err = service.getVPCInfo(obj.ObjectMeta.Namespace)
			if err != nil {
				return err
			}
			orgID := (*vpcInfo).OrgID
			projectID := (*vpcInfo).ProjectID
			vpcID := (*vpcInfo).VPCID

			err = service.NSXClient.VpcGroupClient.Patch(orgID, projectID, vpcID, *group.Id, *group)
			err = nsxutil.TransNSXApiError(err)
		} else {
			err = service.NSXClient.GroupClient.Patch(getDomain(service), *group.Id, *group)
			err = nsxutil.TransNSXApiError(err)
		}
	}

	if err != nil {
		return err
	}
	err = service.groupStore.Apply(&finalGroups)
	if err != nil {
		return err
	}
	log.Info("Successfully create or update groups", "nsxGroups", finalGroups)
	return nil
}

func (service *SecurityPolicyService) getUpdateRules(existingRules []*model.Rule, expectedRules []model.Rule) []model.Rule {
	changed, stale := common.CompareResources(RulesPtrToComparable(existingRules), RulesToComparable(expectedRules))
	changedRules, staleRules := ComparableToRules(changed), ComparableToRules(stale)
	finalRules := make([]model.Rule, 0)
	for i := len(staleRules) - 1; i >= 0; i-- { // Don't use range, it would copy the element
		staleRules[i].MarkedForDelete = &MarkedForDelete // NSX clients need this field to delete the rules
	}
	finalRules = append(finalRules, staleRules...)
	finalRules = append(finalRules, changedRules...)
	return finalRules
}

func (service *SecurityPolicyService) getUpdateGroups(existingGroups []*model.Group, expectedGroups []model.Group) []model.Group {
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

func (service *SecurityPolicyService) getUpdateShares(existingShares []*model.Share, expectedShares []model.Share) []model.Share {
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

func (service *SecurityPolicyService) getMarkDeleteGroups(existingGroups []*model.Group, sp types.UID) []model.Group {
	deleteGroups := make([]model.Group, 0)

	if len(existingGroups) == 0 {
		log.Debug("Did not get groups with SecurityPolicy index", "securityPolicyUID", string(sp))
		return deleteGroups
	}
	for _, group := range existingGroups {
		deleteGroups = append(deleteGroups, *group)
	}
	for i := len(deleteGroups) - 1; i >= 0; i-- {
		(deleteGroups)[i].MarkedForDelete = &MarkedForDelete
	}
	return deleteGroups
}

func (service *SecurityPolicyService) getMarkDeleteRules(existingRules []*model.Rule, sp types.UID) []model.Rule {
	deleteRules := make([]model.Rule, 0)

	if len(existingRules) == 0 {
		log.Debug("Did not get rules with SecurityPolicy index", "securityPolicyUID", string(sp))
		return deleteRules
	}
	for _, rule := range existingRules {
		deleteRules = append(deleteRules, *rule)
	}
	for i := len(deleteRules) - 1; i >= 0; i-- {
		(deleteRules)[i].MarkedForDelete = &MarkedForDelete
	}
	return deleteRules
}

func (service *SecurityPolicyService) getMarkDeleteShares(existingShares []*model.Share, sp types.UID) []model.Share {
	deleteShares := make([]model.Share, 0)

	if len(existingShares) == 0 {
		log.Debug("Did not get shares with SecurityPolicy index", "securityPolicyUID", string(sp))
		return deleteShares
	}
	for _, share := range existingShares {
		deleteShares = append(deleteShares, *share)
	}
	for i := len(deleteShares) - 1; i >= 0; i-- {
		(deleteShares)[i].MarkedForDelete = &MarkedForDelete
	}
	return deleteShares
}

func (service *SecurityPolicyService) getStaleUpdateShares(nsxShares []model.Share) (staleShares []model.Share, updatedShares []model.Share) {
	finalStaleShares := make([]model.Share, 0)
	finalChangedShares := make([]model.Share, 0)
	for i := len(nsxShares) - 1; i >= 0; i-- {
		if nsxShares[i].MarkedForDelete != nil && (*nsxShares[i].MarkedForDelete == MarkedForDelete) {
			finalStaleShares = append(finalStaleShares, nsxShares[i])
		} else {
			finalChangedShares = append(finalChangedShares, nsxShares[i])
		}
	}
	return finalStaleShares, finalChangedShares
}

func (service *SecurityPolicyService) getStaleUpdateGroups(nsxGroups []model.Group) (staleShares []model.Group, updatedShares []model.Group) {
	finalStaleGroups := make([]model.Group, 0)
	finalChangedGroups := make([]model.Group, 0)

	for i := len(nsxGroups) - 1; i >= 0; i-- {
		if nsxGroups[i].MarkedForDelete != nil && (*nsxGroups[i].MarkedForDelete == MarkedForDelete) {
			finalStaleGroups = append(finalStaleGroups, nsxGroups[i])
		} else {
			finalChangedGroups = append(finalChangedGroups, nsxGroups[i])
		}
	}
	return finalStaleGroups, finalChangedGroups
}

func (service *SecurityPolicyService) markSecurityPolicyResourcesDelete(indexScope string, spUID types.UID) (
	*model.SecurityPolicy, []model.Group, []model.Share, []model.Group, []model.Share, []model.Group, common.VPCResourceInfo, error,
) {
	var nsxSecurityPolicy *model.SecurityPolicy
	var err error

	securityPolicyStore, ruleStore, groupStore := service.getSecurityPolicyResourceStores()
	infraGroupStore, infraShareStore, projectGroupStore, projectShareStore := service.getVPCShareResourceStores()

	existingSecurityPolices := securityPolicyStore.GetByIndex(indexScope, string(spUID))
	// There is no NSX groups/rules in the security policy retrieved from securityPolicy store.
	// The groups/rules associated the deleting security policy can only be gotten from group/rule store.
	existingGroups := groupStore.GetByIndex(indexScope, string(spUID))
	existingRules := ruleStore.GetByIndex(indexScope, string(spUID))
	nsxGroups := service.getMarkDeleteGroups(existingGroups, spUID)
	nsxRules := service.getMarkDeleteRules(existingRules, spUID)

	// For other GC cases, it will follow the normal deletion process as below.
	var vpcInfo common.VPCResourceInfo
	if len(existingSecurityPolices) != 0 {
		nsxSecurityPolicy = existingSecurityPolices[0]
		// vpcInfo should be listed directly from security policy store to avoid calling VPC service.
		// Get orgID, projectID, vpcID from security policy path "/orgs/<orgID>/projects/<projectID>/vpcs/<vpcID>/security-policies/<spID>"
		if nsxSecurityPolicy.Path == nil {
			err = errors.New("nsxSecurityPolicy path is empty")
			log.Error(err, "Failed to delete SecurityPolicy in VPC", "nsxSecurityPolicyUID", spUID)
			return nil, nil, nil, nil, nil, nil, vpcInfo, err
		}
		vpcInfo, _ = common.ParseVPCResourcePath(*(nsxSecurityPolicy.Path))
		nsxSecurityPolicy.MarkedForDelete = &MarkedForDelete
		nsxSecurityPolicy.Rules = nsxRules
	} else {
		// In case there is no NSX security policy, we still need to get vpcInfo from existing VPC groups.
		// For each SecurityPolicy, there must be VPC groups created, either policy appliedTo or rule appliedTo
		// or source/destination group created under VPC level.
		// So here we can get vpcInfo from the existing VPC group's path.
		for _, g := range existingGroups {
			if g.Path != nil {
				vpcInfo, _ = common.ParseVPCResourcePath(*(g.Path))
				break
			}
		}
	}

	existingNsxInfraShares := infraShareStore.GetByIndex(indexScope, string(spUID))
	existingNsxInfraShareGroups := infraGroupStore.GetByIndex(indexScope, string(spUID))
	existingProjectShares := projectShareStore.GetByIndex(indexScope, string(spUID))
	existingProjectGroups := projectGroupStore.GetByIndex(indexScope, string(spUID))
	nsxInfraShares := service.getMarkDeleteShares(existingNsxInfraShares, spUID)
	nsxInfraShareGroups := service.getMarkDeleteGroups(existingNsxInfraShareGroups, spUID)
	nsxProjectShares := service.getMarkDeleteShares(existingProjectShares, spUID)
	nsxProjectShareGroups := service.getMarkDeleteGroups(existingProjectGroups, spUID)

	return nsxSecurityPolicy, nsxGroups, nsxInfraShares, nsxInfraShareGroups, nsxProjectShares, nsxProjectShareGroups, vpcInfo, nil
}

// createOrUpdateNSXSecurityPolicy uses hierarchy API call to create/update SecurityPolicy on the whole resource tree for non-Default Project.
func (service *SecurityPolicyService) createOrUpdateNSXSecurityPolicy(nsxSecurityPolicy *model.SecurityPolicy, nsxGroups []model.Group,
	nsxShares []model.Share, nsxShareGroups []model.Group, vpcInfo *common.VPCResourceInfo,
) (*model.SecurityPolicy, error) {
	var err error
	var projectInfraResource []*data.StructValue

	if len(nsxShares) != 0 {
		// Wrap project groups and shares into project child infra.
		projectInfraResource, err = service.wrapHierarchyProjectResources(nsxShares, nsxShareGroups)
		if err != nil {
			log.Error(err, "Failed to wrap NSX project groups and shares", "nsxSecurityPolicyId", nsxSecurityPolicy.Id)
			return nil, err
		}
	}

	// Wrap SecurityPolicy, groups, rules under VPC level together with project groups and shares into one hierarchy resource tree.
	orgRoot, err := service.wrapHierarchyVpcSecurityPolicy(nsxSecurityPolicy, nsxGroups, projectInfraResource, vpcInfo)
	if err != nil {
		log.Error(err, "Failed to wrap NSX SecurityPolicy resources in VPC", "nsxSecurityPolicyId", nsxSecurityPolicy.Id)
		return nil, err
	}
	// Create/update SecurityPolicy together with groups, rules under VPC level and project groups, shares.
	err = service.NSXClient.OrgRootClient.Patch(*orgRoot, &EnforceRevisionCheckParam)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "Failed to create or update NSX SecurityPolicy in VPC", "nsxSecurityPolicyId", nsxSecurityPolicy.Id)
		return nil, err
	}

	// Get SecurityPolicy from NSX after HAPI call as NSX renders several fields like `path`/`parent_path`.
	nsxGetSecurityPolicy, err := service.NSXClient.VPCSecurityClient.Get(vpcInfo.OrgID, vpcInfo.ProjectID, vpcInfo.VPCID, *nsxSecurityPolicy.Id)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "Failed to get NSX SecurityPolicy in VPC", "nsxSecurityPolicyId", nsxSecurityPolicy.Id)
		return nil, err
	}

	// Check SecurityPolicy realization state
	err = service.checkSecurityPolicyRealizationState(&nsxGetSecurityPolicy, *(nsxGetSecurityPolicy.Path))
	if err != nil {
		return nil, err
	}

	return &nsxGetSecurityPolicy, nil
}

// createOrUpdateNSXSecurityPolicyForDefaultProject uses hierarchy API call to create/update SecurityPolicy on the whole resource tree for Default Project.
func (service *SecurityPolicyService) createOrUpdateNSXSecurityPolicyForDefaultProject(nsxSecurityPolicy *model.SecurityPolicy, nsxGroups []model.Group,
	nsxShares []model.Share, nsxShareGroups []model.Group, vpcInfo *common.VPCResourceInfo,
) (*model.SecurityPolicy, error) {
	var err error
	var infraResource *model.Infra
	var projectInfraResource []*data.StructValue
	nsxGetSecurityPolicy := model.SecurityPolicy{}

	finalStaleShares, finalChangedShares := service.getStaleUpdateShares(nsxShares)
	finalStaleShareGroups, finalChangedShareGroups := service.getStaleUpdateGroups(nsxShareGroups)

	// It's needed to create/update the infra resources before these resources are referred by VPC resources.
	if len(finalChangedShares) != 0 || len(finalChangedShareGroups) != 0 {
		// Wrap infra groups and shares into infra child infra.
		infraResource, err = service.wrapHierarchyInfraResources(finalChangedShares, finalChangedShareGroups)
		if err != nil {
			log.Error(err, "Failed to wrap NSX infra changed groups and shares", "nsxSecurityPolicyId", nsxSecurityPolicy.Id)
			return nil, err
		}

		err = service.NSXClient.InfraClient.Patch(*infraResource, &EnforceRevisionCheckParam)
		err = nsxutil.TransNSXApiError(err)
		if err != nil {
			log.Error(err, "Failed to create or update NSX infra resource", "nsxSecurityPolicyId", nsxSecurityPolicy.Id)
			return nil, err
		}
	}

	// Wrap SecurityPolicy, groups, rules under VPC level into one hierarchy resource tree.
	orgRoot, err := service.wrapHierarchyVpcSecurityPolicy(nsxSecurityPolicy, nsxGroups, projectInfraResource, vpcInfo)
	if err != nil {
		log.Error(err, "Failed to wrap SecurityPolicy in VPC", "nsxSecurityPolicyId", nsxSecurityPolicy.Id)
		return nil, err
	}

	// Create/update SecurityPolicy together with groups, rules under VPC level.
	err = service.NSXClient.OrgRootClient.Patch(*orgRoot, &EnforceRevisionCheckParam)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "Failed to create or update SecurityPolicy in VPC", "nsxSecurityPolicyId", nsxSecurityPolicy.Id)
		return nil, err
	}

	// The infra share resources can be deleted only after the rules under VPC level which are referring the share resources have been deleted.
	if len(finalStaleShares) != 0 || len(finalStaleShareGroups) != 0 {
		// Wrap infra groups and shares into infra child infra.
		infraResource, err = service.wrapHierarchyInfraResources(finalStaleShares, finalStaleShareGroups)
		if err != nil {
			log.Error(err, "Failed to wrap NSX infra stale groups and shares", "nsxSecurityPolicyId", nsxSecurityPolicy.Id)
			return nil, err
		}
		err = service.NSXClient.InfraClient.Patch(*infraResource, &EnforceRevisionCheckParam)
		err = nsxutil.TransNSXApiError(err)
		if err != nil {
			log.Error(err, "Failed to delete NSX infra Resource", "nsxSecurityPolicyId", nsxSecurityPolicy.Id)
			return nil, err
		}
	}

	// Get SecurityPolicy from NSX after HAPI call as NSX renders several fields like `path`/`parent_path`.
	nsxGetSecurityPolicy, err = service.NSXClient.VPCSecurityClient.Get(vpcInfo.OrgID, vpcInfo.ProjectID, vpcInfo.VPCID, *nsxSecurityPolicy.Id)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "Failed to get SecurityPolicy in VPC", "nsxSecurityPolicyId", nsxSecurityPolicy.Id)
		return nil, err
	}

	// Check SecurityPolicy realization state
	err = service.checkSecurityPolicyRealizationState(&nsxGetSecurityPolicy, *(nsxGetSecurityPolicy.Path))
	if err != nil {
		return nil, err
	}

	return &nsxGetSecurityPolicy, nil
}

// deleteNSXSecurityPolicy deletes NSX SecurityPolicy, rules and the groups/shares for both NSX Default Project and non-Default Project.
func (service *SecurityPolicyService) deleteNSXSecurityPolicy(nsxSecurityPolicy *model.SecurityPolicy, vpcInfo *common.VPCResourceInfo) error {
	var err error

	// Delete NSX SecurityPolicy and rules only.
	err = service.NSXClient.VPCSecurityClient.Delete(vpcInfo.OrgID, vpcInfo.ProjectID, vpcInfo.VPCID, *nsxSecurityPolicy.Id)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "Failed to delete NSX SecurityPolicy in VPC", "nsxSecurityPolicyId", nsxSecurityPolicy.Id)
		return err
	}

	return nil
}

// deleteNSXSecurityPolicyGroupShare deletes NSX SecurityPolicy associated the groups/shares for non-Default Project.
func (service *SecurityPolicyService) deleteNSXSecurityPolicyGroupShare(nsxGroups []model.Group,
	nsxShares []model.Share, nsxShareGroups []model.Group, vpcInfo *common.VPCResourceInfo,
) error {
	var err error
	var projectInfraResource []*data.StructValue

	if len(nsxShares) != 0 {
		// Wrap project groups and shares into project child infra.
		projectInfraResource, err = service.wrapHierarchyProjectResources(nsxShares, nsxShareGroups)
		if err != nil {
			log.Error(err, "Failed to wrap NSX project groups and shares")
			return err
		}
	}

	// Wrap groups under VPC level together with project groups and shares into one hierarchy resource tree.
	orgRoot, err := service.wrapHierarchyVpcSecurityPolicy(nil, nsxGroups, projectInfraResource, vpcInfo)
	if err != nil {
		log.Error(err, "Failed to wrap NSX VPC groups and project groups, shares in VPC")
		return err
	}
	// Delete groups under VPC level together with project groups, shares.
	err = service.NSXClient.OrgRootClient.Patch(*orgRoot, &EnforceRevisionCheckParam)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "Failed to delete NSX groups and shares in VPC")
		return err
	}

	return nil
}

// deleteNSXSecurityPolicyGroupShareForDefaultProject deletes NSX SecurityPolicy associated the groups/shares for Default Project.
func (service *SecurityPolicyService) deleteNSXSecurityPolicyGroupShareForDefaultProject(nsxGroups []model.Group,
	nsxShares []model.Share, nsxShareGroups []model.Group, vpcInfo *common.VPCResourceInfo,
) error {
	var projectInfraResource []*data.StructValue

	if len(nsxGroups) != 0 {
		// Wrap VPC level groups into project child infra.
		orgRoot, err := service.wrapHierarchyVpcSecurityPolicy(nil, nsxGroups, projectInfraResource, vpcInfo)
		if err != nil {
			log.Error(err, "Failed to wrap NSX project groups and shares")
			return err
		}

		// Delete groups under VPC level.
		err = service.NSXClient.OrgRootClient.Patch(*orgRoot, &EnforceRevisionCheckParam)
		err = nsxutil.TransNSXApiError(err)
		if err != nil {
			log.Error(err, "Failed to delete NSX groups in VPC")
			return err
		}
	}

	if len(nsxShares) != 0 {
		// Wrap infra groups and shares into infra child infra.
		infraResource, err := service.wrapHierarchyInfraResources(nsxShares, nsxShareGroups)
		if err != nil {
			log.Error(err, "Failed to wrap NSX infra groups and shares")
			return err
		}
		// Delete infra groups and shares.
		err = service.NSXClient.InfraClient.Patch(*infraResource, &EnforceRevisionCheckParam)
		err = nsxutil.TransNSXApiError(err)
		if err != nil {
			log.Error(err, "Failed to delete NSX infra groups and shares")
			return err
		}
	}
	return nil
}

func (service *SecurityPolicyService) applySecurityPolicyStore(nsxSecurityPolicy *model.SecurityPolicy, nsxRules []model.Rule, isChanged bool) error {
	var err error
	securityPolicyStore, ruleStore, _ := service.getSecurityPolicyResourceStores()
	if isChanged {
		err = securityPolicyStore.Apply(nsxSecurityPolicy)
		if err != nil {
			log.Error(err, "Failed to apply store", "securityPolicy", nsxSecurityPolicy)
			return err
		}
	}

	err = ruleStore.Apply(&nsxRules)
	if err != nil {
		log.Error(err, "Failed to apply store", "nsxRules", nsxRules)
		return err
	}

	return nil
}

func (service *SecurityPolicyService) applyVPCGroupShareStore(nsxGroups []model.Group, nsxShares []model.Share, nsxShareGroups []model.Group, isDefaultProject bool) error {
	var err error
	_, _, groupStore := service.getSecurityPolicyResourceStores()
	infraGroupStore, infraShareStore, projectGroupStore, projectShareStore := service.getVPCShareResourceStores()

	err = groupStore.Apply(&nsxGroups)
	if err != nil {
		log.Error(err, "Failed to apply store", "nsxGroups", nsxGroups)
		return err
	}

	if isDefaultProject {
		err = infraGroupStore.Apply(&nsxShareGroups)
		if err != nil {
			log.Error(err, "Failed to apply store", "nsxInfraGroups", nsxShareGroups)
			return err
		}
		err = infraShareStore.Apply(&nsxShares)
		if err != nil {
			log.Error(err, "Failed to apply store", "nsxInfraShares", nsxShares)
			return err
		}
	} else {
		err = projectGroupStore.Apply(&nsxShareGroups)
		if err != nil {
			log.Error(err, "Failed to apply store", "nsxProjectGroups", nsxShareGroups)
			return err
		}
		err = projectShareStore.Apply(&nsxShares)
		if err != nil {
			log.Error(err, "Failed to apply store", "nsxProjectShares", nsxShares)
			return err
		}
	}
	return nil
}

func (service *SecurityPolicyService) ListSecurityPolicyID() sets.Set[string] {
	indexScope := common.TagValueScopeSecurityPolicyUID
	return service.getGCSecurityPolicyIDSet(indexScope)
}

func (service *SecurityPolicyService) ListNetworkPolicyID() sets.Set[string] {
	indexScope := common.TagScopeNetworkPolicyUID
	return service.getGCSecurityPolicyIDSet(indexScope)
}

func (service *SecurityPolicyService) ListSecurityPolicyByName(ns, name string) []*model.SecurityPolicy {
	var result []*model.SecurityPolicy
	securityPolicies := service.securityPolicyStore.GetByIndex(common.TagScopeNamespace, ns)
	for _, securityPolicy := range securityPolicies {
		securityPolicyCRName := nsxutil.FindTag(securityPolicy.Tags, common.TagValueScopeSecurityPolicyName)
		if securityPolicyCRName == name {
			result = append(result, securityPolicy)
		}
	}
	return result
}

func (service *SecurityPolicyService) ListNetworkPolicyByName(ns, name string) []*model.SecurityPolicy {
	var result []*model.SecurityPolicy
	securityPolicies := service.securityPolicyStore.GetByIndex(common.TagScopeNamespace, ns)
	for _, securityPolicy := range securityPolicies {
		securityPolicyCRName := nsxutil.FindTag(securityPolicy.Tags, common.TagScopeNetworkPolicyName)
		if securityPolicyCRName == name {
			result = append(result, securityPolicy)
		}
	}
	return result
}

func (service *SecurityPolicyService) getGCSecurityPolicyIDSet(indexScope string) sets.Set[string] {
	// List SecurityPolicyID to which groups resources are associated in group store
	groupSet := service.groupStore.ListIndexFuncValues(indexScope)

	policySet := service.securityPolicyStore.ListIndexFuncValues(indexScope)

	// List SecurityPolicyID to which share resources are associated in project share/group store
	projectShareSet := service.projectShareStore.ListIndexFuncValues(indexScope)
	projectGroupSet := service.projectGroupStore.ListIndexFuncValues(indexScope)
	// List SecurityPolicyID to which share resources are associated in infra share/group store
	infraShareSet := service.infraShareStore.ListIndexFuncValues(indexScope)
	infraGroupSet := service.infraGroupStore.ListIndexFuncValues(indexScope)

	return groupSet.Union(policySet).Union(projectShareSet).Union(projectGroupSet).Union(infraShareSet).Union(infraGroupSet)
}

func (service *SecurityPolicyService) getVPCInfo(spNameSpace string) (*common.VPCResourceInfo, error) {
	vpcInfo := service.vpcService.ListVPCInfo(spNameSpace)
	if len(vpcInfo) == 0 {
		errorMsg := fmt.Sprintf("there is no VPC info found for namespace %s", spNameSpace)
		err := errors.New(errorMsg)
		return nil, err
	}
	return &vpcInfo[0], nil
}

func (service *SecurityPolicyService) checkSecurityPolicyRealizationState(sp *model.SecurityPolicy, spPath string) error {
	log.Trace("Check NSX SecurityPolicy realization state", "nsxSecurityPolicyId", *sp.Id)
	realizeService := realizestate.InitializeRealizeState(service.Service)
	if err := realizeService.CheckRealizeState(util.NSXTRealizeRetry, spPath, []string{}); err != nil {
		log.Error(err, "Failed to check NSX SecurityPolicy realization state", "nsxSecurityPolicyId", *sp.Id)
		if nsxutil.IsRealizeStateError(err) {
			log.Error(err, "The created SecurityPolicy is in error realization state", "nsxSecurityPolicyId", *sp.Id)
		}
		return err
	}
	return nil
}
