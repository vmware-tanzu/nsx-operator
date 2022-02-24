/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package services

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	allowAction = v1alpha1.RuleActionAllow
	directionIn = v1alpha1.RuleDirectionIn

	tagScopeGroupType = util.TagScopeGroupType

	tagScopeCluster              = util.TagScopeCluster
	tagScopeNamespace            = util.TagScopeNamespace
	tagScopeSecurityPolicyCRName = util.TagScopeSecurityPolicyCRName
	tagScopeSecurityPolicyCRUID  = util.TagScopeSecurityPolicyCRUID
	tagScopeRuleID               = util.TagScopeRuleID
	tagScopeSelectorHash         = util.TagScopeSelectorHash
	spName                       = "ns1-spA"
	spGroupName                  = "ns1-spA-scope"
	spID                         = "sp_uidA"
	spID2                        = "sp_uidB"
	spGroupID                    = "sp_uidA_scope"
	seq0                         = int64(0)
	ruleNameWithPodSelector      = "rule-with-pod-selector"
	ruleNameWithVMSelector       = "rule-with-VM-selector"
	ruleID0                      = "sp_uidA_0"
	nsxDirectionIn               = "IN"
	nsxActionAllow               = "ALLOW"
	cluster                      = "k8scl-one"
	tagValueScope                = "scope"
	tagValueNS                   = "ns1"
	tagValuePolicyCRName         = "spA"
	tagValuePolicyCRUID          = "uidA"
	tagValuePodSelectorHash      = "a42321575d78a6c340c6963c7a82c86c7217f847"
	timeStamp                    = int64(1641892699021)

	service = &SecurityPolicyService{
		NSXConfig: &config.NSXOperatorConfig{
			CoeConfig: &config.CoeConfig{
				Cluster: "k8scl-one",
			},
		},
	}

	spWithPodSelector = v1alpha1.SecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "spA", UID: "uidA"},
		Spec: v1alpha1.SecurityPolicySpec{
			AppliedTo: []v1alpha1.SecurityPolicyTarget{
				{
					PodSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"pod_selector_1": "pod_value_1"},
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      "k1",
								Operator: metav1.LabelSelectorOpIn,
								Values: []string{
									"a1",
									"a2",
								},
							},
							{
								Key:      "k2",
								Operator: metav1.LabelSelectorOpNotIn,
								Values: []string{
									"a2",
									"a3",
								},
							},
							{
								Key:      "k2",
								Operator: metav1.LabelSelectorOpNotIn,
								Values: []string{
									"a3",
									"a4",
								},
							},
						},
					},
				},
			},
			Rules: []v1alpha1.SecurityPolicyRule{
				{
					Action:    &allowAction,
					Direction: &directionIn,
					Name:      "rule-with-pod-selector",
				},
			},
			Priority: 0,
		},
	}

	spWithVMSelector = v1alpha1.SecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "spA", UID: "uidA"},
		Spec: v1alpha1.SecurityPolicySpec{
			AppliedTo: []v1alpha1.SecurityPolicyTarget{
				{
					VMSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"VM_selector_1": "VM_value_1"},
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      "k3",
								Operator: metav1.LabelSelectorOpDoesNotExist,
							},
							{
								Key:      "k4",
								Operator: metav1.LabelSelectorOpExists,
							},
						},
					},
				},
			},
			Rules: []v1alpha1.SecurityPolicyRule{
				{
					Action:    &allowAction,
					Direction: &directionIn,
					Name:      "rule-with-VM-selector",
				},
			},
			Priority: 0,
		},
	}

	basicTags = []model.Tag{
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
	}
)

func TestGetCluster(t *testing.T) {
	assert.Equal(t, "k8scl-one", service.getCluster())
}

func TestBuildSecurityPolicy(t *testing.T) {
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
						Id:                &ruleID0,
						DestinationGroups: []string{"ANY"},
						Direction:         &nsxDirectionIn,
						Scope:             []string{"/infra/domains/k8scl-one/groups/sp_uidA_scope"},
						SequenceNumber:    &seq0,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"ANY"},
						Action:            &nsxActionAllow,
						ServiceEntries:    []*data.StructValue{},
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
						Id:                &ruleID0,
						DestinationGroups: []string{"ANY"},
						Direction:         &nsxDirectionIn,
						Scope:             []string{"/infra/domains/k8scl-one/groups/sp_uidA_scope"},
						SequenceNumber:    &seq0,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"ANY"},
						Action:            &nsxActionAllow,
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
					VMSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"VM_selector_1": "VM_value_1"},
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      "k1",
								Operator: metav1.LabelSelectorOpIn,
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

func TestSecurityPolicyEqual(t *testing.T) {
	tests := []struct {
		name           string
		inputPolicy1   *model.SecurityPolicy
		inputPolicy2   *model.SecurityPolicy
		expectedResult bool
	}{
		{
			name: "security-policy-without-addtional-properties-true",
			inputPolicy1: &model.SecurityPolicy{
				Id: &spID,
			},
			inputPolicy2: &model.SecurityPolicy{
				Id: &spID,
			},
			expectedResult: true,
		},
		{
			name: "security-policy-without-addtional-properties-false",
			inputPolicy1: &model.SecurityPolicy{
				Id: &spID,
			},
			inputPolicy2: &model.SecurityPolicy{
				Id: &spID2,
			},
			expectedResult: false,
		},
		{
			name: "security-policy-with-addtional-properties",
			inputPolicy1: &model.SecurityPolicy{
				Id:               &spID,
				LastModifiedTime: &timeStamp,
			},
			inputPolicy2: &model.SecurityPolicy{
				Id: &spID,
			},
			expectedResult: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedResult, service.securityPolicyEqual(tt.inputPolicy1, tt.inputPolicy2))
		},
		)
	}
}

func TestListSecurityPolicy(t *testing.T) {
	groupStore := cache.NewIndexer(keyFunc, cache.Indexers{util.TagScopeSecurityPolicyCRUID: securityPolicyCRUIDScopeIndexFunc})
	policyStore := cache.NewIndexer(keyFunc, cache.Indexers{util.TagScopeSecurityPolicyCRUID: securityPolicyCRUIDScopeIndexFunc})
	ruleStore := cache.NewIndexer(keyFunc, cache.Indexers{util.TagScopeSecurityPolicyCRUID: securityPolicyCRUIDScopeIndexFunc})

	group := model.Group{}
	scope := "nsx-op/security_policy_cr_uid"
	uuid := "111111111"
	id := "1234"
	group.Id = &id
	group.UniqueId = &uuid

	group.Tags = []model.Tag{{Scope: &scope, Tag: &id}}
	groupStore.Add(group)

	id1 := "4567"
	uuid1 := "111111112"
	group1 := model.Group{}
	group1.Id = &id1
	group1.UniqueId = &uuid1
	group1.Tags = []model.Tag{{Scope: &scope, Tag: &id1}}
	groupStore.Add(group1)

	policy := model.SecurityPolicy{}
	id2 := "1235"
	policy.Id = &id2
	policy.UniqueId = &uuid
	policy.Tags = []model.Tag{{Scope: &scope, Tag: &id2}}
	policyStore.Add(policy)

	type fields struct {
		NSXClient           *nsx.Client
		GroupStore          cache.Indexer
		SecurityPolicyStore cache.Indexer
		RuleStore           cache.Indexer
	}
	field := fields{NSXClient: nil, GroupStore: groupStore, SecurityPolicyStore: policyStore, RuleStore: ruleStore}

	tests := []struct {
		name    string
		fields  fields
		want    sets.String
		wantErr bool
	}{
		{
			name:    "test",
			fields:  field,
			wantErr: false,
		},
	}

	tests[0].want = sets.NewString()
	tests[0].want.Insert(id)
	tests[0].want.Insert(id1)
	tests[0].want.Insert(id2)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &SecurityPolicyService{
				NSXClient:           tt.fields.NSXClient,
				GroupStore:          tt.fields.GroupStore,
				SecurityPolicyStore: tt.fields.SecurityPolicyStore,
				RuleStore:           tt.fields.RuleStore,
			}
			got := service.ListSecurityPolicy()
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SecurityPolicyService.ListSecurityPolicy() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMergeSelectorMatchExpression(t *testing.T) {
	matchExpressions := []metav1.LabelSelectorRequirement{
		{
			Key:      "k1",
			Operator: metav1.LabelSelectorOpIn,
			Values: []string{
				"a1",
				"a2",
			},
		},
		{
			Key:      "k1",
			Operator: metav1.LabelSelectorOpIn,
			Values: []string{
				"a2",
				"a3",
			},
		},
	}

	// Case: the same key and the same operator will merge Values list
	mergedMatchExpressions := service.mergeSelectorMatchExpression(matchExpressions)
	assert.Equal(t, 1, len(*mergedMatchExpressions))
	assert.Equal(t, metav1.LabelSelectorOpIn, (*mergedMatchExpressions)[0].Operator)
	assert.Equal(t, "k1", (*mergedMatchExpressions)[0].Key)
	assert.Equal(t, 3, len((*mergedMatchExpressions)[0].Values))

	// Case: the same key with different operator will not merge
	matchExpressions = []metav1.LabelSelectorRequirement{
		{
			Key:      "k1",
			Operator: metav1.LabelSelectorOpNotIn,
			Values: []string{
				"a1",
				"a2",
			},
		},
		{
			Key:      "k1",
			Operator: metav1.LabelSelectorOpIn,
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

	mergedExpressions := []metav1.LabelSelectorRequirement{
		{
			Key:      "k1",
			Operator: metav1.LabelSelectorOpIn,
			Values: []string{
				"a1",
				"a2",
			},
		},
		{
			Key:      "k2",
			Operator: metav1.LabelSelectorOpNotIn,
			Values: []string{
				"a2",
				"a3",
			},
		},
		{
			Key:      "k3",
			Operator: metav1.LabelSelectorOpExists,
		},
		{
			Key:      "k4",
			Operator: metav1.LabelSelectorOpDoesNotExist,
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

	// Case: total count of expressions exceed NSX limit '5' in one criteria based on same member type
	matchLabelsCount = 3
	_, _, err = service.validateSelectorExpressions(matchLabelsCount, matchExpressionsCount, opInValueCount, false)
	assert.NotEqual(t, nil, err)

	// Case: with Operator IN for mixed criteria
	matchExpressionsCount = 12
	totalCriteriaCount, totalExprCount, err = service.validateSelectorExpressions(matchLabelsCount, matchExpressionsCount, opInValueCount, true)
	assert.Equal(t, nil, err)
	assert.Equal(t, 2, totalCriteriaCount)
	assert.Equal(t, 30, totalExprCount)

	// Case: total count of expressions exceed NSX limit '15' in one criteria mixed criteria
	matchExpressionsCount = 13
	_, _, err = service.validateSelectorExpressions(matchLabelsCount, matchExpressionsCount, opInValueCount, true)
	assert.NotEqual(t, nil, err)
}

func TestValidateSelectorOpIn(t *testing.T) {
	matchExpressions := []metav1.LabelSelectorRequirement{
		{
			Key:      "k1",
			Operator: metav1.LabelSelectorOpIn,
			Values: []string{
				"a1",
				"a2",
			},
		},
		{
			Key:      "k2",
			Operator: metav1.LabelSelectorOpNotIn,
			Values: []string{
				"a2",
				"a3",
			},
		},
		{
			Key:      "k3",
			Operator: metav1.LabelSelectorOpExists,
		},
		{
			Key:      "k4",
			Operator: metav1.LabelSelectorOpDoesNotExist,
		},
	}

	// Case: normal function
	opInValueCount, err := service.validateSelectorOpIn(matchExpressions)
	assert.Equal(t, nil, err)
	assert.Equal(t, 2, opInValueCount)

	// Case: count of Operator 'IN' exceed limit '1'
	matchExpressions[1].Operator = metav1.LabelSelectorOpIn
	opInValueCount, err = service.validateSelectorOpIn(matchExpressions)
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
	matchExpressions[1].Operator = metav1.LabelSelectorOpNotIn
	opInValueCount, err = service.validateSelectorOpIn(matchExpressions)
	assert.NotEqual(t, nil, err)
	assert.Equal(t, 6, opInValueCount)
}

func TestUpdateMixedExpressionsMatchExpression(t *testing.T) {
	group := model.Group{}
	expressions := service.buildGroupExpression(&group.Expression)
	nsMatchLabels := map[string]string{"ns_selector_1": "ns_1"}
	matchLabels := map[string]string{"pod_selector_1": "pod_value_1"}

	matchExpressions := []metav1.LabelSelectorRequirement{
		{
			Key:      "k1",
			Operator: metav1.LabelSelectorOpIn,
			Values: []string{
				"a1",
				"a2",
			},
		},
		{
			Key:      "k2",
			Operator: metav1.LabelSelectorOpNotIn,
			Values: []string{
				"a2",
				"a3",
			},
		},
		{
			Key:      "k3",
			Operator: metav1.LabelSelectorOpExists,
		},
		{
			Key:      "k4",
			Operator: metav1.LabelSelectorOpDoesNotExist,
		},
	}

	nsMergedMatchExpressions := []metav1.LabelSelectorRequirement{
		{
			Key:      "k2",
			Operator: metav1.LabelSelectorOpNotIn,
			Values: []string{
				"a2",
				"a3",
			},
		},
		{
			Key:      "k3",
			Operator: metav1.LabelSelectorOpExists,
		},
		{
			Key:      "k4",
			Operator: metav1.LabelSelectorOpDoesNotExist,
		},
	}

	// Case: normal function with Operator 'IN'
	err := service.updateMixedExpressionsMatchExpression(nsMergedMatchExpressions, nsMatchLabels,
		matchExpressions, matchLabels, &group.Expression, nil, nil, expressions)
	assert.Equal(t, nil, err)

	// Case: normal function without Operator 'IN'
	matchExpressions[0].Operator = metav1.LabelSelectorOpNotIn
	group = model.Group{}
	err = service.updateMixedExpressionsMatchExpression(nsMergedMatchExpressions, nsMatchLabels,
		matchExpressions, matchLabels, &group.Expression, nil, nil, expressions)
	assert.Equal(t, nil, err)

	// Case: with more than one Operator 'IN'
	matchExpressions[0].Operator = metav1.LabelSelectorOpIn
	nsMergedMatchExpressions[0].Operator = metav1.LabelSelectorOpIn
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
	matchExpressions[0].Operator = metav1.LabelSelectorOpNotIn
	group = model.Group{}
	err = service.updateMixedExpressionsMatchExpression(nsMergedMatchExpressions, nsMatchLabels,
		matchExpressions, matchLabels, &group.Expression, nil, nil, expressions)
	assert.NotEqual(t, nil, err)
}
