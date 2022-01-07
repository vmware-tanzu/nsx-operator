/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
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
	tagValuePodSelectorHash      = "6fb11ac7a06285f0ed64a85310de99cf5bf423d0"

	spWithPodSelector = v1alpha1.SecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "spA", UID: "uidA"},
		Spec: v1alpha1.SecurityPolicySpec{
			AppliedTo: []v1alpha1.SecurityPolicyTarget{
				{
					PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"pod_selector_1": "pod_value_1"}},
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
					VMSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"VM_selector_1": "VM_value_1"}},
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
	config.AddFlags()
	assert.Equal(t, "k8scl-one", getCluster())
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
					model.Rule{
						DisplayName:       &ruleNameWithPodSelector,
						Id:                &ruleID0,
						DestinationGroups: []string{"ANY"},
						Direction:         &nsxDirectionIn,
						Scope:             []string{"ANY"},
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
					model.Rule{
						DisplayName:       &ruleNameWithVMSelector,
						Id:                &ruleID0,
						DestinationGroups: []string{"ANY"},
						Direction:         &nsxDirectionIn,
						Scope:             []string{"ANY"},
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
			observedPolicy, _, _ := buildSecurityPolicy(tt.inputPolicy)
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
			observedGroup, observedGroupPath, _ := buildPolicyGroup(tt.inputPolicy)
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
			assert.Equal(t, tt.expectedTags, buildTargetTags(tt.inputPolicy, tt.inputTargets, tt.inputIndex))
		})
	}
}
