package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
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
						DisplayName:       &ruleNameWithPodSelector,
						Id:                &ruleIDPort0,
						DestinationGroups: []string{"ANY"},
						Direction:         &nsxDirectionIn,
						Scope:             []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_scope"},
						SequenceNumber:    &seq0,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_src"},
						Action:            &nsxActionAllow,
						ServiceEntries:    []*data.StructValue{},
						Tags:              basicTags,
					},
					{
						DisplayName:       &ruleNameWithNsSelector,
						Id:                &ruleIDPort1,
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
						DisplayName:       &ruleNameWithVMSelector,
						Id:                &ruleIDPort0,
						DestinationGroups: []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_dst"},
						Direction:         &nsxDirectionOut,
						Scope:             []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_scope"},
						SequenceNumber:    &seq0,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"ANY"},
						Action:            &nsxActionDrop,
						ServiceEntries:    []*data.StructValue{},
						Tags:              basicTags,
					},
					{
						DisplayName:       &ruleNameWithNsSelector,
						Id:                &ruleIDPort1,
						DestinationGroups: []string{"/infra/domains/k8scl-one/groups/sp_uidA_1_dst"},
						Direction:         &nsxDirectionOut,
						Scope:             []string{"ANY"},
						SequenceNumber:    &seq1,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"ANY"},
						Action:            &nsxActionDrop,
						ServiceEntries:    []*data.StructValue{},
						Tags:              basicTags,
					},

					{
						DisplayName:       &ruleNameWithIpBlock,
						Id:                &ruleIDPort2,
						DestinationGroups: []string{"/infra/domains/k8scl-one/groups/sp_uidA_2_dst"},
						Direction:         &nsxDirectionOut,
						Scope:             []string{"ANY"},
						SequenceNumber:    &seq2,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"ANY"},
						Action:            &nsxActionDrop,
						ServiceEntries:    []*data.StructValue{},
						Tags:              basicTags,
					},
				},
				Tags: basicTags,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observedPolicy, _, _ := service.buildSecurityPolicy(tt.inputPolicy)
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observedGroup, observedGroupPath, _ := service.buildPolicyGroup(tt.inputPolicy)
			assert.Equal(t, tt.expectedPolicyGroupID, observedGroup.Id)
			assert.Equal(t, tt.expectedPolicyGroupName, observedGroup.DisplayName)
			assert.Equal(t, tt.expectedPolicyGroupPath, observedGroupPath)
		})
	}
}

func TestBuildTargetTags(t *testing.T) {
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
					Scope: &tagScopeGroupType,
					Tag:   &tagValueScope,
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
					Scope: &tagScopeSecurityPolicyCRName,
					Tag:   &tagValuePolicyCRName,
				},
				{
					Scope: &tagScopeSecurityPolicyCRUID,
					Tag:   &tagValuePolicyCRUID,
				},
				{
					Scope: &tagScopeRuleID,
					Tag:   &ruleID0,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedTags, service.buildTargetTags(tt.inputPolicy, tt.inputTargets, tt.inputIndex))
		})
	}
}

func TestBuildPeerTags(t *testing.T) {
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
					Scope: &tagScopeGroupType,
					Tag:   &tagValueScope,
				},
				{
					Scope: &tagScopeRuleID,
					Tag:   &ruleID0,
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedTags, service.buildPeerTags(tt.inputPolicy, &tt.inputPolicy.Spec.Rules[0].Sources, tt.inputIndex))
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

	// Case: total count of expressions exceed NSX limit '15' in one criterion mixed criteria
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

	// Case: matchLabels has duplication expression with matchexpression operator 'In'
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
