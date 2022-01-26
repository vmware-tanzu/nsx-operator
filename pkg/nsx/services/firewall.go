package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra/domains"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type SecurityPolicyService struct {
	NSXClient           *nsx.Client
	NSXConfig           *config.NSXOperatorConfig
	GroupStore          cache.Indexer
	SecurityPolicyStore cache.Indexer
	RuleStore           cache.Indexer
}

var (
	log = logf.Log.WithName("service").WithName("firewall")
)

// InitializeSecurityPolicy sync NSX resources
func InitializeSecurityPolicy(NSXClient *nsx.Client, cf *config.NSXOperatorConfig) (*SecurityPolicyService, error) {
	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)

	wg.Add(3)
	service := &SecurityPolicyService{NSXClient: NSXClient}
	service.GroupStore = cache.NewIndexer(keyFunc, cache.Indexers{util.TagScopeNamespace: namespaceIndexFunc, util.TagScopeSecurityPolicyCRUID: securityPolicyCRUIDScopeIndexFunc})
	service.SecurityPolicyStore = cache.NewIndexer(keyFunc, cache.Indexers{util.TagScopeSecurityPolicyCRUID: securityPolicyCRUIDScopeIndexFunc})
	service.RuleStore = cache.NewIndexer(keyFunc, cache.Indexers{util.TagScopeSecurityPolicyCRUID: securityPolicyCRUIDScopeIndexFunc})
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

func (service *SecurityPolicyService) buildSecurityPolicy(obj *v1alpha1.SecurityPolicy) (*model.SecurityPolicy, *[]model.Group, error) {
	var nsxRules []model.Rule
	var nsxGroups []model.Group
	log.V(1).Info("building the model SecurityPolicy from CR SecurityPolicy", "object", *obj)
	nsxSecurityPolicy := &model.SecurityPolicy{}

	policyID := fmt.Sprintf("sp_%s", obj.UID)
	nsxSecurityPolicy.Id = &policyID

	policyName := fmt.Sprintf("%s-%s", obj.ObjectMeta.Namespace, obj.ObjectMeta.Name)
	nsxSecurityPolicy.DisplayName = &policyName

	// TODO: confirm the sequence number: offset
	policyPriority := int64(obj.Spec.Priority)
	nsxSecurityPolicy.SequenceNumber = &policyPriority

	policyGroup, policyGroupPath, _ := service.buildPolicyGroup(obj)
	nsxSecurityPolicy.Scope = []string{policyGroupPath}
	if policyGroup != nil {
		nsxGroups = append(nsxGroups, *policyGroup)
	}

	rules := obj.Spec.Rules

	for i, rule := range rules {
		nsxRule, ruleGroups, err := service.buildRuleAndGroups(obj, &rule, i)
		if err != nil {
			log.Error(err, "failed to build rule and groups", "rule", rule, "ruleIndex", i)
			return nil, nil, err
		}
		nsxRules = append(nsxRules, *nsxRule)
		for _, ruleGroup := range *ruleGroups {
			nsxGroups = append(nsxGroups, ruleGroup)
		}
	}
	nsxSecurityPolicy.Rules = nsxRules
	tags := service.buildBasicTags(obj)
	nsxSecurityPolicy.Tags = tags
	log.V(1).Info("built nsxSecurityPolicy", "nsxSecurityPolicy", nsxSecurityPolicy)
	return nsxSecurityPolicy, &nsxGroups, nil
}

func (service *SecurityPolicyService) buildPolicyGroup(obj *v1alpha1.SecurityPolicy) (*model.Group, string, error) {
	policyGroup := model.Group{}

	policyGroupID := service.buildPolicyGroupID(obj)
	policyGroup.Id = &policyGroupID

	// TODO: have a common function to generate ID and Name with parameters like prefix, suffix
	policyGroupName := fmt.Sprintf("%s-%s-scope", obj.ObjectMeta.Namespace, obj.ObjectMeta.Name)
	policyGroup.DisplayName = &policyGroupName

	appliedTo := obj.Spec.AppliedTo
	targetTags := service.buildTargetTags(obj, &appliedTo, -1)
	policyGroup.Tags = targetTags
	if len(appliedTo) == 0 {
		return nil, "ANY", nil
	}

	for i, target := range appliedTo {
		service.updateTargetExpressions(obj, &target, &policyGroup, i)
	}

	policyGroupPath := service.buildPolicyGroupPath(obj)
	return &policyGroup, policyGroupPath, nil
}

func (service *SecurityPolicyService) buildTargetTags(obj *v1alpha1.SecurityPolicy, targets *[]v1alpha1.SecurityPolicyTarget, idx int) []model.Tag {
	basicTags := service.buildBasicTags(obj)
	tagScopeGroupType := util.TagScopeGroupType
	tagScopeRuleID := util.TagScopeRuleID
	tagValueScope := "scope"
	tagScopeSelectorHash := util.TagScopeSelectorHash
	sort.Slice(*targets, func(i, j int) bool {
		k1, _ := json.Marshal((*targets)[i])
		k2, _ := json.Marshal((*targets)[j])
		return string(k1) < string(k2)
	})
	serializedBytes, _ := json.Marshal(*targets)
	groupHash := util.Sha1(string(serializedBytes))
	var targetTags = []model.Tag{
		model.Tag{
			Scope: &tagScopeGroupType,
			Tag:   &tagValueScope,
		},
		model.Tag{
			Scope: &tagScopeSelectorHash,
			Tag:   &groupHash,
		},
	}
	for _, tag := range basicTags {
		targetTags = append(targetTags, tag)
	}
	if idx != -1 {
		// the appliedTo group belongs to a rule so it needs a tag including the rule id
		ruleID := service.buildRuleID(obj, idx)
		targetTags = append(targetTags,
			model.Tag{
				Scope: &tagScopeRuleID,
				Tag:   &ruleID,
			},
		)
	}
	return targetTags
}

func (service *SecurityPolicyService) buildBasicTags(obj *v1alpha1.SecurityPolicy) []model.Tag {
	uid := string(obj.UID)
	clusterTag := service.getCluster()
	tagScopeCluster := util.TagScopeCluster
	tagScopeNamespace := util.TagScopeNamespace
	tagScopeSecurityPolicyCRName := util.TagScopeSecurityPolicyCRName
	tagScopeSecurityPolicyCRUID := util.TagScopeSecurityPolicyCRUID
	var tags = []model.Tag{
		model.Tag{
			Scope: &tagScopeCluster,
			Tag:   &clusterTag,
		},
		model.Tag{
			Scope: &tagScopeNamespace,
			Tag:   &obj.ObjectMeta.Namespace,
		},
		// TODO: get namespace uid
		model.Tag{
			Scope: &tagScopeSecurityPolicyCRName,
			Tag:   &obj.ObjectMeta.Name,
		},
		model.Tag{
			Scope: &tagScopeSecurityPolicyCRUID,
			Tag:   &uid,
		},
	}
	return tags
}

func (service *SecurityPolicyService) updateTargetExpressions(obj *v1alpha1.SecurityPolicy, target *v1alpha1.SecurityPolicyTarget, group *model.Group, idx int) {
	service.appendOperatorIfNeeded(&group.Expression, "OR")
	expressions := data.NewListValue()
	expressionFrame := data.NewStructValue(
		"",
		map[string]data.DataValue{
			"expressions":   expressions,
			"resource_type": data.NewStringValue("NestedExpression"),
		},
	)
	group.Expression = append(group.Expression, expressionFrame)

	clusterExpression := service.buildExpression(
		"Condition", "SegmentPort",
		fmt.Sprintf("%s|%s", util.TagScopeNCPCluster, service.getCluster()),
		"Tag", "EQUALS",
	)
	expressions.Add(clusterExpression)
	if target.PodSelector != nil {
		service.addOperatorIfNeeded(expressions, "AND")
		// TODO: consider to use project_uid instead of project
		nsExpression := service.buildExpression(
			"Condition", "SegmentPort",
			fmt.Sprintf("%s|%s", util.TagScopeNCPProject, obj.ObjectMeta.Namespace),
			"Tag", "EQUALS",
		)
		expressions.Add(nsExpression)
		service.updatePortExpressions(target.PodSelector.MatchLabels, expressions)
	}
	if target.VMSelector != nil {
		service.addOperatorIfNeeded(expressions, "AND")
		nsExpression := service.buildExpression(
			"Condition", "SegmentPort",
			fmt.Sprintf("%s|%s", util.TagScopeNCPVIFProject, obj.ObjectMeta.Namespace),
			"Tag", "EQUALS",
		)
		expressions.Add(nsExpression)
		service.updatePortExpressions(target.VMSelector.MatchLabels, expressions)
	}
}

func (service *SecurityPolicyService) appendOperatorIfNeeded(policyExpression *[]*data.StructValue, op string) {
	if len(*policyExpression) > 0 {
		operator := service.buildConjOperator(op)
		*policyExpression = append(*policyExpression, operator)
	}
}

func (service *SecurityPolicyService) buildConjOperator(op string) *data.StructValue {
	operator := data.NewStructValue(
		"",
		map[string]data.DataValue{
			"conjunction_operator": data.NewStringValue(op),
			"resource_type":        data.NewStringValue("ConjunctionOperator"),
		},
	)
	return operator
}

func (service *SecurityPolicyService) buildExpression(resource_type, member_type, value, key, operator string) *data.StructValue {
	expression := data.NewStructValue(
		"",
		map[string]data.DataValue{
			"resource_type": data.NewStringValue(resource_type),
			"member_type":   data.NewStringValue(member_type),
			"value":         data.NewStringValue(value),
			"key":           data.NewStringValue(key),
			"operator":      data.NewStringValue(operator),
		},
	)
	return expression
}

func (service *SecurityPolicyService) addOperatorIfNeeded(expressions *data.ListValue, op string) {
	if !expressions.IsEmpty() {
		operator := service.buildConjOperator(op)
		expressions.Add(operator)
	}
}

func (service *SecurityPolicyService) updatePortExpressions(matchLabels map[string]string, expressions *data.ListValue) {
	for k, v := range *util.NormalizeLabels(&matchLabels) {
		service.addOperatorIfNeeded(expressions, "AND")
		expression := service.buildExpression(
			"Condition", "SegmentPort",
			fmt.Sprintf("%s|%s", k, v),
			"Tag", "EQUALS",
		)
		expressions.Add(expression)
	}
}

func (service *SecurityPolicyService) buildPolicyGroupID(obj *v1alpha1.SecurityPolicy) string {
	return fmt.Sprintf("sp_%s_scope", obj.UID)
}

func (service *SecurityPolicyService) buildPolicyGroupPath(obj *v1alpha1.SecurityPolicy) string {
	policyGroupID := service.buildPolicyGroupID(obj)
	return fmt.Sprintf("/infra/domains/%s/groups/%s", service.getDomain(), policyGroupID)
}

func (service *SecurityPolicyService) buildRuleAndGroups(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, idx int) (*model.Rule, *[]model.Group, error) {
	var direction string
	sequenceNumber := int64(idx)
	nsxRuleID := service.buildRuleID(obj, idx)
	var nsxRuleName string
	var ruleGroups []model.Group
	var nsxRuleAppliedGroup *model.Group
	var nsxRuleSrcGroup *model.Group
	var nsxRuleDstGroup *model.Group
	var nsxRuleAppliedGroupPath string
	var nsxRuleDstGroupPath string
	var nsxRuleSrcGroupPath string
	if len(rule.Name) > 0 {
		nsxRuleName = rule.Name
	} else {
		nsxRuleName = fmt.Sprintf("%s-%d", obj.ObjectMeta.Name, idx)
	}
	ruleAction := toUpper(*rule.Action)
	if ruleAction != toUpper(v1alpha1.RuleActionAllow) && ruleAction != toUpper(v1alpha1.RuleActionDrop) && ruleAction != toUpper(v1alpha1.RuleActionReject) {
		return nil, nil, errors.New("invalid rule action")
	}
	ruleDirection := toUpper(*rule.Direction)
	if ruleDirection == toUpper(v1alpha1.RuleDirectionIngress) || ruleDirection == toUpper(v1alpha1.RuleDirectionIn) {
		direction = "IN"
	} else if ruleDirection == toUpper(v1alpha1.RuleDirectionEgress) || ruleDirection == toUpper(v1alpha1.RuleDirectionOut) {
		direction = "OUT"
	} else {
		return nil, nil, errors.New("invalide rule direction")
	}

	nsxRule := model.Rule{
		Id:             &nsxRuleID,
		DisplayName:    &nsxRuleName,
		Direction:      &direction,
		SequenceNumber: &sequenceNumber,
		Action:         &ruleAction,
		Services:       []string{"ANY"},
		Tags:           service.buildBasicTags(obj),
	}

	if direction == "IN" {
		if len(rule.Sources) > 0 {
			nsxRuleSrcGroup, nsxRuleSrcGroupPath, _ = service.buildRuleSrcGroup(obj, rule, idx)
			ruleGroups = append(ruleGroups, *nsxRuleSrcGroup)
		} else {
			nsxRuleSrcGroupPath = "ANY"
		}
		nsxRuleDstGroupPath = "ANY"
	} else if direction == "OUT" {
		if len(rule.Destinations) > 0 {
			nsxRuleDstGroup, nsxRuleDstGroupPath, _ = service.buildRuleDstGroup(obj, rule, idx)
			ruleGroups = append(ruleGroups, *nsxRuleDstGroup)
		} else {
			nsxRuleDstGroupPath = "ANY"
		}
		nsxRuleSrcGroupPath = "ANY"
	}
	nsxRule.SourceGroups = []string{nsxRuleSrcGroupPath}
	nsxRule.DestinationGroups = []string{nsxRuleDstGroupPath}
	ruleServiceEntries := service.buildRuleServiceEntries(&rule.Ports)
	nsxRule.ServiceEntries = *ruleServiceEntries

	if len(rule.AppliedTo) > 0 {
		nsxRuleAppliedGroup, nsxRuleAppliedGroupPath, _ = service.buildRuleAppliedGroup(obj, rule, idx)
		ruleGroups = append(ruleGroups, *nsxRuleAppliedGroup)
	} else {
		if nsxRuleSrcGroupPath == "ANY" && nsxRuleDstGroupPath == "ANY" {
			// NSX-T manager will report error if all of the rule's scope/src/dst are "ANY"
			// TODO: raise an accurate error if both policy appliedTo and rule appliedTo are empty
			nsxRuleAppliedGroupPath = service.buildPolicyGroupPath(obj)
		} else {
			nsxRuleAppliedGroupPath = "ANY"
		}
	}
	nsxRule.Scope = []string{nsxRuleAppliedGroupPath}

	log.V(1).Info("built rule and groups", "nsxRuleAppliedGroup", nsxRuleAppliedGroup, "nsxRuleSrcGroup", nsxRuleSrcGroup, "nsxRuleDstGroup", nsxRuleDstGroup, "action", *nsxRule.Action, "direction", *nsxRule.Direction)

	return &nsxRule, &ruleGroups, nil
}

func toUpper(obj interface{}) string {
	str := fmt.Sprintf("%s", obj)
	return strings.ToUpper(str)
}

func (service *SecurityPolicyService) buildRuleID(obj *v1alpha1.SecurityPolicy, idx int) string {
	return fmt.Sprintf("sp_%s_%d", obj.UID, idx)
}

func (service *SecurityPolicyService) buildRuleAppliedGroup(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, idx int) (*model.Group, string, error) {
	var ruleAppliedGroupName string
	appliedTo := rule.AppliedTo
	ruleAppliedGroupID := fmt.Sprintf("sp_%s_%d_scope", obj.UID, idx)
	if len(rule.Name) > 0 {
		ruleAppliedGroupName = fmt.Sprintf("%s-scope", rule.Name)
	} else {
		ruleAppliedGroupName = fmt.Sprintf("%s-%d-scope", obj.ObjectMeta.Name, idx)
	}
	targetTags := service.buildTargetTags(obj, &appliedTo, idx)
	ruleAppliedGroupPath := fmt.Sprintf("/infra/domains/%s/groups/%s", service.getDomain(), ruleAppliedGroupID)
	ruleAppliedGroup := model.Group{
		Id:          &ruleAppliedGroupID,
		DisplayName: &ruleAppliedGroupName,
		Tags:        targetTags,
	}

	for i, target := range appliedTo {
		service.updateTargetExpressions(obj, &target, &ruleAppliedGroup, i)

	}
	return &ruleAppliedGroup, ruleAppliedGroupPath, nil
}

func (service *SecurityPolicyService) buildRuleSrcGroup(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, idx int) (*model.Group, string, error) {
	var ruleSrcGroupName string
	sources := rule.Sources
	ruleSrcGroupID := fmt.Sprintf("sp_%s_%d_src", obj.UID, idx)
	if len(rule.Name) > 0 {
		ruleSrcGroupName = fmt.Sprintf("%s-src", rule.Name)
	} else {
		ruleSrcGroupName = fmt.Sprintf("%s-%d-src", obj.ObjectMeta.Name, idx)
	}
	ruleSrcGroupPath := fmt.Sprintf("/infra/domains/%s/groups/%s", service.getDomain(), ruleSrcGroupID)
	peerTags := service.buildPeerTags(obj, &sources, idx)
	ruleSrcGroup := model.Group{
		Id:          &ruleSrcGroupID,
		DisplayName: &ruleSrcGroupName,
		Tags:        peerTags,
	}
	for i, peer := range sources {
		service.updatePeerExpressions(obj, &peer, &ruleSrcGroup, i)
	}
	return &ruleSrcGroup, ruleSrcGroupPath, nil
}

func (service *SecurityPolicyService) updatePeerExpressions(obj *v1alpha1.SecurityPolicy, peer *v1alpha1.SecurityPolicyPeer, group *model.Group, idx int) {
	if len(peer.IPBlocks) > 0 {
		addresses := data.NewListValue()
		for _, block := range peer.IPBlocks {
			addresses.Add(data.NewStringValue(block.CIDR))
		}
		service.appendOperatorIfNeeded(&group.Expression, "OR")

		blockExpression := data.NewStructValue(
			"",
			map[string]data.DataValue{
				"resource_type": data.NewStringValue("IPAddressExpression"),
				"ip_addresses":  addresses,
			},
		)
		group.Expression = append(group.Expression, blockExpression)
	}

	if peer.PodSelector == nil && peer.VMSelector == nil && peer.NamespaceSelector == nil {
		return
	}

	service.appendOperatorIfNeeded(&group.Expression, "OR")
	expressions := data.NewListValue()
	expressionFrame := data.NewStructValue(
		"",
		map[string]data.DataValue{
			"expressions":   expressions,
			"resource_type": data.NewStringValue("NestedExpression"),
		},
	)
	group.Expression = append(group.Expression, expressionFrame)

	clusterExpression := service.buildExpression(
		"Condition", "SegmentPort",
		fmt.Sprintf("%s|%s", util.TagScopeNCPCluster, service.getCluster()),
		"Tag", "EQUALS",
	)
	expressions.Add(clusterExpression)

	if peer.PodSelector != nil {
		service.addOperatorIfNeeded(expressions, "AND")
		podExpression := service.buildExpression(
			"Condition", "SegmentPort", util.TagScopeNCPPod, "Tag", "EQUALS")
		expressions.Add(podExpression)
		service.updatePortExpressions(peer.PodSelector.MatchLabels, expressions)
	}
	if peer.VMSelector != nil {
		service.addOperatorIfNeeded(expressions, "AND")
		vmExpression := service.buildExpression(
			"Condition", "SegmentPort", util.TagScopeNCPVNETInterface, "Tag", "EQUALS")
		expressions.Add(vmExpression)
		service.updatePortExpressions(peer.VMSelector.MatchLabels, expressions)
	}
	if peer.NamespaceSelector != nil {
		service.updateSegmentSelectorExpressions(peer.NamespaceSelector.MatchLabels, expressions)
	}
}

func (service *SecurityPolicyService) updateSegmentSelectorExpressions(matchLabels map[string]string, expressions *data.ListValue) {
	for k, v := range *util.NormalizeLabels(&matchLabels) {
		service.addOperatorIfNeeded(expressions, "AND")
		expression := service.buildExpression(
			"Condition", "Segment",
			fmt.Sprintf("%s|%s", k, v),
			"Tag", "EQUALS",
		)
		expressions.Add(expression)
	}
}

// TODO: merge buildRuleSrcGroup and buildRuleDstGroup
func (service *SecurityPolicyService) buildRuleDstGroup(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, idx int) (*model.Group, string, error) {
	var ruleDstGroupName string
	destinations := rule.Destinations
	ruleDstGroupID := fmt.Sprintf("sp_%s_%d_dst", obj.UID, idx)
	if len(rule.Name) > 0 {
		ruleDstGroupName = fmt.Sprintf("%s-dst", rule.Name)
	} else {
		ruleDstGroupName = fmt.Sprintf("%s-%d-dst", obj.ObjectMeta.Name, idx)
	}
	ruleDstGroupPath := fmt.Sprintf("/infra/domains/%s/groups/%s", service.getDomain(), ruleDstGroupID)
	peerTags := service.buildPeerTags(obj, &destinations, idx)
	ruleDstGroup := model.Group{
		Id:          &ruleDstGroupID,
		DisplayName: &ruleDstGroupName,
		Tags:        peerTags,
	}
	for i, peer := range destinations {
		service.updatePeerExpressions(obj, &peer, &ruleDstGroup, i)
	}
	return &ruleDstGroup, ruleDstGroupPath, nil
}

func (service *SecurityPolicyService) buildRuleServiceEntries(rulePorts *[]v1alpha1.SecurityPolicyPort) *[]*data.StructValue {
	ruleServiceEntries := []*data.StructValue{}
	for _, port := range *rulePorts {
		sourcePorts := data.NewListValue()
		sourcePorts.Add(data.NewIntegerValue(int64(port.Port.IntValue())))
		destinationPorts := data.NewListValue()
		// In case that the destination_port in NSX-T is 0.
		endPort := int64(port.EndPort)
		if endPort == 0 {
			endPort = int64(port.Port.IntValue())
		}
		destinationPorts.Add(data.NewIntegerValue(endPort))
		serviceEntry := data.NewStructValue(
			"",
			map[string]data.DataValue{
				"source_ports":      sourcePorts,
				"destination_ports": destinationPorts,
				"l4_protocol":       data.NewStringValue(string(port.Protocol)),
				"resource_type":     data.NewStringValue("L4PortSetServiceEntry"),
			},
		)
		ruleServiceEntries = append(ruleServiceEntries, serviceEntry)
	}
	return &ruleServiceEntries
}

func (service *SecurityPolicyService) createOrUpdateGroups(groupsClient domains.GroupsClient, nsxGroups *[]model.Group) error {
	for _, group := range *nsxGroups {
		err := groupsClient.Patch(service.getDomain(), *group.Id, group)
		if err != nil {
			log.Error(err, "failed to patch group", "group", group)
			return err
		}
		service.GroupStore.Add(group)
	}
	return nil
}

func (service *SecurityPolicyService) CreateOrUpdateSecurityPolicy(obj *v1alpha1.SecurityPolicy) error {
	policiesClient := service.NSXClient.SecurityClient
	groupsClient := service.NSXClient.GroupClient
	nsxSecurityPolicy, nsxGroups, err := service.buildSecurityPolicy(obj)
	if err != nil {
		log.Error(err, "failed to build SecurityPolicy")
		return err
	}

	if len(nsxSecurityPolicy.Scope) == 0 {
		// warning
		log.Info("SecurityPolicy %s has empty policy-level appliedTo")
	}

	indexResults, err := service.GroupStore.ByIndex(util.TagScopeSecurityPolicyCRUID, string(obj.UID))
	if err != nil {
		log.Error(err, "failed to get groups by security policy UID", "SecurityPolicyCR.UID", obj.UID)
		return err
	}
	existingGroups := []model.Group{}
	for _, group := range indexResults {
		existingGroups = append(existingGroups, group.(model.Group))
	}

	if service.groupsEqual(existingGroups, *nsxGroups) {
		log.Info("groups not changed, skipping", "nsxSecurityPolicy.Id", nsxSecurityPolicy.Id)
	} else {
		err = service.createOrUpdateGroups(groupsClient, nsxGroups)
		if err != nil {
			log.Error(err, "failed to create or update groups", "nsxSecurityPolicy.Id", nsxSecurityPolicy.Id)
			return err
		}
	}

	existingSecurityPolicy := model.SecurityPolicy{}
	res, ok, _ := service.SecurityPolicyStore.GetByKey(string(*nsxSecurityPolicy.Id))
	if ok {
		existingSecurityPolicy = res.(model.SecurityPolicy)
	}
	indexResults, err = service.RuleStore.ByIndex(util.TagScopeSecurityPolicyCRUID, string(obj.UID))
	if err != nil {
		log.Error(err, "failed to get rules by security policy UID", "SecurityPolicyCR.UID", obj.UID)
		return err
	}
	existingRules := []model.Rule{}
	for _, rule := range indexResults {
		existingRules = append(existingRules, rule.(model.Rule))
	}
	if service.securityPolicyEqual(&existingSecurityPolicy, nsxSecurityPolicy) && service.rulesEqual(existingRules, nsxSecurityPolicy.Rules) {
		log.Info("security policy not changed, skipping", "nsxSecurityPolicy.Id", nsxSecurityPolicy.Id)
	} else {
		err = policiesClient.Patch(service.getDomain(), *nsxSecurityPolicy.Id, *nsxSecurityPolicy)
		if err != nil {
			log.Error(err, "failed to patch security policy", "nsxSecurityPolicy", nsxSecurityPolicy)
			return err
		}
		service.SecurityPolicyStore.Add(*nsxSecurityPolicy)
		for _, rule := range nsxSecurityPolicy.Rules {
			service.RuleStore.Add(rule)
		}
		log.Info("successfully created or updated nsxSecurityPolicy", "nsxSecurityPolicy", nsxSecurityPolicy)
	}
	return nil
}

func (service *SecurityPolicyService) securityPolicyEqual(sp1 *model.SecurityPolicy, sp2 *model.SecurityPolicy) bool {
	v1, _ := json.Marshal(service.simplifySecurityPolicy(sp1))
	v2, _ := json.Marshal(service.simplifySecurityPolicy(sp2))
	return string(v1) == string(v2)
}

func (service *SecurityPolicyService) rulesEqual(rules1 []model.Rule, rules2 []model.Rule) bool {
	if len(rules1) != len(rules2) {
		return false
	}
	for i := 0; i < len(rules1); i++ {
		v1, _ := json.Marshal(service.simplifyRule(&rules1[i]))
		v2, _ := json.Marshal(service.simplifyRule(&rules2[i]))
		if string(v1) != string(v2) {
			return false
		}
	}
	return true
}

func (service *SecurityPolicyService) groupsEqual(groups1 []model.Group, groups2 []model.Group) bool {
	if len(groups1) != len(groups2) {
		return false
	}
	for i := 0; i < len(groups1); i++ {
		v1, _ := json.Marshal(service.simplifyGroup(&groups1[i]))
		v2, _ := json.Marshal(service.simplifyGroup(&groups2[i]))
		if string(v1) != string(v2) {
			return false
		}
	}
	return true
}

// simplifySecurityPolicy is used for abstract the key properties from model.SecurityPolicy, so that
// some unnecessary properties like "CreateTime" can be ignored then we can compare the existing one
// and disired one to determin whther the NSX-T resource should be updated.
func (service *SecurityPolicyService) simplifySecurityPolicy(sp *model.SecurityPolicy) *model.SecurityPolicy {
	return &model.SecurityPolicy{
		Id:             sp.Id,
		DisplayName:    sp.DisplayName,
		SequenceNumber: sp.SequenceNumber,
		Scope:          sp.Scope,
		Tags:           sp.Tags,
	}
}

func (service *SecurityPolicyService) simplifyRule(rule *model.Rule) *model.Rule {
	return &model.Rule{
		DisplayName:       rule.DisplayName,
		Id:                rule.Id,
		Tags:              rule.Tags,
		Direction:         rule.Direction,
		Scope:             rule.Scope,
		SequenceNumber:    rule.SequenceNumber,
		Action:            rule.Action,
		Services:          rule.Services,
		ServiceEntries:    rule.ServiceEntries,
		DestinationGroups: rule.DestinationGroups,
		SourceGroups:      rule.SourceGroups,
	}
}

func (service *SecurityPolicyService) simplifyGroup(group *model.Group) *model.Group {
	return &model.Group{
		Id:          group.Id,
		DisplayName: group.Id,
		Tags:        group.Tags,
	}
}

func (service *SecurityPolicyService) getCluster() string {
	return service.NSXConfig.Cluster
}

func (service *SecurityPolicyService) getDomain() string {
	return service.getCluster()
}

func (service *SecurityPolicyService) buildPeerTags(obj *v1alpha1.SecurityPolicy, peers *[]v1alpha1.SecurityPolicyPeer, idx int) []model.Tag {
	basicTags := service.buildBasicTags(obj)
	ruleID := service.buildRuleID(obj, idx)
	tagScopeGroupType := util.TagScopeGroupType
	tagScopeRuleID := util.TagScopeRuleID
	tagScopeSelectorHash := util.TagScopeSelectorHash
	tagValueScope := "scope"
	// TODO: abstract sort func for both peers and targets
	sort.Slice(*peers, func(i, j int) bool {
		k1, _ := json.Marshal((*peers)[i])
		k2, _ := json.Marshal((*peers)[j])
		return string(k1) < string(k2)
	})
	serializedBytes, _ := json.Marshal(*peers)
	groupHash := util.Sha1(string(serializedBytes))
	var peerTags = []model.Tag{
		{
			Scope: &tagScopeGroupType,
			Tag:   &tagValueScope,
		},
		{
			Scope: &tagScopeRuleID,
			Tag:   &ruleID,
		},
		{
			Scope: &tagScopeSelectorHash,
			Tag:   &groupHash,
		},
	}
	for _, tag := range basicTags {
		peerTags = append(peerTags, tag)
	}
	return peerTags
}

func (service *SecurityPolicyService) deleteGroup(groupsClient domains.GroupsClient, nsxGroup *model.Group) error {
	failIfSubtreeExistsParam := false
	forceParam := false
	return groupsClient.Delete(service.getDomain(), *nsxGroup.Id, &failIfSubtreeExistsParam, &forceParam)
}

func (service *SecurityPolicyService) DeleteSecurityPolicy(UID types.UID) error {
	groupsClient := service.NSXClient.GroupClient
	policiesClient := service.NSXClient.SecurityClient

	policy, err := service.SecurityPolicyStore.ByIndex(util.TagScopeSecurityPolicyCRUID, string(UID))
	if err != nil {
		log.Error(err, "failed to find NSX SecurityPolicy from store", "UID", string(UID))
		return err
	}
	if len(policy) == 0 {
		log.V(1).Info("no NSX SecurityPolicy is found", "UID", string(UID))
		return nil
	}
	nsxSecurityPolicy := policy[0].(model.SecurityPolicy)
	err = policiesClient.Delete(service.getDomain(), *nsxSecurityPolicy.Id)
	if err != nil {
		log.Error(err, "failed to delete NSX SecurityPolicy", "UID", string(UID))
		return err
	}
	service.SecurityPolicyStore.Delete(nsxSecurityPolicy)

	rules, err := service.SecurityPolicyStore.ByIndex(util.TagScopeSecurityPolicyCRUID, string(UID))
	if err != nil {
		log.Error(err, "failed to find NSX Rules from store", "UID", string(UID))
		return err
	}
	for _, rule := range rules {
		service.RuleStore.Delete(rule.(model.Rule))
	}

	groups, err := service.GroupStore.ByIndex(util.TagScopeSecurityPolicyCRUID, string(UID))
	if err != nil {
		log.Error(err, "failed to find NSX Group from store", "UID", string(UID))
		return err
	}
	if len(groups) == 0 {
		log.V(1).Info("no NSX Group is found", "UID", string(UID))
		return nil
	}
	for _, group := range groups {
		nsxGroup := group.(model.Group)
		err = service.deleteGroup(groupsClient, &nsxGroup)
		if err != nil {
			log.Error(err, "failed to delete NSX Group", "UID", string(UID))
			return err
		}
		service.GroupStore.Delete(nsxGroup)
	}

	log.Info("successfully deleted NSX SecurityPolicy", "nsxSecurityPolicy", nsxSecurityPolicy)
	return nil
}

func (service *SecurityPolicyService) ListSecurityPolicy() sets.String {
	groups := service.GroupStore.ListIndexFuncValues(util.TagScopeSecurityPolicyCRUID)
	groupSet := sets.NewString()
	for _, group := range groups {
		groupSet.Insert(group)
	}
	securityPolicies := service.SecurityPolicyStore.ListIndexFuncValues(util.TagScopeSecurityPolicyCRUID)
	policySet := sets.NewString()
	for _, policy := range securityPolicies {
		policySet.Insert(policy)
	}
	return groupSet.Union(policySet)
}
