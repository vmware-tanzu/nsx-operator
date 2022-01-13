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
	GroupStore          cache.Indexer
	SecurityPolicyStore cache.Indexer
	RuleStore           cache.Indexer
}

var (
	log = logf.Log.WithName("service").WithName("firewall")
)

// InitializeSecurityPolicy sync NSX resources
func InitializeSecurityPolicy(NSXClient *nsx.Client) (*SecurityPolicyService, error) {
	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)

	wg.Add(3)
	service := &SecurityPolicyService{NSXClient: NSXClient}
	service.GroupStore = cache.NewIndexer(keyFunc, cache.Indexers{util.TagScopeNamespace: namespaceIndexFunc, util.TagScopeSecurityPolicyCRUID: objectCRUIDScopeIndexFunc})
	service.SecurityPolicyStore = cache.NewIndexer(keyFunc, cache.Indexers{util.TagScopeSecurityPolicyCRUID: objectCRUIDScopeIndexFunc})
	service.RuleStore = cache.NewIndexer(keyFunc, cache.Indexers{util.TagScopeSecurityPolicyCRUID: objectCRUIDScopeIndexFunc})

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

func buildSecurityPolicy(obj *v1alpha1.SecurityPolicy) (*model.SecurityPolicy, *[]model.Group, error) {
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

	policyGroup, policyGroupPath, _ := buildPolicyGroup(obj)
	nsxSecurityPolicy.Scope = []string{policyGroupPath}
	nsxGroups = append(nsxGroups, *policyGroup)

	rules := obj.Spec.Rules

	for i, rule := range rules {
		nsxRule, ruleGroups, err := buildRuleAndGroups(obj, &rule, i)
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
	tags := buildBasicTags(obj)
	nsxSecurityPolicy.Tags = tags
	log.V(1).Info("built nsxSecurityPolicy", "nsxSecurityPolicy", nsxSecurityPolicy)
	return nsxSecurityPolicy, &nsxGroups, nil
}

func buildPolicyGroup(obj *v1alpha1.SecurityPolicy) (*model.Group, string, error) {
	policyGroup := model.Group{}

	policyGroupID := fmt.Sprintf("sp_%s_scope", obj.UID)
	policyGroup.Id = &policyGroupID

	// TODO: have a common function to generate ID and Name with parameters like prefix, suffix
	policyGroupName := fmt.Sprintf("%s-%s-scope", obj.ObjectMeta.Namespace, obj.ObjectMeta.Name)
	policyGroup.DisplayName = &policyGroupName

	appliedTo := obj.Spec.AppliedTo
	targetTags := buildTargetTags(obj, &appliedTo, -1)
	policyGroup.Tags = targetTags

	for i, target := range appliedTo {
		updateTargetExpressions(obj, &target, &policyGroup, i)
	}

	policyGroupPath := fmt.Sprintf("/infra/domains/%s/groups/%s", getDomain(), policyGroupID)
	return &policyGroup, policyGroupPath, nil
}

func buildTargetTags(obj *v1alpha1.SecurityPolicy, targets *[]v1alpha1.SecurityPolicyTarget, idx int) []model.Tag {
	basicTags := buildBasicTags(obj)
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
		ruleID := buildRuleID(obj, idx)
		targetTags = append(targetTags,
			model.Tag{
				Scope: &tagScopeRuleID,
				Tag:   &ruleID,
			},
		)
	}
	return targetTags
}

func buildBasicTags(obj *v1alpha1.SecurityPolicy) []model.Tag {
	uid := string(obj.UID)
	clusterTag := getCluster()
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

func updateTargetExpressions(obj *v1alpha1.SecurityPolicy, target *v1alpha1.SecurityPolicyTarget, group *model.Group, idx int) {
	appendOperatorIfNeeded(&group.Expression, "OR")
	expressions := data.NewListValue()
	expressionFrame := data.NewStructValue(
		"",
		map[string]data.DataValue{
			"expressions":   expressions,
			"resource_type": data.NewStringValue("NestedExpression"),
		},
	)
	group.Expression = append(group.Expression, expressionFrame)

	clusterExpression := buildExpression(
		"Condition", "SegmentPort",
		fmt.Sprintf("%s|%s", util.TagScopeNCPCluster, getCluster()),
		"Tag", "EQUALS",
	)
	expressions.Add(clusterExpression)
	if target.PodSelector != nil {
		addOperatorIfNeeded(expressions, "AND")
		// TODO: consider to use project_uid instead of project
		nsExpression := buildExpression(
			"Condition", "SegmentPort",
			fmt.Sprintf("%s|%s", util.TagScopeNCPProject, obj.ObjectMeta.Namespace),
			"Tag", "EQUALS",
		)
		expressions.Add(nsExpression)
		updatePortExpressions(target.PodSelector.MatchLabels, expressions)
	}
	if target.VMSelector != nil {
		addOperatorIfNeeded(expressions, "AND")
		nsExpression := buildExpression(
			"Condition", "SegmentPort",
			fmt.Sprintf("%s|%s", util.TagScopeNCPVIFProject, obj.ObjectMeta.Namespace),
			"Tag", "EQUALS",
		)
		expressions.Add(nsExpression)
		updatePortExpressions(target.VMSelector.MatchLabels, expressions)
	}
}

func appendOperatorIfNeeded(policyExpression *[]*data.StructValue, op string) {
	if len(*policyExpression) > 0 {
		operator := buildConjOperator(op)
		*policyExpression = append(*policyExpression, operator)
	}
}

func buildConjOperator(op string) *data.StructValue {
	operator := data.NewStructValue(
		"",
		map[string]data.DataValue{
			"conjunction_operator": data.NewStringValue(op),
			"resource_type":        data.NewStringValue("ConjunctionOperator"),
		},
	)
	return operator
}

func buildExpression(resource_type, member_type, value, key, operator string) *data.StructValue {
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

func addOperatorIfNeeded(expressions *data.ListValue, op string) {
	if !expressions.IsEmpty() {
		operator := buildConjOperator(op)
		expressions.Add(operator)
	}
}

func updatePortExpressions(matchLabels map[string]string, expressions *data.ListValue) {
	for k, v := range *util.NormalizeLabels(&matchLabels) {
		addOperatorIfNeeded(expressions, "AND")
		expression := buildExpression(
			"Condition", "SegmentPort",
			fmt.Sprintf("%s|%s", k, v),
			"Tag", "EQUALS",
		)
		expressions.Add(expression)
	}
}

func buildRuleAndGroups(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, idx int) (*model.Rule, *[]model.Group, error) {
	var direction string
	sequenceNumber := int64(idx)
	nsxRuleID := buildRuleID(obj, idx)
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
	ruleAction := strings.ToUpper(string(*rule.Action))
	if ruleAction != "ALLOW" && ruleAction != "DROP" && ruleAction != "REJECT" {
		return nil, nil, errors.New("invalid rule action")
	}
	if *rule.Direction == v1alpha1.RuleDirectionIngress || *rule.Direction == v1alpha1.RuleDirectionIn {
		direction = "IN"
	} else if *rule.Direction == v1alpha1.RuleDirectionEgress || *rule.Direction == v1alpha1.RuleDirectionOut {
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
		Tags:           buildBasicTags(obj),
	}

	if len(rule.AppliedTo) > 0 {
		nsxRuleAppliedGroup, nsxRuleAppliedGroupPath, _ = buildRuleAppliedGroup(obj, rule, idx)
		ruleGroups = append(ruleGroups, *nsxRuleAppliedGroup)
	} else {
		nsxRuleAppliedGroupPath = "ANY"
	}
	nsxRule.Scope = []string{nsxRuleAppliedGroupPath}

	if direction == "IN" {
		if len(rule.Sources) > 0 {
			nsxRuleSrcGroup, nsxRuleSrcGroupPath, _ = buildRuleSrcGroup(obj, rule, idx)
			ruleGroups = append(ruleGroups, *nsxRuleSrcGroup)
		} else {
			nsxRuleSrcGroupPath = "ANY"
		}
		nsxRuleDstGroupPath = "ANY"
	} else if direction == "OUT" {
		if len(rule.Destinations) > 0 {
			nsxRuleDstGroup, nsxRuleDstGroupPath, _ = buildRuleDstGroup(obj, rule, idx)
			ruleGroups = append(ruleGroups, *nsxRuleDstGroup)
		} else {
			nsxRuleDstGroupPath = "ANY"
		}
		nsxRuleSrcGroupPath = "ANY"
	}
	nsxRule.SourceGroups = []string{nsxRuleSrcGroupPath}
	nsxRule.DestinationGroups = []string{nsxRuleDstGroupPath}
	ruleServiceEntries := buildRuleServiceEntries(&rule.Ports)
	nsxRule.ServiceEntries = *ruleServiceEntries

	log.V(1).Info("built rule and groups", "nsxRuleAppliedGroup", nsxRuleAppliedGroup, "nsxRuleSrcGroup", nsxRuleSrcGroup, "nsxRuleDstGroup", nsxRuleDstGroup, "action", *nsxRule.Action, "direction", *nsxRule.Direction)

	return &nsxRule, &ruleGroups, nil
}

func buildRuleID(obj *v1alpha1.SecurityPolicy, idx int) string {
	return fmt.Sprintf("sp_%s_%d", obj.UID, idx)
}

func buildRuleAppliedGroup(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, idx int) (*model.Group, string, error) {
	var ruleAppliedGroupName string
	appliedTo := rule.AppliedTo
	ruleAppliedGroupID := fmt.Sprintf("sp_%s_%d_scope", obj.UID, idx)
	if len(rule.Name) > 0 {
		ruleAppliedGroupName = fmt.Sprintf("%s-scope", rule.Name)
	} else {
		ruleAppliedGroupName = fmt.Sprintf("%s-%d-scope", obj.ObjectMeta.Name, idx)
	}
	targetTags := buildTargetTags(obj, &appliedTo, idx)
	ruleAppliedGroupPath := fmt.Sprintf("/infra/domains/%s/groups/%s", getDomain(), ruleAppliedGroupID)
	ruleAppliedGroup := model.Group{
		Id:          &ruleAppliedGroupID,
		DisplayName: &ruleAppliedGroupName,
		Tags:        targetTags,
	}

	for i, target := range appliedTo {
		updateTargetExpressions(obj, &target, &ruleAppliedGroup, i)

	}
	return &ruleAppliedGroup, ruleAppliedGroupPath, nil
}

func buildRuleSrcGroup(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, idx int) (*model.Group, string, error) {
	var ruleSrcGroupName string
	sources := rule.Sources
	ruleSrcGroupID := fmt.Sprintf("sp_%s_%d_src", obj.UID, idx)
	if len(rule.Name) > 0 {
		ruleSrcGroupName = fmt.Sprintf("%s-src", rule.Name)
	} else {
		ruleSrcGroupName = fmt.Sprintf("%s-%d-src", obj.ObjectMeta.Name, idx)
	}
	ruleSrcGroupPath := fmt.Sprintf("/infra/domains/%s/groups/%s", getDomain(), ruleSrcGroupID)
	peerTags := buildPeerTags(obj, &sources, idx)
	ruleSrcGroup := model.Group{
		Id:          &ruleSrcGroupID,
		DisplayName: &ruleSrcGroupName,
		Tags:        peerTags,
	}
	for i, peer := range sources {
		updatePeerExpressions(obj, &peer, &ruleSrcGroup, i)
	}
	return &ruleSrcGroup, ruleSrcGroupPath, nil
}

func updatePeerExpressions(obj *v1alpha1.SecurityPolicy, peer *v1alpha1.SecurityPolicyPeer, group *model.Group, idx int) {
	appendOperatorIfNeeded(&group.Expression, "OR")
	expressions := data.NewListValue()
	expressionFrame := data.NewStructValue(
		"",
		map[string]data.DataValue{
			"expressions":   expressions,
			"resource_type": data.NewStringValue("NestedExpression"),
		},
	)
	group.Expression = append(group.Expression, expressionFrame)

	clusterExpression := buildExpression(
		"Condition", "SegmentPort",
		fmt.Sprintf("%s|%s", util.TagScopeNCPCluster, getCluster()),
		"Tag", "EQUALS",
	)
	expressions.Add(clusterExpression)

	if peer.PodSelector != nil {
		addOperatorIfNeeded(expressions, "AND")
		podExpression := buildExpression(
			"Condition", "SegmentPort", util.TagScopeNCPPod, "Tag", "EQUALS")
		expressions.Add(podExpression)
		updatePortExpressions(peer.PodSelector.MatchLabels, expressions)
	}
	if peer.VMSelector != nil {
		addOperatorIfNeeded(expressions, "AND")
		vmExpression := buildExpression(
			"Condition", "SegmentPort", util.TagScopeNCPVNETInterface, "Tag", "EQUALS")
		expressions.Add(vmExpression)
		updatePortExpressions(peer.VMSelector.MatchLabels, expressions)
	}
	if peer.NamespaceSelector != nil {
		updateSegmentSelectorExpressions(peer.NamespaceSelector.MatchLabels, expressions)
	}
}

func updateSegmentSelectorExpressions(matchLabels map[string]string, expressions *data.ListValue) {
	for k, v := range *util.NormalizeLabels(&matchLabels) {
		addOperatorIfNeeded(expressions, "AND")
		expression := buildExpression(
			"Condition", "Segment",
			fmt.Sprintf("%s|%s", k, v),
			"Tag", "EQUALS",
		)
		expressions.Add(expression)
	}
}

// TODO: merge buildRuleSrcGroup and buildRuleDstGroup
func buildRuleDstGroup(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, idx int) (*model.Group, string, error) {
	var ruleDstGroupName string
	destinations := rule.Destinations
	ruleDstGroupID := fmt.Sprintf("sp_%s_%d_dst", obj.UID, idx)
	if len(rule.Name) > 0 {
		ruleDstGroupName = fmt.Sprintf("%s-dst", rule.Name)
	} else {
		ruleDstGroupName = fmt.Sprintf("%s-%d-dst", obj.ObjectMeta.Name, idx)
	}
	ruleDstGroupPath := fmt.Sprintf("/infra/domains/%s/groups/%s", getDomain(), ruleDstGroupID)
	peerTags := buildPeerTags(obj, &destinations, idx)
	ruleDstGroup := model.Group{
		Id:          &ruleDstGroupID,
		DisplayName: &ruleDstGroupName,
		Tags:        peerTags,
	}
	for i, peer := range destinations {
		updatePeerExpressions(obj, &peer, &ruleDstGroup, i)
	}
	return &ruleDstGroup, ruleDstGroupPath, nil
}

func buildRuleServiceEntries(rulePorts *[]v1alpha1.SecurityPolicyPort) *[]*data.StructValue {
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

func createOrUpdateGroups(groupsClient domains.GroupsClient, nsxGroups *[]model.Group) error {
	for _, group := range *nsxGroups {
		err := groupsClient.Patch(getDomain(), *group.Id, group)
		if err != nil {
			log.Error(err, "failed to patch group", "group", group)
			return err
		}
	}
	return nil
}

func (service *SecurityPolicyService) CreateOrUpdateSecurityPolicy(obj *v1alpha1.SecurityPolicy) error {
	policiesClient := service.NSXClient.SecurityClient
	groupsClient := service.NSXClient.GroupClient
	nsxSecurityPolicy, nsxGroups, err := buildSecurityPolicy(obj)
	if err != nil {
		log.Error(err, "failed to build SecurityPolicy")
		return err
	}

	if len(nsxSecurityPolicy.Scope) == 0 {
		// warning
		log.Info("SecurityPolicy %s has empty policy-level appliedTo")
	}

	err = createOrUpdateGroups(groupsClient, nsxGroups)
	if err != nil {
		log.Error(err, "failed to create or update groups", "nsxSecurityPolicy.Id", nsxSecurityPolicy.Id)
		return err
	}

	err = policiesClient.Patch(getDomain(), *nsxSecurityPolicy.Id, *nsxSecurityPolicy)
	if err != nil {
		log.Error(err, "failed to patch security policy", "nsxSecurityPolicy", nsxSecurityPolicy)
		return err
	}
	log.Info("successfully created or updated nsxSecurityPolicy", "nsxSecurityPolicy", nsxSecurityPolicy)
	return nil
}

func getCluster() string {
	// TODO: get cluster from global variable instead of reading from file
	cf, _ := config.NewNSXOperatorConfigFromFile()
	return cf.Cluster
}

func getDomain() string {
	return getCluster()
}

func buildPeerTags(obj *v1alpha1.SecurityPolicy, peers *[]v1alpha1.SecurityPolicyPeer, idx int) []model.Tag {
	basicTags := buildBasicTags(obj)
	ruleID := buildRuleID(obj, idx)
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
		model.Tag{
			Scope: &tagScopeGroupType,
			Tag:   &tagValueScope,
		},
		model.Tag{
			Scope: &tagScopeRuleID,
			Tag:   &ruleID,
		},
		model.Tag{
			Scope: &tagScopeSelectorHash,
			Tag:   &groupHash,
		},
	}
	for _, tag := range basicTags {
		peerTags = append(peerTags, tag)
	}
	return peerTags
}

func deleteGroups(groupsClient domains.GroupsClient, nsxGroups *[]model.Group) error {
	failIfSubtreeExistsParam := false
	forceParam := false
	for _, group := range *nsxGroups {
		err := groupsClient.Delete(getDomain(), *group.Id, &failIfSubtreeExistsParam, &forceParam)
		if err != nil {
			log.Error(err, "failed to delete group", "group", group)
			return err
		}
	}
	return nil
}

func (service *SecurityPolicyService) DeleteSecurityPolicy(key types.UID) error {
	groupsClient := service.NSXClient.GroupClient
	policiesClient := service.NSXClient.SecurityClient

	policy, err := service.SecurityPolicyStore.ByIndex(util.TagScopeSecurityPolicyCRUID, string(key))
	if err != nil {
		log.Error(err, "failed to find SecurityPolicy", "ID", key)
		return err
	}
	nsxSecurityPolicy := policy[0].(model.SecurityPolicy)
	err = policiesClient.Delete(getDomain(), *nsxSecurityPolicy.Id)
	if err != nil {
		log.Error(err, "failed to delete security policy", "nsxSecurityPolicy", nsxSecurityPolicy)
		return err
	}
	service.SecurityPolicyStore.Delete(nsxSecurityPolicy)

	rules, err := service.SecurityPolicyStore.ByIndex(util.TagScopeSecurityPolicyCRUID, string(key))
	if err != nil {
		log.Error(err, "failed to find Rules", "ID", key)
		return err
	}
	for _, rule := range rules {
		service.RuleStore.Delete(rule.(model.Rule))
	}

	groups, err := service.GroupStore.ByIndex(util.TagScopeSecurityPolicyCRUID, string(key))
	if err != nil {
		log.Error(err, "failed to find Group", "ID", string(key))
		return err
	}

	var nsxGroups []model.Group
	for _, group := range groups {
		nsxGroups = append(nsxGroups, group.(model.Group))
	}
	err = deleteGroups(groupsClient, &nsxGroups)
	if err != nil {
		log.Error(err, "failed to delete groups", "nsxSecurityPolicy.Id", string(key))
		return err
	}
	for _, group := range nsxGroups {
		service.GroupStore.Delete(group)
	}

	log.Info("successfully deleted nsxSecurityPolicy", "nsxSecurityPolicy", nsxSecurityPolicy)
	return nil
}

func (service *SecurityPolicyService) ListSecurityPolicy() (sets.String, error) {
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
	return groupSet.Union(policySet), nil
}
