package securitypolicy

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
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

var (
	String = common.String
	Int64  = common.Int64
)

func (service *SecurityPolicyService) buildSecurityPolicy(obj *v1alpha1.SecurityPolicy) (*model.SecurityPolicy, *[]model.Group, error) {
	var nsxRules []model.Rule
	var nsxGroups []model.Group

	contains := func(groups []model.Group, group model.Group) bool {
		for _, a := range groups {
			if *a.Id == *group.Id {
				return true
			}
		}
		return false
	}

	log.V(1).Info("building the model SecurityPolicy from CR SecurityPolicy", "object", *obj)
	nsxSecurityPolicy := &model.SecurityPolicy{}

	nsxSecurityPolicy.Id = String(fmt.Sprintf("sp_%s", obj.UID))
	nsxSecurityPolicy.DisplayName = String(fmt.Sprintf("%s-%s", obj.ObjectMeta.Namespace, obj.ObjectMeta.Name))

	// TODO: confirm the sequence number: offset
	nsxSecurityPolicy.SequenceNumber = Int64(int64(obj.Spec.Priority))

	policyGroup, policyGroupPath, err := service.buildPolicyGroup(obj)
	if err != nil {
		log.Error(err, "failed to build policy group", "policy", *obj)
		return nil, nil, err
	}

	nsxSecurityPolicy.Scope = []string{policyGroupPath}
	if policyGroup != nil {
		nsxGroups = append(nsxGroups, *policyGroup)
	}

	for ruleIdx, rule := range obj.Spec.Rules {
		// A rule containing named port may expand to multiple rules if the name maps to multiple port numbers.
		expandRules, ruleGroups, err := service.buildRuleAndGroups(obj, &rule, ruleIdx)
		if err != nil {
			log.Error(err, "failed to build rule and groups", "rule", rule, "ruleIndex", ruleIdx)
			return nil, nil, err
		}
		for _, nsxRule := range expandRules {
			if nsxRule != nil {
				nsxRules = append(nsxRules, *nsxRule)
			}
		}
		for _, ruleGroup := range ruleGroups {
			if ruleGroup != nil {
				// If the group is already in the list, skip it.
				if !contains(nsxGroups, *ruleGroup) {
					nsxGroups = append(nsxGroups, *ruleGroup)
				}
			}
		}
	}
	nsxSecurityPolicy.Rules = nsxRules
	nsxSecurityPolicy.Tags = service.buildBasicTags(obj)
	log.V(1).Info("built nsxSecurityPolicy", "nsxSecurityPolicy", nsxSecurityPolicy, "nsxGroups", nsxGroups)
	return nsxSecurityPolicy, &nsxGroups, nil
}

func (service *SecurityPolicyService) buildPolicyGroup(obj *v1alpha1.SecurityPolicy) (*model.Group, string, error) {
	policyGroup := model.Group{}

	policyGroup.Id = String(service.buildPolicyGroupID(obj))

	// TODO: have a common function to generate ID and Name with parameters like prefix, suffix
	policyGroup.DisplayName = String(fmt.Sprintf("%s-%s-scope", obj.ObjectMeta.Namespace, obj.ObjectMeta.Name))

	appliedTo := obj.Spec.AppliedTo
	targetTags := service.buildTargetTags(obj, &appliedTo, -1)
	policyGroup.Tags = targetTags
	if len(appliedTo) == 0 {
		return nil, "ANY", nil
	}

	targetGroupCount, targetGroupTotalExprCount := 0, 0
	criteriaCount, totalExprCount := 0, 0
	var err error = nil
	errorMsg := ""
	for i, target := range appliedTo {
		criteriaCount, totalExprCount, err = service.updateTargetExpressions(
			obj,
			&target,
			&policyGroup,
			i,
		)
		if err == nil {
			targetGroupCount += criteriaCount
			targetGroupTotalExprCount += totalExprCount
		} else {
			return nil, "", err
		}
	}
	log.V(2).Info("build policy target group criteria", "total criteria",
		targetGroupCount, "total expressions of criteria", targetGroupTotalExprCount)

	if targetGroupCount > MaxCriteria {
		errorMsg = fmt.Sprintf(
			"total counts of policy target group criteria %d exceed NSX limit of %d",
			targetGroupCount,
			MaxCriteria,
		)
	} else if targetGroupTotalExprCount > MaxTotalCriteriaExpressions {
		errorMsg = fmt.Sprintf("total expression counts in policy target group criteria %d exceed NSX limit of %d",
			targetGroupTotalExprCount, MaxTotalCriteriaExpressions)
	}

	if len(errorMsg) != 0 {
		err = errors.New(errorMsg)
		return nil, "", err
	}

	policyGroupPath := service.buildPolicyGroupPath(obj)
	return &policyGroup, policyGroupPath, nil
}

func (service *SecurityPolicyService) buildTargetTags(obj *v1alpha1.SecurityPolicy, targets *[]v1alpha1.SecurityPolicyTarget, idx int) []model.Tag {
	basicTags := service.buildBasicTags(obj)
	sort.Slice(*targets, func(i, j int) bool {
		k1, _ := json.Marshal((*targets)[i])
		k2, _ := json.Marshal((*targets)[j])
		return string(k1) < string(k2)
	})
	serializedBytes, _ := json.Marshal(*targets)
	targetTags := []model.Tag{
		{
			Scope: String(common.TagScopeGroupType),
			Tag:   String("scope"),
		},
		{
			Scope: String(common.TagScopeSelectorHash),
			Tag:   String(util.Sha1(string(serializedBytes))),
		},
	}
	for _, tag := range basicTags {
		targetTags = append(targetTags, tag)
	}
	if idx != -1 {
		// the appliedTo group belongs to a rule, so it needs a tag including the rule id
		targetTags = append(targetTags,
			model.Tag{
				Scope: String(common.TagScopeRuleID),
				Tag:   String(service.buildRuleID(obj, idx)),
			},
		)
	}
	return targetTags
}

func (service *SecurityPolicyService) buildBasicTags(obj *v1alpha1.SecurityPolicy) []model.Tag {
	tags := []model.Tag{
		{
			Scope: String(common.TagScopeCluster),
			Tag:   String(getCluster(service)),
		},
		{
			Scope: String(common.TagScopeNamespace),
			Tag:   String(obj.ObjectMeta.Namespace),
		},
		// TODO: get namespace uid
		{
			Scope: String(common.TagScopeSecurityPolicyCRName),
			Tag:   String(obj.ObjectMeta.Name),
		},
		{
			Scope: String(common.TagScopeSecurityPolicyCRUID),
			Tag:   String(string(obj.UID)),
		},
	}
	return tags
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

func (service *SecurityPolicyService) buildGroupExpression(policyExpression *[]*data.StructValue) *data.ListValue {
	expressions := data.NewListValue()
	expressionFrame := data.NewStructValue(
		"",
		map[string]data.DataValue{
			"expressions":   expressions,
			"resource_type": data.NewStringValue("NestedExpression"),
		},
	)
	*policyExpression = append(*policyExpression, expressionFrame)
	return expressions
}

func (service *SecurityPolicyService) buildExpression(resource_type, member_type, value, key, operator, scope_op string) *data.StructValue {
	var expression *data.StructValue
	if scope_op == "NOTEQUALS" {
		// when scope_op is "NOTEQUALS", the tag operator and value field will not be used
		expression = data.NewStructValue(
			"",
			map[string]data.DataValue{
				"resource_type":  data.NewStringValue(resource_type),
				"member_type":    data.NewStringValue(member_type),
				"value":          data.NewStringValue(value),
				"key":            data.NewStringValue(key),
				"scope_operator": data.NewStringValue(scope_op),
			},
		)
	} else {
		expression = data.NewStructValue(
			"",
			map[string]data.DataValue{
				"resource_type":  data.NewStringValue(resource_type),
				"member_type":    data.NewStringValue(member_type),
				"value":          data.NewStringValue(value),
				"key":            data.NewStringValue(key),
				"operator":       data.NewStringValue(operator),
				"scope_operator": data.NewStringValue(scope_op),
			},
		)
	}
	return expression
}

func (service *SecurityPolicyService) buildExpressionsMatchExpression(matchExpressions []v1.LabelSelectorRequirement, memberType string, expressions *data.ListValue) error {
	var err error = nil
	errorMsg := ""

	for _, expr := range matchExpressions {
		switch expr.Operator {
		case v1.LabelSelectorOpIn:
			continue

		case v1.LabelSelectorOpNotIn:
			service.addOperatorIfNeeded(expressions, "AND")
			joinValues := strings.Join(expr.Values[:], ",")

			expression := service.buildExpression(
				"Condition", memberType,
				fmt.Sprintf("%s|%s", expr.Key, joinValues),
				"Tag", "NOTIN", "EQUALS",
			)
			expressions.Add(expression)

		case v1.LabelSelectorOpExists:
			service.addOperatorIfNeeded(expressions, "AND")
			expression := service.buildExpression(
				"Condition", memberType,
				fmt.Sprintf("%s|", expr.Key),
				"Tag", "EQUALS", "EQUALS",
			)
			expressions.Add(expression)

		case v1.LabelSelectorOpDoesNotExist:
			service.addOperatorIfNeeded(expressions, "AND")
			expression := service.buildExpression(
				"Condition", memberType,
				fmt.Sprintf("%s|", expr.Key),
				"Tag", "", "NOTEQUALS",
			)
			expressions.Add(expression)

		default:
			errorMsg = fmt.Sprintf("invalid operator %s in matchExpressions", expr.Operator)
		}
	}

	if len(errorMsg) != 0 {
		err = errors.New(errorMsg)
	}
	return err
}

func (service *SecurityPolicyService) buildPolicyGroupID(obj *v1alpha1.SecurityPolicy) string {
	return fmt.Sprintf("sp_%s_scope", obj.UID)
}

func (service *SecurityPolicyService) buildPolicyGroupPath(obj *v1alpha1.SecurityPolicy) string {
	return fmt.Sprintf("/infra/domains/%s/groups/%s", getDomain(service), service.buildPolicyGroupID(obj))
}

func (service *SecurityPolicyService) buildRuleAndGroups(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, ruleIdx int) ([]*model.Rule, []*model.Group, error) {
	var ruleGroups []*model.Group
	var nsxRuleAppliedGroup *model.Group
	var nsxRuleSrcGroup *model.Group
	var nsxRuleDstGroup *model.Group
	var nsxRuleAppliedGroupPath string
	var nsxRuleDstGroupPath string
	var nsxRuleSrcGroupPath string
	var err error

	ruleDirection, err := getRuleDirection(rule)
	if err != nil {
		return nil, nil, err
	}

	// Since a named port may map to multiple port numbers, then it would return multiple rules.
	// We use the destination port number of service entry to group the rules.
	ipSetGroups, nsxRules, err := service.expandRule(obj, rule, ruleIdx)
	if err != nil {
		return nil, nil, err
	}
	for _, g := range ipSetGroups {
		ruleGroups = append(ruleGroups, g)
	}

	for _, nsxRule := range nsxRules {
		if ruleDirection == "IN" {
			nsxRuleSrcGroup, nsxRuleSrcGroupPath, nsxRuleDstGroupPath, err = service.buildRuleInGroup(
				obj,
				rule,
				nsxRule,
				ruleIdx,
			)
			if err != nil {
				return nil, nil, err
			}
			ruleGroups = append(ruleGroups, nsxRuleSrcGroup)
		} else if ruleDirection == "OUT" {
			nsxRuleDstGroup, nsxRuleSrcGroupPath, nsxRuleDstGroupPath, err = service.buildRuleOutGroup(obj, rule, nsxRule, ruleIdx)
			if err != nil {
				return nil, nil, err
			}
			ruleGroups = append(ruleGroups, nsxRuleDstGroup)
		}
		nsxRule.SourceGroups = []string{nsxRuleSrcGroupPath}
		nsxRule.DestinationGroups = []string{nsxRuleDstGroupPath}

		nsxRuleAppliedGroup, nsxRuleAppliedGroupPath, err = service.buildRuleAppliedToGroup(
			obj,
			rule,
			ruleIdx,
			nsxRuleSrcGroupPath,
			nsxRuleDstGroupPath,
		)
		if err != nil {
			return nil, nil, err
		}
		ruleGroups = append(ruleGroups, nsxRuleAppliedGroup)
		nsxRule.Scope = []string{nsxRuleAppliedGroupPath}

		log.V(2).Info("built rule and groups", "nsxRuleAppliedGroup", nsxRuleAppliedGroup,
			"~", nsxRuleSrcGroup, "nsxRuleDstGroup", nsxRuleDstGroup,
			"action", *nsxRule.Action, "direction", *nsxRule.Direction)
	}
	return nsxRules, ruleGroups, nil
}

func (service *SecurityPolicyService) buildRuleServiceEntries(port v1alpha1.SecurityPolicyPort, portAddress nsxutil.PortAddress) *data.StructValue {
	var portRange string
	sourcePorts := data.NewListValue()
	destinationPorts := data.NewListValue()

	// In case that the destination_port in NSX-T is 0.
	endPort := port.EndPort
	if endPort == 0 {
		portRange = fmt.Sprint(portAddress.Port)
	} else {
		portRange = fmt.Sprintf("%d-%d", portAddress.Port, endPort)
	}
	destinationPorts.Add(data.NewStringValue(portRange))

	serviceEntry := data.NewStructValue(
		"",
		map[string]data.DataValue{
			"source_ports":      sourcePorts,
			"destination_ports": destinationPorts,
			"l4_protocol":       data.NewStringValue(string(port.Protocol)),
			"resource_type":     data.NewStringValue("L4PortSetServiceEntry"),
			// Adding the following default values to make it easy when compare the
			// existing object from store and the new built object
			"marked_for_delete": data.NewBooleanValue(false),
			"overridden":        data.NewBooleanValue(false),
		},
	)
	log.V(2).Info("built service entry", "serviceEntry", serviceEntry)
	return serviceEntry
}

func (service *SecurityPolicyService) buildRuleAppliedToGroup(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, ruleIdx int, nsxRuleSrcGroupPath string, nsxRuleDstGroupPath string) (*model.Group, string, error) {
	var nsxRuleAppliedGroup *model.Group
	var nsxRuleAppliedGroupPath string
	var err error
	if len(rule.AppliedTo) > 0 {
		nsxRuleAppliedGroup, nsxRuleAppliedGroupPath, err = service.buildRuleAppliedGroupByRule(
			obj,
			rule,
			ruleIdx,
		)
		if err != nil {
			return nil, "", err
		}
	} else {
		nsxRuleAppliedGroupPath, err = service.buildRuleAppliedGroupByPolicy(obj,
			nsxRuleSrcGroupPath, nsxRuleDstGroupPath)
		if err != nil {
			return nil, "", err
		}
	}
	return nsxRuleAppliedGroup, nsxRuleAppliedGroupPath, nil
}

func (service *SecurityPolicyService) buildRuleInGroup(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, nsxRule *model.Rule, ruleIdx int) (*model.Group, string, string, error) {
	var nsxRuleSrcGroup *model.Group
	var nsxRuleSrcGroupPath string
	var nsxRuleDstGroupPath string
	var err error
	if len(rule.Sources) > 0 {
		nsxRuleSrcGroup, nsxRuleSrcGroupPath, err = service.buildRuleSrcGroup(obj, rule, ruleIdx)
		if err != nil {
			return nil, "", "", err
		}
	} else {
		nsxRuleSrcGroupPath = "ANY"
	}

	if len(nsxRule.DestinationGroups) > 0 {
		nsxRuleDstGroupPath = nsxRule.DestinationGroups[0]
	} else {
		nsxRuleDstGroupPath = "ANY"
	}
	return nsxRuleSrcGroup, nsxRuleSrcGroupPath, nsxRuleDstGroupPath, nil
}

func (service *SecurityPolicyService) buildRuleOutGroup(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, nsxRule *model.Rule, ruleIdx int) (*model.Group, string, string, error) {
	var nsxRuleDstGroup *model.Group
	var nsxRuleSrcGroupPath string
	var nsxRuleDstGroupPath string
	var err error
	if len(nsxRule.DestinationGroups) > 0 {
		nsxRuleDstGroupPath = nsxRule.DestinationGroups[0]
	} else {
		if len(rule.Destinations) > 0 {
			nsxRuleDstGroup, nsxRuleDstGroupPath, err = service.buildRuleDstGroup(obj, rule, ruleIdx)
			if err != nil {
				return nil, "", "", err
			}
		} else {
			nsxRuleDstGroupPath = "ANY"
		}
	}
	nsxRuleSrcGroupPath = "ANY"
	return nsxRuleDstGroup, nsxRuleSrcGroupPath, nsxRuleDstGroupPath, nil
}

func (service *SecurityPolicyService) buildRuleID(obj *v1alpha1.SecurityPolicy, idx int) string {
	return fmt.Sprintf("sp_%s_%d", obj.UID, idx)
}

func (service *SecurityPolicyService) buildRuleName(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, idx int) string {
	if len(rule.Name) > 0 {
		return rule.Name
	} else {
		return fmt.Sprintf("%s-%d", obj.ObjectMeta.Name, idx)
	}
}

func (service *SecurityPolicyService) buildRuleAppliedGroupByPolicy(obj *v1alpha1.SecurityPolicy, nsxRuleSrcGroupPath string, nsxRuleDstGroupPath string) (string, error) {
	var nsxRuleAppliedGroupPath string
	if len(obj.Spec.AppliedTo) == 0 {
		return "", errors.New("appliedTo needs to be set in either spec or rules")
	}
	if nsxRuleSrcGroupPath == "ANY" && nsxRuleDstGroupPath == "ANY" {
		// NSX-T manager will report error if all the rule's scope/src/dst are "ANY".
		// So if the rule's scope is empty while policy's not, the rule's scope also
		// will be set to the policy's scope to avoid this case.
		nsxRuleAppliedGroupPath = service.buildPolicyGroupPath(obj)
	} else {
		nsxRuleAppliedGroupPath = "ANY"
	}
	return nsxRuleAppliedGroupPath, nil
}

func (service *SecurityPolicyService) buildRuleAppliedGroupByRule(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, idx int) (*model.Group, string, error) {
	var ruleAppliedGroupName string
	appliedTo := rule.AppliedTo
	ruleAppliedGroupID := fmt.Sprintf("sp_%s_%d_scope", obj.UID, idx)
	if len(rule.Name) > 0 {
		ruleAppliedGroupName = fmt.Sprintf("%s-scope", rule.Name)
	} else {
		ruleAppliedGroupName = fmt.Sprintf("%s-%d-scope", obj.ObjectMeta.Name, idx)
	}
	targetTags := service.buildTargetTags(obj, &appliedTo, idx)
	ruleAppliedGroupPath := fmt.Sprintf("/infra/domains/%s/groups/%s", getDomain(service), ruleAppliedGroupID)
	ruleAppliedGroup := model.Group{
		Id:          &ruleAppliedGroupID,
		DisplayName: &ruleAppliedGroupName,
		Tags:        targetTags,
	}

	ruleGroupCount, ruleGroupTotalExprCount := 0, 0
	criteriaCount, totalExprCount := 0, 0
	var err error = nil
	errorMsg := ""
	for i, target := range appliedTo {
		criteriaCount, totalExprCount, err = service.updateTargetExpressions(
			obj,
			&target,
			&ruleAppliedGroup,
			i,
		)
		if err == nil {
			ruleGroupCount += criteriaCount
			ruleGroupTotalExprCount += totalExprCount
		} else {
			return nil, "", err
		}
	}
	log.V(2).Info("build rule applied group criteria", "total criteria", ruleGroupCount, "total expressions of criteria", ruleGroupTotalExprCount)

	if ruleGroupCount > MaxCriteria {
		errorMsg = fmt.Sprintf(
			"total counts of rule applied group criteria %d exceed NSX limit of %d",
			ruleGroupCount,
			MaxCriteria,
		)
	} else if ruleGroupTotalExprCount > MaxTotalCriteriaExpressions {
		errorMsg = fmt.Sprintf("total expression counts in rule applied group criteria %d exceed NSX limit of %d", ruleGroupTotalExprCount, MaxTotalCriteriaExpressions)
	}

	if len(errorMsg) != 0 {
		err = errors.New(errorMsg)
		return nil, "", err
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
	ruleSrcGroupPath := fmt.Sprintf("/infra/domains/%s/groups/%s", getDomain(service), ruleSrcGroupID)
	peerTags := service.BuildPeerTags(obj, &sources, idx)
	ruleSrcGroup := model.Group{
		Id:          &ruleSrcGroupID,
		DisplayName: &ruleSrcGroupName,
		Tags:        peerTags,
	}

	ruleSrcGroupCount, ruleSrcGroupTotalExprCount := 0, 0
	criteriaCount, totalExprCount := 0, 0
	var err error = nil
	errorMsg := ""
	for i, peer := range sources {
		criteriaCount, totalExprCount, err = service.updatePeerExpressions(
			obj,
			&peer,
			&ruleSrcGroup,
			i,
		)
		if err == nil {
			ruleSrcGroupCount += criteriaCount
			ruleSrcGroupTotalExprCount += totalExprCount
		} else {
			return nil, "", err
		}
	}
	log.V(2).Info("build rule source group criteria", "total criteria", ruleSrcGroupCount, "total expressions of criteria", ruleSrcGroupTotalExprCount)

	if ruleSrcGroupCount > MaxCriteria {
		errorMsg = fmt.Sprintf(
			"total counts of rule source group criteria %d exceed NSX limit of %d",
			ruleSrcGroupCount,
			MaxCriteria,
		)
	} else if ruleSrcGroupTotalExprCount > MaxTotalCriteriaExpressions {
		errorMsg = fmt.Sprintf("total expression counts in source group criteria %d exceed NSX limit of %d", ruleSrcGroupTotalExprCount, MaxTotalCriteriaExpressions)
	}

	if len(errorMsg) != 0 {
		err = errors.New(errorMsg)
		return nil, "", err
	}

	return &ruleSrcGroup, ruleSrcGroupPath, err
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
	ruleDstGroupPath := fmt.Sprintf("/infra/domains/%s/groups/%s", getDomain(service), ruleDstGroupID)
	peerTags := service.BuildPeerTags(obj, &destinations, idx)
	ruleDstGroup := model.Group{
		Id:          &ruleDstGroupID,
		DisplayName: &ruleDstGroupName,
		Tags:        peerTags,
	}

	ruleDstGroupCount, ruleDstGroupTotalExprCount := 0, 0
	criteriaCount, totalExprCount := 0, 0
	var err error = nil
	errorMsg := ""
	for i, peer := range destinations {
		criteriaCount, totalExprCount, err = service.updatePeerExpressions(
			obj,
			&peer,
			&ruleDstGroup,
			i,
		)
		if err == nil {
			ruleDstGroupCount += criteriaCount
			ruleDstGroupTotalExprCount += totalExprCount
		} else {
			return nil, "", err
		}
	}
	log.V(2).Info("build rule destination group criteria", "total criteria", ruleDstGroupCount, "total expressions of criteria", ruleDstGroupTotalExprCount)

	if ruleDstGroupCount > MaxCriteria {
		errorMsg = fmt.Sprintf(
			"total counts of rule destination group criteria %d exceed NSX limit of %d",
			ruleDstGroupCount,
			MaxCriteria,
		)
	} else if ruleDstGroupTotalExprCount > MaxTotalCriteriaExpressions {
		errorMsg = fmt.Sprintf("total expression counts in rule destination group criteria %d exceed NSX limit of %d", ruleDstGroupTotalExprCount, MaxTotalCriteriaExpressions)
	}

	if len(errorMsg) != 0 {
		err = errors.New(errorMsg)
		return nil, "", err
	}
	return &ruleDstGroup, ruleDstGroupPath, err
}

// Build rule basic info, ruleIdx is the index of the rules of security policy,
// portIdx is the index of rule's ports, portAddressIdx is the index
// of multiple port number if one named port maps to multiple port numbers.
func (service *SecurityPolicyService) buildRuleBasicInfo(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, ruleIdx int, portIdx int, portAddressIdx int) (*model.Rule, error) {
	ruleAction, err := getRuleAction(rule)
	if err != nil {
		return nil, err
	}
	ruleDirection, err := getRuleDirection(rule)
	if err != nil {
		return nil, err
	}

	nsxRule := model.Rule{
		Id:             String(fmt.Sprintf("%s_%d_%d", service.buildRuleID(obj, ruleIdx), portIdx, portAddressIdx)),
		DisplayName:    String(fmt.Sprintf("%s-%d-%d", service.buildRuleName(obj, rule, ruleIdx), portIdx, portAddressIdx)),
		Direction:      &ruleDirection,
		SequenceNumber: Int64(int64(ruleIdx)),
		Action:         &ruleAction,
		Services:       []string{"ANY"},
		Tags:           service.buildBasicTags(obj),
	}
	log.V(1).Info("built rule basic info", "nsxRule", nsxRule)
	return &nsxRule, nil
}

func (service *SecurityPolicyService) BuildPeerTags(obj *v1alpha1.SecurityPolicy, peers *[]v1alpha1.SecurityPolicyPeer, idx int) []model.Tag {
	basicTags := service.buildBasicTags(obj)
	// TODO: abstract sort func for both peers and targets
	sort.Slice(*peers, func(i, j int) bool {
		k1, _ := json.Marshal((*peers)[i])
		k2, _ := json.Marshal((*peers)[j])
		return string(k1) < string(k2)
	})
	serializedBytes, _ := json.Marshal(*peers)
	peerTags := []model.Tag{
		{
			Scope: String(common.TagScopeGroupType),
			Tag:   String("scope"),
		},
		{
			Scope: String(common.TagScopeRuleID),
			Tag:   String(service.buildRuleID(obj, idx)),
		},
		{
			Scope: String(common.TagScopeSelectorHash),
			Tag:   String(util.Sha1(string(serializedBytes))),
		},
	}
	for _, tag := range basicTags {
		peerTags = append(peerTags, tag)
	}
	return peerTags
}

func (service *SecurityPolicyService) updateTargetExpressions(obj *v1alpha1.SecurityPolicy, target *v1alpha1.SecurityPolicyTarget, group *model.Group, idx int) (int, int, error) {
	var err error = nil
	var tagValueExpression *data.StructValue = nil
	memberType := "SegmentPort"
	var matchLabels map[string]string
	var matchExpressions *[]v1.LabelSelectorRequirement = nil
	var mergedMatchExpressions *[]v1.LabelSelectorRequirement = nil
	opInValueCount, totalCriteriaCount, totalExprCount := 0, 0, 0
	matchLabelsCount, matchExpressionsCount := 0, 0

	if target.PodSelector != nil && target.VMSelector != nil {
		errorMsg := "PodSelector and VMSelector are not allowed to set in one group"
		err = errors.New(errorMsg)
		return 0, 0, err
	}

	log.V(2).Info("build target expressions", "index", idx)
	service.appendOperatorIfNeeded(&group.Expression, "OR")
	expressions := service.buildGroupExpression(&group.Expression)

	// Setting cluster member type to "Segment" for PodSelector and VMSelector ensure the criteria is mixed
	// because the following conditions must have condition whose memberType=SegmentPort
	clusterExpression := service.buildExpression(
		"Condition", "Segment",
		fmt.Sprintf("%s|%s", common.TagScopeNCPCluster, getCluster(service)),
		"Tag", "EQUALS", "EQUALS",
	)
	expressions.Add(clusterExpression)

	if target.PodSelector != nil {
		service.addOperatorIfNeeded(expressions, "AND")
		// TODO: consider to use project_uid instead of project
		nsExpression := service.buildExpression(
			"Condition", memberType,
			fmt.Sprintf("%s|%s", common.TagScopeNCPProject, obj.ObjectMeta.Namespace),
			"Tag", "EQUALS", "EQUALS",
		)
		expressions.Add(nsExpression)

		tagValueExpression = nsExpression
		matchLabels = target.PodSelector.MatchLabels
		matchExpressions = &target.PodSelector.MatchExpressions
	}
	if target.VMSelector != nil {
		service.addOperatorIfNeeded(expressions, "AND")
		nsExpression := service.buildExpression(
			"Condition", memberType,
			fmt.Sprintf("%s|%s", common.TagScopeNCPVIFProject, obj.ObjectMeta.Namespace),
			"Tag", "EQUALS", "EQUALS",
		)
		expressions.Add(nsExpression)

		tagValueExpression = nsExpression
		matchLabels = target.VMSelector.MatchLabels
		matchExpressions = &target.VMSelector.MatchExpressions
	}
	if target.PodSelector != nil || target.VMSelector != nil {
		service.updateExpressionsMatchLabels(matchLabels, memberType, expressions)
		matchLabelsCount = len(matchLabels)
		// PodSelector or VMSelector has two more built-in labels
		matchLabelsCount += ClusterTagCount + ProjectTagCount

		if matchExpressions != nil {
			mergedMatchExpressions = service.mergeSelectorMatchExpression(*matchExpressions)
			matchExpressionsCount = len(*mergedMatchExpressions)
			opInValueCount, err = service.validateSelectorOpIn(*mergedMatchExpressions, matchLabels)

			if err != nil {
				return 0, 0, err
			}
			err = service.updateExpressionsMatchExpression(*mergedMatchExpressions, matchLabels,
				&group.Expression, clusterExpression, tagValueExpression, memberType, expressions)
			if err != nil {
				return 0, 0, err
			}
		}

		// Since cluster is set as default "Segment" memberType, So the final produced group criteria is always treated as a mixed criteria
		totalCriteriaCount, totalExprCount, err = service.validateSelectorExpressions(
			matchLabelsCount,
			matchExpressionsCount,
			opInValueCount,
			true,
		)
		if err != nil {
			return 0, 0, err
		}
	}
	return totalCriteriaCount, totalExprCount, nil
}

func (service *SecurityPolicyService) appendOperatorIfNeeded(policyExpression *[]*data.StructValue, op string) {
	if len(*policyExpression) > 0 {
		operator := service.buildConjOperator(op)
		*policyExpression = append(*policyExpression, operator)
	}
}

func (service *SecurityPolicyService) addOperatorIfNeeded(expressions *data.ListValue, op string) {
	if !expressions.IsEmpty() {
		operator := service.buildConjOperator(op)
		expressions.Add(operator)
	}
}

func (service *SecurityPolicyService) updateExpressionsMatchLabels(matchLabels map[string]string, memberType string, expressions *data.ListValue) {
	for k, v := range *util.NormalizeLabels(&matchLabels) {
		service.addOperatorIfNeeded(expressions, "AND")
		expression := service.buildExpression(
			"Condition", memberType,
			fmt.Sprintf("%s|%s", k, v),
			"Tag", "EQUALS", "EQUALS",
		)
		expressions.Add(expression)
	}
}

// NSX understand the multiple values w.r.t a key in a joined string manner
// this function iterates over input matchExpressions LabelSelectorRequirement
// with same operator and Key, and merges them into one and values to a joined string
// e.g.
// - {key: k1, operator: NotIn, values: [a1, a2, a3]}
// - {key: k1, operator: NotIn, values: [a2, a3, a4]}
//  => {key: k1, operator: NotIn, values: [a1, a2, a3, a4]}
func (service *SecurityPolicyService) mergeSelectorMatchExpression(matchExpressions []v1.LabelSelectorRequirement) *[]v1.LabelSelectorRequirement {
	mergedMatchExpressions := make([]v1.LabelSelectorRequirement, 0)
	var mergedSelector v1.LabelSelectorRequirement
	labelSelectorMap := map[v1.LabelSelectorOperator]map[string][]string{}

	for _, d := range matchExpressions {
		_, exists := labelSelectorMap[d.Operator]
		if !exists {
			labelSelectorMap[d.Operator] = map[string][]string{}
		}
		_, exists = labelSelectorMap[d.Operator][d.Key]
		labelSelectorMap[d.Operator][d.Key] = append(
			labelSelectorMap[d.Operator][d.Key],
			d.Values...)

		if exists {
			labelSelectorMap[d.Operator][d.Key] = util.RemoveDuplicateStr(
				labelSelectorMap[d.Operator][d.Key],
			)
		}
	}

	for key, value := range labelSelectorMap {
		for subKey, subValue := range value {
			mergedSelector.Values = subValue
			mergedSelector.Operator = key
			mergedSelector.Key = subKey
			mergedMatchExpressions = append(mergedMatchExpressions, mergedSelector)
		}
	}

	return &mergedMatchExpressions
}

// Todo, refactor code when NSX support 'In' LabelSelector.
// Given NSX currently doesn't support 'In' LabelSelector, to keep design simple,
// only allow just one 'In' LabelSelector in matchExpressions with at most of five values in it.
func (service *SecurityPolicyService) validateSelectorOpIn(matchExpressions []v1.LabelSelectorRequirement, matchLabels map[string]string) (int, error) {
	mexprInOpCount := 0
	mexprInValueCount := 0
	var err error = nil
	errorMsg := ""
	exists := false
	var opInIndex int

	for i, expr := range matchExpressions {
		if expr.Operator == v1.LabelSelectorOpIn {
			_, exists = matchLabels[expr.Key]
			if exists {
				opInIndex = i
			}
			mexprInOpCount++
			mexprInValueCount += len(expr.Values)
		}
	}
	if mexprInOpCount > MaxMatchExpressionInOp {
		errorMsg = fmt.Sprintf("count of operator 'In' expressions %d exceed limit of %d",
			mexprInOpCount, MaxMatchExpressionIn)
	} else if mexprInValueCount > MaxMatchExpressionInValues {
		errorMsg = fmt.Sprintf("count of values list for operator 'In' expressions %d exceed limit of %d",
			mexprInValueCount, MaxMatchExpressionInValues)
	} else if exists {
		// matchLabels can only be duplicated with matchExpressions operator 'In' expression
		// Since only operator 'In' is equivalent to key-value condition
		for _, value := range matchExpressions[opInIndex].Values {
			if matchLabels[matchExpressions[opInIndex].Key] == value {
				errorMsg = fmt.Sprintf("duplicate expression - %s:%s specified in both matchLabels and matchExpressions operator 'In'",
					matchExpressions[opInIndex].Key, value)
				break
			}
		}
	}

	if len(errorMsg) != 0 {
		err = errors.New(errorMsg)
	}
	return mexprInValueCount, err
}

func (service *SecurityPolicyService) validateNsSelectorOpNotIn(nsMatchExpressions []v1.LabelSelectorRequirement) error {
	var err error = nil
	errorMsg := ""

	for _, expr := range nsMatchExpressions {
		if expr.Operator == v1.LabelSelectorOpNotIn {
			errorMsg = "operator 'NotIn' for NamespaceSelector is not supported in NSX-T since its member type is Segment"
			err = errors.New(errorMsg)
			break
		}
	}
	return err
}

func (service *SecurityPolicyService) validateSelectorExpressions(matchLabelsCount int, matchExpressionsCount int, opInValueCount int, mixedCriteria bool) (int, int, error) {
	var err error = nil
	errorMsg := ""
	totalExprCount := 0
	totalCriteria := 0

	// Check total count of expressions from LabelSelectors in one group criteria
	if matchExpressionsCount != 0 {
		totalExprCount = matchLabelsCount + matchExpressionsCount
	} else {
		totalExprCount = matchLabelsCount
	}

	if !mixedCriteria && totalExprCount > MaxCriteriaExpressions {
		errorMsg = fmt.Sprintf(
			"total count of labelSelectors expressions %d exceed NSX limit of %d in one criteria based on same member type",
			totalExprCount,
			MaxCriteriaExpressions,
		)
	} else if mixedCriteria && totalExprCount > MaxMixedCriteriaExpressions {
		errorMsg = fmt.Sprintf("total count of labelSelectors expressions %d exceed NSX limit of %d in one criteria inside a mixed member type",
			totalExprCount, MaxMixedCriteriaExpressions)
	}

	if len(errorMsg) != 0 {
		err = errors.New(errorMsg)
		return 0, 0, err
	}

	// Compute total expression counts of final produced criteria
	if matchLabelsCount != 0 || matchExpressionsCount != 0 {
		if opInValueCount != 0 {
			totalCriteria = opInValueCount
			totalExprCount *= opInValueCount
		} else {
			// matchExpressions will be 'AND' with matchLabels(if present) to produce 1 criteria.
			totalCriteria = 1
		}
	}
	return totalCriteria, totalExprCount, err
}

// Todo, refactor code when NSX support 'In' LabelSelector.
func (service *SecurityPolicyService) matchExpressionOpInExist(
	matchExpressions []v1.LabelSelectorRequirement,
) (bool, int) {
	operatorInIndex := -1
	isFound := false
	for i := 0; i < len(matchExpressions); i++ {
		// find operator 'In'
		if matchExpressions[i].Operator == v1.LabelSelectorOpIn {
			operatorInIndex = i
			isFound = true
			break
		}
	}
	return isFound, operatorInIndex
}

// Todo, refactor code when NSX support 'In' LabelSelector.
// Currently NSX only supports 'EQUALS' not 'In'. So, we have to make each value to be AND with other expressions
// and finally produce a union set to translate from K8s 'In' to NSX EQUALS'.
// e.g. - {key: k1, operator: NotIn, values: [a1,a2]}
//      - {key: k2, operator: In, values: [a3,a4]}
// The above two expressions will be translated to:
// => {k1 NotIn [a1,a2]} AND {k2 EQUALS a3} OR {k1 NotIn [a1,a2]} AND {k2 EQUALS a4}
func (service *SecurityPolicyService) updateExpressionsMatchExpression(matchExpressions []v1.LabelSelectorRequirement, matchLabels map[string]string,
	policyExpression *[]*data.StructValue, clusterExpression *data.StructValue, tagValueExpression *data.StructValue, memberType string, expressions *data.ListValue,
) error {
	var err error = nil
	found, opInIdx := service.matchExpressionOpInExist(matchExpressions)
	if !found {
		err = service.buildExpressionsMatchExpression(matchExpressions, memberType, expressions)
	} else {
		expr := matchExpressions[opInIdx]
		for i := 0; i < len(expr.Values); i++ {
			if i != 0 {
				service.appendOperatorIfNeeded(policyExpression, "OR")
				expressions = service.buildGroupExpression(policyExpression)

				if clusterExpression != nil {
					expressions.Add(clusterExpression)
				}
				if tagValueExpression != nil {
					if clusterExpression != nil {
						service.addOperatorIfNeeded(expressions, "AND")
					}
					expressions.Add(tagValueExpression)
				}
				service.updateExpressionsMatchLabels(matchLabels, memberType, expressions)
			}

			service.addOperatorIfNeeded(expressions, "AND")
			expression := service.buildExpression(
				"Condition", memberType,
				fmt.Sprintf("%s|%s", expr.Key, expr.Values[i]),
				"Tag", "EQUALS", "EQUALS",
			)
			expressions.Add(expression)
			err = service.buildExpressionsMatchExpression(matchExpressions, memberType, expressions)
			if err != nil {
				break
			}
		}
	}
	return err
}

// Todo, refactor code when NSX support 'In' LabelSelector.
// Support Pod/VM Selector mixed with NamespaceSelector
func (service *SecurityPolicyService) updateMixedExpressionsMatchExpression(nsMatchExpressions []v1.LabelSelectorRequirement,
	nsMatchLabels map[string]string, matchExpressions []v1.LabelSelectorRequirement, matchLabels map[string]string,
	policyExpression *[]*data.StructValue, clusterExpression *data.StructValue, tagValueExpression *data.StructValue,
	expressions *data.ListValue,
) error {
	var err error = nil
	opInIdx := 0
	var opInMatchExpressions []v1.LabelSelectorRequirement = nil
	memberType := ""

	nsFound, opInIdx1 := service.matchExpressionOpInExist(nsMatchExpressions)
	portFound, opInIdx2 := service.matchExpressionOpInExist(matchExpressions)

	if nsFound && portFound {
		errorMsg := "operator 'In' is set in both Pod/VM selector and NamespaceSelector"
		err = errors.New(errorMsg)
		return err
	}

	if nsFound {
		opInIdx = opInIdx1
		memberType = "Segment"
		opInMatchExpressions = nsMatchExpressions
	} else if portFound {
		opInIdx = opInIdx2
		memberType = "SegmentPort"
		opInMatchExpressions = matchExpressions
	}

	if !nsFound && !portFound {
		err = service.buildExpressionsMatchExpression(matchExpressions, "SegmentPort", expressions)
		if err == nil {
			err = service.buildExpressionsMatchExpression(
				nsMatchExpressions,
				"Segment",
				expressions,
			)
		}
	} else {
		expr := opInMatchExpressions[opInIdx]
		for i := 0; i < len(expr.Values); i++ {
			if i != 0 {
				service.appendOperatorIfNeeded(policyExpression, "OR")
				expressions = service.buildGroupExpression(policyExpression)

				if clusterExpression != nil {
					expressions.Add(clusterExpression)
				}
				if tagValueExpression != nil {
					if clusterExpression != nil {
						service.addOperatorIfNeeded(expressions, "AND")
					}
					expressions.Add(tagValueExpression)
				}

				service.updateExpressionsMatchLabels(matchLabels, "SegmentPort", expressions)
				service.updateExpressionsMatchLabels(nsMatchLabels, "Segment", expressions)
			}

			if nsFound {
				err = service.buildExpressionsMatchExpression(matchExpressions, "SegmentPort", expressions)
			} else {
				err = service.buildExpressionsMatchExpression(nsMatchExpressions, "Segment", expressions)
			}
			if err != nil {
				break
			}

			service.addOperatorIfNeeded(expressions, "AND")
			expression := service.buildExpression(
				"Condition", memberType,
				fmt.Sprintf("%s|%s", expr.Key, expr.Values[i]),
				"Tag", "EQUALS", "EQUALS",
			)
			expressions.Add(expression)

			err = service.buildExpressionsMatchExpression(opInMatchExpressions, memberType, expressions)
			if err != nil {
				break
			}
		}
	}
	return err
}

func (service *SecurityPolicyService) updatePeerExpressions(obj *v1alpha1.SecurityPolicy, peer *v1alpha1.SecurityPolicyPeer, group *model.Group, idx int) (int, int, error) {
	var err error = nil
	errorMsg := ""
	var tagValueExpression *data.StructValue = nil
	var memberType string
	var matchLabels map[string]string
	var matchExpressions *[]v1.LabelSelectorRequirement = nil
	var mergedMatchExpressions *[]v1.LabelSelectorRequirement = nil
	opInValueCount, totalCriteriaCount, totalExprCount := 0, 0, 0
	matchLabelsCount, matchExpressionsCount := 0, 0
	mixedNsSelector := false

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

	log.V(2).Info("update peer expressions", "index", idx)
	if peer.PodSelector == nil && peer.VMSelector == nil && peer.NamespaceSelector == nil {
		return 0, 0, nil
	} else if peer.PodSelector != nil && peer.VMSelector != nil && peer.NamespaceSelector == nil {
		errorMsg = "PodSelector and VMSelector are not allowed to set in one group"
	} else if peer.PodSelector != nil && peer.VMSelector != nil && peer.NamespaceSelector != nil {
		errorMsg = "PodSelector, VMSelector and NamespaceSelector are not allowed to set in one group"
	}

	if len(errorMsg) != 0 {
		err = errors.New(errorMsg)
		return 0, 0, err
	}

	service.appendOperatorIfNeeded(&group.Expression, "OR")
	expressions := service.buildGroupExpression(&group.Expression)

	// Setting cluster member type to "Segment" for PodSelector and VMSelector ensure the criteria is mixed
	// because the following conditions must have condition whose memberType=SegmentPort
	clusterMemberType := "Segment"
	// Setting cluster member type to "SegmentPort" for NamespaceSelector ensure the criteria is mixed
	// because the following conditions must have condition whose memberType=Segment when NamespaceSelector isn't empty
	if peer.PodSelector == nil && peer.VMSelector == nil && peer.NamespaceSelector != nil &&
		peer.NamespaceSelector.Size() > 0 {
		clusterMemberType = "SegmentPort"
	}

	clusterExpression := service.buildExpression(
		"Condition", clusterMemberType,
		fmt.Sprintf("%s|%s", common.TagScopeNCPCluster, getCluster(service)),
		"Tag", "EQUALS", "EQUALS",
	)
	expressions.Add(clusterExpression)

	if peer.PodSelector != nil {
		memberType = "SegmentPort"
		service.addOperatorIfNeeded(expressions, "AND")
		podExpression := service.buildExpression(
			"Condition",
			memberType,
			fmt.Sprintf("%s|", common.TagScopeNCPPod),
			"Tag",
			"EQUALS",
			"EQUALS",
		)

		if peer.NamespaceSelector == nil {
			podExpression = service.buildExpression(
				"Condition", memberType,
				fmt.Sprintf("%s|%s", common.TagScopeNCPProject, obj.ObjectMeta.Namespace),
				"Tag", "EQUALS", "EQUALS")
			mixedNsSelector = false
		} else {
			mixedNsSelector = true
		}

		expressions.Add(podExpression)
		tagValueExpression = podExpression
		matchLabels = peer.PodSelector.MatchLabels
		matchExpressions = &peer.PodSelector.MatchExpressions
		matchLabelsCount = len(matchLabels)
		// PodSelector has two more built-in labels
		matchLabelsCount += ClusterTagCount + ProjectTagCount
	}
	if peer.VMSelector != nil {
		memberType = "SegmentPort"
		service.addOperatorIfNeeded(expressions, "AND")
		vmExpression := service.buildExpression(
			"Condition",
			memberType,
			fmt.Sprintf("%s|", common.TagScopeNCPVNETInterface),
			"Tag",
			"EQUALS",
			"EQUALS",
		)

		if peer.NamespaceSelector == nil {
			vmExpression = service.buildExpression(
				"Condition", memberType,
				fmt.Sprintf("%s|%s", common.TagScopeNCPVIFProject, obj.ObjectMeta.Namespace),
				"Tag", "EQUALS", "EQUALS")
			mixedNsSelector = false
		} else {
			mixedNsSelector = true
		}

		expressions.Add(vmExpression)
		tagValueExpression = vmExpression
		matchLabels = peer.VMSelector.MatchLabels
		matchExpressions = &peer.VMSelector.MatchExpressions
		matchLabelsCount = len(matchLabels)
		// VMSelector has two more built-in labels
		matchLabelsCount += ClusterTagCount + ProjectTagCount
	}
	if peer.NamespaceSelector != nil {
		if !mixedNsSelector {
			if peer.NamespaceSelector.Size() == 0 {
				// Since expressions list in model.Group must follow criteria from NSX-T:
				// 1. A non-empty expression list, must be of odd size
				// 2. An expression list size is equal to or greater than 3
				// 3. In a list, with indices starting from 0, all non-conjunction expressions must be at even indices
				// Hence, add one more SegmentPort member condition to meet the criteria aforementioned
				service.addOperatorIfNeeded(expressions, "AND")
				clusterSegPortExpression := service.buildExpression(
					"Condition", "SegmentPort",
					fmt.Sprintf("%s|%s", common.TagScopeNCPCluster, getCluster(service)),
					"Tag", "EQUALS", "EQUALS",
				)
				expressions.Add(clusterSegPortExpression)
				matchLabelsCount = ClusterTagCount + 1
				matchExpressionsCount = 0
			} else {
				tagValueExpression = nil
				memberType = "Segment"
				matchLabels = peer.NamespaceSelector.MatchLabels
				matchExpressions = &peer.NamespaceSelector.MatchExpressions
				// NamespaceSelector has one more built-in labels
				matchLabelsCount = len(matchLabels) + ClusterTagCount
			}
		} else { // Handle PodSelector or VMSelector mixed with NamespaceSelector
			memberType = "Segment"
			nsMatchLabels := peer.NamespaceSelector.MatchLabels
			nsMatchExpressions := &peer.NamespaceSelector.MatchExpressions

			err = service.validateNsSelectorOpNotIn(*nsMatchExpressions)
			if err != nil {
				return 0, 0, err
			}

			// Validate expressions for POD/VM Selectors
			mergedMatchExpressions = service.mergeSelectorMatchExpression(*matchExpressions)
			opInValueCount, err = service.validateSelectorOpIn(*mergedMatchExpressions, matchLabels)

			nsMergedMatchExpressions := service.mergeSelectorMatchExpression(*nsMatchExpressions)
			nsOpInValCount, opErr := service.validateSelectorOpIn(*nsMergedMatchExpressions, nsMatchLabels)

			if err != nil || opErr != nil {
				return 0, 0, err
			}

			if opInValueCount > 0 && nsOpInValCount > 0 {
				errorMsg = "operator 'In' is set in both Pod/VM selector and NamespaceSelector"
				err = errors.New(errorMsg)
				return 0, 0, err
			}

			matchLabelsCount += len(nsMatchLabels)
			matchExpressionsCount = len(*mergedMatchExpressions) + len(*nsMergedMatchExpressions)
			opInValueCount += nsOpInValCount

			service.updateExpressionsMatchLabels(matchLabels, "SegmentPort", expressions)
			service.updateExpressionsMatchLabels(nsMatchLabels, memberType, expressions)

			// NamespaceSelector AND with PodSelector or VMSelector expressions to produce final expressions
			err = service.updateMixedExpressionsMatchExpression(*nsMergedMatchExpressions, nsMatchLabels,
				*matchExpressions, matchLabels, &group.Expression, clusterExpression, tagValueExpression, expressions)

			if err != nil {
				return 0, 0, err
			}
		}
	}

	if peer.PodSelector != nil || peer.VMSelector != nil || peer.NamespaceSelector != nil {
		if !mixedNsSelector {
			service.updateExpressionsMatchLabels(matchLabels, memberType, expressions)

			if matchExpressions != nil {
				if peer.NamespaceSelector != nil {
					err = service.validateNsSelectorOpNotIn(*matchExpressions)
					if err != nil {
						return 0, 0, err
					}
				}

				mergedMatchExpressions = service.mergeSelectorMatchExpression(*matchExpressions)
				matchExpressionsCount = len(*mergedMatchExpressions)
				opInValueCount, err = service.validateSelectorOpIn(
					*mergedMatchExpressions,
					matchLabels,
				)

				if err != nil {
					return 0, 0, err
				}

				err = service.updateExpressionsMatchExpression(
					*mergedMatchExpressions,
					matchLabels,
					&group.Expression,
					clusterExpression,
					tagValueExpression,
					memberType,
					expressions,
				)
				if err != nil {
					return 0, 0, err
				}
			}
		}

		// Since cluster is set as "Segment" or "SegmentPort" memberType, So the final produced group criteria is always treated as a mixed criteria
		totalCriteriaCount, totalExprCount, err = service.validateSelectorExpressions(
			matchLabelsCount,
			matchExpressionsCount,
			opInValueCount,
			true,
		)
		if err != nil {
			return 0, 0, err
		}
	}

	return totalCriteriaCount, totalExprCount, nil
}
