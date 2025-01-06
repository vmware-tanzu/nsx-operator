/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package securitypolicy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

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

func (service *SecurityPolicyService) buildSecurityPolicyName(obj *v1alpha1.SecurityPolicy) string {
	return util.GenerateTruncName(common.MaxNameLength, obj.Name, "", "", "", "")
}

func (service *SecurityPolicyService) buildSecurityPolicyID(obj *v1alpha1.SecurityPolicy, createdFor string) string {
	if IsVPCEnabled(service) {
		return util.GenerateIDByObject(obj)
	}

	prefix := common.SecurityPolicyPrefix
	if createdFor != common.ResourceTypeSecurityPolicy {
		prefix = common.NetworkPolicyPrefix
	}
	nsxSecurityPolicyID := util.GenerateID(string(obj.UID), prefix, "", "")
	return nsxSecurityPolicyID
}

func (service *SecurityPolicyService) buildSecurityPolicy(obj *v1alpha1.SecurityPolicy, createdFor string) (*model.SecurityPolicy, *[]model.Group, *[]GroupShare, error) {
	var nsxRules []model.Rule
	var nsxGroups []model.Group
	var nsxShareGroups []model.Group
	var nsxShares []model.Share
	var nsxGroupShares []GroupShare

	log.V(1).Info("Building the model SecurityPolicy from CR SecurityPolicy", "object", *obj)
	nsxSecurityPolicy := &model.SecurityPolicy{}

	nsxSecurityPolicy.Id = String(service.buildSecurityPolicyID(obj, createdFor))
	nsxSecurityPolicy.DisplayName = String(service.buildSecurityPolicyName(obj))
	// TODO: confirm the sequence number: offset
	nsxSecurityPolicy.SequenceNumber = Int64(int64(obj.Spec.Priority))

	policyGroup, policyGroupPath, err := service.buildPolicyGroup(obj, createdFor)
	if err != nil {
		log.Error(err, "Failed to build policy group", "policy", *obj)
		return nil, nil, nil, err
	}

	nsxSecurityPolicy.Scope = []string{policyGroupPath}
	if policyGroup != nil {
		nsxGroups = append(nsxGroups, *policyGroup)
	}
	currentSet := sets.Set[string]{}
	for ruleIdx, r := range obj.Spec.Rules {
		rule := r
		// A rule containing named port may be expanded to multiple rules if the named ports map to multiple port numbers.
		expandRules, buildGroups, buildGroupShares, err := service.buildRuleAndGroups(obj, &rule, ruleIdx, createdFor)
		if err != nil {
			log.Error(err, "Failed to build rule and groups", "rule", rule, "ruleIndex", ruleIdx)
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
		for _, item := range buildGroupShares {
			if item != nil {
				if !currentSet.Has(*item.share.Id) {
					currentSet.Insert(*item.share.Id)
					nsxGroupShares = append(nsxGroupShares, *item)
					nsxShareGroups = append(nsxShareGroups, *item.shareGroup)
					nsxShares = append(nsxShares, *item.share)
				}
			}
		}

	}
	nsxSecurityPolicy.Rules = nsxRules
	nsxSecurityPolicy.Tags = service.buildBasicTags(obj, createdFor)
	// nsxRules info are included in nsxSecurityPolicy obj
	log.Info("Built nsxSecurityPolicy", "nsxSecurityPolicy", nsxSecurityPolicy, "nsxGroups", nsxGroups,
		"nsxShareGroups", nsxShareGroups, "nsxShares", nsxShares)

	return nsxSecurityPolicy, &nsxGroups, &nsxGroupShares, nil
}

func (service *SecurityPolicyService) buildPolicyGroup(obj *v1alpha1.SecurityPolicy, createdFor string) (*model.Group, string, error) {
	policyAppliedGroup := model.Group{}
	policyAppliedGroup.Id = String(service.buildAppliedGroupID(obj, -1))

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
	log.V(2).Info("Build policy target group criteria",
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

	policyAppliedGroupPath, err := service.buildAppliedGroupPath(obj, -1)
	if err != nil {
		return nil, "", err
	}

	log.V(1).Info("Built policy target group", "policyAppliedGroup", policyAppliedGroup)
	return &policyAppliedGroup, policyAppliedGroupPath, nil
}

func (service *SecurityPolicyService) buildTargetTags(obj *v1alpha1.SecurityPolicy, targets *[]v1alpha1.SecurityPolicyTarget,
	rule *v1alpha1.SecurityPolicyRule, ruleIdx int, createdFor string,
) []model.Tag {
	basicTags := service.buildBasicTags(obj, createdFor)
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

	// In non-VPC network, there is no need to add NSX share createdFor tag for target groups
	if IsVPCEnabled(service) {
		// the target group for policy or rule is always not group shared
		// because target group doesn't have nameSpace selector
		targetTags = append(targetTags,
			model.Tag{
				Scope: String(common.TagScopeNSXShareCreatedFor),
				Tag:   String(common.TagValueShareNotCreated),
			},
		)
	}

	if ruleIdx != -1 && rule != nil {
		// the appliedTo group belongs to a rule, so it needs a tag including the rule id
		targetTags = append(targetTags,
			model.Tag{
				Scope: String(common.TagScopeRuleID),
				Tag:   String(service.buildRuleID(obj, ruleIdx)),
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
func (service *SecurityPolicyService) buildAppliedGroupID(obj *v1alpha1.SecurityPolicy, ruleIdx int) string {
	if IsVPCEnabled(service) {
		suffix := common.TargetGroupSuffix
		if ruleIdx != -1 {
			ruleHash := service.buildLimitedRuleHashString(&(obj.Spec.Rules[ruleIdx]))
			suffix = strings.Join([]string{ruleHash, suffix}, common.ConnectorUnderline)
		}
		return util.GenerateIDByObjectWithSuffix(obj, suffix)
	}

	ruleIdxStr := ""
	if ruleIdx != -1 {
		ruleIdxStr = fmt.Sprintf("%d", ruleIdx)
	}
	prefix := common.SecurityPolicyPrefix
	return util.GenerateID(string(obj.UID), prefix, common.TargetGroupSuffix, ruleIdxStr)
}

// build appliedTo group path for both policy and rule levels.
func (service *SecurityPolicyService) buildAppliedGroupPath(obj *v1alpha1.SecurityPolicy, ruleIdx int) (string, error) {
	groupID := service.buildAppliedGroupID(obj, ruleIdx)

	if IsVPCEnabled(service) {
		vpcInfo, err := service.getVPCInfo(obj.ObjectMeta.Namespace)
		if err != nil {
			return "", err
		}
		orgID := (*vpcInfo).OrgID
		projectID := (*vpcInfo).ProjectID
		vpcID := (*vpcInfo).VPCID
		return fmt.Sprintf("/orgs/%s/projects/%s/vpcs/%s/groups/%s", orgID, projectID, vpcID, groupID), nil
	}

	return fmt.Sprintf("/infra/domains/%s/groups/%s", getDomain(service), groupID), nil
}

// build appliedTo group display name for both policy and rule levels.
func (service *SecurityPolicyService) buildAppliedGroupName(obj *v1alpha1.SecurityPolicy, ruleIdx int) string {
	if ruleIdx != -1 {
		ruleHash := service.buildLimitedRuleHashString(&(obj.Spec.Rules[ruleIdx]))
		suffix := strings.Join([]string{ruleHash, common.TargetGroupSuffix}, common.ConnectorUnderline)
		return util.GenerateTruncName(common.MaxNameLength, obj.Name, "", suffix, "", "")
	}

	return util.GenerateTruncName(common.MaxNameLength, obj.Name, "", common.TargetGroupSuffix, "", "")
}

func (service *SecurityPolicyService) buildRuleAndGroups(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule,
	ruleIdx int, createdFor string,
) ([]*model.Rule, []*model.Group, []*GroupShare, error) {
	var ruleGroups []*model.Group
	var nsxRuleAppliedGroup *model.Group
	var nsxRuleSrcGroup *model.Group
	var nsxRuleDstGroup *model.Group
	var nsxGroupShare *GroupShare
	var nsxGroupShares []*GroupShare
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
			nsxRuleSrcGroup, nsxRuleSrcGroupPath, nsxRuleDstGroupPath, nsxGroupShare, err = service.buildRuleInGroup(
				obj, rule, nsxRule, ruleIdx, createdFor)
			if err != nil {
				return nil, nil, nil, err
			}

			if nsxRuleSrcGroup != nil {
				ruleGroups = append(ruleGroups, nsxRuleSrcGroup)
			}
		} else if ruleDirection == "OUT" {
			nsxRuleDstGroup, nsxRuleSrcGroupPath, nsxRuleDstGroupPath, nsxGroupShare, err = service.buildRuleOutGroup(
				obj, rule, nsxRule, ruleIdx, createdFor)
			if err != nil {
				return nil, nil, nil, err
			}

			if nsxRuleDstGroup != nil {
				ruleGroups = append(ruleGroups, nsxRuleDstGroup)
			}
		}
		if nsxGroupShare != nil {
			nsxGroupShares = append(nsxGroupShares, nsxGroupShare)
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
	return nsxRules, ruleGroups, nsxGroupShares, nil
}

func buildRuleServiceEntries(port v1alpha1.SecurityPolicyPort) *data.StructValue {
	var portRange string
	sourcePorts := data.NewListValue()
	destinationPorts := data.NewListValue()

	// Note: the caller ensures the given port.Port type is Int. For named port case, the caller should
	// convert to a new SecurityPolicyPort using the correct port number.
	// In case that the destination_port in NSX-T is 0.
	if port.EndPort == 0 {
		portRange = port.Port.String()
	} else {
		portRange = fmt.Sprintf("%s-%d", port.Port.String(), port.EndPort)
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
	log.V(1).Info("Built rule service entry", "destinationPorts", portRange, "protocol", port.Protocol)
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
	log.V(1).Info("Built rule target group", "ruleAppliedGroup", nsxRuleAppliedGroup)
	return nsxRuleAppliedGroup, nsxRuleAppliedGroupPath, nil
}

func (service *SecurityPolicyService) buildRuleInGroup(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule,
	nsxRule *model.Rule, ruleIdx int, createdFor string,
) (*model.Group, string, string, *GroupShare, error) {
	var nsxRuleSrcGroup *model.Group
	var nsxGroupShare *GroupShare
	var nsxRuleSrcGroupPath string
	var nsxRuleDstGroupPath string
	var err error
	if len(rule.Sources) > 0 {
		nsxRuleSrcGroup, nsxRuleSrcGroupPath, nsxGroupShare, err = service.buildRulePeerGroup(obj, rule, ruleIdx, true, createdFor)
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
	return nsxRuleSrcGroup, nsxRuleSrcGroupPath, nsxRuleDstGroupPath, nsxGroupShare, nil
}

func (service *SecurityPolicyService) buildRuleOutGroup(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule,
	nsxRule *model.Rule, ruleIdx int, createdFor string,
) (*model.Group, string, string, *GroupShare, error) {
	var nsxRuleDstGroup *model.Group
	var nsxGroupShare *GroupShare
	var nsxRuleSrcGroupPath string
	var nsxRuleDstGroupPath string
	var err error
	if len(nsxRule.DestinationGroups) > 0 {
		nsxRuleDstGroupPath = nsxRule.DestinationGroups[0]
	} else {
		if len(rule.Destinations) > 0 {
			nsxRuleDstGroup, nsxRuleDstGroupPath, nsxGroupShare, err = service.buildRulePeerGroup(obj, rule, ruleIdx, false, createdFor)
			if err != nil {
				return nil, "", "", nil, err
			}
		} else {
			nsxRuleDstGroupPath = "ANY"
		}
	}
	nsxRuleSrcGroupPath = "ANY"
	return nsxRuleDstGroup, nsxRuleSrcGroupPath, nsxRuleDstGroupPath, nsxGroupShare, nil
}

func (service *SecurityPolicyService) buildRuleID(obj *v1alpha1.SecurityPolicy, ruleIdx int) string {
	ruleIndexHash := service.buildRuleHashString(&(obj.Spec.Rules[ruleIdx]))

	if IsVPCEnabled(service) {
		ruleIndexHash = service.buildLimitedRuleHashString(&(obj.Spec.Rules[ruleIdx]))
		return util.GenerateIDByObjectWithSuffix(obj, ruleIndexHash)
	}

	prefix := common.SecurityPolicyPrefix
	ruleIdxStr := fmt.Sprintf("%d", ruleIdx)
	return strings.Join([]string{prefix, string(obj.UID), ruleIndexHash, ruleIdxStr}, common.ConnectorUnderline)
}

// A rule containing named port may be expanded to multiple NSX rules if the name ports map to multiple port numbers.
// So, in VPC network, the rule port numbers, which either are defined in rule Port or resolved from named port, will be appended as CR rule baseID to distinguish them.
// For T1, the portIdx and portAddressIdx are appended as suffix.
func (service *SecurityPolicyService) buildExpandedRuleID(obj *v1alpha1.SecurityPolicy, ruleIdx int,
	createdFor string, namedPort *portInfo,
) string {
	ruleBaseID := service.buildRuleID(obj, ruleIdx)

	if IsVPCEnabled(service) {
		portNumberSuffix := ""
		if namedPort != nil {
			portNumberSuffix = service.buildRulePortNumberString(namedPort.port)
		} else {
			portNumberSuffix = service.buildRulePortsNumberString(obj.Spec.Rules[ruleIdx].Ports)
		}
		return strings.Join([]string{ruleBaseID, portNumberSuffix}, common.ConnectorUnderline)
	}

	// With T1 topology, the NSX Rule ID includes the index of the rule's SecurityPolicyPort and the
	// index of the PortAddress, this is to make ID format consistent with named port case. For a Rule
	// without any named ports, 0 is used for both SecurityPolicyPort and PortAddress indexes.
	idSuffix := "0_0"
	if namedPort != nil {
		idSuffix = namedPort.idSuffix
	}
	return strings.Join([]string{ruleBaseID, idSuffix}, common.ConnectorUnderline)
}

func (service *SecurityPolicyService) buildRuleDisplayName(rule *v1alpha1.SecurityPolicyRule, createdFor string, namedPortInfo *portInfo) (string, error) {
	var ruleName string
	var ruleAct string

	ruleDirection, err := getRuleDirection(rule)
	if err != nil {
		return "", err
	}
	ruleAction, err := getRuleAction(rule)
	if err != nil {
		return "", err
	}

	switch ruleAction {
	case util.ToUpper(v1alpha1.RuleActionAllow):
		ruleAct = common.RuleActionAllow
	case util.ToUpper(v1alpha1.RuleActionDrop):
		ruleAct = common.RuleActionDrop
	case util.ToUpper(v1alpha1.RuleActionReject):
		ruleAct = common.RuleActionReject
	}
	ruleDir := common.RuleEgress
	if ruleDirection == "IN" {
		ruleDir = common.RuleIngress
	}
	suffix := strings.Join([]string{ruleDir, ruleAct}, common.ConnectorUnderline)

	if len(rule.Name) > 0 {
		// For the internal security policy rule converted from network policy, skipping to add suffix for the rule name
		// if it has its own name generated, usually, it's for the internal isolation security policy rule created for network policy.
		ruleName = rule.Name
		// We don't append the suffix to display name if the NSX security rule is created for a NetworkPolicy and
		// its rule.Name is set. This is applicable for the internally generated "isolation" SecurityPolicy from a
		// user created NetworkPolicy.
		if createdFor == common.ResourceTypeNetworkPolicy {
			suffix = ""
		}
	} else {
		ruleName = service.buildRulePortsString(rule.Ports)
	}

	if namedPortInfo == nil {
		return util.GenerateTruncName(common.MaxNameLength, ruleName, "", suffix, "", ""), nil
	}

	// For the security policy rule with namedPort, it will be expanded to the multiple security policy rules based on resolution of named port.
	// e.g. input: security policy's rule name: TCP.http_UDP.1234_ingress_allow,
	// expand to NSX security policy rules with name TCP.http_UDP.1234.TCP.80_ingress_allow and TCP.http_UDP.1234.UDP.1234_ingress_allow.
	// in case that user defined input security policy's rule name: sp_namedport_rule,
	// expand to NSX security policy rules with name sp_namedport_rule.TCP.80_ingress_allow and sp_namedport_rule.UDP.1234_ingress_allow.

	return util.GenerateTruncName(common.MaxNameLength, ruleName+"."+service.buildRulePortString(namedPortInfo.port), "", suffix, "", ""), nil
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
		nsxRuleAppliedGroupPath, err = service.buildAppliedGroupPath(obj, -1)
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
	ruleAppliedGroupID := service.buildAppliedGroupID(obj, ruleIdx)
	ruleAppliedGroupName = service.buildAppliedGroupName(obj, ruleIdx)

	targetTags := service.buildTargetTags(obj, &appliedTo, rule, ruleIdx, createdFor)
	ruleAppliedGroupPath, err := service.buildAppliedGroupPath(obj, ruleIdx)
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
	log.V(2).Info("Build rule applied group criteria", "totalCriteria",
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

	if IsVPCEnabled(service) {
		ruleHash := service.buildLimitedRuleHashString(&(obj.Spec.Rules[ruleIdx]))
		suffix = strings.Join([]string{ruleHash, suffix}, common.ConnectorUnderline)
		return util.GenerateIDByObjectWithSuffix(obj, suffix)
	}

	return util.GenerateID(string(obj.UID), common.SecurityPolicyPrefix, suffix, strconv.Itoa(ruleIdx))
}

func (service *SecurityPolicyService) buildRulePeerGroupName(obj *v1alpha1.SecurityPolicy, ruleIdx int, isSource bool) string {
	suffix := common.DstGroupSuffix
	if isSource == true {
		suffix = common.SrcGroupSuffix
	}
	ruleHash := service.buildLimitedRuleHashString(&(obj.Spec.Rules[ruleIdx]))
	suffix = strings.Join([]string{ruleHash, suffix}, common.ConnectorUnderline)

	return util.GenerateTruncName(common.MaxNameLength, obj.Name, "", suffix, "", "")
}

func (service *SecurityPolicyService) buildRulePeerGroupPath(obj *v1alpha1.SecurityPolicy, ruleIdx int, isSource, infraGroupShared, projectGroupShared bool, vpcInfo *common.VPCResourceInfo) (string, error) {
	groupID := service.buildRulePeerGroupID(obj, ruleIdx, isSource)

	if IsVPCEnabled(service) {
		if infraGroupShared {
			return fmt.Sprintf("/infra/domains/%s/groups/%s", getDefaultProjectDomain(), groupID), nil
		}
		if projectGroupShared {
			return fmt.Sprintf("/orgs/%s/projects/%s/infra/domains/%s/groups/%s", vpcInfo.OrgID, vpcInfo.ProjectID, getVPCProjectDomain(), groupID), nil
		}
		return fmt.Sprintf("/orgs/%s/projects/%s/vpcs/%s/groups/%s", vpcInfo.OrgID, vpcInfo.ProjectID, vpcInfo.VPCID, groupID), nil
	}

	return fmt.Sprintf("/infra/domains/%s/groups/%s", getDomain(service), groupID), nil
}

func (service *SecurityPolicyService) buildRulePeerGroup(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule,
	ruleIdx int, isSource bool, createdFor string,
) (*model.Group, string, *GroupShare, error) {
	var rulePeers []v1alpha1.SecurityPolicyPeer
	var ruleDirection string
	var err error
	var vpcInfo *common.VPCResourceInfo
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
	infraGroupShared := false
	projectGroupShared := false
	for _, peer := range rulePeers {
		if peer.NamespaceSelector != nil {
			groupShared = true
			break
		}
	}

	if IsVPCEnabled(service) {
		vpcInfo, err = service.getVPCInfo(obj.ObjectMeta.Namespace)
		if err != nil {
			return nil, "", nil, err
		}
		if groupShared {
			if vpcInfo.ProjectID == common.DefaultProject {
				infraGroupShared = true
			} else {
				projectGroupShared = true
			}
		}
	}

	rulePeerGroupPath, err := service.buildRulePeerGroupPath(obj, ruleIdx, isSource, infraGroupShared, projectGroupShared, vpcInfo)
	if err != nil {
		return nil, "", nil, err
	}

	peerTags := service.buildPeerTags(obj, rule, ruleIdx, isSource, infraGroupShared, projectGroupShared, createdFor)
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
	log.V(2).Info(fmt.Sprintf("Build rule %s group criteria", ruleDirection),
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

	if IsVPCEnabled(service) && (groupShared == true) {
		var projectGroupShare GroupShare
		var infraGroupShare GroupShare

		log.V(1).Info("Building share in Namespace", "Namespace", obj.ObjectMeta.Namespace)
		if infraGroupShared == true {
			infraGroupShare.shareGroup = &rulePeerGroup
			// Share group with the project in which SecurityPolicy rule is put
			sharedWith := service.buildSharedWith(vpcInfo, true, false)
			// Build a NSX share resource in infra level
			nsxInfraShare, err := service.buildGroupShare(obj, &rulePeerGroup, []string{rulePeerGroupPath}, *sharedWith, vpcInfo, true, false, createdFor)
			if err != nil {
				log.Error(err, "Failed to build NSX infra share", "ruleGroupName", rulePeerGroupName)
				return nil, "", nil, err
			}
			infraGroupShare.share = nsxInfraShare
			return nil, rulePeerGroupPath, &infraGroupShare, err
		} else {
			projectGroupShare.shareGroup = &rulePeerGroup
			// Share group with the VPC in which SecurityPolicy rule is put
			sharedWith := service.buildSharedWith(vpcInfo, false, true)
			// Build a NSX share resource in project level
			nsxProjectShare, err := service.buildGroupShare(obj, &rulePeerGroup, []string{rulePeerGroupPath}, *sharedWith, vpcInfo, false, true, createdFor)
			if err != nil {
				log.Error(err, "Failed to build NSX project share", "ruleGroupName", rulePeerGroupName)
				return nil, "", nil, err
			}
			projectGroupShare.share = nsxProjectShare

			return nil, rulePeerGroupPath, &projectGroupShare, err
		}
	}

	return &rulePeerGroup, rulePeerGroupPath, nil, err
}

// Build rule basic info, ruleIdx is the index of the rules of security policy,
// portIdx is the index of rule's ports, portAddressIdx is the index
// of multiple port number if one named port maps to multiple port numbers.
func (service *SecurityPolicyService) buildRuleBasicInfo(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, ruleIdx int,
	createdFor string, namedPortInfo *portInfo,
) (*model.Rule, error) {
	ruleAction, err := getRuleAction(rule)
	if err != nil {
		return nil, err
	}
	ruleDirection, err := getRuleDirection(rule)
	if err != nil {
		return nil, err
	}
	displayName, err := service.buildRuleDisplayName(rule, createdFor, namedPortInfo)
	if err != nil {
		log.Error(err, "Failed to build rule's display name", "securityPolicyUID", obj.UID, "rule", rule, "createdFor", createdFor)
	}

	nsxRule := model.Rule{
		Id:             String(service.buildExpandedRuleID(obj, ruleIdx, createdFor, namedPortInfo)),
		DisplayName:    &displayName,
		Direction:      &ruleDirection,
		SequenceNumber: Int64(int64(ruleIdx)),
		Action:         &ruleAction,
		Services:       []string{"ANY"},
		Tags:           service.buildBasicTags(obj, createdFor),
	}
	log.V(1).Info("Built rule basic info", "nsxRule", nsxRule)
	return &nsxRule, nil
}

func (service *SecurityPolicyService) buildPeerTags(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, ruleIdx int, isSource, infraGroupShared, projectGroupShared bool, createdFor string) []model.Tag {
	basicTags := service.buildBasicTags(obj, createdFor)
	groupTypeTag := String(common.TagValueGroupDestination)
	peers := &rule.Destinations
	if isSource == true {
		groupTypeTag = String(common.TagValueGroupSource)
		peers = &rule.Sources
	}
	serializedBytes, _ := json.Marshal(*peers)

	peerTags := []model.Tag{
		{
			Scope: String(common.TagScopeGroupType),
			Tag:   groupTypeTag,
		},
		{
			Scope: String(common.TagScopeRuleID),
			Tag:   String(service.buildRuleID(obj, ruleIdx)),
		},
		{
			Scope: String(common.TagScopeSelectorHash),
			Tag:   String(util.Sha1(string(serializedBytes))),
		},
	}
	for _, tag := range basicTags {
		peerTags = append(peerTags, tag)
	}

	// In non-VPC network, there is no need to add NSX share createdFor tag for rule peer groups
	if IsVPCEnabled(service) {
		if infraGroupShared {
			peerTags = append(peerTags,
				model.Tag{
					Scope: String(common.TagScopeNSXShareCreatedFor),
					Tag:   String(common.TagValueShareCreatedForInfra),
				},
			)
		} else if projectGroupShared {
			peerTags = append(peerTags,
				model.Tag{
					Scope: String(common.TagScopeNSXShareCreatedFor),
					Tag:   String(common.TagValueShareCreatedForProject),
				},
			)
		} else {
			peerTags = append(peerTags,
				model.Tag{
					Scope: String(common.TagScopeNSXShareCreatedFor),
					Tag:   String(common.TagValueShareNotCreated),
				},
			)
		}
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
	if IsVPCEnabled(service) {
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

		if IsVPCEnabled(service) {
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
	isVpcEnable := IsVPCEnabled(service)

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

	log.V(2).Info("Update peer expressions", "ruleIndex", ruleIdx)
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

func (service *SecurityPolicyService) buildShareName(nsxProjectID, groupName string) string {
	resName := strings.Join([]string{nsxProjectID, "group", groupName}, common.ConnectorUnderline)
	nsxShareName := util.GenerateTruncName(common.MaxNameLength, resName, "", common.ShareSuffix, "", "")
	return nsxShareName
}

func (service *SecurityPolicyService) buildShareID(nsxProjectID, groupID string) string {
	resID := strings.Join([]string{nsxProjectID, "group", groupID}, common.ConnectorUnderline)
	nsxShareId := util.GenerateID(resID, "", common.ShareSuffix, "")
	return nsxShareId
}

func (service *SecurityPolicyService) buildShareTags(obj *v1alpha1.SecurityPolicy, infraGroupShared, projectGroupShared bool, createdFor string) []model.Tag {
	basicTags := service.buildBasicTags(obj, createdFor)
	shareTags := []model.Tag{}

	for _, tag := range basicTags {
		shareTags = append(shareTags, tag)
	}

	if infraGroupShared {
		shareTags = append(shareTags,
			model.Tag{
				Scope: String(common.TagScopeNSXShareCreatedFor),
				Tag:   String(common.TagValueShareCreatedForInfra),
			},
		)
	} else if projectGroupShared {
		shareTags = append(shareTags,
			model.Tag{
				Scope: String(common.TagScopeNSXShareCreatedFor),
				Tag:   String(common.TagValueShareCreatedForProject),
			},
		)
	}

	return shareTags
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

func (service *SecurityPolicyService) buildGroupShare(obj *v1alpha1.SecurityPolicy, group *model.Group,
	sharedGroupPath, sharedWith []string, vpcInfo *common.VPCResourceInfo, infraGroupShared, projectGroupShared bool, createdFor string,
) (*model.Share, error) {
	var shareId string
	var shareTags []model.Tag
	var groupShare model.Share
	var shareName string

	resourceType := common.ResourceTypeShare
	projectID := vpcInfo.ProjectID
	shareId = service.buildShareID(projectID, *group.Id)
	shareName = service.buildShareName(projectID, *group.DisplayName)
	shareTags = service.buildShareTags(obj, infraGroupShared, projectGroupShared, createdFor)
	childSharedResource, err := service.buildChildSharedResource(shareId, sharedGroupPath)
	if err != nil {
		return nil, err
	}

	groupShare = model.Share{
		Id:           &shareId,
		DisplayName:  &shareName,
		Tags:         shareTags,
		ResourceType: &resourceType,
		SharedWith:   sharedWith,
		// Sharing Strategy is default value: NONE_DESCENDANTS if sharing with non-default projects.
		Children: childSharedResource,
	}
	if infraGroupShared {
		// Sharing Strategy must be ALL_DESCENDANTS if sharing with default project.
		groupShare.SharingStrategy = String(model.Share_SHARING_STRATEGY_ALL_DESCENDANTS)
	}

	return &groupShare, nil
}

func (service *SecurityPolicyService) buildSharedWith(vpcInfo *common.VPCResourceInfo, infraGroupShared, projectGroupShared bool) *[]string {
	var sharedWith []string
	if infraGroupShared {
		sharedWithPath := fmt.Sprintf("/orgs/%s/projects/%s", vpcInfo.OrgID, vpcInfo.ProjectID)
		sharedWith = append(sharedWith, sharedWithPath)
		return &sharedWith
	}
	if projectGroupShared {
		sharedWithPath := vpcInfo.GetVPCPath()
		sharedWith = append(sharedWith, sharedWithPath)
		return &sharedWith
	}
	return nil
}

func (service *SecurityPolicyService) getNamespaceUID(ns string) (nsUid types.UID) {
	namespace := &corev1.Namespace{}
	namespacedName := types.NamespacedName{
		Name: ns,
	}
	if err := service.Client.Get(context.Background(), namespacedName, namespace); err != nil {
		log.Error(err, "Failed to get namespace UID", "namespace", ns)
		return ""
	}
	namespace_uid := namespace.UID
	return namespace_uid
}

func (service *SecurityPolicyService) buildRulePortString(port v1alpha1.SecurityPolicyPort) string {
	return fmt.Sprintf("%s.%s", port.Protocol, service.buildRulePortNumberString(port))
}

func (service *SecurityPolicyService) buildRulePortsString(ports []v1alpha1.SecurityPolicyPort) string {
	if ports == nil || len(ports) == 0 {
		return common.RuleAnyPorts
	}
	portStrings := make([]string, len(ports))
	for idx := range ports {
		portStrings[idx] = service.buildRulePortString(ports[idx])
	}
	return strings.Join(portStrings, common.ConnectorUnderline)
}

func (service *SecurityPolicyService) buildRulePortNumberString(port v1alpha1.SecurityPolicyPort) string {
	// Build the rule port number string name for non named port.
	// This is a common case where the string is built from port definition. For instance,
	// - protocol: TCP
	//   port: 8282
	//   endPort: 8286
	// The built port number string is: 8282.8286
	// - protocol: UDP
	//   port: 3308
	// The built port number string is: 3308
	if port.EndPort != 0 {
		return fmt.Sprintf("%s.%d", (port.Port).String(), port.EndPort)
	}
	return (port.Port).String()
}

func (service *SecurityPolicyService) buildRulePortsNumberString(ports []v1alpha1.SecurityPolicyPort) string {
	if ports == nil || len(ports) == 0 {
		return common.RuleAnyPorts
	}

	portNumStrings := make([]string, len(ports))
	for idx, p := range ports {
		port := p
		portNumStrings[idx] = service.buildRulePortNumberString(port)
	}
	return strings.Join(portNumStrings, common.ConnectorUnderline)
}

func (service *SecurityPolicyService) buildLimitedRuleHashString(rule *v1alpha1.SecurityPolicyRule) string {
	serializedBytes, _ := json.Marshal(rule)
	return util.Sha1(string(serializedBytes))[:common.HashLength]
}

func (service *SecurityPolicyService) buildRuleHashString(rule *v1alpha1.SecurityPolicyRule) string {
	serializedBytes, _ := json.Marshal(rule)
	return util.Sha1(string(serializedBytes))
}

func (service *SecurityPolicyService) BuildNetworkPolicyAllowPolicyID(uid string) string {
	return strings.Join([]string{uid, common.RuleActionAllow}, common.ConnectorUnderline)
}

func (service *SecurityPolicyService) BuildNetworkPolicyIsolationPolicyID(uid string) string {
	return strings.Join([]string{uid, common.RuleActionDrop}, common.ConnectorUnderline)
}

type portInfo struct {
	port v1alpha1.SecurityPolicyPort
	ips  []string

	// idSuffix is used in T1 environment to generate the NSX rule ID. It is constructed by
	// SecurityPolicyPortIdx_PortAddressIdx.
	// TODO: Remove this field after we don't use the SecurityPolicyPort index in NSX rule ID with T1 topology.
	idSuffix string
}

func newPortInfo(port v1alpha1.SecurityPolicyPort) *portInfo {
	return &portInfo{
		port:     port,
		idSuffix: fmt.Sprintf("0%s0", common.ConnectorUnderline),
	}
}

// newPortInfoForNamedPort constructs a new SecurityPolicyPort object using the provided protocol and
// portAddr, and the generated SecurityPolicyPort.Port type is Int.
// Note, we don't support the case a SecurityPolicyPort is configured with a named port and end port at the
// same time, so we don't set the generated SecurityPolicyPort.EndPort in this function. The caller has a
// pre-check on that case.
func newPortInfoForNamedPort(portAddr nsxutil.PortAddress, protocol corev1.Protocol) *portInfo {
	return &portInfo{
		port: v1alpha1.SecurityPolicyPort{
			Protocol: protocol,
			Port:     intstr.FromInt32(int32(portAddr.Port)),
		},
		ips:      portAddr.IPs,
		idSuffix: fmt.Sprintf("0%s0", common.ConnectorUnderline),
	}
}
