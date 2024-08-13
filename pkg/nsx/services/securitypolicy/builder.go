package securitypolicy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
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
	NameSpaceTagCount           int = 1
)

var (
	String = common.String
	Int64  = common.Int64
)

func (service *SecurityPolicyService) buildSecurityPolicyName(obj *v1alpha1.SecurityPolicy, createdFor string) string {
	if isVpcEnabled(service) {
		// For VPC scenario, we use obj.Name as the NSX resource display name for both SecurityPolicy and NetworkPolicy.
		return util.GenerateTruncName(common.MaxNameLength, obj.Name, "", "", "", "")

	}
	prefix := common.SecurityPolicyPrefix
	if createdFor != common.ResourceTypeSecurityPolicy {
		prefix = common.NetworkPolicyPrefix
	}
	// For T1 scenario, we use ns-name as the key resource name for SecurityPolicy, it is to be consistent with the
	// previous solutions.
	return util.GenerateTruncName(common.MaxNameLength, fmt.Sprintf("%s-%s", obj.Namespace, obj.Name), prefix, "", "", "")
}

func (service *SecurityPolicyService) buildSecurityPolicyID(obj *v1alpha1.SecurityPolicy, createdFor string) string {
	if isVpcEnabled(service) {
		return util.GenerateIDByObject(obj)
	}
	prefix := common.SecurityPolicyPrefix
	if createdFor != common.ResourceTypeSecurityPolicy {
		prefix = common.NetworkPolicyPrefix
	}
	nsxSecurityPolicyID := util.GenerateID(string(obj.UID), prefix, "", "")
	return nsxSecurityPolicyID
}

func (service *SecurityPolicyService) buildSecurityPolicy(obj *v1alpha1.SecurityPolicy, createdFor string) (*model.SecurityPolicy, *[]model.Group, *[]ProjectShare, error) {
	var nsxRules []model.Rule
	var nsxGroups []model.Group
	var nsxProjectGroups []model.Group
	var nsxProjectShares []model.Share
	var projectShares []ProjectShare

	log.V(1).Info("building the model SecurityPolicy from CR SecurityPolicy", "object", *obj)
	nsxSecurityPolicy := &model.SecurityPolicy{}

	nsxSecurityPolicy.Id = String(service.buildSecurityPolicyID(obj, createdFor))
	nsxSecurityPolicy.DisplayName = String(service.buildSecurityPolicyName(obj, createdFor))
	// TODO: confirm the sequence number: offset
	nsxSecurityPolicy.SequenceNumber = Int64(int64(obj.Spec.Priority))

	policyGroup, policyGroupPath, err := service.buildPolicyGroup(obj, createdFor)
	if err != nil {
		log.Error(err, "failed to build policy group", "policy", *obj)
		return nil, nil, nil, err
	}

	nsxSecurityPolicy.Scope = []string{policyGroupPath}
	if policyGroup != nil {
		nsxGroups = append(nsxGroups, *policyGroup)
	}
	currentSet := sets.Set[string]{}
	for ruleIdx, r := range obj.Spec.Rules {
		rule := r
		// A rule containing named port may expand to multiple rules if the name maps to multiple port numbers.
		expandRules, buildGroups, buildProjectShares, err := service.buildRuleAndGroups(obj, &rule, ruleIdx, createdFor)
		if err != nil {
			log.Error(err, "failed to build rule and groups", "rule", rule, "ruleIndex", ruleIdx)
			return nil, nil, nil, err
		}

		for _, nsxRule := range expandRules {
			if nsxRule != nil {
				if !currentSet.Has(*nsxRule.Id) {
					currentSet.Insert(*nsxRule.Id)
					nsxRules = append(nsxRules, *nsxRule)
				}
			}
		}

		currentSet.Clear()
		for _, nsxGroup := range buildGroups {
			if nsxGroup != nil {
				if !currentSet.Has(*nsxGroup.Id) {
					currentSet.Insert(*nsxGroup.Id)
					nsxGroups = append(nsxGroups, *nsxGroup)
				}
			}
		}

		currentSet.Clear()
		for _, projectShare := range buildProjectShares {
			if projectShare != nil {
				if !currentSet.Has(*projectShare.share.Id) {
					currentSet.Insert(*projectShare.share.Id)
					projectShares = append(projectShares, *projectShare)
					nsxProjectGroups = append(nsxProjectGroups, *projectShare.shareGroup)
					nsxProjectShares = append(nsxProjectShares, *projectShare.share)
				}
			}
		}

	}
	nsxSecurityPolicy.Rules = nsxRules
	nsxSecurityPolicy.Tags = service.buildBasicTags(obj, createdFor)
	// nsxRules info are included in nsxSecurityPolicy obj
	log.Info("built nsxSecurityPolicy", "nsxSecurityPolicy", nsxSecurityPolicy, "nsxGroups", nsxGroups, "nsxProjectGroups", nsxProjectGroups, "nsxProjectShares", nsxProjectShares)

	return nsxSecurityPolicy, &nsxGroups, &projectShares, nil
}

func (service *SecurityPolicyService) buildPolicyGroup(obj *v1alpha1.SecurityPolicy, createdFor string) (*model.Group, string, error) {
	policyAppliedGroup := model.Group{}
	policyAppliedGroup.Id = String(service.buildAppliedGroupID(obj, -1, createdFor))

	policyAppliedGroup.DisplayName = String(service.buildAppliedGroupName(obj, -1))

	appliedTo := obj.Spec.AppliedTo
	targetTags := service.buildTargetTags(obj, &appliedTo, nil, -1, createdFor)
	policyAppliedGroup.Tags = targetTags
	if len(appliedTo) == 0 {
		return nil, "ANY", nil
	}

	targetGroupCriteriaCount, targetGroupTotalExprCount := 0, 0
	criteriaCount, totalExprCount := 0, 0
	var err error
	errorMsg := ""
	for i := range appliedTo {
		criteriaCount, totalExprCount, err = service.updateTargetExpressions(
			obj,
			&appliedTo[i],
			&policyAppliedGroup,
			i,
		)
		if err == nil {
			targetGroupCriteriaCount += criteriaCount
			targetGroupTotalExprCount += totalExprCount
		} else {
			return nil, "", err
		}
	}
	log.V(2).Info("build policy target group criteria",
		"totalCriteria", targetGroupCriteriaCount, "totalExprsOfCriteria", targetGroupTotalExprCount)

	if targetGroupCriteriaCount > MaxCriteria {
		errorMsg = fmt.Sprintf(
			"total counts of policy target group criteria %d exceed NSX limit of %d",
			targetGroupCriteriaCount,
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

	policyAppliedGroupPath, err := service.buildAppliedGroupPath(obj, -1, createdFor)
	if err != nil {
		return nil, "", err
	}

	log.V(1).Info("built policy target group", "policyAppliedGroup", policyAppliedGroup)
	return &policyAppliedGroup, policyAppliedGroupPath, nil
}

func (service *SecurityPolicyService) buildTargetTags(obj *v1alpha1.SecurityPolicy, targets *[]v1alpha1.SecurityPolicyTarget,
	rule *v1alpha1.SecurityPolicyRule, ruleIdx int, createdFor string,
) []model.Tag {
	basicTags := service.buildBasicTags(obj, createdFor)
	sort.Slice(*targets, func(i, j int) bool {
		k1, _ := json.Marshal((*targets)[i])
		k2, _ := json.Marshal((*targets)[j])
		return string(k1) < string(k2)
	})
	serializedBytes, _ := json.Marshal(*targets)
	targetTags := []model.Tag{
		{
			Scope: String(common.TagScopeGroupType),
			Tag:   String(common.TagValueGroupScope),
		},
		{
			Scope: String(common.TagScopeSelectorHash),
			Tag:   String(util.Sha1(string(serializedBytes))),
		},
	}

	for _, tag := range basicTags {
		targetTags = append(targetTags, tag)
	}

	// In non-VPC network, there is no need to add is_project_shared tag for target groups
	if isVpcEnabled(service) {
		// the target group for policy or rule is always not group shared
		// because target group doesn't have nameSpace selector
		targetTags = append(targetTags,
			model.Tag{
				Scope: String(common.TagScopeProjectGroupShared),
				Tag:   String("false"),
			},
		)
	}

	if ruleIdx != -1 && rule != nil {
		// the appliedTo group belongs to a rule, so it needs a tag including the rule id
		targetTags = append(targetTags,
			model.Tag{
				Scope: String(common.TagScopeRuleID),
				Tag:   String(service.buildRuleID(obj, rule, ruleIdx, createdFor)),
			},
		)
	}
	return targetTags
}

func (service *SecurityPolicyService) buildBasicTags(obj *v1alpha1.SecurityPolicy, createdFor string) []model.Tag {
	scopeOwnerName := common.TagValueScopeSecurityPolicyName
	scopeOwnerUID := common.TagValueScopeSecurityPolicyUID
	if createdFor == common.ResourceTypeNetworkPolicy {
		scopeOwnerName = common.TagScopeNetworkPolicyName
		scopeOwnerUID = common.TagScopeNetworkPolicyUID
	}

	tags := util.BuildBasicTags(getCluster(service), obj, service.getNamespaceUID(obj.ObjectMeta.Namespace))
	tags = append(tags, []model.Tag{
		{
			Scope: String(scopeOwnerName),
			Tag:   String(obj.Name),
		},
		{
			Scope: String(scopeOwnerUID),
			Tag:   String(string(obj.UID)),
		},
	}...)
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
	var err error
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

// build appliedTo group ID for both policy and rule levels.
func (service *SecurityPolicyService) buildAppliedGroupID(obj *v1alpha1.SecurityPolicy, ruleIdx int, createdFor string) string {
	if isVpcEnabled(service) {
		suffix := common.TargetGroupSuffix
		if ruleIdx != -1 {
			suffix = fmt.Sprintf("%d_%s", ruleIdx, suffix)
		}
		return util.GenerateIDByObjectWithSuffix(obj, suffix)
	}

	ruleIdxStr := ""
	if ruleIdx != -1 {
		ruleIdxStr = fmt.Sprintf("%d", ruleIdx)
	}
	prefix := common.SecurityPolicyPrefix
	if createdFor == common.ResourceTypeNetworkPolicy {
		prefix = common.NetworkPolicyPrefix
	}
	return util.GenerateID(string(obj.UID), prefix, common.TargetGroupSuffix, ruleIdxStr)
}

// build appliedTo group path for both policy and rule levels.
func (service *SecurityPolicyService) buildAppliedGroupPath(obj *v1alpha1.SecurityPolicy, ruleIdx int, createdFor string) (string, error) {
	groupID := service.buildAppliedGroupID(obj, ruleIdx, createdFor)

	if isVpcEnabled(service) {
		vpcInfo, err := service.getVpcInfo(obj.ObjectMeta.Namespace)
		if err != nil {
			return "", err
		}
		orgId := (*vpcInfo).OrgID
		projectId := (*vpcInfo).ProjectID
		vpcId := (*vpcInfo).VPCID
		return fmt.Sprintf("/orgs/%s/projects/%s/vpcs/%s/groups/%s", orgId, projectId, vpcId, groupID), nil
	}

	return fmt.Sprintf("/infra/domains/%s/groups/%s", getDomain(service), groupID), nil
}

// build appliedTo group display name for both policy and rule levels.
func (service *SecurityPolicyService) buildAppliedGroupName(obj *v1alpha1.SecurityPolicy, ruleIdx int) string {
	var rule *v1alpha1.SecurityPolicyRule
	if ruleIdx != -1 {
		rule = &(obj.Spec.Rules[ruleIdx])
		ruleName := fmt.Sprintf("%s-%d", obj.Name, ruleIdx)
		if len(rule.Name) > 0 {
			ruleName = rule.Name
		}
		return util.GenerateTruncName(common.MaxNameLength, ruleName, "", common.TargetGroupSuffix, "", "")
	}
	ruleName := fmt.Sprintf("%s-%s", obj.Namespace, obj.Name)
	return util.GenerateTruncName(common.MaxNameLength, ruleName, "", common.TargetGroupSuffix, "", "")
}

func (service *SecurityPolicyService) buildRuleAndGroups(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, ruleIdx int, createdFor string) ([]*model.Rule, []*model.Group, []*ProjectShare, error) {
	var ruleGroups []*model.Group
	var projectShares []*ProjectShare
	var nsxRuleAppliedGroup *model.Group
	var nsxRuleSrcGroup *model.Group
	var nsxRuleDstGroup *model.Group
	var nsxProjectShare *ProjectShare
	var nsxRuleAppliedGroupPath string
	var nsxRuleDstGroupPath string
	var nsxRuleSrcGroupPath string
	var err error

	ruleDirection, err := getRuleDirection(rule)
	if err != nil {
		return nil, nil, nil, err
	}

	// Since a named port may map to multiple port numbers, then it would return multiple rules.
	// We use the destination port number of service entry to group the rules.
	ipSetGroups, nsxRules, err := service.expandRule(obj, rule, ruleIdx, createdFor)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, g := range ipSetGroups {
		ruleGroups = append(ruleGroups, g)
	}

	for _, nsxRule := range nsxRules {
		if ruleDirection == "IN" {
			nsxRuleSrcGroup, nsxRuleSrcGroupPath, nsxRuleDstGroupPath, nsxProjectShare, err = service.buildRuleInGroup(
				obj, rule, nsxRule, ruleIdx, createdFor)
			if err != nil {
				return nil, nil, nil, err
			}

			if nsxRuleSrcGroup != nil {
				ruleGroups = append(ruleGroups, nsxRuleSrcGroup)
			}
			if nsxProjectShare != nil {
				projectShares = append(projectShares, nsxProjectShare)
			}
		} else if ruleDirection == "OUT" {
			nsxRuleDstGroup, nsxRuleSrcGroupPath, nsxRuleDstGroupPath, nsxProjectShare, err = service.buildRuleOutGroup(
				obj, rule, nsxRule, ruleIdx, createdFor)
			if err != nil {
				return nil, nil, nil, err
			}

			if nsxRuleDstGroup != nil {
				ruleGroups = append(ruleGroups, nsxRuleDstGroup)
			}
			if nsxProjectShare != nil {
				projectShares = append(projectShares, nsxProjectShare)
			}
		}

		nsxRule.SourceGroups = []string{nsxRuleSrcGroupPath}
		nsxRule.DestinationGroups = []string{nsxRuleDstGroupPath}

		nsxRuleAppliedGroup, nsxRuleAppliedGroupPath, err = service.buildRuleAppliedToGroup(
			obj, rule, ruleIdx, nsxRuleSrcGroupPath, nsxRuleDstGroupPath, createdFor)
		if err != nil {
			return nil, nil, nil, err
		}
		ruleGroups = append(ruleGroups, nsxRuleAppliedGroup)
		nsxRule.Scope = []string{nsxRuleAppliedGroupPath}
	}
	return nsxRules, ruleGroups, projectShares, nil
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
	log.V(1).Info("built rule service entry", "destinationPorts", portRange, "protocol", port.Protocol)
	return serviceEntry
}

func (service *SecurityPolicyService) buildRuleAppliedToGroup(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, ruleIdx int, nsxRuleSrcGroupPath string, nsxRuleDstGroupPath string, createdFor string) (*model.Group, string, error) {
	var nsxRuleAppliedGroup *model.Group
	var nsxRuleAppliedGroupPath string
	var err error
	if len(rule.AppliedTo) > 0 {
		nsxRuleAppliedGroup, nsxRuleAppliedGroupPath, err = service.buildRuleAppliedGroupByRule(
			obj, rule, ruleIdx, createdFor)
		if err != nil {
			return nil, "", err
		}
	} else {
		nsxRuleAppliedGroupPath, err = service.buildRuleAppliedGroupByPolicy(obj,
			nsxRuleSrcGroupPath, nsxRuleDstGroupPath, createdFor)
		if err != nil {
			return nil, "", err
		}
	}
	log.V(1).Info("built rule target group", "ruleAppliedGroup", nsxRuleAppliedGroup)
	return nsxRuleAppliedGroup, nsxRuleAppliedGroupPath, nil
}

func (service *SecurityPolicyService) buildRuleInGroup(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, nsxRule *model.Rule, ruleIdx int, createdFor string) (*model.Group, string, string, *ProjectShare, error) {
	var nsxRuleSrcGroup *model.Group
	var nsxProjectShare *ProjectShare
	var nsxRuleSrcGroupPath string
	var nsxRuleDstGroupPath string
	var err error
	if len(rule.Sources) > 0 {
		nsxRuleSrcGroup, nsxRuleSrcGroupPath, nsxProjectShare, err = service.buildRulePeerGroup(obj, rule, ruleIdx, true, createdFor)
		if err != nil {
			return nil, "", "", nil, err
		}
	} else {
		nsxRuleSrcGroupPath = "ANY"
	}

	if len(nsxRule.DestinationGroups) > 0 {
		nsxRuleDstGroupPath = nsxRule.DestinationGroups[0]
	} else {
		nsxRuleDstGroupPath = "ANY"
	}
	return nsxRuleSrcGroup, nsxRuleSrcGroupPath, nsxRuleDstGroupPath, nsxProjectShare, nil
}

func (service *SecurityPolicyService) buildRuleOutGroup(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, nsxRule *model.Rule, ruleIdx int, createdFor string) (*model.Group, string, string, *ProjectShare, error) {
	var nsxRuleDstGroup *model.Group
	var nsxProjectShare *ProjectShare
	var nsxRuleSrcGroupPath string
	var nsxRuleDstGroupPath string
	var err error
	if len(nsxRule.DestinationGroups) > 0 {
		nsxRuleDstGroupPath = nsxRule.DestinationGroups[0]
	} else {
		if len(rule.Destinations) > 0 {
			nsxRuleDstGroup, nsxRuleDstGroupPath, nsxProjectShare, err = service.buildRulePeerGroup(obj, rule, ruleIdx, false, createdFor)
			if err != nil {
				return nil, "", "", nil, err
			}
		} else {
			nsxRuleDstGroupPath = "ANY"
		}
	}
	nsxRuleSrcGroupPath = "ANY"
	return nsxRuleDstGroup, nsxRuleSrcGroupPath, nsxRuleDstGroupPath, nsxProjectShare, nil
}

func (service *SecurityPolicyService) buildRuleID(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, ruleIdx int, createdFor string) string {
	serializedBytes, _ := json.Marshal(rule)
	ruleHash := fmt.Sprintf("%s", util.Sha1(string(serializedBytes)))
	ruleIdxStr := fmt.Sprintf("%d", ruleIdx)
	if isVpcEnabled(service) {
		suffix := fmt.Sprintf("%s_%s", ruleIdxStr, ruleHash)
		return util.GenerateIDByObjectWithSuffix(obj, suffix)
	}
	prefix := common.SecurityPolicyPrefix
	if createdFor == common.ResourceTypeNetworkPolicy {
		prefix = common.NetworkPolicyPrefix
	}
	return util.GenerateID(fmt.Sprintf("%s", obj.UID), prefix, ruleHash, ruleIdxStr)
}

func (service *SecurityPolicyService) buildRuleDisplayName(rule *v1alpha1.SecurityPolicyRule, portIdx, portNumber int, hasNamedport bool, createdFor string) (string, error) {
	var ruleName string
	var suffix string

	ruleDirection, err := getRuleDirection(rule)
	if err != nil {
		return "", err
	}
	ruleAction, err := getRuleAction(rule)
	if err != nil {
		return "", err
	}

	if ruleDirection == "IN" {
		switch ruleAction {
		case util.ToUpper(v1alpha1.RuleActionAllow):
			suffix = common.RuleSuffixIngressAllow
		case util.ToUpper(v1alpha1.RuleActionDrop):
			suffix = common.RuleSuffixIngressDrop
		case util.ToUpper(v1alpha1.RuleActionReject):
			suffix = common.RuleSuffixIngressReject
		}
	} else {
		switch ruleAction {
		case util.ToUpper(v1alpha1.RuleActionAllow):
			suffix = common.RuleSuffixEgressAllow
		case util.ToUpper(v1alpha1.RuleActionDrop):
			suffix = common.RuleSuffixEgressDrop
		case util.ToUpper(v1alpha1.RuleActionReject):
			suffix = common.RuleSuffixEgressReject
		}
	}

	if len(rule.Name) > 0 {
		// For the internal security policy rule converted from network policy, skipping to add suffix for the rule name
		// if it has its own name generated, usually, it's for the internal isolation security policy rule created for network policy.
		ruleName = rule.Name
		if createdFor != common.ResourceTypeNetworkPolicy {
			// If user defines the rule name, the generated NSX security policy rule will also be added with the same suffix: "-direction-action" as building rulePortsString
			// e.g. input security policy's rule name: sp-rule,
			// the generated NSX security policy rule name: sp-rule-ingress-allow
			ruleName = ruleName + "-" + suffix
		}
	} else {
		ruleName = service.buildRulePortsString(&rule.Ports, suffix)
	}

	if !hasNamedport {
		return util.GenerateTruncName(common.MaxNameLength, ruleName, "", "", "", ""), nil
	} else {
		// For the security policy rule with namedPort, it will be expanded to the multiple security policy rules based on resolution of named port.
		// e.g. input: security policy's rule name: TCP.http-UDP.1234-ingress-allow,
		// expand to NSX security policy rules with name TCP.http-UDP.1234.TCP.80-ingress-allow and TCP.http-UDP.1234.UDP.1234-ingress-allow.
		// in case that user defined input security policy's rule name: sp-namedport-rule,
		// expand to NSX security policy rules with name sp-namedport-rule.TCP.80-ingress-allow and sp-namedport-rule.UDP.1234-ingress-allow.
		index := strings.Index(ruleName, "-"+suffix)
		return util.GenerateTruncName(common.MaxNameLength, ruleName[:index]+"."+service.buildRulePortString(&rule.Ports[portIdx], true, portNumber), "", suffix, "", ""), nil
	}
}

func (service *SecurityPolicyService) buildRuleAppliedGroupByPolicy(obj *v1alpha1.SecurityPolicy, nsxRuleSrcGroupPath string, nsxRuleDstGroupPath string, createdFor string) (string, error) {
	var nsxRuleAppliedGroupPath string
	var err error
	if len(obj.Spec.AppliedTo) == 0 {
		return "", errors.New("appliedTo needs to be set in either spec or rules")
	}
	if nsxRuleSrcGroupPath == "ANY" && nsxRuleDstGroupPath == "ANY" {
		// NSX-T manager will report error if all the rule's scope/src/dst are "ANY".
		// So if the rule's scope is empty while policy's not, the rule's scope also
		// will be set to the policy's scope to avoid this case.
		nsxRuleAppliedGroupPath, err = service.buildAppliedGroupPath(obj, -1, createdFor)
		if err != nil {
			return "", err
		}
	} else {
		nsxRuleAppliedGroupPath = "ANY"
	}
	return nsxRuleAppliedGroupPath, nil
}

func (service *SecurityPolicyService) buildRuleAppliedGroupByRule(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, ruleIdx int, createdFor string) (*model.Group, string, error) {
	var ruleAppliedGroupName string
	appliedTo := rule.AppliedTo
	ruleAppliedGroupID := service.buildAppliedGroupID(obj, ruleIdx, createdFor)
	ruleAppliedGroupName = service.buildAppliedGroupName(obj, ruleIdx)

	targetTags := service.buildTargetTags(obj, &appliedTo, rule, ruleIdx, createdFor)
	ruleAppliedGroupPath, err := service.buildAppliedGroupPath(obj, ruleIdx, createdFor)
	if err != nil {
		return nil, "", err
	}
	ruleAppliedGroup := model.Group{
		Id:          &ruleAppliedGroupID,
		DisplayName: &ruleAppliedGroupName,
		Tags:        targetTags,
	}

	ruleGroupCriteriaCount, ruleGroupTotalExprCount := 0, 0
	criteriaCount, totalExprCount := 0, 0
	errorMsg := ""
	for i := range appliedTo {
		criteriaCount, totalExprCount, err = service.updateTargetExpressions(
			obj,
			&appliedTo[i],
			&ruleAppliedGroup,
			i,
		)
		if err == nil {
			ruleGroupCriteriaCount += criteriaCount
			ruleGroupTotalExprCount += totalExprCount
		} else {
			return nil, "", err
		}
	}
	log.V(2).Info("build rule applied group criteria", "totalCriteria",
		ruleGroupCriteriaCount, "totalExprsOfCriteria", ruleGroupTotalExprCount)

	if ruleGroupCriteriaCount > MaxCriteria {
		errorMsg = fmt.Sprintf(
			"total counts of rule applied group criteria %d exceed NSX limit of %d",
			ruleGroupCriteriaCount,
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

func (service *SecurityPolicyService) buildRulePeerGroupID(obj *v1alpha1.SecurityPolicy, ruleIdx int, isSource bool) string {
	suffix := common.DstGroupSuffix
	if isSource == true {
		suffix = common.SrcGroupSuffix
	}
	if isVpcEnabled(service) {
		return util.GenerateIDByObjectWithSuffix(obj, fmt.Sprintf("%d_%s", ruleIdx, suffix))
	}
	return util.GenerateID(string(obj.UID), common.SecurityPolicyPrefix, suffix, fmt.Sprintf("%d", ruleIdx))
}

func (service *SecurityPolicyService) buildRulePeerGroupName(obj *v1alpha1.SecurityPolicy, ruleIdx int, isSource bool) string {
	rule := &(obj.Spec.Rules[ruleIdx])
	suffix := common.DstGroupSuffix
	if isSource == true {
		suffix = common.SrcGroupSuffix
	}
	ruleName := fmt.Sprintf("%s-%d", obj.Name, ruleIdx)
	if len(rule.Name) > 0 {
		ruleName = rule.Name
	}
	return util.GenerateTruncName(common.MaxNameLength, ruleName, "", suffix, "", "")
}

func (service *SecurityPolicyService) buildRulePeerGroupPath(obj *v1alpha1.SecurityPolicy, ruleIdx int, isSource, groupShared bool) (string, error) {
	groupID := service.buildRulePeerGroupID(obj, ruleIdx, isSource)

	if isVpcEnabled(service) {
		vpcInfo, err := service.getVpcInfo(obj.ObjectMeta.Namespace)
		if err != nil {
			return "", err
		}
		orgId := (*vpcInfo).OrgID
		projectId := (*vpcInfo).ProjectID
		vpcId := (*vpcInfo).VPCID

		if groupShared {
			return fmt.Sprintf("/orgs/%s/projects/%s/infra/domains/%s/groups/%s", orgId, projectId, getVpcProjectDomain(), groupID), nil
		}
		return fmt.Sprintf("/orgs/%s/projects/%s/vpcs/%s/groups/%s", orgId, projectId, vpcId, groupID), nil
	}

	return fmt.Sprintf("/infra/domains/%s/groups/%s", getDomain(service), groupID), nil
}

func (service *SecurityPolicyService) buildRulePeerGroup(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, ruleIdx int, isSource bool, createdFor string) (*model.Group, string, *ProjectShare, error) {
	var rulePeers []v1alpha1.SecurityPolicyPeer
	var ruleDirection string
	rulePeerGroupID := service.buildRulePeerGroupID(obj, ruleIdx, isSource)
	rulePeerGroupName := service.buildRulePeerGroupName(obj, ruleIdx, isSource)
	if isSource == true {
		rulePeers = rule.Sources
		ruleDirection = "source"
	} else {
		rulePeers = rule.Destinations
		ruleDirection = "destination"
	}

	groupShared := false
	for _, peer := range rulePeers {
		if peer.NamespaceSelector != nil {
			groupShared = true
			break
		}
	}

	rulePeerGroupPath, err := service.buildRulePeerGroupPath(obj, ruleIdx, isSource, groupShared)
	if err != nil {
		return nil, "", nil, err
	}

	peerTags := service.buildPeerTags(obj, rule, ruleIdx, isSource, groupShared, createdFor)
	rulePeerGroup := model.Group{
		Id:          &rulePeerGroupID,
		DisplayName: &rulePeerGroupName,
		Tags:        peerTags,
	}

	rulePeerGroupCriteriaCount, rulePeerGroupTotalExprCount := 0, 0
	criteriaCount, totalExprCount := 0, 0
	errorMsg := ""
	for i := range rulePeers {
		criteriaCount, totalExprCount, err = service.updatePeerExpressions(
			obj,
			&rulePeers[i],
			&rulePeerGroup,
			i,
			groupShared,
		)
		if err == nil {
			rulePeerGroupCriteriaCount += criteriaCount
			rulePeerGroupTotalExprCount += totalExprCount
		} else {
			return nil, "", nil, err
		}
	}
	log.V(2).Info(fmt.Sprintf("build rule %s group criteria", ruleDirection),
		"totalCriteria", rulePeerGroupCriteriaCount, "totalExprsOfCriteria", rulePeerGroupTotalExprCount)

	if rulePeerGroupCriteriaCount > MaxCriteria {
		errorMsg = fmt.Sprintf(
			"total counts of rule %s group criteria %d exceed NSX limit of %d",
			ruleDirection,
			rulePeerGroupCriteriaCount,
			MaxCriteria,
		)
	} else if rulePeerGroupTotalExprCount > MaxTotalCriteriaExpressions {
		errorMsg = fmt.Sprintf("total expression counts in %s group criteria %d exceed NSX limit of %d",
			ruleDirection,
			rulePeerGroupTotalExprCount,
			MaxTotalCriteriaExpressions,
		)
	}

	if len(errorMsg) != 0 {
		err = errors.New(errorMsg)
		return nil, "", nil, err
	}

	if isVpcEnabled(service) && (groupShared == true) {
		var projectShare ProjectShare
		projectShare.shareGroup = &rulePeerGroup

		var sharedNamespace []string
		// Share group with the VPC in which SecurityPolicy rule is put
		sharedNamespace = append(sharedNamespace, obj.ObjectMeta.Namespace)

		sharedWith, err := service.buildSharedWith(&sharedNamespace)
		if err != nil {
			log.Error(err, "failed to build SharedWith path", "ruleGroupName", rulePeerGroupName)
			return nil, "", nil, err
		}
		// Build a nsx share resource in project level
		nsxShare, err := service.buildProjectShare(obj, &rulePeerGroup, []string{rulePeerGroupPath}, *sharedWith, createdFor)
		if err != nil {
			log.Error(err, "failed to build NSX project share", "ruleGroupName", rulePeerGroupName)
			return nil, "", nil, err
		}

		projectShare.share = nsxShare
		return nil, rulePeerGroupPath, &projectShare, err
	}

	return &rulePeerGroup, rulePeerGroupPath, nil, err
}

func (service *SecurityPolicyService) buildExpandedRuleId(ruleBaseId string, portIdx int, portAddressIdx int) string {
	return fmt.Sprintf("%s_%d_%d", ruleBaseId, portIdx, portAddressIdx)
}

// Build rule basic info, ruleIdx is the index of the rules of security policy,
// portIdx is the index of rule's ports, portAddressIdx is the index
// of multiple port number if one named port maps to multiple port numbers.
func (service *SecurityPolicyService) buildRuleBasicInfo(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, ruleIdx int, portIdx int, portAddressIdx int,
	portNumber int, hasNamedport bool, createdFor string,
) (*model.Rule, error) {
	ruleAction, err := getRuleAction(rule)
	if err != nil {
		return nil, err
	}
	ruleDirection, err := getRuleDirection(rule)
	if err != nil {
		return nil, err
	}
	displayName, err := service.buildRuleDisplayName(rule, portIdx, portNumber, hasNamedport, createdFor)
	if err != nil {
		log.Error(err, "failed to build rule's display name", "object.UID", obj.UID, "rule", rule, "createdFor", createdFor)
	}

	nsxRule := model.Rule{
		Id:             String(service.buildExpandedRuleId(service.buildRuleID(obj, rule, ruleIdx, createdFor), portIdx, portAddressIdx)),
		DisplayName:    &displayName,
		Direction:      &ruleDirection,
		SequenceNumber: Int64(int64(ruleIdx)),
		Action:         &ruleAction,
		Services:       []string{"ANY"},
		Tags:           service.buildBasicTags(obj, createdFor),
	}
	log.V(1).Info("built rule basic info", "nsxRule", nsxRule)
	return &nsxRule, nil
}

func (service *SecurityPolicyService) buildPeerTags(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, ruleIdx int, isSource, groupShared bool, createdFor string) []model.Tag {
	basicTags := service.buildBasicTags(obj, createdFor)
	groupTypeTag := String(common.TagValueGroupDestination)
	peers := &rule.Destinations
	if isSource == true {
		groupTypeTag = String(common.TagValueGroupSource)
		peers = &rule.Sources
	}

	// TODO: abstract sort func for both peers and targets
	sort.Slice(*peers, func(i, j int) bool {
		k1, _ := json.Marshal((*peers)[i])
		k2, _ := json.Marshal((*peers)[j])
		return string(k1) < string(k2)
	})
	serializedBytes, _ := json.Marshal(*peers)

	groupSharedTag := String("false")
	if groupShared == true {
		groupSharedTag = String("true")
	}
	peerTags := []model.Tag{
		{
			Scope: String(common.TagScopeGroupType),
			Tag:   groupTypeTag,
		},
		{
			Scope: String(common.TagScopeRuleID),
			Tag:   String(service.buildRuleID(obj, rule, ruleIdx, createdFor)),
		},
		{
			Scope: String(common.TagScopeSelectorHash),
			Tag:   String(util.Sha1(string(serializedBytes))),
		},
	}
	for _, tag := range basicTags {
		peerTags = append(peerTags, tag)
	}

	// In non-VPC network, there is no need to add is_project_shared tag for rule peer groups
	if isVpcEnabled(service) {
		peerTags = append(peerTags,
			model.Tag{
				Scope: String(common.TagScopeProjectGroupShared),
				Tag:   groupSharedTag,
			},
		)
	}
	return peerTags
}

func (service *SecurityPolicyService) updateTargetExpressions(obj *v1alpha1.SecurityPolicy, target *v1alpha1.SecurityPolicyTarget, group *model.Group, ruleIdx int) (int, int, error) {
	var err error
	var tagValueExpression *data.StructValue
	var matchLabels map[string]string
	var matchExpressions *[]v1.LabelSelectorRequirement
	var mergedMatchExpressions *[]v1.LabelSelectorRequirement
	opInValueCount, totalCriteriaCount, totalExprCount := 0, 0, 0
	matchLabelsCount, matchExpressionsCount := 0, 0

	memberType, clusterMemberType := "SegmentPort", "Segment"
	if isVpcEnabled(service) {
		memberType, clusterMemberType = "VpcSubnetPort", "VpcSubnetPort"
	}

	if target.PodSelector != nil && target.VMSelector != nil {
		errorMsg := "PodSelector and VMSelector are not allowed to set in one group"
		err = errors.New(errorMsg)
		return 0, 0, err
	}

	log.V(2).Info("update target expressions", "ruleIndex", ruleIdx)
	service.appendOperatorIfNeeded(&group.Expression, "OR")
	expressions := service.buildGroupExpression(&group.Expression)

	// In non-VPC network, setting cluster memberType to Segment for PodSelector and VMSelector ensures the criteria is mixed,
	// Because the following conditions must have condition whose memberType is SegmentPort.
	// In VPC network, setting cluster memberType VpcSubnetPort because VPC level group doesn't support mixed criteria.
	// Also, following conditions must have condition whose memberType is VpcSubnetPort.
	// Segment and SegmentPort are not supported in VPC level group.
	// Target group must be put under VPC level group path.
	clusterExpression := service.buildExpression(
		"Condition", clusterMemberType,
		fmt.Sprintf("%s|%s", getScopeCluserTag(service), getCluster(service)),
		"Tag", "EQUALS", "EQUALS",
	)
	expressions.Add(clusterExpression)

	if target.PodSelector != nil {
		service.addOperatorIfNeeded(expressions, "AND")
		nsExpression := service.buildExpression(
			"Condition", memberType,
			fmt.Sprintf("%s|%s", getScopeNamespaceUIDTag(service, false), string(service.getNamespaceUID(obj.ObjectMeta.Namespace))),
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
			fmt.Sprintf("%s|%s", getScopeNamespaceUIDTag(service, true), string(service.getNamespaceUID(obj.ObjectMeta.Namespace))),
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
		matchLabelsCount += ClusterTagCount + NameSpaceTagCount

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

		if isVpcEnabled(service) {
			// In VPC level, it doesn't support mixed expression criteria
			totalCriteriaCount, totalExprCount, err = service.validateSelectorExpressions(
				matchLabelsCount,
				matchExpressionsCount,
				opInValueCount,
				false,
			)
		} else {
			// Since cluster memberType is set as default Segment, So the final produced group criteria is always treated as a mixed criteria
			totalCriteriaCount, totalExprCount, err = service.validateSelectorExpressions(
				matchLabelsCount,
				matchExpressionsCount,
				opInValueCount,
				true,
			)
		}
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
//   - {key: k1, operator: NotIn, values: [a1, a2, a3]}
//   - {key: k1, operator: NotIn, values: [a2, a3, a4]}
//     => {key: k1, operator: NotIn, values: [a1, a2, a3, a4]}
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
	var err error
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
	var err error
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
	var err error
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
			"total count of labelSelectors expressions %d exceed NSX limit of %d in one criteria based on the same member type",
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
//   - {key: k2, operator: In, values: [a3,a4]}
//
// The above two expressions will be translated to:
// => {k1 NotIn [a1,a2]} AND {k2 EQUALS a3} OR {k1 NotIn [a1,a2]} AND {k2 EQUALS a4}
func (service *SecurityPolicyService) updateExpressionsMatchExpression(matchExpressions []v1.LabelSelectorRequirement, matchLabels map[string]string,
	policyExpression *[]*data.StructValue, clusterExpression *data.StructValue, tagValueExpression *data.StructValue, memberType string, expressions *data.ListValue,
) error {
	var err error
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
	var err error
	opInIdx := 0
	var opInMatchExpressions []v1.LabelSelectorRequirement
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

func (service *SecurityPolicyService) updatePeerExpressions(obj *v1alpha1.SecurityPolicy, peer *v1alpha1.SecurityPolicyPeer, group *model.Group, ruleIdx int, groupShared bool) (int, int, error) {
	var err error
	errorMsg := ""
	var tagValueExpression *data.StructValue
	var matchLabels map[string]string
	var matchExpressions *[]v1.LabelSelectorRequirement
	var mergedMatchExpressions *[]v1.LabelSelectorRequirement
	opInValueCount, totalCriteriaCount, totalExprCount := 0, 0, 0
	matchLabelsCount, matchExpressionsCount := 0, 0
	mixedNsSelector := false
	isVpcEnable := isVpcEnabled(service)

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

	log.V(2).Info("update peer expressions", "ruleIndex", ruleIdx)
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

	// In non-VPC network, setting cluster memberType to Segment for PodSelector and VMSelector ensures the criteria is mixed,
	// Because the following conditions must have condition whose memberType is SegmentPort
	clusterMemberType := "Segment"
	// Setting cluster memberType to SegmentPort for NamespaceSelector only case ensures the criteria is mixed,
	// Because the following conditions must have condition whose memberType is Segment when NamespaceSelector isn't empty
	if peer.PodSelector == nil && peer.VMSelector == nil && peer.NamespaceSelector != nil &&
		peer.NamespaceSelector.Size() > 0 {
		clusterMemberType = "SegmentPort"
	}

	memberType := "SegmentPort"
	// In VPC network, cluster memberType must be set as VpcSubnetPort when no NamespaceSelector.
	// Moreover, VPC level group doesn't support mixed criteria but project level group can support mixed criteria.
	// MemberType must be set as VpcSubnetPort for the VPC level group.
	// If NamespaceSelector is specified, peer group must be put under project level for sharing with VPC.
	// VpcSubnet and VpcSubnetPort types are not supported in project level group.
	// Project level group can support SegmentPort and Segment type.
	// If groupShared is True, it means there are NamespaceSelectors in the rule groups, so we can use mixed criteria.
	if isVpcEnable && !groupShared {
		clusterMemberType = "VpcSubnetPort"
		memberType = "VpcSubnetPort"
	}

	clusterExpression := service.buildExpression(
		"Condition", clusterMemberType,
		fmt.Sprintf("%s|%s", getScopeCluserTag(service), getCluster(service)),
		"Tag", "EQUALS", "EQUALS",
	)
	expressions.Add(clusterExpression)

	if peer.PodSelector != nil {
		service.addOperatorIfNeeded(expressions, "AND")
		podExpression := service.buildExpression(
			"Condition",
			memberType,
			fmt.Sprintf("%s|", getScopePodTag(service)),
			"Tag",
			"EQUALS",
			"EQUALS",
		)

		if peer.NamespaceSelector == nil {
			podExpression = service.buildExpression(
				"Condition", memberType,
				fmt.Sprintf("%s|%s", getScopeNamespaceUIDTag(service, false), string(service.getNamespaceUID(obj.ObjectMeta.Namespace))),
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
		matchLabelsCount += ClusterTagCount + NameSpaceTagCount
	}
	if peer.VMSelector != nil {
		service.addOperatorIfNeeded(expressions, "AND")
		vmExpression := service.buildExpression(
			"Condition",
			memberType,
			fmt.Sprintf("%s|", getScopeVMInterfaceTag(service)),
			"Tag",
			"EQUALS",
			"EQUALS",
		)

		if peer.NamespaceSelector == nil {
			vmExpression = service.buildExpression(
				"Condition", memberType,
				fmt.Sprintf("%s|%s", getScopeNamespaceUIDTag(service, true), string(service.getNamespaceUID(obj.ObjectMeta.Namespace))),
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
		matchLabelsCount += ClusterTagCount + NameSpaceTagCount
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
					fmt.Sprintf("%s|%s", getScopeCluserTag(service), getCluster(service)),
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

		if isVpcEnable && !groupShared {
			// In VPC level, it doesn't support mixed expression criteria
			totalCriteriaCount, totalExprCount, err = service.validateSelectorExpressions(
				matchLabelsCount,
				matchExpressionsCount,
				opInValueCount,
				false,
			)
		} else {
			// Since cluster memberType is set as "Segment" or "SegmentPort", So the final produced group criteria is always treated as a mixed criteria
			totalCriteriaCount, totalExprCount, err = service.validateSelectorExpressions(
				matchLabelsCount,
				matchExpressionsCount,
				opInValueCount,
				true,
			)
		}
		if err != nil {
			return 0, 0, err
		}
	}

	return totalCriteriaCount, totalExprCount, nil
}

func (service *SecurityPolicyService) buildShareName(nsxProjectName, groupName string) string {
	nsxProjectShareName := util.GenerateTruncName(common.MaxNameLength, nsxProjectName, common.SharePrefix, fmt.Sprintf("group-%s", groupName), "", "")
	return nsxProjectShareName
}

func (service *SecurityPolicyService) buildShareID(nsxProjectName, groupID string) string {
	nsxProjectShareId := util.GenerateID(nsxProjectName, common.SharePrefix, fmt.Sprintf("group_%s", groupID), "")
	return nsxProjectShareId
}

func (service *SecurityPolicyService) buildShareTags(obj *v1alpha1.SecurityPolicy, projectId string, group *model.Group, createdFor string) []model.Tag {
	var scopeOwnerName, scopeOwnerUID string
	if createdFor == common.ResourceTypeSecurityPolicy {
		scopeOwnerName = common.TagValueScopeSecurityPolicyName
		scopeOwnerUID = common.TagValueScopeSecurityPolicyUID
	} else {
		scopeOwnerName = common.TagScopeNetworkPolicyName
		scopeOwnerUID = common.TagScopeNetworkPolicyUID
	}
	tags := []model.Tag{
		{
			Scope: String(common.TagScopeVersion),
			Tag:   String(strings.Join(common.TagValueVersion, ".")),
		},
		{
			Scope: String(common.TagScopeCluster),
			Tag:   String(getCluster(service)),
		},
		{
			Scope: String(common.TagScopeNSXProjectID),
			Tag:   String(projectId),
		},
		{
			Scope: String(scopeOwnerName),
			Tag:   String(obj.ObjectMeta.Name),
		},
		{
			Scope: String(scopeOwnerUID),
			Tag:   String(string(obj.UID)),
		},
		{
			Scope: String(common.TagScopeGoupID),
			Tag:   String(*group.Id),
		},
	}
	return tags
}

func (service *SecurityPolicyService) buildSharedResource(shareId string, sharedGroupPath []string) (*model.SharedResource, error) {
	var resourceObjects []model.ResourceObject
	resourceType := common.ResourceTypeSharedResource
	includeChildren := false

	for _, path := range sharedGroupPath {
		groupPath := path
		resourceObject := model.ResourceObject{
			IncludeChildren: &includeChildren,
			ResourcePath:    &groupPath,
		}
		resourceObjects = append(resourceObjects, resourceObject)
	}
	sharedResource := model.SharedResource{
		Id:              &shareId,
		ResourceType:    &resourceType,
		ResourceObjects: resourceObjects,
	}

	return &sharedResource, nil
}

func (service *SecurityPolicyService) buildChildSharedResource(shareId string, sharedGroupPath []string) ([]*data.StructValue, error) {
	resourceType := common.ResourceTypeChildSharedResource
	sharedResource, err := service.buildSharedResource(shareId, sharedGroupPath)
	if err != nil {
		return nil, err
	}

	childSharedResource := model.ChildSharedResource{
		ResourceType:   resourceType,
		SharedResource: sharedResource,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childSharedResource, model.ChildSharedResourceBindingType())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return []*data.StructValue{dataValue.(*data.StructValue)}, nil
}

func (service *SecurityPolicyService) buildProjectShare(obj *v1alpha1.SecurityPolicy, group *model.Group,
	sharedGroupPath, sharedWith []string, createdFor string,
) (*model.Share, error) {
	resourceType := common.ResourceTypeShare
	vpcInfo, err := service.getVpcInfo(obj.ObjectMeta.Namespace)
	if err != nil {
		return nil, err
	}
	projectId := (*vpcInfo).ProjectID

	projectShareId := service.buildShareID(projectId, *group.Id)
	projectShareTags := service.buildShareTags(obj, projectId, group, createdFor)
	projectShareName := service.buildShareName(projectId, *group.DisplayName)

	childSharedResource, err := service.buildChildSharedResource(projectShareId, sharedGroupPath)
	if err != nil {
		return nil, err
	}

	projectShare := model.Share{
		Id:           &projectShareId,
		DisplayName:  &projectShareName,
		Tags:         projectShareTags,
		ResourceType: &resourceType,
		SharedWith:   sharedWith,
		Children:     childSharedResource,
	}

	return &projectShare, nil
}

func (service *SecurityPolicyService) buildSharedWith(sharedNamespace *[]string) (*[]string, error) {
	var sharedWith []string
	for _, ns := range *sharedNamespace {
		log.V(1).Info("building shared with in Namespace", "sharedNamespace", ns)

		vpcInfo, err := service.getVpcInfo(ns)
		if err != nil {
			return nil, err
		}
		orgId := (*vpcInfo).OrgID
		projectId := (*vpcInfo).ProjectID
		vpcId := (*vpcInfo).VPCID

		sharedWithPath := fmt.Sprintf("/orgs/%s/projects/%s/vpcs/%s", orgId, projectId, vpcId)
		sharedWith = append(sharedWith, sharedWithPath)
	}

	return &sharedWith, nil
}

func (service *SecurityPolicyService) getNamespaceUID(ns string) (nsUid types.UID) {
	namespace := &corev1.Namespace{}
	namespacedName := types.NamespacedName{
		Name: ns,
	}
	if err := service.Client.Get(context.Background(), namespacedName, namespace); err != nil {
		log.Error(err, "failed to get namespace UID", "namespace", ns)
		return ""
	}
	namespace_uid := namespace.UID
	return namespace_uid
}

func (service *SecurityPolicyService) buildRulePortString(port *v1alpha1.SecurityPolicyPort, hasNamedport bool, portNumber int) string {
	protocol := string(port.Protocol)
	// Build the rule port string name for non named port.
	// This is a common case where the string is built from port definition. For instance,
	// - protocol: TCP
	//   port: 8282
	//   endPort: 8286
	// The built port string is: TCP.8282.8286
	// - protocol: UDP
	//   port: 3308
	// The built port string is: UDP.3308
	if !hasNamedport {
		if port.EndPort != 0 {
			return fmt.Sprintf("%s.%s.%d", protocol, (port.Port).String(), port.EndPort)
		}
		return fmt.Sprintf("%s.%s", protocol, (port.Port).String())
	} else {
		// Build the rule port string name for named port.
		// The port string is built from specific port number resolved from named port.
		return fmt.Sprintf("%s.%d", protocol, portNumber)
	}
}

func (service *SecurityPolicyService) buildRulePortsString(ports *[]v1alpha1.SecurityPolicyPort, suffix string) string {
	portsString := ""
	if ports == nil || len(*ports) == 0 {
		portsString = "all"
	} else {
		for idx, p := range *ports {
			port := p
			portString := service.buildRulePortString(&port, false, -1)
			if idx == 0 {
				portsString = portString
			} else {
				portsString = portsString + "-" + portString
			}
		}
	}
	return util.GenerateTruncName(common.MaxNameLength, portsString, "", suffix, "", "")
}

func (service *SecurityPolicyService) BuildNetworkPolicyAllowPolicyName(uid string) string {
	return fmt.Sprintf("%s-allow", uid)
}

func (service *SecurityPolicyService) BuildNetworkPolicyAllowPolicyID(uid string) string {
	return fmt.Sprintf("%s_allow", uid)
}

func (service *SecurityPolicyService) BuildNetworkPolicyIsolationPolicyName(uid string) string {
	return fmt.Sprintf("%s-isolation", uid)
}

func (service *SecurityPolicyService) BuildNetworkPolicyIsolationPolicyID(uid string) string {
	return fmt.Sprintf("%s_isolation", uid)
}
