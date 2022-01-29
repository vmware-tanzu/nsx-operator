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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
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

	policyGroup, policyGroupPath, err := service.buildPolicyGroup(obj)
	if err != nil {
		log.Error(err, "failed to build policy group")
		return nil, nil, err
	}

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

	targetGroupCount, targetGroupTotalExprCount := 0, 0
	criteriaCount, totalExprCount := 0, 0
	var err error = nil
	var errorMsg string = ""
	for i, target := range appliedTo {
		criteriaCount, totalExprCount, err = service.updateTargetExpressions(obj, &target, &policyGroup, i)
		if err == nil {
			targetGroupCount += criteriaCount
			targetGroupTotalExprCount += totalExprCount
		} else {
			return nil, "", err
		}
	}
	log.V(1).Info("build policy target group criteria", "total criteria", targetGroupCount, "total expressions of criteria", targetGroupTotalExprCount)

	if targetGroupCount > MaxCriteria {
		errorMsg = fmt.Sprintf("total counts of policy target group criteria %d exceed NSX limit of %d", targetGroupCount, MaxCriteria)
	} else if targetGroupTotalExprCount > MaxTotalCriteriaExpressions {
		errorMsg = fmt.Sprintf("total expression counts in policy target group criteria %d exceed NSX limit of %d", targetGroupTotalExprCount, MaxTotalCriteriaExpressions)
	}

	if len(errorMsg) != 0 {
		err = errors.New(errorMsg)
		log.Error(err, "validate policy group criteria nsx limit failed")
		return nil, "", err
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

func (service *SecurityPolicyService) updateTargetExpressions(obj *v1alpha1.SecurityPolicy, target *v1alpha1.SecurityPolicyTarget, group *model.Group, idx int) (int, int, error) {
	var err error = nil
	var tagValueExpression *data.StructValue = nil
	var memberType string = "SegmentPort"
	var matchLabels map[string]string
	var matchExpressions *[]metav1.LabelSelectorRequirement = nil
	var mergedMatchExpressions *[]metav1.LabelSelectorRequirement = nil
	var opInValueCount, totalCriteriaCount, totalExprCount = 0, 0, 0
	var matchLabelsCount, matchExpressionsCount = 0, 0

	if target.PodSelector != nil && target.VMSelector != nil {
		errorMsg := "PodSelector and VMSelector are not allowed to set in one group"
		err = errors.New(errorMsg)
		log.Error(err, "build selector expressions failed")
		return 0, 0, err
	}

	service.appendOperatorIfNeeded(&group.Expression, "OR")
	expressions := service.buildGroupExpression(&group.Expression)

	clusterExpression := service.buildExpression(
		"Condition", memberType,
		fmt.Sprintf("%s|%s", util.TagScopeNCPCluster, service.getCluster()),
		"Tag", "EQUALS", "EQUALS",
	)
	expressions.Add(clusterExpression)

	if target.PodSelector != nil {
		service.addOperatorIfNeeded(expressions, "AND")
		// TODO: consider to use project_uid instead of project
		nsExpression := service.buildExpression(
			"Condition", memberType,
			fmt.Sprintf("%s|%s", util.TagScopeNCPProject, obj.ObjectMeta.Namespace),
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
			"Condition", "SegmentPort",
			fmt.Sprintf("%s|%s", util.TagScopeNCPVIFProject, obj.ObjectMeta.Namespace),
			"Tag", "EQUALS", "EQUALS",
		)
		expressions.Add(nsExpression)

		tagValueExpression = nsExpression
		matchLabels = target.VMSelector.MatchLabels
		matchExpressions = &target.VMSelector.MatchExpressions
	}
	if target.PodSelector != nil || target.VMSelector != nil {
		service.updateExpressionsMatchLables(matchLabels, memberType, expressions)
		matchLabelsCount = len(matchLabels)
		// PodSelector or VMSelector has two more built-in labels
		matchLabelsCount += ClusterTagCount + ProjectTagCount

		if matchExpressions != nil {
			mergedMatchExpressions = service.mergeSelectorMatchExpression(*matchExpressions)
			matchExpressionsCount = len(*mergedMatchExpressions)
			opInValueCount, err = service.validateSelectorOpIn(*mergedMatchExpressions)

			if err != nil {
				log.Error(err, "validate operator 'IN' in label selector matchExpressions failed")
				return 0, 0, err
			}
			err = service.updateExpressionsMatchExpression(*mergedMatchExpressions, matchLabels,
				&group.Expression, clusterExpression, tagValueExpression, memberType, expressions)
			if err != nil {
				log.Error(err, "build label selector matchExpressions failed")
				return 0, 0, err
			}
		}

		totalCriteriaCount, totalExprCount, err = service.validateSelectorExpressions(matchLabelsCount, matchExpressionsCount, opInValueCount, memberType)
		if err != nil {
			log.Error(err, "validate label selector matchExpressions failed")
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

func (service *SecurityPolicyService) addOperatorIfNeeded(expressions *data.ListValue, op string) {
	if !expressions.IsEmpty() {
		operator := service.buildConjOperator(op)
		expressions.Add(operator)
	}
}

func (service *SecurityPolicyService) updateExpressionsMatchLables(matchLabels map[string]string, memberType string, expressions *data.ListValue) {
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
func (service *SecurityPolicyService) mergeSelectorMatchExpression(matchExpressions []metav1.LabelSelectorRequirement) *[]metav1.LabelSelectorRequirement {
	mergedMatchExpressions := make([]metav1.LabelSelectorRequirement, 0)
	var meregedSelector metav1.LabelSelectorRequirement
	var labelSelectorMap = map[metav1.LabelSelectorOperator]map[string][]string{}

	for _, d := range matchExpressions {
		_, exists := labelSelectorMap[d.Operator]
		if !exists {
			labelSelectorMap[d.Operator] = map[string][]string{}
		}
		_, exists = labelSelectorMap[d.Operator][d.Key]
		labelSelectorMap[d.Operator][d.Key] = append(labelSelectorMap[d.Operator][d.Key], d.Values...)

		if exists {
			labelSelectorMap[d.Operator][d.Key] = util.RemoveDuplicateStr(labelSelectorMap[d.Operator][d.Key])
		}
	}

	for key, value := range labelSelectorMap {
		for subKey, subValue := range value {
			meregedSelector.Values = subValue
			meregedSelector.Operator = key
			meregedSelector.Key = subKey
			mergedMatchExpressions = append(mergedMatchExpressions, meregedSelector)
		}
	}

	return &mergedMatchExpressions
}

// Todo, refactor code when NSX support 'In' LabelSelector.
// Given NSX currently doesn't support 'In' LabelSelector, to keep design simple,
// only allow just one 'In' LabelSelector in matchExpressions with at most of five values in it.
func (service *SecurityPolicyService) validateSelectorOpIn(matchExpressions []metav1.LabelSelectorRequirement) (int, error) {
	var mexprInOpCount = 0
	var mexprInValueCount = 0
	var err error = nil
	var errorMsg string = ""

	for _, expr := range matchExpressions {
		if expr.Operator == metav1.LabelSelectorOpIn {
			mexprInOpCount++
			mexprInValueCount += len(expr.Values)
		}
	}
	if mexprInOpCount > MaxMatchExpressionInOp {
		errorMsg = fmt.Sprintf("count of operator 'IN' expressions %d exceed limit of %d",
			mexprInOpCount, MaxMatchExpressionIn)
	} else if mexprInValueCount > MaxMatchExpressionInValues {
		errorMsg = fmt.Sprintf("count of values list for operator 'IN' expressions %d exceed limit of %d",
			mexprInValueCount, MaxMatchExpressionInValues)
	}

	if len(errorMsg) != 0 {
		err = errors.New(errorMsg)
	}
	return mexprInValueCount, err
}

func (service *SecurityPolicyService) validateSelectorExpressions(matchLabelsCount int,
	matchExpressionsCount int, opInValueCount int, memberType string) (int, int, error) {
	var err error = nil
	var errorMsg string = ""
	var totalExprCount = 0
	var totoalCriteria = 0

	// Check total count of expressions from LabelSelectors in one group criteria
	if matchExpressionsCount != 0 {
		totalExprCount = matchLabelsCount + matchExpressionsCount
	} else {
		totalExprCount = matchLabelsCount
	}

	if memberType == "SegmentPort" && totalExprCount > MaxCriteriaExpressions {
		errorMsg = fmt.Sprintf("total count of labelSelectors expressions %d exceed NSX limit of %d in one criteria based on same member type",
			totalExprCount, MaxCriteriaExpressions)
	} else if memberType == "Segment" && totalExprCount > MaxMixedCriteriaExpressions {
		// Since cluster is set as default "SegmentPort" memberType, So, group with "Segment" member is always treated as a mixed criteria
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
			totoalCriteria = opInValueCount
			totalExprCount *= opInValueCount
		} else {
			// matchExpressions will be 'AND' with matchLabels(if present) to produce 1 criteria.
			totoalCriteria = 1
		}
	}
	return totoalCriteria, totalExprCount, err
}

// Todo, refactor code when NSX support 'In' LabelSelector.
func (service *SecurityPolicyService) matchExpressionOpInExist(matchExpressions []metav1.LabelSelectorRequirement) (bool, int) {
	var opeartorInIndex = -1
	var isFound = false
	for i := 0; i < len(matchExpressions); i++ {
		// find Opetator IN
		if matchExpressions[i].Operator == metav1.LabelSelectorOpIn {
			opeartorInIndex = i
			isFound = true
			break
		}
	}
	return isFound, opeartorInIndex
}

// Todo, refactor code when NSX support 'In' LabelSelector.
// Currently NSX only supports "EQUALS" but not "IN". So, we have to make each value to be AND with other expressions
// and finally produce a union set to translate from K8s "IN" to NSX "EQUALS".
// e.g. - {key: k1, operator: NotIn, values: [a1,a2]}
//      - {key: k2, operator: In, values: [a3,a4]}
// The above two expressions will be translated to:
// => {k1 NotIn [a1,a2]} AND {k2 EQUALS a3} OR {k1 NotIn [a1,a2]} AND {k2 EQUALS a4}
func (service *SecurityPolicyService) updateExpressionsMatchExpression(matchExpressions []metav1.LabelSelectorRequirement, matchLabels map[string]string,
	policyExpression *[]*data.StructValue, clusterExpression *data.StructValue, tagValueExpression *data.StructValue,
	memberType string, expressions *data.ListValue) error {
	var err error = nil
	var found, opInIdx = service.matchExpressionOpInExist(matchExpressions)
	if !found {
		err = service.buildExpressionsMatchExpression(matchExpressions, memberType, expressions)
	} else {
		var expr = matchExpressions[opInIdx]
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
				service.updateExpressionsMatchLables(matchLabels, memberType, expressions)
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
func (service *SecurityPolicyService) updateMixedExpressionsMatchExpression(nsMatchExpressions []metav1.LabelSelectorRequirement, nsMatchLabels map[string]string,
	matchExpressions []metav1.LabelSelectorRequirement, matchLabels map[string]string,
	policyExpression *[]*data.StructValue, clusterExpression *data.StructValue, tagValueExpression *data.StructValue, expressions *data.ListValue) error {
	var err error = nil
	var opInIdx = 0
	var found bool = false
	var opInMatchExpressions []metav1.LabelSelectorRequirement = nil
	var memberType = ""

	nsFound, opInIdx1 := service.matchExpressionOpInExist(nsMatchExpressions)
	portFound, opInIdx2 := service.matchExpressionOpInExist(matchExpressions)

	if nsFound {
		opInIdx = opInIdx1
		memberType = "Segment"
		opInMatchExpressions = nsMatchExpressions
		found = true
	} else if portFound {
		opInIdx = opInIdx2
		memberType = "SegmentPort"
		opInMatchExpressions = matchExpressions
		found = true
	}

	if !found {
		err = service.buildExpressionsMatchExpression(matchExpressions, "SegmentPort", expressions)
		if err == nil {
			err = service.buildExpressionsMatchExpression(nsMatchExpressions, "Segment", expressions)
		}
	} else {
		var expr = opInMatchExpressions[opInIdx]
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

				service.updateExpressionsMatchLables(matchLabels, "SegmentPort", expressions)
				service.updateExpressionsMatchLables(nsMatchLabels, "Segment", expressions)
			}

			if nsFound {
				err = service.buildExpressionsMatchExpression(matchExpressions, "SegmentPort", expressions)
				if err != nil {
					break
				}
			} else {
				err = service.buildExpressionsMatchExpression(nsMatchExpressions, "Segment", expressions)
				if err != nil {
					break
				}
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

func (service *SecurityPolicyService) buildExpressionsMatchExpression(matchExpressions []metav1.LabelSelectorRequirement,
	memberType string, expressions *data.ListValue) error {
	var err error = nil
	var errorMsg string = ""

	for _, expr := range matchExpressions {
		switch expr.Operator {
		case metav1.LabelSelectorOpIn:
			continue

		case metav1.LabelSelectorOpNotIn:
			service.addOperatorIfNeeded(expressions, "AND")
			joinValues := strings.Join(expr.Values[:], ",")

			expression := service.buildExpression(
				"Condition", memberType,
				fmt.Sprintf("%s|%s", expr.Key, joinValues),
				"Tag", "NOTIN", "EQUALS",
			)
			expressions.Add(expression)

		case metav1.LabelSelectorOpExists:
			service.addOperatorIfNeeded(expressions, "AND")
			expression := service.buildExpression(
				"Condition", memberType,
				fmt.Sprintf("%s|", expr.Key),
				"Tag", "EQUALS", "EQUALS",
			)
			expressions.Add(expression)

		case metav1.LabelSelectorOpDoesNotExist:
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
	var err error = nil
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
		return nil, nil, errors.New("invalid rule direction")
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
			nsxRuleSrcGroup, nsxRuleSrcGroupPath, err = service.buildRuleSrcGroup(obj, rule, idx)
			if err == nil {
				ruleGroups = append(ruleGroups, *nsxRuleSrcGroup)
			} else {
				log.Error(err, "failed to build rule source groups")
				return nil, nil, err
			}
		} else {
			nsxRuleSrcGroupPath = "ANY"
		}
		nsxRuleDstGroupPath = "ANY"
	} else if direction == "OUT" {
		if len(rule.Destinations) > 0 {
			nsxRuleDstGroup, nsxRuleDstGroupPath, err = service.buildRuleDstGroup(obj, rule, idx)
			if err == nil {
				ruleGroups = append(ruleGroups, *nsxRuleDstGroup)
			} else {
				log.Error(err, "failed to build rule destination groups")
				return nil, nil, err
			}
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
		nsxRuleAppliedGroup, nsxRuleAppliedGroupPath, err = service.buildRuleAppliedGroup(obj, rule, idx)
		if err == nil {
			ruleGroups = append(ruleGroups, *nsxRuleAppliedGroup)
		} else {
			log.Error(err, "failed to build rule applied groups")
			return nil, nil, err
		}
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

	ruleGroupCount, ruleGroupTotalExprCount := 0, 0
	criteriaCount, totalExprCount := 0, 0
	var err error = nil
	var errorMsg string = ""
	for i, target := range appliedTo {
		criteriaCount, totalExprCount, err = service.updateTargetExpressions(obj, &target, &ruleAppliedGroup, i)
		if err == nil {
			ruleGroupCount += criteriaCount
			ruleGroupTotalExprCount += totalExprCount
		} else {
			return nil, "", err
		}
	}
	log.V(1).Info("build rule applied group criteria", "total criteria", ruleGroupCount, "total expressions of criteria", ruleGroupTotalExprCount)

	if ruleGroupCount > MaxCriteria {
		errorMsg = fmt.Sprintf("total counts of rule applied group criteria %d exceed NSX limit of %d", ruleGroupCount, MaxCriteria)
	} else if ruleGroupTotalExprCount > MaxTotalCriteriaExpressions {
		errorMsg = fmt.Sprintf("total expression counts in rule applied group criteria %d exceed NSX limit of %d", ruleGroupTotalExprCount, MaxTotalCriteriaExpressions)
	}

	if len(errorMsg) != 0 {
		err = errors.New(errorMsg)
		log.Error(err, "validate rule applied group criteria nsx limit failed")
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
	ruleSrcGroupPath := fmt.Sprintf("/infra/domains/%s/groups/%s", service.getDomain(), ruleSrcGroupID)
	peerTags := service.buildPeerTags(obj, &sources, idx)
	ruleSrcGroup := model.Group{
		Id:          &ruleSrcGroupID,
		DisplayName: &ruleSrcGroupName,
		Tags:        peerTags,
	}

	ruleSrcGroupCount, ruleSrcGroupTotalExprCount := 0, 0
	criteriaCount, totalExprCount := 0, 0
	var err error = nil
	var errorMsg string = ""
	for i, peer := range sources {
		criteriaCount, totalExprCount, err = service.updatePeerExpressions(obj, &peer, &ruleSrcGroup, i)
		if err == nil {
			ruleSrcGroupCount += criteriaCount
			ruleSrcGroupTotalExprCount += totalExprCount
		} else {
			return nil, "", err
		}
	}
	log.V(1).Info("build rule source group criteria", "total criteria", ruleSrcGroupCount, "total expressions of criteria", ruleSrcGroupTotalExprCount)

	if ruleSrcGroupCount > MaxCriteria {
		errorMsg = fmt.Sprintf("total counts of rule source group criteria %d exceed NSX limit of %d", ruleSrcGroupCount, MaxCriteria)
	} else if ruleSrcGroupTotalExprCount > MaxTotalCriteriaExpressions {
		errorMsg = fmt.Sprintf("total expression counts in source group criteria %d exceed NSX limit of %d", ruleSrcGroupTotalExprCount, MaxTotalCriteriaExpressions)
	}

	if len(errorMsg) != 0 {
		err = errors.New(errorMsg)
		log.Error(err, "validate rule source group criteria nsx limit failed")
		return nil, "", err
	}

	return &ruleSrcGroup, ruleSrcGroupPath, err
}

func (service *SecurityPolicyService) updatePeerExpressions(obj *v1alpha1.SecurityPolicy, peer *v1alpha1.SecurityPolicyPeer, group *model.Group, idx int) (int, int, error) {
	var err error = nil
	var errorMsg string = ""
	var tagValueExpression *data.StructValue = nil
	var memberType string
	var matchLabels map[string]string
	var matchExpressions *[]metav1.LabelSelectorRequirement = nil
	var mergedMatchExpressions *[]metav1.LabelSelectorRequirement = nil
	var opInValueCount, totalCriteriaCount, totalExprCount = 0, 0, 0
	var matchLabelsCount, matchExpressionsCount = 0, 0
	var mixedCriteria bool = false

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
	} else if peer.PodSelector != nil && peer.VMSelector != nil && peer.NamespaceSelector == nil {
		errorMsg = "PodSelector and VMSelector are not allowed to set in one group"
	} else if peer.PodSelector != nil && peer.VMSelector != nil && peer.NamespaceSelector != nil {
		errorMsg = "PodSelector, VMSelector and NamespaceSelector are not allowed to set in one group"
	}

	if len(errorMsg) != 0 {
		err = errors.New(errorMsg)
		log.Error(err, "build selector expressions failed")
		return 0, 0, err
	}

	service.appendOperatorIfNeeded(&group.Expression, "OR")
	expressions := service.buildGroupExpression(&group.Expression)

	clusterExpression := service.buildExpression(
		"Condition", "SegmentPort",
		fmt.Sprintf("%s|%s", util.TagScopeNCPCluster, service.getCluster()),
		"Tag", "EQUALS", "EQUALS",
	)
	expressions.Add(clusterExpression)

	if peer.PodSelector != nil {
		memberType = "SegmentPort"
		service.addOperatorIfNeeded(expressions, "AND")
		podExpression := service.buildExpression(
			"Condition", memberType, fmt.Sprintf("%s|", util.TagScopeNCPPod), "Tag", "EQUALS", "EQUALS")

		if peer.NamespaceSelector == nil {
			podExpression = service.buildExpression(
				"Condition", memberType,
				fmt.Sprintf("%s|%s", util.TagScopeNCPProject, obj.ObjectMeta.Namespace),
				"Tag", "EQUALS", "EQUALS")
			mixedCriteria = false
		} else {
			mixedCriteria = true
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
			"Condition", memberType, fmt.Sprintf("%s|", util.TagScopeNCPVNETInterface), "Tag", "EQUALS", "EQUALS")

		if peer.NamespaceSelector == nil {
			vmExpression = service.buildExpression(
				"Condition", memberType,
				fmt.Sprintf("%s|%s", util.TagScopeNCPVIFProject, obj.ObjectMeta.Namespace),
				"Tag", "EQUALS", "EQUALS")
			mixedCriteria = false
		} else {
			mixedCriteria = true
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
		if !mixedCriteria {
			tagValueExpression = nil
			memberType = "Segment"
			matchLabels = peer.NamespaceSelector.MatchLabels
			matchExpressions = &peer.NamespaceSelector.MatchExpressions
			matchLabelsCount = len(matchLabels)
			// NamespaceSelector has one more built-in labels
			matchLabelsCount += ClusterTagCount
		} else { // Handle PodSelector or VMSelector mixed with NamespaceSelector
			memberType = "Segment"
			nsMatchLabels := peer.NamespaceSelector.MatchLabels
			nsMatchExpressions := &peer.NamespaceSelector.MatchExpressions

			// Validate expressions for POD/VM Selectors
			mergedMatchExpressions = service.mergeSelectorMatchExpression(*matchExpressions)
			opInValueCount, err = service.validateSelectorOpIn(*mergedMatchExpressions)

			nsMergedMatchExpressions := service.mergeSelectorMatchExpression(*nsMatchExpressions)
			nsOpInValCount, opErr := service.validateSelectorOpIn(*nsMergedMatchExpressions)

			if err != nil || opErr != nil {
				log.Error(err, "validate Operator 'IN' in label selector matchExpressions failed")
				return 0, 0, err
			}

			if opInValueCount > 0 && nsOpInValCount > 0 {
				// Opeartor 'IN' is set in both Pod/VM selector and NamespaceSelector
				errorMsg = "opeartor 'IN' is set in both Pod/VM selector and NamespaceSelector"
				err = errors.New(errorMsg)
				log.Error(err, "validate operator 'IN' in label selector matchExpressions failed")
				return 0, 0, err
			}

			matchLabelsCount += len(nsMatchLabels)
			matchExpressionsCount = len(*mergedMatchExpressions) + len(*nsMergedMatchExpressions)
			opInValueCount += nsOpInValCount

			service.updateExpressionsMatchLables(matchLabels, "SegmentPort", expressions)
			service.updateExpressionsMatchLables(nsMatchLabels, memberType, expressions)

			// NamespaceSelector AND with PodSelector or VMSelector expressions to produce final expressions
			err = service.updateMixedExpressionsMatchExpression(*nsMergedMatchExpressions, nsMatchLabels,
				*matchExpressions, matchLabels, &group.Expression, clusterExpression, tagValueExpression, expressions)

			if err != nil {
				log.Error(err, "build label selector matchExpressions failed")
				return 0, 0, err
			}
		}
	}

	if peer.PodSelector != nil || peer.VMSelector != nil || peer.NamespaceSelector != nil {
		if !mixedCriteria {
			service.updateExpressionsMatchLables(matchLabels, memberType, expressions)

			if matchExpressions != nil {
				mergedMatchExpressions = service.mergeSelectorMatchExpression(*matchExpressions)
				matchExpressionsCount = len(*mergedMatchExpressions)
				opInValueCount, err = service.validateSelectorOpIn(*mergedMatchExpressions)

				if err != nil {
					log.Error(err, "validate operator 'IN' in label selector matchExpressions failed")
					return 0, 0, err
				}

				err = service.updateExpressionsMatchExpression(*mergedMatchExpressions, matchLabels,
					&group.Expression, clusterExpression, tagValueExpression, memberType, expressions)
				if err != nil {
					log.Error(err, "build label selector matchExpressions failed")
					return 0, 0, err
				}
			}
		}

		totalCriteriaCount, totalExprCount, err = service.validateSelectorExpressions(matchLabelsCount, matchExpressionsCount, opInValueCount, memberType)
		if err != nil {
			log.Error(err, "validate label selector matchExpressions failed")
			return 0, 0, err
		}
	}

	return totalCriteriaCount, totalExprCount, nil
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

	ruleDstGroupCount, ruleDstGroupTotalExprCount := 0, 0
	criteriaCount, totalExprCount := 0, 0
	var err error = nil
	var errorMsg string = ""
	for i, peer := range destinations {
		criteriaCount, totalExprCount, err = service.updatePeerExpressions(obj, &peer, &ruleDstGroup, i)
		if err == nil {
			ruleDstGroupCount += criteriaCount
			ruleDstGroupTotalExprCount += totalExprCount
		} else {
			return nil, "", err
		}
	}
	log.V(1).Info("build rule destination group criteria", "total criteria", ruleDstGroupCount, "total expressions of criteria", ruleDstGroupTotalExprCount)

	if ruleDstGroupCount > MaxCriteria {
		errorMsg = fmt.Sprintf("total counts of rule destination group criteria %d exceed NSX limit of %d", ruleDstGroupCount, MaxCriteria)
	} else if ruleDstGroupTotalExprCount > MaxTotalCriteriaExpressions {
		errorMsg = fmt.Sprintf("total expression counts in rule destination group criteria %d exceed NSX limit of %d", ruleDstGroupTotalExprCount, MaxTotalCriteriaExpressions)
	}

	if len(errorMsg) != 0 {
		err = errors.New(errorMsg)
		log.Error(err, "validate rule destination group criteria nsx limit failed")
		return nil, "", err
	}
	return &ruleDstGroup, ruleDstGroupPath, err
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
		log.Info("SecurityPolicy has empty policy-level appliedTo")
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
