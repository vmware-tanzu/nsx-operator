/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package securitypolicy

import (
	"fmt"
	"strings"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

// isNamespaceGroupSupported checks whether the NSX backend supports inventory-based
// Namespace/VirtualMachine member types for group expressions.
func (service *SecurityPolicyService) isNamespaceGroupSupported() bool {
	if service.NSXClient == nil || service.NSXClient.Cluster == nil {
		return false
	}
	return service.NSXClient.NSXCheckVersion(nsx.NamespaceGroup)
}

// buildNativeCondition builds a Condition expression for inventory-based groups.
// The NSX API uses: resource_type=Condition, member_type, key=Tag, operator, value=scope|tag, scope_operator.
func (service *SecurityPolicyService) buildNativeCondition(memberType, value, operator, scopeOp string) *data.StructValue {
	return service.buildExpression("Condition", memberType, value, "Tag", operator, scopeOp)
}

// buildNativeNamespaceCondition builds a Namespace tag condition for the given memberType.
// value format: "scope|tag" (e.g., "vm_namespace|ns-uid-123")
func (service *SecurityPolicyService) buildNativeNamespaceCondition(memberType, tagScope, tagValue, operator string) *data.StructValue {
	return service.buildNativeCondition(
		memberType,
		fmt.Sprintf("%s|%s", tagScope, tagValue),
		operator, "EQUALS",
	)
}

// buildNativeNestedExpression wraps a list of conditions.
// If there is only 1 condition, return it directly without a NestedExpression wrapper.
// If there are multiple conditions, wrap them into a NestedExpression with AND conjunctions.
func (service *SecurityPolicyService) buildNativeNestedExpression(conditions []*data.StructValue) *data.StructValue {
	if len(conditions) == 0 {
		return nil
	}
	if len(conditions) == 1 {
		return conditions[0]
	}

	expressions := data.NewListValue()
	for i, cond := range conditions {
		if i > 0 {
			expressions.Add(service.buildConjOperator("AND"))
		}
		expressions.Add(cond)
	}
	return data.NewStructValue(
		"",
		map[string]data.DataValue{
			"resource_type": data.NewStringValue("NestedExpression"),
			"expressions":   expressions,
		},
	)
}

func formatNativeTagScope(key string, memberType string) string {
	if memberType == common.MemberTypeVirtualMachine && !strings.HasPrefix(key, "K8sTag/") {
		return fmt.Sprintf("K8sTag/%s", key)
	}
	return key
}

// buildNativeMatchLabelsConditions converts matchLabels into a list of tag conditions for the given memberType.
func (service *SecurityPolicyService) buildNativeMatchLabelsConditions(matchLabels map[string]string, memberType string) []*data.StructValue {
	var conditions []*data.StructValue
	for k, v := range *util.NormalizeLabels(&matchLabels) {
		tagScope := formatNativeTagScope(k, memberType)
		conditions = append(conditions, service.buildNativeCondition(
			memberType,
			fmt.Sprintf("%s|%s", tagScope, v),
			"EQUALS", "EQUALS",
		))
	}
	return conditions
}

// buildNativeSelectorConditions builds conditions for a LabelSelector (matchLabels + matchExpressions)
// for the given memberType. Returns baseConditions, inExpr (if an In operator is present), and error.
func (service *SecurityPolicyService) buildNativeSelectorConditions(
	selector *v1.LabelSelector, memberType string,
) ([]*data.StructValue, *v1.LabelSelectorRequirement, error) {
	if selector == nil {
		return nil, nil, nil
	}

	if len(selector.MatchExpressions) > 0 {
		if _, err := service.validateSelectorOpIn(selector.MatchExpressions, selector.MatchLabels); err != nil {
			return nil, nil, err
		}
	}

	var baseConditions []*data.StructValue
	var inExpr *v1.LabelSelectorRequirement

	baseConditions = append(baseConditions, service.buildNativeMatchLabelsConditions(selector.MatchLabels, memberType)...)

	for i := range selector.MatchExpressions {
		expr := selector.MatchExpressions[i]
		tagScope := formatNativeTagScope(expr.Key, memberType)
		switch expr.Operator {
		case v1.LabelSelectorOpIn:
			exprCopy := expr
			exprCopy.Key = tagScope
			inExpr = &exprCopy
		case v1.LabelSelectorOpNotIn:
			joinValues := strings.Join(expr.Values[:], ",")
			baseConditions = append(baseConditions, service.buildNativeCondition(
				memberType,
				fmt.Sprintf("%s|%s", tagScope, joinValues),
				"NOTIN", "EQUALS",
			))
		case v1.LabelSelectorOpExists:
			baseConditions = append(baseConditions, service.buildNativeCondition(
				memberType,
				fmt.Sprintf("%s|", tagScope),
				"EQUALS", "EQUALS",
			))
		case v1.LabelSelectorOpDoesNotExist:
			baseConditions = append(baseConditions, service.buildExpression(
				"Condition", memberType,
				fmt.Sprintf("%s|", tagScope),
				"Tag", "", "NOTEQUALS",
			))
		default:
			return nil, nil, &nsxutil.ValidationError{
				Desc: fmt.Sprintf("unsupported matchExpressions operator: %s", expr.Operator),
			}
		}
	}

	return baseConditions, inExpr, nil
}

// updateNativeTargetExpressions builds inventory-based expressions for an appliedTo target.
// Uses NestedExpression with Namespace + VirtualMachine conditions.
func (service *SecurityPolicyService) updateNativeTargetExpressions(
	obj *v1alpha1.SecurityPolicy,
	target *v1alpha1.SecurityPolicyTarget,
	group *model.Group,
) (int, int, error) {
	if target.PodSelector != nil && target.VMSelector != nil {
		return 0, 0, &nsxutil.ValidationError{
			Desc: "PodSelector and VMSelector are not allowed to set in one group",
		}
	}

	var selector *v1.LabelSelector

	if target.PodSelector != nil {
		selector = target.PodSelector
	} else if target.VMSelector != nil {
		selector = target.VMSelector
	} else {
		return 0, 0, nil
	}

	var baseConditions []*data.StructValue

	// VM/Pod selector conditions
	selectorConds, inExpr, err := service.buildNativeSelectorConditions(selector, common.MemberTypeVirtualMachine)
	if err != nil {
		return 0, 0, err
	}
	baseConditions = append(baseConditions, selectorConds...)

	addedCount := 0
	totalConds := 0

	if inExpr == nil || len(inExpr.Values) == 0 {
		nested := service.buildNativeNestedExpression(baseConditions)
		if nested != nil {
			service.appendOperatorIfNeeded(&group.Expression, "OR")
			group.Expression = append(group.Expression, nested)
			addedCount = 1
			totalConds = len(baseConditions)
		}
	} else {
		for _, val := range inExpr.Values {
			branchConds := append([]*data.StructValue{}, baseConditions...)
			branchConds = append(branchConds, service.buildNativeCondition(
				common.MemberTypeVirtualMachine,
				fmt.Sprintf("%s|%s", inExpr.Key, val),
				"EQUALS", "EQUALS",
			))
			nested := service.buildNativeNestedExpression(branchConds)
			if nested != nil {
				service.appendOperatorIfNeeded(&group.Expression, "OR")
				group.Expression = append(group.Expression, nested)
				addedCount++
				totalConds += len(branchConds)
			}
		}
	}

	return addedCount, totalConds, nil
}

// updateNativePeerExpressions builds inventory-based expressions for a rule peer.
// Handles: namespace-only, pod/vm-only, combined, and IPBlock selectors.
func (service *SecurityPolicyService) updateNativePeerExpressions(
	obj *v1alpha1.SecurityPolicy,
	peer *v1alpha1.SecurityPolicyPeer,
	group *model.Group,
) (int, int, error) {
	if peer.PodSelector != nil && peer.VMSelector != nil {
		return 0, 0, &nsxutil.ValidationError{
			Desc: "PodSelector and VMSelector are not allowed to set in one group",
		}
	}

	// Handle IPBlocks
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
		return 0, 0, nil
	}

	var allBaseConditions []*data.StructValue
	var nsInExpr *v1.LabelSelectorRequirement
	var vmInExpr *v1.LabelSelectorRequirement

	// Build namespace conditions
	if peer.NamespaceSelector != nil {
		nsConds, nsIn, err := service.buildNativeSelectorConditions(peer.NamespaceSelector, common.MemberTypeNamespace)
		if err != nil {
			return 0, 0, err
		}
		nsInExpr = nsIn
		if len(nsConds) == 0 && nsIn == nil && peer.NamespaceSelector.Size() == 0 {
			// Empty namespace selector means "all namespaces" — add cluster-scoped namespace condition
			nsConds = append(nsConds, service.buildNativeNamespaceCondition(
				common.MemberTypeNamespace,
				getScopeCluserTag(service), getCluster(service), "EQUALS",
			))
		}
		allBaseConditions = append(allBaseConditions, nsConds...)
	}

	// Build VM/Pod conditions
	if peer.PodSelector != nil {
		vmConds, vmIn, err := service.buildNativeSelectorConditions(peer.PodSelector, common.MemberTypeVirtualMachine)
		if err != nil {
			return 0, 0, err
		}
		vmInExpr = vmIn
		allBaseConditions = append(allBaseConditions, vmConds...)
	} else if peer.VMSelector != nil {
		vmConds, vmIn, err := service.buildNativeSelectorConditions(peer.VMSelector, common.MemberTypeVirtualMachine)
		if err != nil {
			return 0, 0, err
		}
		vmInExpr = vmIn
		allBaseConditions = append(allBaseConditions, vmConds...)
	}

	nsVals := []string{""}
	if nsInExpr != nil && len(nsInExpr.Values) > 0 {
		nsVals = nsInExpr.Values
	}

	vmVals := []string{""}
	if vmInExpr != nil && len(vmInExpr.Values) > 0 {
		vmVals = vmInExpr.Values
	}

	addedCount := 0
	totalConds := 0

	for _, nsVal := range nsVals {
		for _, vmVal := range vmVals {
			branchConds := append([]*data.StructValue{}, allBaseConditions...)
			if nsVal != "" {
				branchConds = append(branchConds, service.buildNativeCondition(
					common.MemberTypeNamespace,
					fmt.Sprintf("%s|%s", nsInExpr.Key, nsVal),
					"EQUALS", "EQUALS",
				))
			}
			if vmVal != "" {
				branchConds = append(branchConds, service.buildNativeCondition(
					common.MemberTypeVirtualMachine,
					fmt.Sprintf("%s|%s", vmInExpr.Key, vmVal),
					"EQUALS", "EQUALS",
				))
			}
			nested := service.buildNativeNestedExpression(branchConds)
			if nested != nil {
				service.appendOperatorIfNeeded(&group.Expression, "OR")
				group.Expression = append(group.Expression, nested)
				addedCount++
				totalConds += len(branchConds)
			}
		}
	}

	return addedCount, totalConds, nil
}

// buildNativePolicyGroup builds the policy-level applied-to group using inventory-based expressions.
func (service *SecurityPolicyService) buildNativePolicyGroup(
	obj *v1alpha1.SecurityPolicy,
	createdFor string,
	vpcInfo *common.VPCResourceInfo,
) (*model.Group, string, error) {
	appliedTo := obj.Spec.AppliedTo
	if len(appliedTo) == 0 {
		return nil, "ANY", nil
	}

	policyAppliedGroup := model.Group{}
	groupID, groupName := service.buildAppliedGroupIDAndName(obj, -1, "", createdFor)
	policyAppliedGroup.Id = String(groupID)
	policyAppliedGroup.DisplayName = String(groupName)

	targetTags := service.buildTargetTags(obj, &appliedTo, "", createdFor)
	policyAppliedGroup.Tags = targetTags

	for i := range appliedTo {
		_, _, err := service.updateNativeTargetExpressions(obj, &appliedTo[i], &policyAppliedGroup)
		if err != nil {
			return nil, "", err
		}
	}

	groupPath, err := service.buildAppliedGroupPath(obj, groupID, vpcInfo)
	if err != nil {
		return nil, "", err
	}
	policyAppliedGroup.Path = String(groupPath)

	log.Debug("Built native policy target group", "group", policyAppliedGroup)
	return &policyAppliedGroup, groupPath, nil
}

// buildNativeRuleAppliedGroupByRule builds a rule-level applied-to group using inventory-based expressions.
func (service *SecurityPolicyService) buildNativeRuleAppliedGroupByRule(
	obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, ruleIdx int,
	ruleBaseID, createdFor string, vpcInfo *common.VPCResourceInfo,
) (*model.Group, string, error) {
	appliedTo := rule.AppliedTo
	ruleAppliedGroupID, ruleAppliedGroupName := service.buildAppliedGroupIDAndName(obj, ruleIdx, ruleBaseID, createdFor)

	targetTags := service.buildTargetTags(obj, &appliedTo, ruleBaseID, createdFor)
	ruleAppliedGroupPath, err := service.buildAppliedGroupPath(obj, ruleAppliedGroupID, vpcInfo)
	if err != nil {
		return nil, "", err
	}
	ruleAppliedGroup := model.Group{
		Id:          &ruleAppliedGroupID,
		DisplayName: &ruleAppliedGroupName,
		Tags:        targetTags,
		Path:        &ruleAppliedGroupPath,
	}

	for i := range appliedTo {
		_, _, err := service.updateNativeTargetExpressions(obj, &appliedTo[i], &ruleAppliedGroup)
		if err != nil {
			return nil, "", err
		}
	}

	return &ruleAppliedGroup, ruleAppliedGroupPath, nil
}

// buildNativeRulePeerGroup builds a rule peer group (source or destination) using inventory-based expressions.
// In the inventory-based model, all groups are placed under Project Infra scope.
func (service *SecurityPolicyService) buildNativeRulePeerGroup(
	obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule,
	ruleIdx int, ruleBaseID string, isSource bool, createdFor string,
	vpcInfo *common.VPCResourceInfo,
) (*model.Group, string, *GroupShare, error) {
	var rulePeers []v1alpha1.SecurityPolicyPeer
	var ruleDirection string

	if isSource {
		rulePeers = getRuleSourcePeers(rule)
		ruleDirection = "source"
	} else {
		rulePeers = getRuleDestinationPeers(rule)
		ruleDirection = "destination"
	}

	groupScope := ProjectInfraScopeGroup
	rulePeerGroupID := service.buildRulePeerGroupID(obj, ruleIdx, ruleBaseID, isSource, groupScope)
	rulePeerGroupName := service.buildRulePeerGroupName(obj, ruleIdx, isSource)
	rulePeerGroupPath, err := service.buildNativeGroupPath(rulePeerGroupID, vpcInfo)
	if err != nil {
		return nil, "", nil, err
	}

	peerTags := service.buildPeerTags(obj, rule, ruleBaseID, isSource, groupScope, createdFor)
	rulePeerGroup := model.Group{
		Id:          &rulePeerGroupID,
		DisplayName: &rulePeerGroupName,
		Tags:        peerTags,
		Path:        &rulePeerGroupPath,
	}

	rulePeers = service.dedupBlocks(rulePeers)
	for i := range rulePeers {
		_, _, err = service.updateNativePeerExpressions(obj, &rulePeers[i], &rulePeerGroup)
		if err != nil {
			return nil, "", nil, err
		}
	}
	log.Debug(fmt.Sprintf("Built native rule %s group", ruleDirection), "group", rulePeerGroup)

	// In inventory-based model, groups are under Project scope.
	// If VPC SecurityPolicy needs to reference this Project group, a Share is needed.
	if IsVPCEnabled(service) {
		var projectGroupShare GroupShare
		projectGroupShare.shareGroup = &rulePeerGroup
		sharedWith := service.buildSharedWith(vpcInfo, groupScope)
		nsxProjectShare, err := service.buildGroupShare(obj, &rulePeerGroup, []string{rulePeerGroupPath}, *sharedWith, vpcInfo, groupScope, createdFor)
		if err != nil {
			log.Error(err, "Failed to build NSX project share for native group", "ruleGroupName", rulePeerGroupName)
			return nil, "", nil, err
		}
		projectGroupShare.share = nsxProjectShare
		return nil, rulePeerGroupPath, &projectGroupShare, nil
	}

	return &rulePeerGroup, rulePeerGroupPath, nil, nil
}

// buildNativeGroupPath builds the group path under Project Infra domain.
// For inventory-based groups, all groups are placed under Project scope.
func (service *SecurityPolicyService) buildNativeGroupPath(groupID string, vpcInfo *common.VPCResourceInfo) (string, error) {
	if IsVPCEnabled(service) {
		if vpcInfo == nil {
			return "", fmt.Errorf("vpcInfo is nil when building native group path for group %s", groupID)
		}
		return fmt.Sprintf("/orgs/%s/projects/%s/infra/domains/%s/groups/%s",
			vpcInfo.OrgID, vpcInfo.ProjectID, getVPCProjectDomain(), groupID), nil
	}
	return fmt.Sprintf("/infra/domains/%s/groups/%s", getDomain(service), groupID), nil
}

// buildNativeRuleAppliedToGroup builds the rule-level applied-to group using inventory-based expressions.
func (service *SecurityPolicyService) buildNativeRuleAppliedToGroup(
	obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, ruleIdx int,
	nsxRuleSrcGroupPath string, nsxRuleDstGroupPath string, createdFor string,
	policyAppliedGroupPath string, ruleBaseID string, vpcInfo *common.VPCResourceInfo,
) (*model.Group, string, error) {
	if len(rule.AppliedTo) > 0 {
		return service.buildNativeRuleAppliedGroupByRule(obj, rule, ruleIdx, ruleBaseID, createdFor, vpcInfo)
	}
	appliedGroupPath, err := service.buildRuleAppliedGroupByPolicy(obj, nsxRuleSrcGroupPath, nsxRuleDstGroupPath, policyAppliedGroupPath)
	return nil, appliedGroupPath, err
}

// buildNativeRuleInGroup builds the source peer group for an ingress rule using inventory-based expressions.
func (service *SecurityPolicyService) buildNativeRuleInGroup(
	obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule,
	nsxRule *model.Rule, ruleIdx int, ruleBaseID, createdFor string,
	vpcInfo *common.VPCResourceInfo,
) (*model.Group, string, string, *GroupShare, error) {
	var nsxRuleSrcGroup *model.Group
	var nsxGroupShare *GroupShare
	var nsxRuleSrcGroupPath string
	var nsxRuleDstGroupPath string

	sources := getRuleSourcePeers(rule)
	if len(sources) > 0 {
		var err error
		nsxRuleSrcGroup, nsxRuleSrcGroupPath, nsxGroupShare, err = service.buildNativeRulePeerGroup(
			obj, rule, ruleIdx, ruleBaseID, true, createdFor, vpcInfo)
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

// buildNativeRuleOutGroup builds the destination peer group for an egress rule using inventory-based expressions.
func (service *SecurityPolicyService) buildNativeRuleOutGroup(
	obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule,
	nsxRule *model.Rule, ruleIdx int, ruleBaseID, createdFor string,
	vpcInfo *common.VPCResourceInfo,
) (*model.Group, string, string, *GroupShare, error) {
	var nsxRuleDstGroup *model.Group
	var nsxGroupShare *GroupShare
	var nsxRuleSrcGroupPath string
	var nsxRuleDstGroupPath string

	if len(nsxRule.DestinationGroups) > 0 {
		nsxRuleDstGroupPath = nsxRule.DestinationGroups[0]
	} else {
		destinations := getRuleDestinationPeers(rule)
		if len(destinations) > 0 {
			var err error
			nsxRuleDstGroup, nsxRuleDstGroupPath, nsxGroupShare, err = service.buildNativeRulePeerGroup(
				obj, rule, ruleIdx, ruleBaseID, false, createdFor, vpcInfo)
			if err != nil {
				return nil, "", "", nil, err
			}
		} else {
			nsxRuleDstGroupPath = "ANY"
		}
	}

	if len(nsxRule.SourceGroups) > 0 {
		nsxRuleSrcGroupPath = nsxRule.SourceGroups[0]
	} else {
		nsxRuleSrcGroupPath = "ANY"
	}
	return nsxRuleDstGroup, nsxRuleSrcGroupPath, nsxRuleDstGroupPath, nsxGroupShare, nil
}

// buildNativeRuleAndGroups builds rule and associated groups using inventory-based expressions.
// This is the native equivalent of buildRuleAndGroups.
func (service *SecurityPolicyService) buildNativeRuleAndGroups(
	obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule,
	ruleIdx int, createdFor string, policyGroupPath string,
	vpcInfo *common.VPCResourceInfo,
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

	ruleDirection, err := getRuleDirection(rule)
	if err != nil {
		return nil, nil, nil, err
	}

	ruleBaseID := service.buildRuleID(obj, ruleIdx, createdFor)
	ipSetGroups, nsxRules, err := service.expandRule(obj, rule, ruleIdx, ruleBaseID, createdFor, vpcInfo)
	if err != nil {
		return nil, nil, nil, err
	}
	ruleGroups = append(ruleGroups, ipSetGroups...)

	for _, nsxRule := range nsxRules {
		switch ruleDirection {
		case "IN":
			nsxRuleSrcGroup, nsxRuleSrcGroupPath, nsxRuleDstGroupPath, nsxGroupShare, err = service.buildNativeRuleInGroup(
				obj, rule, nsxRule, ruleIdx, ruleBaseID, createdFor, vpcInfo)
			if err != nil {
				return nil, nil, nil, err
			}
			if nsxRuleSrcGroup != nil {
				ruleGroups = append(ruleGroups, nsxRuleSrcGroup)
			}
		case "OUT":
			nsxRuleDstGroup, nsxRuleSrcGroupPath, nsxRuleDstGroupPath, nsxGroupShare, err = service.buildNativeRuleOutGroup(
				obj, rule, nsxRule, ruleIdx, ruleBaseID, createdFor, vpcInfo)
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

		nsxRuleAppliedGroup, nsxRuleAppliedGroupPath, err = service.buildNativeRuleAppliedToGroup(
			obj, rule, ruleIdx, nsxRuleSrcGroupPath, nsxRuleDstGroupPath, createdFor, policyGroupPath, ruleBaseID, vpcInfo)
		if err != nil {
			return nil, nil, nil, err
		}
		ruleGroups = append(ruleGroups, nsxRuleAppliedGroup)
		nsxRule.Scope = []string{nsxRuleAppliedGroupPath}
	}
	return nsxRules, ruleGroups, nsxGroupShares, nil
}

// buildNativeSecurityPolicy builds the full SecurityPolicy model using inventory-based group expressions.
func (service *SecurityPolicyService) buildNativeSecurityPolicy(
	obj *v1alpha1.SecurityPolicy, createdFor string,
	vpcInfo *common.VPCResourceInfo,
) (*model.SecurityPolicy, *[]model.Group, *[]GroupShare, error) {
	var nsxRules []model.Rule
	var nsxGroups []model.Group
	var nsxShareGroups []model.Group
	var nsxShares []model.Share
	var nsxGroupShares []GroupShare

	log.Debug("Building native inventory-based SecurityPolicy from CR", "object", *obj)
	if IsVPCEnabled(service) && vpcInfo == nil {
		return nil, nil, nil, fmt.Errorf("vpcInfo is nil when building native SecurityPolicy %s", obj.GetName())
	}

	nsxSecurityPolicy := &model.SecurityPolicy{}
	tags := service.buildBasicTags(obj, createdFor)

	policyID, policyName := service.buildSecurityPolicyIDAndName(obj, createdFor)
	nsxSecurityPolicy.Id = String(policyID)
	nsxSecurityPolicy.DisplayName = String(policyName)
	nsxSecurityPolicy.SequenceNumber = Int64(int64(obj.Spec.Priority))

	policyGroup, policyGroupPath, err := service.buildNativePolicyGroup(obj, createdFor, vpcInfo)
	if err != nil {
		log.Error(err, "Failed to build native policy group", "policy", *obj)
		return nil, nil, nil, err
	}

	nsxSecurityPolicy.Scope = []string{policyGroupPath}
	if policyGroup != nil {
		nsxGroups = append(nsxGroups, *policyGroup)
	}

	ruleIDs := sets.Set[string]{}
	groupIDs := sets.Set[string]{}
	shareIDs := sets.Set[string]{}

	if policyGroup != nil && policyGroup.Id != nil {
		groupIDs.Insert(*policyGroup.Id)
	}

	for ruleIdx, r := range obj.Spec.Rules {
		rule := r
		expandRules, buildGroups, buildGroupShares, err := service.buildNativeRuleAndGroups(
			obj, &rule, ruleIdx, createdFor, policyGroupPath, vpcInfo)
		if err != nil {
			log.Error(err, "Failed to build native rule and groups", "rule", rule, "ruleIndex", ruleIdx)
			return nil, nil, nil, err
		}

		for _, nsxRule := range expandRules {
			if nsxRule != nil && nsxRule.Id != nil {
				if !ruleIDs.Has(*nsxRule.Id) {
					ruleIDs.Insert(*nsxRule.Id)
					nsxRules = append(nsxRules, *nsxRule)
				}
			}
		}

		for _, nsxGroup := range buildGroups {
			if nsxGroup != nil && nsxGroup.Id != nil {
				if !groupIDs.Has(*nsxGroup.Id) {
					groupIDs.Insert(*nsxGroup.Id)
					nsxGroups = append(nsxGroups, *nsxGroup)
				}
			}
		}

		for _, item := range buildGroupShares {
			if item != nil && item.share != nil && item.share.Id != nil {
				if !shareIDs.Has(*item.share.Id) {
					shareIDs.Insert(*item.share.Id)
					nsxGroupShares = append(nsxGroupShares, *item)
					nsxShareGroups = append(nsxShareGroups, *item.shareGroup)
					nsxShares = append(nsxShares, *item.share)
				}
			}
		}
	}

	nsxSecurityPolicy.Rules = nsxRules
	nsxSecurityPolicy.Tags = tags
	log.Info("Built native nsxSecurityPolicy", "nsxSecurityPolicy", nsxSecurityPolicy,
		"nsxGroups", nsxGroups, "nsxShareGroups", nsxShareGroups, "nsxShares", nsxShares)

	return nsxSecurityPolicy, &nsxGroups, &nsxGroupShares, nil
}
