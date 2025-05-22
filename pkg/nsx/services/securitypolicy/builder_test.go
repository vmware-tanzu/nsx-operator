/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

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

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/mock"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

func Test_BuildSecurityPolicyForT1(t *testing.T) {
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

	podSelectorRule0Name00 := "rule-with-pod-ns-selector_ingress_allow"
	podSelectorRule0IDPort000 := "sp_uidA_2c822e90b1377b346014adfa583f08a99dee52a8_0_0_0"

	podSelectorRule1Name00 := "rule-with-ns-selector_ingress_allow"
	podSelectorRule1IDPort000 := "sp_uidA_2a4595d0dd582c2ae5613245ad7b39de5ade2e20_1_0_0"

	vmSelectorRule0Name00 := "rule-with-VM-selector_egress_isolation"
	vmSelectorRule0IDPort000 := "sp_uidB_67410606c486d2ba38002ed076a2a4211c9d49b5_0_0_0"

	vmSelectorRule1Name00 := "rule-with-ns-selector_egress_isolation"
	vmSelectorRule1IDPort000 := "sp_uidB_7d721f087be35f0bf318f4847b5acdc3d2b91446_1_0_0"

	vmSelectorRule2Name00 := "all_egress_isolation"
	vmSelectorRule2IDPort000 := "sp_uidB_a40c813916cc397fcd2260e48cc773d4c9b08565_2_0_0"

	tests := []struct {
		name           string
		inputPolicy    *v1alpha1.SecurityPolicy
		expectedPolicy *model.SecurityPolicy
	}{
		{
			name:        "security-policy-with-pod-selector For T1",
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
						Direction:         &nsxRuleDirectionIn,
						Scope:             []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_scope"},
						SequenceNumber:    &seq0,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_src"},
						Action:            &nsxRuleActionAllow,
						Tags:              basicTags,
					},
					{
						DisplayName:       &podSelectorRule1Name00,
						Id:                &podSelectorRule1IDPort000,
						DestinationGroups: []string{"ANY"},
						Direction:         &nsxRuleDirectionIn,
						Scope:             []string{"ANY"},
						SequenceNumber:    &seq1,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"/infra/domains/k8scl-one/groups/sp_uidA_1_src"},
						Action:            &nsxRuleActionAllow,
						ServiceEntries:    []*data.StructValue{serviceEntry},
						Tags:              basicTags,
					},
				},
				Tags: basicTags,
			},
		},
		{
			name:        "security-policy-with-VM-selector For T1",
			inputPolicy: &spWithVMSelector,
			expectedPolicy: &model.SecurityPolicy{
				DisplayName:    common.String("spB"),
				Id:             common.String("sp_uidB"),
				Scope:          []string{"/infra/domains/k8scl-one/groups/sp_uidB_scope"},
				SequenceNumber: &seq0,
				Rules: []model.Rule{
					{
						DisplayName:       &vmSelectorRule0Name00,
						Id:                &vmSelectorRule0IDPort000,
						DestinationGroups: []string{"/infra/domains/k8scl-one/groups/sp_uidB_0_dst"},
						Direction:         &nsxRuleDirectionOut,
						Scope:             []string{"/infra/domains/k8scl-one/groups/sp_uidB_0_scope"},
						SequenceNumber:    &seq0,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"ANY"},
						Action:            &nsxRuleActionDrop,
						Tags:              basicTagsForSpWithVMSelector,
					},
					{
						DisplayName:       &vmSelectorRule1Name00,
						Id:                &vmSelectorRule1IDPort000,
						DestinationGroups: []string{"/infra/domains/k8scl-one/groups/sp_uidB_1_dst"},
						Direction:         &nsxRuleDirectionOut,
						Scope:             []string{"ANY"},
						SequenceNumber:    &seq1,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"ANY"},
						Action:            &nsxRuleActionDrop,
						Tags:              basicTagsForSpWithVMSelector,
					},

					{
						DisplayName:       &vmSelectorRule2Name00,
						Id:                &vmSelectorRule2IDPort000,
						DestinationGroups: []string{"/infra/domains/k8scl-one/groups/sp_uidB_2_dst"},
						Direction:         &nsxRuleDirectionOut,
						Scope:             []string{"ANY"},
						SequenceNumber:    &seq2,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"ANY"},
						Action:            &nsxRuleActionDrop,
						Tags:              basicTagsForSpWithVMSelector,
					},
				},
				Tags: basicTagsForSpWithVMSelector,
			},
		},
	}

	s := &SecurityPolicyService{
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster:          "k8scl-one",
					EnableVPCNetwork: false,
				},
			},
		},
	}
	s.setUpStore(common.TagValueScopeSecurityPolicyUID, false)
	patches := gomonkey.ApplyMethod(reflect.TypeOf(&s.Service), "GetNamespaceUID",
		func(s *common.Service, ns string) types.UID {
			return types.UID(tagValueNSUID)
		})
	defer patches.Reset()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observedPolicy, _, _, _ := s.buildSecurityPolicy(tt.inputPolicy, common.ResourceTypeSecurityPolicy)
			assert.Equal(t, tt.expectedPolicy, observedPolicy)
		})
	}
}

func Test_BuildSecurityPolicyForVPC(t *testing.T) {
	VPCInfo := make([]common.VPCResourceInfo, 1)
	VPCInfo[0].OrgID = "default"
	VPCInfo[0].ProjectID = "projectQuality"
	VPCInfo[0].VPCID = "vpc1"

	fakeService := fakeSecurityPolicyService()
	fakeService.NSXConfig.EnableVPCNetwork = true
	mockVPCService := mock.MockVPCServiceProvider{}
	fakeService.vpcService = &mockVPCService
	fakeService.setUpStore(common.TagValueScopeSecurityPolicyUID, false)

	// For VPC mode
	common.TagValueScopeSecurityPolicyName = common.TagScopeSecurityPolicyName
	common.TagValueScopeSecurityPolicyUID = common.TagScopeSecurityPolicyUID

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

	patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(fakeService), "getVPCInfo",
		func(s *SecurityPolicyService, spNameSpace string) (*common.VPCResourceInfo, error) {
			return &VPCInfo[0], nil
		})

	patches.ApplyMethod(reflect.TypeOf(&fakeService.Service), "GetNamespaceUID",
		func(s *common.Service, ns string) types.UID {
			return types.UID(tagValueNSUID)
		})
	defer patches.Reset()

	podSelectorRule0Name00 := "rule-with-pod-ns-selector_ingress_allow"
	podSelectorRule0IDPort000 := "spA_uidA_2c822e90_all"

	podSelectorRule1Name00 := "rule-with-ns-selector_ingress_allow"
	podSelectorRule1IDPort000 := "spA_uidA_2a4595d0_53"

	vmSelectorRule0Name00 := "rule-with-VM-selector_egress_isolation"
	vmSelectorRule0IDPort000 := "spB_uidB_67410606_all"

	vmSelectorRule1Name00 := "rule-with-ns-selector_egress_isolation"
	vmSelectorRule1IDPort000 := "spB_uidB_7d721f08_all"

	vmSelectorRule2Name00 := "all_egress_isolation"
	vmSelectorRule2IDPort000 := "spB_uidB_a40c8139_all"

	tests := []struct {
		name           string
		inputPolicy    *v1alpha1.SecurityPolicy
		expectedPolicy *model.SecurityPolicy
	}{
		{
			name:        "security-policy-with-pod-selector For VPC",
			inputPolicy: &spWithPodSelector,
			expectedPolicy: &model.SecurityPolicy{
				DisplayName:    common.String("spA"),
				Id:             common.String("spA_re0bz"),
				Scope:          []string{"/orgs/default/projects/projectQuality/vpcs/vpc1/groups/spA-scope_re0bz"},
				SequenceNumber: &seq0,
				Rules: []model.Rule{
					{
						DisplayName:       &podSelectorRule0Name00,
						Id:                &podSelectorRule0IDPort000,
						DestinationGroups: []string{"ANY"},
						Direction:         &nsxRuleDirectionIn,
						Scope:             []string{"/orgs/default/projects/projectQuality/vpcs/vpc1/groups/spA-2c822e90-scope_re0bz"},
						SequenceNumber:    &seq0,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"/orgs/default/projects/projectQuality/infra/domains/default/groups/spA-2c822e90-src_re0bz"},
						Action:            &nsxRuleActionAllow,
						Tags:              vpcBasicTags,
					},
					{
						DisplayName:       &podSelectorRule1Name00,
						Id:                &podSelectorRule1IDPort000,
						DestinationGroups: []string{"ANY"},
						Direction:         &nsxRuleDirectionIn,
						Scope:             []string{"ANY"},
						SequenceNumber:    &seq1,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"/orgs/default/projects/projectQuality/infra/domains/default/groups/spA-2a4595d0-src_re0bz"},
						Action:            &nsxRuleActionAllow,
						ServiceEntries:    []*data.StructValue{serviceEntry},
						Tags:              vpcBasicTags,
					},
				},
				Tags: vpcBasicTags,
			},
		},
		{
			name:        "security-policy-with-VM-selector For VPC",
			inputPolicy: &spWithVMSelector,
			expectedPolicy: &model.SecurityPolicy{
				DisplayName:    common.String("spB"),
				Id:             common.String("spB_9u8w9"),
				Scope:          []string{"/orgs/default/projects/projectQuality/vpcs/vpc1/groups/spB-scope_9u8w9"},
				SequenceNumber: &seq0,
				Rules: []model.Rule{
					{
						DisplayName:       &vmSelectorRule0Name00,
						Id:                &vmSelectorRule0IDPort000,
						DestinationGroups: []string{"/orgs/default/projects/projectQuality/vpcs/vpc1/groups/spB-67410606-dst_9u8w9"},
						Direction:         &nsxRuleDirectionOut,
						Scope:             []string{"/orgs/default/projects/projectQuality/vpcs/vpc1/groups/spB-67410606-scope_9u8w9"},
						SequenceNumber:    &seq0,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"ANY"},
						Action:            &nsxRuleActionDrop,
						Tags:              vpcBasicTagsForSpWithVMSelector,
					},
					{
						DisplayName:       &vmSelectorRule1Name00,
						Id:                &vmSelectorRule1IDPort000,
						DestinationGroups: []string{"/orgs/default/projects/projectQuality/infra/domains/default/groups/spB-7d721f08-dst_9u8w9"},
						Direction:         &nsxRuleDirectionOut,
						Scope:             []string{"ANY"},
						SequenceNumber:    &seq1,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"ANY"},
						Action:            &nsxRuleActionDrop,
						Tags:              vpcBasicTagsForSpWithVMSelector,
					},

					{
						DisplayName:       &vmSelectorRule2Name00,
						Id:                &vmSelectorRule2IDPort000,
						DestinationGroups: []string{"/orgs/default/projects/projectQuality/vpcs/vpc1/groups/spB-a40c8139-dst_9u8w9"},
						Direction:         &nsxRuleDirectionOut,
						Scope:             []string{"ANY"},
						SequenceNumber:    &seq2,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"ANY"},
						Action:            &nsxRuleActionDrop,
						Tags:              vpcBasicTagsForSpWithVMSelector,
					},
				},
				Tags: vpcBasicTagsForSpWithVMSelector,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observedPolicy, _, _, _ := fakeService.buildSecurityPolicy(tt.inputPolicy, common.ResourceTypeSecurityPolicy)
			assert.Equal(t, tt.expectedPolicy, observedPolicy)
		})
	}
}

func Test_BuildPolicyGroup(t *testing.T) {
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
	s := &SecurityPolicyService{
		Service: common.Service{},
	}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(&s.Service), "GetNamespaceUID",
		func(s *common.Service, ns string) types.UID {
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

func Test_BuildTargetTags(t *testing.T) {
	common.TagValueScopeSecurityPolicyName = common.TagScopeSecurityPolicyCRName
	common.TagValueScopeSecurityPolicyUID = common.TagScopeSecurityPolicyCRUID

	ruleTagID0 := service.buildRuleID(&spWithPodSelector, 0)
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
					Tag:   &clusterName,
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
	s := &SecurityPolicyService{
		Service: common.Service{},
	}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(&s.Service), "GetNamespaceUID",
		func(s *common.Service, ns string) types.UID {
			return types.UID(tagValueNSUID)
		})
	defer patches.Reset()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ruleBaseID := service.buildRuleID(tt.inputPolicy, tt.inputIndex)
			assert.ElementsMatch(t, tt.expectedTags, service.buildTargetTags(tt.inputPolicy, tt.inputTargets, ruleBaseID, common.ResourceTypeSecurityPolicy))
		})
	}
}

func Test_BuildPeerTags(t *testing.T) {
	ruleTagID0 := service.buildRuleID(&spWithPodSelector, 0)
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
					Tag:   &clusterName,
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
	s := &SecurityPolicyService{
		Service: common.Service{},
	}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(&s.Service), "GetNamespaceUID",
		func(s *common.Service, ns string) types.UID {
			return types.UID(tagValueNSUID)
		})
	defer patches.Reset()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.ElementsMatch(t, tt.expectedTags, service.buildPeerTags(tt.inputPolicy, &tt.inputPolicy.Spec.Rules[0], tt.inputIndex, true, false, false, common.ResourceTypeSecurityPolicy))
		})
	}
}

func Test_MergeSelectorMatchExpression(t *testing.T) {
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

func Test_UpdateExpressionsMatchExpression(t *testing.T) {
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

func Test_ValidateSelectorExpressions(t *testing.T) {
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

func Test_ValidateSelectorOpIn(t *testing.T) {
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

func Test_ValidateNsSelectorOpNotIn(t *testing.T) {
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

func Test_UpdateMixedExpressionsMatchExpression(t *testing.T) {
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
	ObjectMeta: v1.ObjectMeta{Namespace: "ns1", Name: "spMulPorts", UID: "spMulPortsuidA"},
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
			{
				Action:    &allowAction,
				Direction: &directionOut,
				Ports: []v1alpha1.SecurityPolicyPort{
					{
						Protocol: "TCP",
						Port:     intstr.IntOrString{Type: intstr.Int, IntVal: 80},
					},
					{
						Protocol: "UDP",
						Port:     intstr.IntOrString{Type: intstr.Int, IntVal: 1234},
						EndPort:  1234,
					},
				},
			},
		},
	},
}

var securityPolicyWithOneNamedPort = v1alpha1.SecurityPolicy{
	ObjectMeta: v1.ObjectMeta{Namespace: "ns1", Name: "spNamedPorts", UID: "spNamedPortsuidA"},
	Spec: v1alpha1.SecurityPolicySpec{
		Rules: []v1alpha1.SecurityPolicyRule{
			{
				Name:      "user-defined-rule-namedport",
				Action:    &allowAction,
				Direction: &directionIn,
				Ports: []v1alpha1.SecurityPolicyPort{
					{
						Protocol: "TCP",
						Port:     intstr.IntOrString{Type: intstr.String, StrVal: "http"}, // http port is 80
					},
					{
						Protocol: "UDP",
						Port:     intstr.IntOrString{Type: intstr.Int, IntVal: 1234},
						EndPort:  1235,
					},
				},
			},
			{
				Action:    &allowAction,
				Direction: &directionIn,
				Ports: []v1alpha1.SecurityPolicyPort{
					{
						Protocol: "TCP",
						Port:     intstr.IntOrString{Type: intstr.String, StrVal: "https"}, // http port is 443
					},
					{
						Protocol: "UDP",
						Port:     intstr.IntOrString{Type: intstr.Int, IntVal: 1236},
						EndPort:  1237,
					},
				},
			},
			{
				Action:    &allowAction,
				Direction: &directionIn,
				Ports: []v1alpha1.SecurityPolicyPort{
					{
						Protocol: "TCP",
						Port:     intstr.IntOrString{Type: intstr.String, StrVal: "web"},
					},
					{
						Protocol: "UDP",
						Port:     intstr.IntOrString{Type: intstr.Int, IntVal: 533},
					},
				},
			},
			{
				Action:    &allowAction,
				Direction: &directionIn,
				Ports: []v1alpha1.SecurityPolicyPort{
					{
						Protocol: "TCP",
						Port:     intstr.IntOrString{Type: intstr.String, StrVal: "db"},
					},
				},
			},
		},
	},
}

func Test_BuildRulePortsString(t *testing.T) {
	tests := []struct {
		name                    string
		inputPorts              []v1alpha1.SecurityPolicyPort
		expectedRulePortsString string
	}{
		{
			name:                    "build-string-for-multiple-ports-without-named-port",
			inputPorts:              securityPolicyWithMultipleNormalPorts.Spec.Rules[0].Ports,
			expectedRulePortsString: "TCP.80_UDP.1234.1235",
		},
		{
			name:                    "build-string-for-multiple-ports-userdefinedrule-without-named-port",
			inputPorts:              securityPolicyWithMultipleNormalPorts.Spec.Rules[1].Ports,
			expectedRulePortsString: "TCP.88_UDP.1236.1237",
		},
		{
			name:                    "build-string-for-multiple-ports-start-end-port-same-without-named-port",
			inputPorts:              securityPolicyWithMultipleNormalPorts.Spec.Rules[2].Ports,
			expectedRulePortsString: "TCP.80_UDP.1234.1234",
		},
		{
			name:                    "build-string-for-multiple-ports-with-http-named-port",
			inputPorts:              securityPolicyWithOneNamedPort.Spec.Rules[0].Ports,
			expectedRulePortsString: "TCP.http_UDP.1234.1235",
		},
		{
			name:                    "build-string-for-multiple-ports-with-https-named-port",
			inputPorts:              securityPolicyWithOneNamedPort.Spec.Rules[1].Ports,
			expectedRulePortsString: "TCP.https_UDP.1236.1237",
		},
		{
			name:                    "build-string-for-multiple-ports-with-web-named-port",
			inputPorts:              securityPolicyWithOneNamedPort.Spec.Rules[2].Ports,
			expectedRulePortsString: "TCP.web_UDP.533",
		},
		{
			name:                    "build-string-for-multiple-ports-with-db-named-port",
			inputPorts:              securityPolicyWithOneNamedPort.Spec.Rules[3].Ports,
			expectedRulePortsString: "TCP.db",
		},
		{
			name:                    "build-string-for-nil-ports",
			inputPorts:              nil,
			expectedRulePortsString: "all",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observedString := service.buildRulePortsString(tt.inputPorts)
			assert.Equal(t, tt.expectedRulePortsString, observedString)
		})
	}
}

func Test_BuildRulePortsNumberString(t *testing.T) {
	tests := []struct {
		name                    string
		inputPorts              []v1alpha1.SecurityPolicyPort
		expectedRulePortsString string
	}{
		{
			name:                    "build-string-for-multiple-ports-without-named-port",
			inputPorts:              securityPolicyWithMultipleNormalPorts.Spec.Rules[0].Ports,
			expectedRulePortsString: "80_1234.1235",
		},
		{
			name:                    "build-string-for-multiple-ports-userdefinedrule-without-named-port",
			inputPorts:              securityPolicyWithMultipleNormalPorts.Spec.Rules[1].Ports,
			expectedRulePortsString: "88_1236.1237",
		},
		{
			name:                    "build-string-for-multiple-ports-start-end-port-same-without-named-port",
			inputPorts:              securityPolicyWithMultipleNormalPorts.Spec.Rules[2].Ports,
			expectedRulePortsString: "80_1234.1234",
		},
		{
			name:                    "build-string-for-multiple-ports-with-http-named-port",
			inputPorts:              securityPolicyWithOneNamedPort.Spec.Rules[0].Ports,
			expectedRulePortsString: "http_1234.1235",
		},
		{
			name:                    "build-string-for-multiple-ports-with-https-named-port",
			inputPorts:              securityPolicyWithOneNamedPort.Spec.Rules[1].Ports,
			expectedRulePortsString: "https_1236.1237",
		},
		{
			name:                    "build-string-for-multiple-ports-with-web-named-port",
			inputPorts:              securityPolicyWithOneNamedPort.Spec.Rules[2].Ports,
			expectedRulePortsString: "web_533",
		},
		{
			name:                    "build-string-for-multiple-ports-with-db-named-port",
			inputPorts:              securityPolicyWithOneNamedPort.Spec.Rules[3].Ports,
			expectedRulePortsString: "db",
		},
		{
			name:                    "build-string-for-nil-ports",
			inputPorts:              nil,
			expectedRulePortsString: "all",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observedString := service.buildRulePortsNumberString(tt.inputPorts)
			assert.Equal(t, tt.expectedRulePortsString, observedString)
		})
	}
}

func Test_BuildRuleDisplayName(t *testing.T) {
	tests := []struct {
		name                    string
		inputSecurityPolicy     *v1alpha1.SecurityPolicy
		inputRule               *v1alpha1.SecurityPolicyRule
		ruleIdx                 int
		createdFor              string
		namedPort               *portInfo
		expectedRuleDisplayName string
	}{
		{
			name:                    "build-display-name-for-multiple-ports-0",
			inputSecurityPolicy:     &securityPolicyWithMultipleNormalPorts,
			inputRule:               &securityPolicyWithMultipleNormalPorts.Spec.Rules[0],
			ruleIdx:                 0,
			createdFor:              common.ResourceTypeNetworkPolicy,
			namedPort:               nil,
			expectedRuleDisplayName: "TCP.80_UDP.1234.1235_ingress_allow",
		},
		{
			name:                    "build-display-name-for-multiple-ports-1",
			inputSecurityPolicy:     &securityPolicyWithMultipleNormalPorts,
			inputRule:               &securityPolicyWithMultipleNormalPorts.Spec.Rules[1],
			ruleIdx:                 1,
			createdFor:              common.ResourceTypeNetworkPolicy,
			namedPort:               nil,
			expectedRuleDisplayName: "MultipleNormalPorts-rule1",
		},
		{
			name:                    "build-display-name-for-multiple-ports-1",
			inputSecurityPolicy:     &securityPolicyWithMultipleNormalPorts,
			inputRule:               &securityPolicyWithMultipleNormalPorts.Spec.Rules[1],
			ruleIdx:                 1,
			createdFor:              common.ResourceTypeSecurityPolicy,
			namedPort:               nil,
			expectedRuleDisplayName: "MultipleNormalPorts-rule1_egress_isolation",
		},
		{
			name:                    "build-display-name-for-user-defined-rulename-with-one-named-http-port",
			inputSecurityPolicy:     &securityPolicyWithOneNamedPort,
			inputRule:               &securityPolicyWithOneNamedPort.Spec.Rules[0],
			ruleIdx:                 0,
			createdFor:              common.ResourceTypeSecurityPolicy,
			namedPort:               newPortInfoForNamedPort(nsxutil.PortAddress{Port: 80}, "TCP"),
			expectedRuleDisplayName: "user-defined-rule-namedport.TCP.80_ingress_allow",
		},
		{
			name:                    "build-display-name-for-multiple-ports-with-one-named-https-port",
			inputSecurityPolicy:     &securityPolicyWithOneNamedPort,
			inputRule:               &securityPolicyWithOneNamedPort.Spec.Rules[1],
			ruleIdx:                 1,
			createdFor:              common.ResourceTypeSecurityPolicy,
			namedPort:               newPortInfoForNamedPort(nsxutil.PortAddress{Port: 443}, "TCP"),
			expectedRuleDisplayName: "TCP.https_UDP.1236.1237.TCP.443_ingress_allow",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observedDisplayName, observedError := service.buildRuleDisplayName(tt.inputRule, tt.createdFor, tt.namedPort)
			assert.Equal(t, tt.expectedRuleDisplayName, observedDisplayName)
			assert.Equal(t, nil, observedError)
		})
	}
}

func Test_BuildExpandedRuleID(t *testing.T) {
	svc := &SecurityPolicyService{
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "cluster1",
				},
			},
		},
	}

	tests := []struct {
		name                string
		vpcEnabled          bool
		inputSecurityPolicy *v1alpha1.SecurityPolicy
		inputRule           *v1alpha1.SecurityPolicyRule
		ruleIdx             int
		createdFor          string
		namedPort           *portInfo
		expectedRuleID      string
	}{
		{
			name:                "build-ruleID-for-multiple-ports-0-for-vpc",
			vpcEnabled:          true,
			inputSecurityPolicy: &securityPolicyWithMultipleNormalPorts,
			inputRule:           &securityPolicyWithMultipleNormalPorts.Spec.Rules[0],
			ruleIdx:             0,
			createdFor:          common.ResourceTypeSecurityPolicy,
			namedPort:           nil,
			expectedRuleID:      "spMulPorts_spMulPortsuidA_d0b8e36c_80_1234.1235",
		},
		{
			name:                "build-ruleID-for-multiple-ports-0-for-T1",
			vpcEnabled:          false,
			inputSecurityPolicy: &securityPolicyWithMultipleNormalPorts,
			inputRule:           &securityPolicyWithMultipleNormalPorts.Spec.Rules[0],
			ruleIdx:             0,
			createdFor:          common.ResourceTypeSecurityPolicy,
			namedPort:           nil,
			expectedRuleID:      "sp_spMulPortsuidA_d0b8e36cf858e76624b9706c3c8e77b6006c0e10_0_0_0",
		},
		{
			name:                "build-ruleID-for-multiple-ports-1-for-vpc-NP",
			vpcEnabled:          true,
			inputSecurityPolicy: &securityPolicyWithMultipleNormalPorts,
			inputRule:           &securityPolicyWithMultipleNormalPorts.Spec.Rules[1],
			ruleIdx:             1,
			createdFor:          common.ResourceTypeNetworkPolicy,
			namedPort:           nil,
			expectedRuleID:      "spMulPorts_spMulPortsuidA_555356be_88_1236.1237",
		},
		{
			name:                "build-ruleID-for-multiple-ports-with-one-named-port-for-VPC",
			vpcEnabled:          true,
			inputSecurityPolicy: &securityPolicyWithOneNamedPort,
			inputRule:           &securityPolicyWithOneNamedPort.Spec.Rules[0],
			ruleIdx:             0,
			createdFor:          common.ResourceTypeSecurityPolicy,
			namedPort:           newPortInfoForNamedPort(nsxutil.PortAddress{Port: 80}, "TCP"),
			expectedRuleID:      "spNamedPorts_spNamedPortsuidA_3f7c7d8c_80",
		},
		{
			name:                "build-ruleID-for-multiple-ports-with-one-named-port-for-T1",
			vpcEnabled:          false,
			inputSecurityPolicy: &securityPolicyWithOneNamedPort,
			inputRule:           &securityPolicyWithOneNamedPort.Spec.Rules[0],
			ruleIdx:             0,
			createdFor:          common.ResourceTypeSecurityPolicy,
			namedPort:           newPortInfoForNamedPort(nsxutil.PortAddress{Port: 80}, "TCP"),
			expectedRuleID:      "sp_spNamedPortsuidA_3f7c7d8c8449687178002f23599add04bf0c3250_0_0_0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc.NSXConfig.EnableVPCNetwork = tt.vpcEnabled
			observedRuleID := svc.buildExpandedRuleID(tt.inputSecurityPolicy, tt.ruleIdx, tt.createdFor, tt.namedPort)
			assert.Equal(t, tt.expectedRuleID, observedRuleID)
		})
	}
}

func Test_BuildSecurityPolicyName(t *testing.T) {
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
		name                   string
		vpcEnabled             bool
		obj                    *v1alpha1.SecurityPolicy
		createdFor             string
		existingSecurityPolicy *model.SecurityPolicy
		expName                string
		expId                  string
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
			expName:    "securitypolicy1",
			expId:      "sp_uid1",
		},
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
			existingSecurityPolicy: &model.SecurityPolicy{
				Id:          common.String("sp_uid1"),
				DisplayName: common.String("securitypolicy1"),
				Tags: []model.Tag{
					{
						Scope: common.String(common.TagValueScopeSecurityPolicyUID),
						Tag:   common.String("uid1"),
					},
				},
			},
			createdFor: common.ResourceTypeSecurityPolicy,
			expName:    "securitypolicy1",
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
			expId:      "securitypolicy2_qh94x",
		},
		{
			name:       "Upgrade case for SecurityPolicy with VPC enabled",
			vpcEnabled: true,
			obj: &v1alpha1.SecurityPolicy{
				ObjectMeta: v1.ObjectMeta{
					UID:       "uid2",
					Name:      "securitypolicy2",
					Namespace: "ns2",
				},
			},
			existingSecurityPolicy: &model.SecurityPolicy{
				Id:          common.String("securitypolicy2_uid2"),
				DisplayName: common.String("securitypolicy2"),
				Tags: []model.Tag{
					{
						Scope: common.String(common.TagValueScopeSecurityPolicyUID),
						Tag:   common.String("uid2"),
					},
				},
			},
			createdFor: common.ResourceTypeSecurityPolicy,
			expName:    "securitypolicy2",
			expId:      "securitypolicy2_uid2",
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
			expId:      "networkpolicy1_kjv4c",
		},
		{
			name:       "Upgrade case for NetworkPolicy with VPC enabled",
			vpcEnabled: true,
			obj: &v1alpha1.SecurityPolicy{
				ObjectMeta: v1.ObjectMeta{
					UID:       "uid3",
					Name:      "networkpolicy1",
					Namespace: "ns3",
				},
			},
			createdFor: common.ResourceTypeNetworkPolicy,
			existingSecurityPolicy: &model.SecurityPolicy{
				Id:          common.String("networkpolicy1_uid3"),
				DisplayName: common.String("networkpolicy1"),
				Tags: []model.Tag{
					{
						Scope: common.String(common.TagScopeNetworkPolicyUID),
						Tag:   common.String("uid3"),
					},
				},
			},
			expName: "networkpolicy1",
			expId:   "networkpolicy1_uid3",
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
			expName:    fmt.Sprintf("%s_n5pcg", strings.Repeat("a", 249)),
			expId:      fmt.Sprintf("%s_tdpcn", strings.Repeat("a", 249)),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc.setUpStore(common.TagValueScopeSecurityPolicyUID, false)
			svc.NSXConfig.EnableVPCNetwork = tc.vpcEnabled
			if tc.existingSecurityPolicy != nil {
				svc.securityPolicyStore.Add(tc.existingSecurityPolicy)
			}
			id, name := svc.buildSecurityPolicyIDAndName(tc.obj, tc.createdFor)
			assert.Equal(t, tc.expId, id)
			assert.Equal(t, tc.expName, name)
			assert.True(t, len(name) <= common.MaxNameLength)
		})
	}
}

func Test_BuildGroupName(t *testing.T) {
	svc := &SecurityPolicyService{
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "cluster1",
				},
			},
		},
	}
	svc.setUpStore(common.TagValueScopeSecurityPolicyUID, false)

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
				name:      "src peer group for rule without user-defined name",
				ruleIdx:   0,
				isSource:  true,
				enableVPC: true,
				expName:   "sp1_d0b8e36c_src",
				expId:     "sp1-d0b8e36c-src_gzkfa",
			},
			{
				name:      "dst peer group for rule without user-defined name",
				ruleIdx:   0,
				isSource:  false,
				enableVPC: true,
				expName:   "sp1_d0b8e36c_dst",
				expId:     "sp1-d0b8e36c-dst_gzkfa",
			},
			{
				name:      "dst peer group for rule without user-defined name for T1",
				ruleIdx:   0,
				isSource:  false,
				enableVPC: false,
				expName:   "sp1_d0b8e36c_dst",
				expId:     "sp_c5db1800-ce4c-11de-bedc-84a0de00c35b_0_dst",
			},
			{
				name:      "src peer group for rule with user-defined name",
				ruleIdx:   1,
				isSource:  true,
				enableVPC: true,
				expName:   "sp1_555356be_src",
				expId:     "sp1-555356be-src_gzkfa",
			},
			{
				name:      "dst peer group for rule with user-defined name",
				ruleIdx:   1,
				isSource:  false,
				enableVPC: true,
				expName:   "sp1_555356be_dst",
				expId:     "sp1-555356be-dst_gzkfa",
			},
			{
				name:      "dst peer group for rule with user-defined name for T1",
				ruleIdx:   1,
				isSource:  false,
				enableVPC: false,
				expName:   "sp1_555356be_dst",
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
		for _, tc := range []struct {
			name      string
			ruleIdx   int
			enableVPC bool
			expName   string
			expId     string
		}{
			{
				name:      "applied group for rule without user-defined name",
				ruleIdx:   0,
				enableVPC: true,
				expName:   "sp1_d0b8e36c_scope",
				expId:     "sp1-d0b8e36c-scope_gzkfa",
			},
			{
				name:      "applied group for rule with user-defined name",
				ruleIdx:   1,
				enableVPC: true,
				expName:   "sp1_555356be_scope",
				expId:     "sp1-555356be-scope_gzkfa",
			},
			{
				name:      "applied group for rule without user-defined name",
				ruleIdx:   0,
				enableVPC: false,
				expName:   "sp1_d0b8e36c_scope",
				expId:     "sp_c5db1800-ce4c-11de-bedc-84a0de00c35b_0_scope",
			},
			{
				name:      "applied group fpr rule with user-defined name for T1",
				ruleIdx:   1,
				enableVPC: false,
				expName:   "sp1_555356be_scope",
				expId:     "sp_c5db1800-ce4c-11de-bedc-84a0de00c35b_1_scope",
			},
			{
				name:      "policy applied group",
				ruleIdx:   -1,
				enableVPC: true,
				expName:   "sp1_scope",
				expId:     "sp1-scope_gzkfa",
			},
			{
				name:      "policy applied group for T1",
				ruleIdx:   -1,
				enableVPC: false,
				expName:   "sp1_scope",
				expId:     "sp_c5db1800-ce4c-11de-bedc-84a0de00c35b_scope",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				svc.NSXConfig.EnableVPCNetwork = tc.enableVPC
				id, dispName := svc.buildAppliedGroupIDAndName(obj, tc.ruleIdx, common.ResourceTypeNetworkPolicy)
				assert.Equal(t, tc.expId, id)
				assert.Equal(t, dispName, tc.expName)
			})
		}
	})
}

func Test_buildRuleServiceEntries(t *testing.T) {
	tests := []struct {
		name     string
		port     v1alpha1.SecurityPolicyPort
		expected *data.StructValue
	}{
		{
			name: "port with the same Port and EndPort",
			port: v1alpha1.SecurityPolicyPort{
				Port:     intstr.FromInt(80),
				EndPort:  80,
				Protocol: "TCP",
			},
			expected: func() *data.StructValue {
				destinationPorts := data.NewListValue()
				destinationPorts.Add(data.NewStringValue("80"))
				return data.NewStructValue(
					"",
					map[string]data.DataValue{
						"source_ports":      data.NewListValue(),
						"destination_ports": destinationPorts,
						"l4_protocol":       data.NewStringValue("TCP"),
						"resource_type":     data.NewStringValue("L4PortSetServiceEntry"),
						"marked_for_delete": data.NewBooleanValue(false),
						"overridden":        data.NewBooleanValue(false),
					},
				)
			}(),
		},
		{
			name: "zero port (ANY)",
			port: v1alpha1.SecurityPolicyPort{
				Protocol: "TCP",
			},
			expected: func() *data.StructValue {
				return data.NewStructValue(
					"",
					map[string]data.DataValue{
						"source_ports":      data.NewListValue(),
						"destination_ports": data.NewListValue(),
						"l4_protocol":       data.NewStringValue("TCP"),
						"resource_type":     data.NewStringValue("L4PortSetServiceEntry"),
						"marked_for_delete": data.NewBooleanValue(false),
						"overridden":        data.NewBooleanValue(false),
					},
				)
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := buildRuleServiceEntries(tt.port)
			assert.Equal(t, actual, tt.expected)
		})
	}
}
