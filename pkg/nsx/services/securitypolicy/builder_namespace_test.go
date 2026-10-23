/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package securitypolicy

import (
	"reflect"
	"testing"

	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

func TestIsNamespaceGroupSupported(t *testing.T) {
	service := &SecurityPolicyService{}
	assert.False(t, service.isNamespaceGroupSupported())

	service.NSXClient = &nsx.Client{Cluster: &nsx.Cluster{}}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, feature int) bool {
		return feature == nsx.NamespaceGroup
	})
	defer patches.Reset()

	assert.True(t, service.isNamespaceGroupSupported())
}

func TestBuildNativeConditions(t *testing.T) {
	service := &SecurityPolicyService{
		Service: common.Service{
			NSXClient: &nsx.Client{
				NsxConfig: &config.NSXOperatorConfig{},
			},
		},
	}

	cond := service.buildNativeCondition(common.MemberTypeNamespace, "vm_namespace|ns-1", "EQUALS", "EQUALS")
	require.NotNil(t, cond)

	m := cond.Fields()
	require.NotNil(t, m)

	nsCond := service.buildNativeNamespaceCondition("vm_namespace", "ns-1", "EQUALS")
	require.NotNil(t, nsCond)

	vmTypeCond := service.buildNativeVMTypeCondition(common.VMTypePod)
	require.NotNil(t, vmTypeCond)
}

func TestBuildNativeSelectorConditions(t *testing.T) {
	service := &SecurityPolicyService{}

	selector := &v1.LabelSelector{
		MatchLabels: map[string]string{"app": "web"},
		MatchExpressions: []v1.LabelSelectorRequirement{
			{Key: "env", Operator: v1.LabelSelectorOpIn, Values: []string{"prod", "staging"}},
			{Key: "tier", Operator: v1.LabelSelectorOpNotIn, Values: []string{"dev"}},
			{Key: "role", Operator: v1.LabelSelectorOpExists},
			{Key: "deprecated", Operator: v1.LabelSelectorOpDoesNotExist},
		},
	}

	baseConds, inExpr, err := service.buildNativeSelectorConditions(selector, common.MemberTypeVirtualMachine)
	require.NoError(t, err)
	assert.Len(t, baseConds, 4) // 1 for matchLabels, 1 for NotIn, 1 for Exists, 1 for DoesNotExist
	require.NotNil(t, inExpr)
	assert.Equal(t, "K8sTag/env", inExpr.Key)
	assert.Equal(t, []string{"prod", "staging"}, inExpr.Values)

	invalidSelector := &v1.LabelSelector{
		MatchExpressions: []v1.LabelSelectorRequirement{
			{Key: "foo", Operator: "InvalidOp"},
		},
	}
	_, _, err = service.buildNativeSelectorConditions(invalidSelector, common.MemberTypeVirtualMachine)
	assert.Error(t, err)
}

func TestUpdateNativeTargetExpressions(t *testing.T) {
	service := &SecurityPolicyService{
		Service: common.Service{
			NSXClient: &nsx.Client{
				NsxConfig: &config.NSXOperatorConfig{},
			},
		},
	}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(&service.Service), "GetNamespaceUID", func(_ *common.Service, ns string) types.UID {
		return "ns-uid-123"
	})
	defer patches.Reset()

	sp := &v1alpha1.SecurityPolicy{
		ObjectMeta: v1.ObjectMeta{
			Name:      "sp-1",
			Namespace: "default",
		},
	}

	// Conflict error test
	targetInvalid := &v1alpha1.SecurityPolicyTarget{
		PodSelector: &v1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
		VMSelector:  &v1.LabelSelector{MatchLabels: map[string]string{"vm": "web"}},
	}
	grp := &model.Group{}
	_, _, err := service.updateNativeTargetExpressions(sp, targetInvalid, grp)
	assert.Error(t, err)

	// Valid pod selector target
	targetValid := &v1alpha1.SecurityPolicyTarget{
		PodSelector: &v1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
	}
	grpValid := &model.Group{}
	added, condCount, err := service.updateNativeTargetExpressions(sp, targetValid, grpValid)
	require.NoError(t, err)
	assert.Equal(t, 1, added)
	// Expect 3 conditions: namespace UID + K8sTag/app=web + NodeType=POD
	assert.Equal(t, 3, condCount)
	assert.NotEmpty(t, grpValid.Expression)
}

func TestUpdateNativePeerExpressions(t *testing.T) {
	service := &SecurityPolicyService{
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "cluster-a"}},
			NSXClient: &nsx.Client{
				NsxConfig: &config.NSXOperatorConfig{},
			},
		},
	}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(&service.Service), "GetNamespaceUID", func(_ *common.Service, ns string) types.UID {
		return "ns-uid-123"
	})
	defer patches.Reset()

	sp := &v1alpha1.SecurityPolicy{
		ObjectMeta: v1.ObjectMeta{
			Name:      "sp-1",
			Namespace: "default",
		},
	}

	// Conflict error
	peerInvalid := &v1alpha1.SecurityPolicyPeer{
		PodSelector: &v1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
		VMSelector:  &v1.LabelSelector{MatchLabels: map[string]string{"vm": "web"}},
	}
	grp := &model.Group{}
	_, _, err := service.updateNativePeerExpressions(sp, peerInvalid, grp)
	assert.Error(t, err)

	// IPBlocks
	peerIP := &v1alpha1.SecurityPolicyPeer{
		IPBlocks: []v1alpha1.IPBlock{
			{CIDR: "192.168.1.0/24"},
		},
	}
	grpIP := &model.Group{}
	added, condCount, err := service.updateNativePeerExpressions(sp, peerIP, grpIP)
	require.NoError(t, err)
	assert.Zero(t, added) // IP expressions do not consume condition criteria.
	assert.Zero(t, condCount)
	assert.NotEmpty(t, grpIP.Expression)

	// PodSelector only (no namespaceSelector) -> same-namespace defaulting
	peerPodOnly := &v1alpha1.SecurityPolicyPeer{
		PodSelector: &v1.LabelSelector{MatchLabels: map[string]string{"app": "client"}},
	}
	grpPodOnly := &model.Group{}
	added, condCount, err = service.updateNativePeerExpressions(sp, peerPodOnly, grpPodOnly)
	require.NoError(t, err)
	assert.Equal(t, 1, added)
	// Expect 3 conditions: same-namespace UID + K8sTag/app=client + NodeType=POD
	assert.Equal(t, 3, condCount)

	// NamespaceSelector + PodSelector
	peerCombined := &v1alpha1.SecurityPolicyPeer{
		NamespaceSelector: &v1.LabelSelector{MatchLabels: map[string]string{"ns-label": "value"}},
		PodSelector:       &v1.LabelSelector{MatchLabels: map[string]string{"app": "client"}},
	}
	grpCombined := &model.Group{}
	added, condCount, err = service.updateNativePeerExpressions(sp, peerCombined, grpCombined)
	require.NoError(t, err)
	assert.Equal(t, 1, added)
	assert.Greater(t, condCount, 0)
}

func TestUpdateNativePeerExpressionsMixedInValidation(t *testing.T) {
	service := &SecurityPolicyService{
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "cluster-a"}},
		},
	}
	for _, tc := range []struct {
		name            string
		namespaceValues []string
		workloadValues  []string
		wantCriteria    int
		wantError       bool
	}{
		{name: "both In within criteria limit", namespaceValues: []string{"prod", "staging"}, workloadValues: []string{"api", "worker"}, wantError: true},
		{name: "both In exceed criteria limit", namespaceValues: []string{"prod", "staging", "dev"}, workloadValues: []string{"api", "worker"}, wantError: true},
		{name: "namespace In only", namespaceValues: []string{"prod", "staging"}, wantCriteria: 2},
		{name: "workload In only", workloadValues: []string{"api", "worker"}, wantCriteria: 2},
	} {
		for _, workload := range []string{"pod", "vm"} {
			t.Run(tc.name+"/"+workload, func(t *testing.T) {
				namespaceSelector := &v1.LabelSelector{}
				if len(tc.namespaceValues) > 0 {
					namespaceSelector.MatchExpressions = []v1.LabelSelectorRequirement{
						{Key: "environment", Operator: v1.LabelSelectorOpIn, Values: tc.namespaceValues},
					}
				}
				workloadSelector := &v1.LabelSelector{}
				if len(tc.workloadValues) > 0 {
					workloadSelector.MatchExpressions = []v1.LabelSelectorRequirement{
						{Key: "role", Operator: v1.LabelSelectorOpIn, Values: tc.workloadValues},
					}
				}
				peer := &v1alpha1.SecurityPolicyPeer{NamespaceSelector: namespaceSelector}
				if workload == "pod" {
					peer.PodSelector = workloadSelector
				} else {
					peer.VMSelector = workloadSelector
				}
				group := &model.Group{}
				added, total, err := service.updateNativePeerExpressions(&v1alpha1.SecurityPolicy{}, peer, group)
				if tc.wantError {
					var validationErr *nsxutil.ValidationError
					require.ErrorAs(t, err, &validationErr)
					assert.Equal(t, "operator 'In' is set in both Pod/VM selector and NamespaceSelector", validationErr.Desc)
					assert.Zero(t, added)
					assert.Zero(t, total)
					assert.Empty(t, group.Expression)
					return
				}
				require.NoError(t, err)
				assert.Equal(t, tc.wantCriteria, added)
				assert.Len(t, group.Expression, 2*tc.wantCriteria-1)
			})
		}
	}
}

func TestUpdateNativePeerExpressionsNamespaceClusterScope(t *testing.T) {
	for _, vpcMode := range []bool{false, true} {
		networkMode := "non-VPC"
		clusterScope := common.TagScopeNCPCluster
		if vpcMode {
			networkMode = "VPC"
			clusterScope = common.TagScopeCluster
		}
		t.Run(networkMode, func(t *testing.T) {
			service := &SecurityPolicyService{
				Service: common.Service{
					NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "cluster-a"}},
				},
				VPCMode: vpcMode,
			}
			for _, tc := range []struct {
				name     string
				selector *v1.LabelSelector
				values   []string
			}{
				{name: "empty", selector: &v1.LabelSelector{}, values: []string{""}},
				{name: "matchLabels", selector: &v1.LabelSelector{MatchLabels: map[string]string{"env": "prod"}}, values: []string{"prod"}},
				{name: "In", selector: &v1.LabelSelector{MatchExpressions: []v1.LabelSelectorRequirement{
					{Key: "env", Operator: v1.LabelSelectorOpIn, Values: []string{"prod", "staging"}},
				}}, values: []string{"prod", "staging"}},
				{name: "In empty value", selector: &v1.LabelSelector{MatchExpressions: []v1.LabelSelectorRequirement{
					{Key: "env", Operator: v1.LabelSelectorOpIn, Values: []string{""}},
				}}, values: []string{""}},
				{name: "In empty and non-empty values", selector: &v1.LabelSelector{MatchExpressions: []v1.LabelSelectorRequirement{
					{Key: "env", Operator: v1.LabelSelectorOpIn, Values: []string{"", "prod"}},
				}}, values: []string{"", "prod"}},
			} {
				for _, workload := range []string{"namespace", "pod", "vm"} {
					t.Run(tc.name+"/"+workload, func(t *testing.T) {
						peer := &v1alpha1.SecurityPolicyPeer{NamespaceSelector: tc.selector}
						workloadConditions := 0
						switch workload {
						case "pod":
							peer.PodSelector = &v1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}
							workloadConditions = 2 // app label and NodeType
						case "vm":
							peer.VMSelector = &v1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}
							workloadConditions = 2
						}
						group := &model.Group{}
						added, total, err := service.updateNativePeerExpressions(&v1alpha1.SecurityPolicy{}, peer, group)
						require.NoError(t, err)
						require.Equal(t, len(tc.values), added)
						require.Len(t, group.Expression, 2*added-1)
						conditionsPerBranch := 1 + workloadConditions // cluster condition
						if tc.name != "empty" {
							conditionsPerBranch++ // namespace label condition
						}
						assert.Equal(t, added*conditionsPerBranch, total)
						for i, value := range tc.values {
							branch := group.Expression[2*i]
							conditions := []data.DataValue{branch}
							if conditionsPerBranch > 1 {
								expressions, err := branch.List("expressions")
								require.NoError(t, err)
								conditions = expressions.List()
								for j := 1; j < len(conditions); j += 2 {
									assert.Equal(t, service.buildConjOperator("AND"), conditions[j])
								}
							}
							assert.Contains(t, conditions, service.buildNativeNamespaceCondition(clusterScope, "cluster-a", "EQUALS"))
							if tc.name != "empty" {
								assert.Contains(t, conditions, service.buildNativeNamespaceCondition("env", value, "EQUALS"))
							}
							if i > 0 {
								assert.Equal(t, service.buildConjOperator("OR"), group.Expression[2*i-1])
							}
						}
					})
				}
			}
		})
	}
}

func TestUpdateNativePeerExpressionsWorkloadInEmptyValue(t *testing.T) {
	service := &SecurityPolicyService{
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "cluster-a"}},
		},
	}
	for _, tc := range []struct {
		name   string
		values []string
	}{
		{name: "empty value", values: []string{""}},
		{name: "empty and non-empty values", values: []string{"", "prod"}},
	} {
		for _, workload := range []string{"pod", "vm"} {
			t.Run(tc.name+"/"+workload, func(t *testing.T) {
				selector := &v1.LabelSelector{MatchExpressions: []v1.LabelSelectorRequirement{
					{Key: "env", Operator: v1.LabelSelectorOpIn, Values: tc.values},
				}}
				peer := &v1alpha1.SecurityPolicyPeer{NamespaceSelector: &v1.LabelSelector{}}
				if workload == "pod" {
					peer.PodSelector = selector
				} else {
					peer.VMSelector = selector
				}
				group := &model.Group{}
				added, total, err := service.updateNativePeerExpressions(&v1alpha1.SecurityPolicy{}, peer, group)
				require.NoError(t, err)
				require.Equal(t, len(tc.values), added)
				require.Len(t, group.Expression, 2*added-1)
				assert.Equal(t, 3*added, total) // cluster, NodeType, and env in every branch
				for i, value := range tc.values {
					expressions, err := group.Expression[2*i].List("expressions")
					require.NoError(t, err)
					assert.Contains(t, expressions.List(), service.buildNativeCondition(
						common.MemberTypeVirtualMachine, "K8sTag/env|"+value, "EQUALS", "EQUALS",
					))
				}
			})
		}
	}
}

func TestBuildNativeSecurityPolicy(t *testing.T) {
	service := &SecurityPolicyService{
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "k8scl-one",
				},
			},
			NSXClient: &nsx.Client{
				Cluster: &nsx.Cluster{},
			},
		},
	}
	service.setUpStore(common.TagValueScopeSecurityPolicyUID, false)
	patchesNSUID := gomonkey.ApplyMethod(reflect.TypeOf(&service.Service), "GetNamespaceUID", func(_ *common.Service, ns string) types.UID {
		return "ns-uid-123"
	})
	defer patchesNSUID.Reset()

	dirIn := v1alpha1.RuleDirectionIn
	actionAllow := v1alpha1.RuleActionAllow
	sp := &v1alpha1.SecurityPolicy{
		ObjectMeta: v1.ObjectMeta{
			Name:      "native-sp",
			Namespace: "default",
			UID:       "uid-sp-123",
		},
		Spec: v1alpha1.SecurityPolicySpec{
			Priority: 10,
			AppliedTo: []v1alpha1.SecurityPolicyTarget{
				{
					PodSelector: &v1.LabelSelector{
						MatchLabels: map[string]string{"app": "server"},
					},
				},
			},
			Rules: []v1alpha1.SecurityPolicyRule{
				{
					Name:      "rule-allow",
					Direction: &dirIn,
					Action:    &actionAllow,
					Sources: []v1alpha1.SecurityPolicyPeer{
						{
							PodSelector: &v1.LabelSelector{
								MatchLabels: map[string]string{"app": "client"},
							},
						},
					},
				},
			},
		},
	}

	policyModel, groups, shares, err := service.buildNativeSecurityPolicy(sp, common.ResourceTypeSecurityPolicy, nil, false)
	require.NoError(t, err)
	require.NotNil(t, policyModel)
	require.NotNil(t, groups)
	require.NotNil(t, shares)
	assert.Equal(t, "sp_uid-sp-123", *policyModel.Id)
	assert.Len(t, policyModel.Rules, 1)
	assert.GreaterOrEqual(t, len(*groups), 1)

	// Test VPC mode
	service.VPCMode = true
	defer func() { service.VPCMode = false }()
	config.SetMixedModeStateForTest(false, true)
	defer config.SetMixedModeStateForTest(false, false)

	vpcInfo := &common.VPCResourceInfo{
		OrgID:     "default",
		ProjectID: "project-1",
		VPCID:     "vpc-1",
	}
	policyModelVPC, groupsVPC, sharesVPC, err := service.buildNativeSecurityPolicy(sp, common.ResourceTypeSecurityPolicy, vpcInfo, false)
	require.NoError(t, err)
	require.NotNil(t, policyModelVPC)
	require.NotNil(t, groupsVPC)
	require.NotNil(t, sharesVPC)
	require.NotNil(t, (*groupsVPC)[0].Path)
	assert.Equal(t, "/orgs/default/projects/project-1/vpcs/vpc-1/groups/native-sp-scope_r91b0", *(*groupsVPC)[0].Path)

	// Test VPC mode with default project (InfraScopeGroup)
	policyModelVPCDef, groupsVPCDef, sharesVPCDef, err := service.buildNativeSecurityPolicy(sp, common.ResourceTypeSecurityPolicy, vpcInfo, true)
	require.NoError(t, err)
	require.NotNil(t, policyModelVPCDef)
	require.NotNil(t, groupsVPCDef)
	require.NotNil(t, sharesVPCDef)
}
