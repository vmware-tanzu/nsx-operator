/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package securitypolicy

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

func TestNativeSelectorMergesInBeforeValidation(t *testing.T) {
	service := &SecurityPolicyService{}
	for _, memberType := range []string{common.MemberTypeNamespace, common.MemberTypeVirtualMachine} {
		for _, tc := range []struct {
			name      string
			first     []string
			second    []string
			want      []string
			wantError bool
		}{
			{name: "intersection", first: []string{"prod", "staging"}, second: []string{"prod"}, want: []string{"prod"}},
			{name: "validate merged value count", first: []string{"a", "b", "c", "d", "e"}, second: []string{"a", "b", "c", "d", "e"}, want: []string{"a", "b", "c", "d", "e"}},
			{name: "empty string intersection", first: []string{"", "prod"}, second: []string{""}, want: []string{""}},
			{name: "disjoint", first: []string{"prod"}, second: []string{"staging"}, wantError: true},
		} {
			t.Run(memberType+"/"+tc.name, func(t *testing.T) {
				selector := &v1.LabelSelector{MatchExpressions: []v1.LabelSelectorRequirement{
					{Key: "env", Operator: v1.LabelSelectorOpIn, Values: tc.first},
					{Key: "env", Operator: v1.LabelSelectorOpIn, Values: tc.second},
				}}
				original := selector.DeepCopy()
				_, in, err := service.buildNativeSelectorConditions(selector, memberType)
				assert.Equal(t, original, selector)
				if tc.wantError {
					var validationErr *nsxutil.ValidationError
					require.ErrorAs(t, err, &validationErr)
					return
				}
				require.NoError(t, err)
				require.NotNil(t, in)
				assert.ElementsMatch(t, tc.want, in.Values)
			})
		}
	}
	_, _, err := service.buildNativeSelectorConditions(&v1.LabelSelector{MatchExpressions: []v1.LabelSelectorRequirement{
		{Key: "env", Operator: v1.LabelSelectorOpIn, Values: []string{"prod"}},
		{Key: "role", Operator: v1.LabelSelectorOpIn, Values: []string{"api"}},
	}}, common.MemberTypeNamespace)
	require.Error(t, err, "different keys must still obey the legacy In limit")
}

func TestNativeSelectorLongKeyConsistency(t *testing.T) {
	service := &SecurityPolicyService{}
	key := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + ".example/env"
	require.Empty(t, validation.IsQualifiedName(key))
	for _, memberType := range []string{common.MemberTypeNamespace, common.MemberTypeVirtualMachine} {
		t.Run(memberType, func(t *testing.T) {
			wantScope := "env"
			if memberType == common.MemberTypeVirtualMachine {
				wantScope = "K8sTag/env"
			}
			conditions, _, err := service.buildNativeSelectorConditions(&v1.LabelSelector{MatchLabels: map[string]string{key: "prod"}}, memberType)
			require.NoError(t, err)
			value, err := conditions[0].StringField("value")
			require.NoError(t, err)
			assert.Equal(t, wantScope+"|prod", value)
			for _, operator := range []v1.LabelSelectorOperator{v1.LabelSelectorOpIn, v1.LabelSelectorOpNotIn, v1.LabelSelectorOpExists, v1.LabelSelectorOpDoesNotExist} {
				t.Run(string(operator), func(t *testing.T) {
					expr := v1.LabelSelectorRequirement{Key: key, Operator: operator}
					if operator == v1.LabelSelectorOpIn || operator == v1.LabelSelectorOpNotIn {
						expr.Values = []string{"prod"}
					}
					conditions, in, err := service.buildNativeSelectorConditions(&v1.LabelSelector{MatchExpressions: []v1.LabelSelectorRequirement{expr}}, memberType)
					require.NoError(t, err)
					if in != nil {
						assert.Equal(t, wantScope, in.Key)
						return
					}
					value, err := conditions[0].StringField("value")
					require.NoError(t, err)
					assert.Equal(t, wantScope, strings.SplitN(value, "|", 2)[0])
					if operator == v1.LabelSelectorOpDoesNotExist {
						tagOperator, err := conditions[0].StringField("operator")
						require.NoError(t, err)
						assert.Equal(t, "EQUALS", tagOperator)
						scopeOperator, err := conditions[0].StringField("scope_operator")
						require.NoError(t, err)
						assert.Equal(t, "NOTEQUALS", scopeOperator)
					}
				})
			}
		})
	}
}

func TestNativeCriteriaLimits(t *testing.T) {
	service := &SecurityPolicyService{Service: common.Service{
		NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "cluster-a"}},
	}}
	patch := gomonkey.ApplyMethod(reflect.TypeOf(&service.Service), "GetNamespaceUID", func(_ *common.Service, _ string) types.UID { return "ns-uid" })
	defer patch.Reset()
	for _, tc := range []struct {
		name       string
		target     bool
		namespace  bool
		in         bool
		labelLimit int
	}{
		{name: "namespace peer", namespace: true, labelLimit: 4},
		{name: "namespace peer In", namespace: true, in: true, labelLimit: 3},
		{name: "pod peer", labelLimit: 13},
		{name: "pod peer In", in: true, labelLimit: 12},
		{name: "appliedTo", target: true, labelLimit: 13},
		{name: "appliedTo In", target: true, in: true, labelLimit: 12},
	} {
		for _, overflow := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/overflow=%t", tc.name, overflow), func(t *testing.T) {
				count := tc.labelLimit
				if overflow {
					count++
				}
				selector := &v1.LabelSelector{MatchLabels: map[string]string{}}
				for i := 0; i < count; i++ {
					selector.MatchLabels[fmt.Sprintf("label%d", i)] = "value"
				}
				if tc.in {
					selector.MatchExpressions = []v1.LabelSelectorRequirement{{Key: "env", Operator: v1.LabelSelectorOpIn, Values: []string{"prod", "staging"}}}
				}
				group := &model.Group{}
				obj := &v1alpha1.SecurityPolicy{ObjectMeta: v1.ObjectMeta{Namespace: "ns"}}
				var added, total int
				var err error
				if tc.target {
					added, total, err = service.updateNativeTargetExpressions(obj, &v1alpha1.SecurityPolicyTarget{PodSelector: selector}, group)
				} else {
					peer := &v1alpha1.SecurityPolicyPeer{PodSelector: selector}
					if tc.namespace {
						peer = &v1alpha1.SecurityPolicyPeer{NamespaceSelector: selector}
					}
					added, total, err = service.updateNativePeerExpressions(obj, peer, group)
				}
				if overflow {
					var validationErr *nsxutil.ValidationError
					require.ErrorAs(t, err, &validationErr)
					assert.Empty(t, group.Expression)
					return
				}
				require.NoError(t, err)
				wantCriteria, wantConditions := 1, MaxMixedCriteriaExpressions
				if tc.in {
					wantCriteria = 2
				}
				if tc.namespace {
					wantConditions = MaxCriteriaExpressions
				}
				assert.Equal(t, wantCriteria, added)
				assert.Equal(t, wantCriteria*wantConditions, total)
			})
		}
	}
}

func TestNativeSelectorTagScopeLength(t *testing.T) {
	service := &SecurityPolicyService{}
	prefix := strings.Repeat("a", 60) + ".example/"
	for _, tc := range []struct {
		name   string
		length int
		last   string
	}{
		{name: "VM scope at limit", length: 121, last: "x"},
		{name: "VM scope over limit", length: 122, last: "x"},
		{name: "previous collision x", length: 124, last: "x"},
		{name: "previous collision y", length: 124, last: "y"},
		{name: "namespace scope at limit", length: 128, last: "x"},
	} {
		key := prefix + strings.Repeat("b", tc.length-len(prefix)-1) + tc.last
		require.Empty(t, validation.IsQualifiedName(key))
		for _, memberType := range []string{common.MemberTypeNamespace, common.MemberTypeVirtualMachine} {
			for _, operator := range []v1.LabelSelectorOperator{"", v1.LabelSelectorOpIn, v1.LabelSelectorOpNotIn, v1.LabelSelectorOpExists, v1.LabelSelectorOpDoesNotExist} {
				name := string(operator)
				if name == "" {
					name = "matchLabels"
				}
				t.Run(tc.name+"/"+memberType+"/"+name, func(t *testing.T) {
					selector := &v1.LabelSelector{MatchLabels: map[string]string{key: "prod"}}
					if operator != "" {
						expr := v1.LabelSelectorRequirement{Key: key, Operator: operator}
						if operator == v1.LabelSelectorOpIn || operator == v1.LabelSelectorOpNotIn {
							expr.Values = []string{"prod"}
						}
						selector = &v1.LabelSelector{MatchExpressions: []v1.LabelSelectorRequirement{expr}}
					}
					conditions, in, err := service.buildNativeSelectorConditions(selector, memberType)
					wantScope := key
					if memberType == common.MemberTypeVirtualMachine {
						wantScope = "K8sTag/" + key
					}
					if len(wantScope) > common.MaxTagScopeLength {
						var validationErr *nsxutil.ValidationError
						require.ErrorAs(t, err, &validationErr)
						assert.Contains(t, validationErr.Desc, key)
						assert.Contains(t, validationErr.Desc, "exceeding NSX limit of 128")
						assert.Nil(t, conditions)
						assert.Nil(t, in)
						return
					}
					require.NoError(t, err)
					if in != nil {
						assert.Equal(t, wantScope, in.Key)
						return
					}
					require.Len(t, conditions, 1)
					value, err := conditions[0].StringField("value")
					require.NoError(t, err)
					assert.Equal(t, wantScope, strings.SplitN(value, "|", 2)[0])
				})
			}
		}
	}
}

func TestNativeTagScopeLengthErrorPropagation(t *testing.T) {
	service := &SecurityPolicyService{Service: common.Service{
		NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "cluster-a"}},
	}}
	patch := gomonkey.ApplyMethod(reflect.TypeOf(&service.Service), "GetNamespaceUID", func(_ *common.Service, _ string) types.UID { return "ns-uid" })
	defer patch.Reset()
	key := strings.Repeat("a", 60) + ".example/" + strings.Repeat("b", 55)
	selector := &v1.LabelSelector{MatchLabels: map[string]string{key: "prod"}}
	for _, workload := range []string{"pod", "vm"} {
		t.Run(workload, func(t *testing.T) {
			target := &v1alpha1.SecurityPolicyTarget{PodSelector: selector}
			peer := &v1alpha1.SecurityPolicyPeer{PodSelector: selector}
			if workload == "vm" {
				target = &v1alpha1.SecurityPolicyTarget{VMSelector: selector}
				peer = &v1alpha1.SecurityPolicyPeer{VMSelector: selector}
			}
			obj := &v1alpha1.SecurityPolicy{}
			group := &model.Group{}
			_, _, err := service.updateNativeTargetExpressions(obj, target, group)
			var validationErr *nsxutil.ValidationError
			require.ErrorAs(t, err, &validationErr)
			assert.Empty(t, group.Expression)
			_, _, err = service.updateNativePeerExpressions(obj, peer, group)
			require.ErrorAs(t, err, &validationErr)
			assert.Empty(t, group.Expression)
		})
	}
}

func TestBuildNativeRuleAndGroupsNoNilRuleGroups(t *testing.T) {
	service := &SecurityPolicyService{Service: common.Service{
		NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "cluster-a"}},
	}}
	patch := gomonkey.ApplyMethod(reflect.TypeOf(&service.Service), "GetNamespaceUID", func(_ *common.Service, _ string) types.UID { return "ns-uid" })
	defer patch.Reset()

	actionAllow := v1alpha1.RuleActionAllow
	dirIn := v1alpha1.RuleDirectionIn
	rule := &v1alpha1.SecurityPolicyRule{
		Action:    &actionAllow,
		Direction: &dirIn,
		From: []v1alpha1.SecurityPolicyPeer{
			{PodSelector: &v1.LabelSelector{MatchLabels: map[string]string{"app": "client"}}},
		},
	}
	sp := &v1alpha1.SecurityPolicy{
		ObjectMeta: v1.ObjectMeta{Name: "sp-1", Namespace: "default", UID: "sp-uid"},
		Spec: v1alpha1.SecurityPolicySpec{
			AppliedTo: []v1alpha1.SecurityPolicyTarget{
				{PodSelector: &v1.LabelSelector{MatchLabels: map[string]string{"app": "target"}}},
			},
			Rules: []v1alpha1.SecurityPolicyRule{*rule},
		},
	}
	_, ruleGroups, _, err := service.buildNativeRuleAndGroups(sp, rule, 0, "sp_created_for", "policy-group-path", nil, false)
	require.NoError(t, err)
	require.NotEmpty(t, ruleGroups)
	for _, rg := range ruleGroups {
		assert.NotNil(t, rg, "ruleGroups must not contain nil elements")
	}
}

func TestNativeIPBlocksCriteriaLimits(t *testing.T) {
	service := &SecurityPolicyService{Service: common.Service{
		NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "cluster-a"}},
	}}
	patch := gomonkey.ApplyMethod(reflect.TypeOf(&service.Service), "GetNamespaceUID", func(_ *common.Service, _ string) types.UID { return "ns-uid" })
	defer patch.Reset()

	sp := &v1alpha1.SecurityPolicy{
		ObjectMeta: v1.ObjectMeta{Name: "sp-1", Namespace: "default", UID: "sp-uid"},
	}

	actionAllow := v1alpha1.RuleActionAllow
	dirIn := v1alpha1.RuleDirectionIn
	for _, tc := range []struct {
		name      string
		peerCount int
		wantError bool
	}{
		{name: "5 IPBlock peers at limit", peerCount: 5, wantError: false},
		{name: "6 IPBlock peers exceed limit", peerCount: 6, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var peers []v1alpha1.SecurityPolicyPeer
			for i := 0; i < tc.peerCount; i++ {
				peers = append(peers, v1alpha1.SecurityPolicyPeer{
					IPBlocks: []v1alpha1.IPBlock{
						{CIDR: fmt.Sprintf("192.168.%d.0/24", i+1)},
					},
				})
			}
			rule := &v1alpha1.SecurityPolicyRule{
				Action:    &actionAllow,
				Direction: &dirIn,
				From:      peers,
			}
			sp.Spec.Rules = []v1alpha1.SecurityPolicyRule{*rule}
			_, _, _, err := service.buildNativeRulePeerGroup(sp, rule, 0, "rule-base-id", true, "sp_created_for", nil, false)
			if tc.wantError {
				var validationErr *nsxutil.ValidationError
				require.ErrorAs(t, err, &validationErr)
				assert.Contains(t, validationErr.Desc, fmt.Sprintf("total counts of rule source group criteria %d exceed NSX limit of 5", tc.peerCount))
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestNativeMatchLabelsDeterministicOrder(t *testing.T) {
	service := &SecurityPolicyService{}
	labels := map[string]string{
		"z-app":  "web",
		"a-tier": "frontend",
		"m-env":  "prod",
	}
	conds1, err := service.buildNativeMatchLabelsConditions(labels, common.MemberTypeVirtualMachine)
	require.NoError(t, err)
	require.Len(t, conds1, 3)

	for i := 0; i < 10; i++ {
		conds2, err := service.buildNativeMatchLabelsConditions(labels, common.MemberTypeVirtualMachine)
		require.NoError(t, err)
		for j := range conds1 {
			val1, _ := conds1[j].StringField("value")
			val2, _ := conds2[j].StringField("value")
			assert.Equal(t, val1, val2)
		}
	}
}
