package securitypolicy

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/nsx.vmware.com/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestBuildSecurityPolicy(t *testing.T) {
	destinationPorts := data.NewListValue()
	destinationPorts.Add(data.NewStringValue("53"))
	serviceEntry := data.NewStructValue(
		"",
		map[string]data.DataValue{
			"source_ports":      data.NewListValue(),
			"destination_ports": destinationPorts,
			"l4_protocol":       data.NewStringValue("UDP"),
			"resource_type":     data.NewStringValue("L4PortSetServiceEntry"),
			"marked_for_delete": data.NewBooleanValue(false),
			"overridden":        data.NewBooleanValue(false),
		},
	)

	podSelectorRule0IDPort000 := service.buildExpandedRuleId(service.buildRuleID(&spWithPodSelector, &spWithPodSelector.Spec.Rules[0], 0, common.ResourceTypeSecurityPolicy), 0, 0)
	podSelectorRule1IDPort000 := service.buildExpandedRuleId(service.buildRuleID(&spWithPodSelector, &spWithPodSelector.Spec.Rules[1], 1, common.ResourceTypeSecurityPolicy), 0, 0)
	vmSelectorRule0IDPort000 := service.buildExpandedRuleId(service.buildRuleID(&spWithVMSelector, &spWithVMSelector.Spec.Rules[0], 0, common.ResourceTypeSecurityPolicy), 0, 0)
	vmSelectorRule1IDPort000 := service.buildExpandedRuleId(service.buildRuleID(&spWithVMSelector, &spWithVMSelector.Spec.Rules[1], 1, common.ResourceTypeSecurityPolicy), 0, 0)
	vmSelectorRule2IDPort000 := service.buildExpandedRuleId(service.buildRuleID(&spWithVMSelector, &spWithVMSelector.Spec.Rules[2], 2, common.ResourceTypeSecurityPolicy), 0, 0)

	podSelectorRule0Name00, _ := service.buildRuleDisplayName(&spWithPodSelector.Spec.Rules[0], 0, -1, false, common.ResourceTypeSecurityPolicy)
	podSelectorRule1Name00, _ := service.buildRuleDisplayName(&spWithPodSelector.Spec.Rules[1], 0, -1, false, common.ResourceTypeSecurityPolicy)

	vmSelectorRule0Name00, _ := service.buildRuleDisplayName(&spWithVMSelector.Spec.Rules[0], 0, -1, false, common.ResourceTypeSecurityPolicy)
	vmSelectorRule1Name00, _ := service.buildRuleDisplayName(&spWithVMSelector.Spec.Rules[1], 0, -1, false, common.ResourceTypeSecurityPolicy)
	vmSelectorRule2Name00, _ := service.buildRuleDisplayName(&spWithVMSelector.Spec.Rules[2], 0, -1, false, common.ResourceTypeSecurityPolicy)

	tests := []struct {
		name           string
		inputPolicy    *v1alpha1.SecurityPolicy
		expectedPolicy *model.SecurityPolicy
	}{
		{
			name:        "security-policy-with-pod-selector",
			inputPolicy: &spWithPodSelector,
			expectedPolicy: &model.SecurityPolicy{
				DisplayName:    &spName,
				Id:             &spID,
				Scope:          []string{"/infra/domains/k8scl-one/groups/sp_uidA_scope"},
				SequenceNumber: &seq0,
				Rules: []model.Rule{
					{
						DisplayName:       &podSelectorRule0Name00,
						Id:                &podSelectorRule0IDPort000,
						DestinationGroups: []string{"ANY"},
						Direction:         &nsxDirectionIn,
						Scope:             []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_scope"},
						SequenceNumber:    &seq0,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_src"},
						Action:            &nsxActionAllow,
						Tags:              basicTags,
					},
					{
						DisplayName:       &podSelectorRule1Name00,
						Id:                &podSelectorRule1IDPort000,
						DestinationGroups: []string{"ANY"},
						Direction:         &nsxDirectionIn,
						Scope:             []string{"ANY"},
						SequenceNumber:    &seq1,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"/infra/domains/k8scl-one/groups/sp_uidA_1_src"},
						Action:            &nsxActionAllow,
						ServiceEntries:    []*data.StructValue{serviceEntry},
						Tags:              basicTags,
					},
				},
				Tags: basicTags,
			},
		},
		{
			name:        "security-policy-with-VM-selector",
			inputPolicy: &spWithVMSelector,
			expectedPolicy: &model.SecurityPolicy{
				DisplayName:    &spName,
				Id:             &spID,
				Scope:          []string{"/infra/domains/k8scl-one/groups/sp_uidA_scope"},
				SequenceNumber: &seq0,
				Rules: []model.Rule{
					{
						DisplayName:       &vmSelectorRule0Name00,
						Id:                &vmSelectorRule0IDPort000,
						DestinationGroups: []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_dst"},
						Direction:         &nsxDirectionOut,
						Scope:             []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_scope"},
						SequenceNumber:    &seq0,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"ANY"},
						Action:            &nsxActionDrop,
						Tags:              basicTags,
					},
					{
						DisplayName:       &vmSelectorRule1Name00,
						Id:                &vmSelectorRule1IDPort000,
						DestinationGroups: []string{"/infra/domains/k8scl-one/groups/sp_uidA_1_dst"},
						Direction:         &nsxDirectionOut,
						Scope:             []string{"ANY"},
						SequenceNumber:    &seq1,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"ANY"},
						Action:            &nsxActionDrop,
						Tags:              basicTags,
					},

					{
						DisplayName:       &vmSelectorRule2Name00,
						Id:                &vmSelectorRule2IDPort000,
						DestinationGroups: []string{"/infra/domains/k8scl-one/groups/sp_uidA_2_dst"},
						Direction:         &nsxDirectionOut,
						Scope:             []string{"ANY"},
						SequenceNumber:    &seq2,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"ANY"},
						Action:            &nsxActionDrop,
						Tags:              basicTags,
					},
				},
				Tags: basicTags,
			},
		},
	}

	var s *SecurityPolicyService
	patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(s), "getNamespaceUID",
		func(s *SecurityPolicyService, ns string) types.UID {
			return types.UID(tagValueNSUID)
		})
	defer patches.Reset()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observedPolicy, _, _, _ := service.buildSecurityPolicy(tt.inputPolicy, common.ResourceTypeSecurityPolicy)
			assert.Equal(t, tt.expectedPolicy, observedPolicy)
		})
	}
}

func TestBuildPolicyGroup(t *testing.T) {
	tests := []struct {
		name                    string
		inputPolicy             *v1alpha1.SecurityPolicy
		expectedPolicyGroupID   *string
		expectedPolicyGroupName *string
		expectedPolicyGroupPath string
	}{
		{
			name:                    "group-with-pod-selector",
			inputPolicy:             &spWithPodSelector,
			expectedPolicyGroupID:   &spGroupID,
			expectedPolicyGroupName: &spGroupName,
			expectedPolicyGroupPath: "/infra/domains/k8scl-one/groups/sp_uidA_scope",
		},
	}
	var s *SecurityPolicyService
	patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(s), "getNamespaceUID",
		func(s *SecurityPolicyService, ns string) types.UID {
			return types.UID(tagValueNSUID)
		})
	defer patches.Reset()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observedGroup, observedGroupPath, _ := service.buildPolicyGroup(tt.inputPolicy, common.ResourceTypeSecurityPolicy)
			assert.Equal(t, tt.expectedPolicyGroupID, observedGroup.Id)
			assert.Equal(t, tt.expectedPolicyGroupName, observedGroup.DisplayName)
			assert.Equal(t, tt.expectedPolicyGroupPath, observedGroupPath)
		})
	}
}

func TestBuildTargetTags(t *testing.T) {
	ruleTagID0 := service.buildRuleID(&spWithPodSelector, &spWithPodSelector.Spec.Rules[0], 0, common.ResourceTypeSecurityPolicy)
	tests := []struct {
		name         string
		inputPolicy  *v1alpha1.SecurityPolicy
		inputTargets *[]v1alpha1.SecurityPolicyTarget
		inputIndex   int
		expectedTags []model.Tag
	}{
		{
			name:        "policy-target-tags-with-pod-selector",
			inputPolicy: &spWithPodSelector,
			inputTargets: &[]v1alpha1.SecurityPolicyTarget{
				{
					VMSelector: &v1.LabelSelector{
						MatchLabels: map[string]string{"VM_selector_1": "VM_value_1"},
						MatchExpressions: []v1.LabelSelectorRequirement{
							{
								Key:      "k1",
								Operator: v1.LabelSelectorOpIn,
								Values: []string{
									"a1",
									"a2",
								},
							},
						},
					},
				},
			},
			inputIndex: 0,
			expectedTags: []model.Tag{
				{
					Scope: &tagScopeVersion,
					Tag:   &tagValueVersion,
				},
				{
					Scope: &tagScopeGroupType,
					Tag:   &tagValueGroupScope,
				},
				{
					Scope: &tagScopeSelectorHash,
					Tag:   &tagValuePodSelectorHash,
				},
				{
					Scope: &tagScopeCluster,
					Tag:   &cluster,
				},
				{
					Scope: &tagScopeNamespace,
					Tag:   &tagValueNS,
				},
				{
					Scope: &tagScopeNamespaceUID,
					Tag:   &tagValueNSUID,
				},
				{
					Scope: &tagScopeSecurityPolicyCRName,
					Tag:   &tagValuePolicyCRName,
				},
				{
					Scope: &tagScopeSecurityPolicyCRUID,
					Tag:   &tagValuePolicyCRUID,
				},
				{
					Scope: &tagScopeRuleID,
					Tag:   &ruleTagID0,
				},
			},
		},
	}
	var s *SecurityPolicyService
	patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(s), "getNamespaceUID",
		func(s *SecurityPolicyService, ns string) types.UID {
			return types.UID(tagValueNSUID)
		})
	defer patches.Reset()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.ElementsMatch(t, tt.expectedTags, service.buildTargetTags(tt.inputPolicy, tt.inputTargets, &tt.inputPolicy.Spec.Rules[0], tt.inputIndex, common.ResourceTypeSecurityPolicy))
		})
	}
}

func TestBuildPeerTags(t *testing.T) {
	ruleTagID0 := service.buildRuleID(&spWithPodSelector, &spWithPodSelector.Spec.Rules[0], 0, common.ResourceTypeSecurityPolicy)
	tests := []struct {
		name         string
		inputPolicy  *v1alpha1.SecurityPolicy
		inputIndex   int
		expectedTags []model.Tag
	}{
		{
			name:        "policy-src-peer-tags-with-pod-selector",
			inputPolicy: &spWithPodSelector,
			inputIndex:  0,
			expectedTags: []model.Tag{
				{
					Scope: &tagScopeVersion,
					Tag:   &tagValueVersion,
				},
				{
					Scope: &tagScopeGroupType,
					Tag:   &tagValueGroupSource,
				},
				{
					Scope: &tagScopeRuleID,
					Tag:   &ruleTagID0,
				},
				{
					Scope: &tagScopeSelectorHash,
					Tag:   &tagValueRuleSrcHash,
				},
				{
					Scope: &tagScopeCluster,
					Tag:   &cluster,
				},
				{
					Scope: &tagScopeNamespace,
					Tag:   &tagValueNS,
				},
				{
					Scope: &tagScopeNamespaceUID,
					Tag:   &tagValueNSUID,
				},
				{
					Scope: &tagScopeSecurityPolicyCRName,
					Tag:   &tagValuePolicyCRName,
				},
				{
					Scope: &tagScopeSecurityPolicyCRUID,
					Tag:   &tagValuePolicyCRUID,
				},
			},
		},
	}
	var s *SecurityPolicyService
	patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(s), "getNamespaceUID",
		func(s *SecurityPolicyService, ns string) types.UID {
			return types.UID(tagValueNSUID)
		})
	defer patches.Reset()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.ElementsMatch(t, tt.expectedTags, service.buildPeerTags(tt.inputPolicy, &tt.inputPolicy.Spec.Rules[0], tt.inputIndex, true, false, common.ResourceTypeSecurityPolicy))
		})
	}
}

func TestMergeSelectorMatchExpression(t *testing.T) {
	matchExpressions := []v1.LabelSelectorRequirement{
		{
			Key:      "k1",
			Operator: v1.LabelSelectorOpIn,
			Values: []string{
				"a1",
				"a2",
			},
		},
		{
			Key:      "k1",
			Operator: v1.LabelSelectorOpIn,
			Values: []string{
				"a2",
				"a3",
			},
		},
	}

	// Case: the same key and the same operator will merge Values list
	mergedMatchExpressions := service.mergeSelectorMatchExpression(matchExpressions)
	assert.Equal(t, 1, len(*mergedMatchExpressions))
	assert.Equal(t, v1.LabelSelectorOpIn, (*mergedMatchExpressions)[0].Operator)
	assert.Equal(t, "k1", (*mergedMatchExpressions)[0].Key)
	assert.Equal(t, 3, len((*mergedMatchExpressions)[0].Values))

	// Case: the same key with different operator will not merge
	matchExpressions = []v1.LabelSelectorRequirement{
		{
			Key:      "k1",
			Operator: v1.LabelSelectorOpNotIn,
			Values: []string{
				"a1",
				"a2",
			},
		},
		{
			Key:      "k1",
			Operator: v1.LabelSelectorOpIn,
			Values: []string{
				"a2",
				"a3",
			},
		},
	}
	mergedMatchExpressions = service.mergeSelectorMatchExpression(matchExpressions)
	assert.Equal(t, 2, len(*mergedMatchExpressions))
	assert.Equal(t, "k1", (*mergedMatchExpressions)[0].Key)
	assert.Equal(t, "k1", (*mergedMatchExpressions)[1].Key)
	assert.Equal(t, 2, len((*mergedMatchExpressions)[0].Values))
	assert.Equal(t, 2, len((*mergedMatchExpressions)[1].Values))
}

func TestUpdateExpressionsMatchExpression(t *testing.T) {
	group := model.Group{}
	expressions := service.buildGroupExpression(&group.Expression)
	memberType := "SegmentPort"
	matchLabels := map[string]string{"VM_selector_1": "VM_value_1"}

	mergedExpressions := []v1.LabelSelectorRequirement{
		{
			Key:      "k1",
			Operator: v1.LabelSelectorOpIn,
			Values: []string{
				"a1",
				"a2",
			},
		},
		{
			Key:      "k2",
			Operator: v1.LabelSelectorOpNotIn,
			Values: []string{
				"a2",
				"a3",
			},
		},
		{
			Key:      "k3",
			Operator: v1.LabelSelectorOpExists,
		},
		{
			Key:      "k4",
			Operator: v1.LabelSelectorOpDoesNotExist,
		},
	}

	// Case: normal function
	err := service.updateExpressionsMatchExpression(mergedExpressions, matchLabels,
		&group.Expression, nil, nil, memberType, expressions)
	assert.Equal(t, nil, err)

	// Case: Unsupported Operator
	mergedExpressions[0].Operator = "DoesExist"
	group = model.Group{}
	err = service.updateExpressionsMatchExpression(mergedExpressions, matchLabels,
		&group.Expression, nil, nil, memberType, expressions)
	assert.NotEqual(t, nil, err)
}

func TestValidateSelectorExpressions(t *testing.T) {
	matchLabelsCount := 2
	matchExpressionsCount := 3
	opInValueCount := 0

	// Case: without Operator IN for the same member type
	totalCriteriaCount, totalExprCount, err := service.validateSelectorExpressions(matchLabelsCount, matchExpressionsCount, opInValueCount, false)
	assert.Equal(t, nil, err)
	assert.Equal(t, 1, totalCriteriaCount)
	assert.Equal(t, 5, totalExprCount)

	// Case: with Operator IN for the same member type
	opInValueCount = 2
	totalCriteriaCount, totalExprCount, err = service.validateSelectorExpressions(matchLabelsCount, matchExpressionsCount, opInValueCount, false)
	assert.Equal(t, nil, err)
	assert.Equal(t, 2, totalCriteriaCount)
	assert.Equal(t, 10, totalExprCount)

	// Case: total count of expressions exceed NSX limit '5' in one criterion based on same member type
	matchLabelsCount = 3
	_, _, err = service.validateSelectorExpressions(matchLabelsCount, matchExpressionsCount, opInValueCount, false)
	assert.NotEqual(t, nil, err)

	// Case: with Operator IN for mixed criteria
	matchExpressionsCount = 12
	totalCriteriaCount, totalExprCount, err = service.validateSelectorExpressions(matchLabelsCount, matchExpressionsCount, opInValueCount, true)
	assert.Equal(t, nil, err)
	assert.Equal(t, 2, totalCriteriaCount)
	assert.Equal(t, 30, totalExprCount)

	// Case: total count of expressions exceed NSX limit '15' in one criterion with mixed member type
	matchExpressionsCount = 13
	_, _, err = service.validateSelectorExpressions(matchLabelsCount, matchExpressionsCount, opInValueCount, true)
	assert.NotEqual(t, nil, err)
}

func TestValidateSelectorOpIn(t *testing.T) {
	var matchLabels map[string]string
	matchExpressions := []v1.LabelSelectorRequirement{
		{
			Key:      "k1",
			Operator: v1.LabelSelectorOpIn,
			Values: []string{
				"a1",
				"a2",
			},
		},
		{
			Key:      "k2",
			Operator: v1.LabelSelectorOpNotIn,
			Values: []string{
				"a2",
				"a3",
			},
		},
		{
			Key:      "k3",
			Operator: v1.LabelSelectorOpExists,
		},
		{
			Key:      "k4",
			Operator: v1.LabelSelectorOpDoesNotExist,
		},
	}

	// Case: normal function
	opInValueCount, err := service.validateSelectorOpIn(matchExpressions, matchLabels)
	assert.Equal(t, nil, err)
	assert.Equal(t, 2, opInValueCount)

	// Case: count of Operator 'IN' exceed limit '1'
	matchExpressions[1].Operator = v1.LabelSelectorOpIn
	opInValueCount, err = service.validateSelectorOpIn(matchExpressions, matchLabels)
	assert.NotEqual(t, nil, err)
	assert.Equal(t, 4, opInValueCount)

	// Case: count of values list for operator 'IN' expressions exceed limit '5'
	matchExpressions[0].Values = []string{
		"a1",
		"a2",
		"a3",
		"a4",
		"a5",
		"a6",
	}
	matchExpressions[1].Operator = v1.LabelSelectorOpNotIn
	opInValueCount, err = service.validateSelectorOpIn(matchExpressions, matchLabels)
	assert.NotEqual(t, nil, err)
	assert.Equal(t, 6, opInValueCount)

	// Case: matchLabels has duplication expression with matchExpressions operator 'In'
	matchLabels = make(map[string]string)
	matchLabels["k1"] = "a5"
	matchExpressions[0].Values = []string{
		"a1",
		"a2",
		"a3",
		"a4",
		"a5",
	}
	matchExpressions[1].Operator = v1.LabelSelectorOpNotIn
	opInValueCount, err = service.validateSelectorOpIn(matchExpressions, matchLabels)
	assert.NotEqual(t, nil, err)
	assert.Equal(t, 5, opInValueCount)
}

func TestValidateNsSelectorOpNotIn(t *testing.T) {
	matchExpressions := []v1.LabelSelectorRequirement{
		{
			Key:      "k1",
			Operator: v1.LabelSelectorOpIn,
			Values: []string{
				"a1",
				"a2",
			},
		},
		{
			Key:      "k3",
			Operator: v1.LabelSelectorOpExists,
		},
		{
			Key:      "k4",
			Operator: v1.LabelSelectorOpDoesNotExist,
		},
	}

	// Case: No Operator 'NotIn'
	err := service.validateNsSelectorOpNotIn(matchExpressions)
	assert.Equal(t, nil, err)

	// Case: With Operator 'NotIn'
	matchExpressions[0].Operator = v1.LabelSelectorOpNotIn
	err = service.validateNsSelectorOpNotIn(matchExpressions)
	assert.NotEqual(t, nil, err)
}

func TestUpdateMixedExpressionsMatchExpression(t *testing.T) {
	group := model.Group{}
	expressions := service.buildGroupExpression(&group.Expression)
	nsMatchLabels := map[string]string{"ns_selector_1": "ns_1"}
	matchLabels := map[string]string{"pod_selector_1": "pod_value_1"}

	matchExpressions := []v1.LabelSelectorRequirement{
		{
			Key:      "k1",
			Operator: v1.LabelSelectorOpIn,
			Values: []string{
				"a1",
				"a2",
			},
		},
		{
			Key:      "k2",
			Operator: v1.LabelSelectorOpNotIn,
			Values: []string{
				"a2",
				"a3",
			},
		},
		{
			Key:      "k3",
			Operator: v1.LabelSelectorOpExists,
		},
		{
			Key:      "k4",
			Operator: v1.LabelSelectorOpDoesNotExist,
		},
	}

	nsMergedMatchExpressions := []v1.LabelSelectorRequirement{
		{
			Key:      "k2",
			Operator: v1.LabelSelectorOpNotIn,
			Values: []string{
				"a2",
				"a3",
			},
		},
		{
			Key:      "k3",
			Operator: v1.LabelSelectorOpExists,
		},
		{
			Key:      "k4",
			Operator: v1.LabelSelectorOpDoesNotExist,
		},
	}

	// Case: normal function with Operator 'IN'
	err := service.updateMixedExpressionsMatchExpression(nsMergedMatchExpressions, nsMatchLabels,
		matchExpressions, matchLabels, &group.Expression, nil, nil, expressions)
	assert.Equal(t, nil, err)

	// Case: normal function without Operator 'IN'
	matchExpressions[0].Operator = v1.LabelSelectorOpNotIn
	group = model.Group{}
	err = service.updateMixedExpressionsMatchExpression(nsMergedMatchExpressions, nsMatchLabels,
		matchExpressions, matchLabels, &group.Expression, nil, nil, expressions)
	assert.Equal(t, nil, err)

	// Case: with more than one Operator 'IN'
	matchExpressions[0].Operator = v1.LabelSelectorOpIn
	nsMergedMatchExpressions[0].Operator = v1.LabelSelectorOpIn
	group = model.Group{}
	err = service.updateMixedExpressionsMatchExpression(nsMergedMatchExpressions, nsMatchLabels,
		matchExpressions, matchLabels, &group.Expression, nil, nil, expressions)
	assert.NotEqual(t, nil, err)

	// Case: Operator 'IN' and unsupported Operator
	nsMergedMatchExpressions[0].Operator = "DoesExist"
	group = model.Group{}
	err = service.updateMixedExpressionsMatchExpression(nsMergedMatchExpressions, nsMatchLabels,
		matchExpressions, matchLabels, &group.Expression, nil, nil, expressions)
	assert.NotEqual(t, nil, err)

	// Case: without Operator 'IN' and unsupported Operator
	matchExpressions[0].Operator = v1.LabelSelectorOpNotIn
	group = model.Group{}
	err = service.updateMixedExpressionsMatchExpression(nsMergedMatchExpressions, nsMatchLabels,
		matchExpressions, matchLabels, &group.Expression, nil, nil, expressions)
	assert.NotEqual(t, nil, err)
}

var securityPolicyWithMultipleNormalPorts = v1alpha1.SecurityPolicy{
	ObjectMeta: v1.ObjectMeta{
		Namespace: "null",
		Name:      "null",
	},
	Spec: v1alpha1.SecurityPolicySpec{
		Rules: []v1alpha1.SecurityPolicyRule{
			{
				Action:    &allowAction,
				Direction: &directionIn,
				Ports: []v1alpha1.SecurityPolicyPort{
					{
						Protocol: "TCP",
						Port:     intstr.IntOrString{Type: intstr.Int, IntVal: 80},
					},
					{
						Protocol: "UDP",
						Port:     intstr.IntOrString{Type: intstr.Int, IntVal: 1234},
						EndPort:  1235,
					},
				},
			},
			{
				Name:      "MultipleNormalPorts-rule1",
				Action:    &allowDrop,
				Direction: &directionOut,
				Ports: []v1alpha1.SecurityPolicyPort{
					{
						Protocol: "TCP",
						Port:     intstr.IntOrString{Type: intstr.Int, IntVal: 88},
					},
					{
						Protocol: "UDP",
						Port:     intstr.IntOrString{Type: intstr.Int, IntVal: 1236},
						EndPort:  1237,
					},
				},
			},
		},
	},
}

var securityPolicyWithOneNamedPort = v1alpha1.SecurityPolicy{
	ObjectMeta: v1.ObjectMeta{
		Namespace: "null",
		Name:      "null",
	},
	Spec: v1alpha1.SecurityPolicySpec{
		Rules: []v1alpha1.SecurityPolicyRule{
			{
				Name:      "TCP.http-UDP.1234.1235-ingress-allow",
				Action:    &allowAction,
				Direction: &directionIn,
				Ports: []v1alpha1.SecurityPolicyPort{
					{
						Protocol: "TCP",
						Port:     intstr.IntOrString{Type: intstr.String, StrVal: "http"},
					},
					{
						Protocol: "UDP",
						Port:     intstr.IntOrString{Type: intstr.Int, IntVal: 1234},
						EndPort:  1235,
					},
				},
			},
		},
	},
}

func TestBuildRulePortsString(t *testing.T) {
	tests := []struct {
		name                    string
		inputPorts              *[]v1alpha1.SecurityPolicyPort
		suffix                  string
		expectedRulePortsString string
	}{
		{
			name:                    "build-string-for-multiple-ports-without-named-port",
			inputPorts:              &securityPolicyWithMultipleNormalPorts.Spec.Rules[0].Ports,
			suffix:                  "ingress-allow",
			expectedRulePortsString: "TCP.80-UDP.1234.1235-ingress-allow",
		},
		{
			name:                    "build-string-for-multiple-ports-without-one-named-port",
			inputPorts:              &securityPolicyWithOneNamedPort.Spec.Rules[0].Ports,
			suffix:                  "ingress-allow",
			expectedRulePortsString: "TCP.http-UDP.1234.1235-ingress-allow",
		},
		{
			name:                    "build-string-for-nil-ports",
			inputPorts:              nil,
			suffix:                  "ingress-allow",
			expectedRulePortsString: "all-ingress-allow",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observedString := service.buildRulePortsString(tt.inputPorts, tt.suffix)
			assert.Equal(t, tt.expectedRulePortsString, observedString)
		})
	}
}

func TestBuildRuleDisplayName(t *testing.T) {
	tests := []struct {
		name                    string
		inputSecurityPolicy     *v1alpha1.SecurityPolicy
		inputRule               *v1alpha1.SecurityPolicyRule
		ruleIdx                 int
		portIdx                 int
		createdFor              string
		expectedRuleDisplayName string
	}{
		{
			name:                    "build-display-name-for-multiple-ports-0",
			inputSecurityPolicy:     &securityPolicyWithMultipleNormalPorts,
			inputRule:               &securityPolicyWithMultipleNormalPorts.Spec.Rules[0],
			ruleIdx:                 0,
			portIdx:                 0,
			createdFor:              common.ResourceTypeNetworkPolicy,
			expectedRuleDisplayName: "TCP.80-UDP.1234.1235-ingress-allow",
		},
		{
			name:                    "build-display-name-for-multiple-ports-1",
			inputSecurityPolicy:     &securityPolicyWithMultipleNormalPorts,
			inputRule:               &securityPolicyWithMultipleNormalPorts.Spec.Rules[1],
			ruleIdx:                 1,
			portIdx:                 0,
			createdFor:              common.ResourceTypeNetworkPolicy,
			expectedRuleDisplayName: "MultipleNormalPorts-rule1",
		},
		{
			name:                    "build-display-name-for-multiple-ports-1",
			inputSecurityPolicy:     &securityPolicyWithMultipleNormalPorts,
			inputRule:               &securityPolicyWithMultipleNormalPorts.Spec.Rules[1],
			ruleIdx:                 1,
			portIdx:                 0,
			createdFor:              common.ResourceTypeSecurityPolicy,
			expectedRuleDisplayName: "MultipleNormalPorts-rule1-egress-isolation",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observedDisplayName, observedError := service.buildRuleDisplayName(tt.inputRule, tt.portIdx, -1, false, tt.createdFor)
			assert.Equal(t, tt.expectedRuleDisplayName, observedDisplayName)
			assert.Equal(t, nil, observedError)
		})
	}
}

func TestBuildSecurityPolicyName(t *testing.T) {
	svc := &SecurityPolicyService{
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "cluster1",
				},
			},
		},
	}

	for _, tc := range []struct {
		name       string
		vpcEnabled bool
		obj        *v1alpha1.SecurityPolicy
		createdFor string
		expName    string
		expId      string
	}{
		{
			name:       "SecurityPolicy with VPC disabled",
			vpcEnabled: false,
			obj: &v1alpha1.SecurityPolicy{
				ObjectMeta: v1.ObjectMeta{
					UID:       "uid1",
					Name:      "securitypolicy1",
					Namespace: "ns1",
				},
			},
			createdFor: common.ResourceTypeSecurityPolicy,
			expName:    "sp-ns1-securitypolicy1",
			expId:      "sp_uid1",
		},
		{
			name:       "SecurityPolicy with VPC enabled",
			vpcEnabled: true,
			obj: &v1alpha1.SecurityPolicy{
				ObjectMeta: v1.ObjectMeta{
					UID:       "uid2",
					Name:      "securitypolicy2",
					Namespace: "ns2",
				},
			},
			createdFor: common.ResourceTypeSecurityPolicy,
			expName:    "securitypolicy2",
			expId:      "securitypolicy2-uid2",
		},
		{
			name:       "NetworkPolicy with VPC enabled",
			vpcEnabled: true,
			obj: &v1alpha1.SecurityPolicy{
				ObjectMeta: v1.ObjectMeta{
					UID:       "uid3",
					Name:      "networkpolicy1",
					Namespace: "ns3",
				},
			},
			createdFor: common.ResourceTypeNetworkPolicy,
			expName:    "networkpolicy1",
			expId:      "networkpolicy1-uid3",
		},
		{
			name:       "NetworkPolicy with VPC enabled with name truncated",
			vpcEnabled: true,
			obj: &v1alpha1.SecurityPolicy{
				ObjectMeta: v1.ObjectMeta{
					UID:       "67c80acd-019a4886-44ce-11ef-b87a-4a38b420eaae",
					Name:      strings.Repeat("a", 260),
					Namespace: strings.Repeat("b", 110),
				},
			},
			createdFor: common.ResourceTypeNetworkPolicy,
			expName:    fmt.Sprintf("%s-c64163f0", strings.Repeat("a", 246)),
			expId:      fmt.Sprintf("%s-fb85d834", strings.Repeat("a", 246)),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc.NSXConfig.EnableVPCNetwork = tc.vpcEnabled
			name := svc.buildSecurityPolicyName(tc.obj, tc.createdFor)
			assert.Equal(t, tc.expName, name)
			assert.True(t, len(name) <= common.MaxNameLength)
			id := svc.buildSecurityPolicyID(tc.obj, tc.createdFor)
			assert.Equal(t, tc.expId, id)
		})
	}
}

func TestBuildGroupName(t *testing.T) {
	svc := &SecurityPolicyService{
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "cluster1",
				},
			},
		},
	}

	obj := &v1alpha1.SecurityPolicy{
		ObjectMeta: v1.ObjectMeta{
			UID:       "c5db1800-ce4c-11de-bedc-84a0de00c35b",
			Name:      "sp1",
			Namespace: "ns1",
		},
		Spec: v1alpha1.SecurityPolicySpec{
			Rules: securityPolicyWithMultipleNormalPorts.Spec.Rules,
		},
	}

	t.Run("build rule peer group name", func(t *testing.T) {
		for _, tc := range []struct {
			name      string
			ruleIdx   int
			isSource  bool
			enableVPC bool
			expName   string
			expId     string
		}{
			{
				name:      "src rule without name",
				ruleIdx:   0,
				isSource:  true,
				enableVPC: true,
				expName:   "sp1-0-src",
				expId:     "sp1-c5db1800-ce4c-11de-bedc-84a0de00c35b_0_src",
			},
			{
				name:      "dst rule without name",
				ruleIdx:   0,
				isSource:  false,
				enableVPC: true,
				expName:   "sp1-0-dst",
				expId:     "sp1-c5db1800-ce4c-11de-bedc-84a0de00c35b_0_dst",
			},
			{
				name:      "dst rule without name with T1",
				ruleIdx:   0,
				isSource:  false,
				enableVPC: false,
				expName:   "sp1-0-dst",
				expId:     "sp_c5db1800-ce4c-11de-bedc-84a0de00c35b_0_dst",
			},
			{
				name:      "src rule with name",
				ruleIdx:   1,
				isSource:  true,
				enableVPC: true,
				expName:   "MultipleNormalPorts-rule1-src",
				expId:     "sp1-c5db1800-ce4c-11de-bedc-84a0de00c35b_1_src",
			},
			{
				name:      "dst rule with name",
				ruleIdx:   1,
				isSource:  false,
				enableVPC: true,
				expName:   "MultipleNormalPorts-rule1-dst",
				expId:     "sp1-c5db1800-ce4c-11de-bedc-84a0de00c35b_1_dst",
			},
			{
				name:      "dst rule with name with T1",
				ruleIdx:   1,
				isSource:  false,
				enableVPC: false,
				expName:   "MultipleNormalPorts-rule1-dst",
				expId:     "sp_c5db1800-ce4c-11de-bedc-84a0de00c35b_1_dst",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				svc.NSXConfig.EnableVPCNetwork = tc.enableVPC
				dispName := svc.buildRulePeerGroupName(obj, tc.ruleIdx, tc.isSource)
				assert.Equal(t, tc.expName, dispName)
				groupID := svc.buildRulePeerGroupID(obj, tc.ruleIdx, tc.isSource)
				assert.Equal(t, tc.expId, groupID)
			})
		}

	})

	t.Run("build applied group name", func(t *testing.T) {
		createdFor := common.ResourceTypeSecurityPolicy
		for _, tc := range []struct {
			name      string
			ruleIdx   int
			enableVPC bool
			expName   string
			expId     string
		}{
			{
				name:      "rule without name",
				ruleIdx:   0,
				enableVPC: true,
				expName:   "sp1-0-scope",
				expId:     "sp1-c5db1800-ce4c-11de-bedc-84a0de00c35b_0_scope",
			},
			{
				name:      "rule with name",
				ruleIdx:   1,
				enableVPC: true,
				expName:   "MultipleNormalPorts-rule1-scope",
				expId:     "sp1-c5db1800-ce4c-11de-bedc-84a0de00c35b_1_scope",
			},
			{
				name:      "policy applied group",
				ruleIdx:   -1,
				enableVPC: true,
				expName:   "ns1-sp1-scope",
				expId:     "sp1-c5db1800-ce4c-11de-bedc-84a0de00c35b_scope",
			},
			{
				name:      "policy applied group with T1",
				ruleIdx:   -1,
				enableVPC: false,
				expName:   "ns1-sp1-scope",
				expId:     "sp_c5db1800-ce4c-11de-bedc-84a0de00c35b_scope",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				svc.NSXConfig.EnableVPCNetwork = tc.enableVPC
				dispName := svc.buildAppliedGroupName(obj, tc.ruleIdx)
				assert.Equal(t, dispName, tc.expName)
				id := svc.buildAppliedGroupID(obj, tc.ruleIdx, createdFor)
				assert.Equal(t, tc.expId, id)
			})
		}
	})
}
