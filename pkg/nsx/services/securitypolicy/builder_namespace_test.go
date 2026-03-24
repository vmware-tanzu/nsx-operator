/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package securitypolicy

import (
	"reflect"
	"testing"

	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
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
	assert.Greater(t, condCount, 0)
	assert.NotEmpty(t, grpValid.Expression)
}

func TestUpdateNativePeerExpressions(t *testing.T) {
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
	_, _, err = service.updateNativePeerExpressions(sp, peerIP, grpIP)
	require.NoError(t, err)
	assert.NotEmpty(t, grpIP.Expression)

	// NamespaceSelector + PodSelector
	peerCombined := &v1alpha1.SecurityPolicyPeer{
		NamespaceSelector: &v1.LabelSelector{MatchLabels: map[string]string{"ns-label": "value"}},
		PodSelector:       &v1.LabelSelector{MatchLabels: map[string]string{"app": "client"}},
	}
	grpCombined := &model.Group{}
	added, condCount, err := service.updateNativePeerExpressions(sp, peerCombined, grpCombined)
	require.NoError(t, err)
	assert.Equal(t, 1, added)
	assert.Greater(t, condCount, 0)
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

	policyModel, groups, shares, err := service.buildNativeSecurityPolicy(sp, common.ResourceTypeSecurityPolicy, nil)
	require.NoError(t, err)
	require.NotNil(t, policyModel)
	require.NotNil(t, groups)
	require.NotNil(t, shares)
	assert.Equal(t, "sp_uid-sp-123", *policyModel.Id)
	assert.Len(t, policyModel.Rules, 1)
	assert.GreaterOrEqual(t, len(*groups), 1)

	// Test VPC mode
	config.SetMixedModeStateForTest(false, true)
	defer config.SetMixedModeStateForTest(false, false)

	vpcInfo := &common.VPCResourceInfo{
		OrgID:     "default",
		ProjectID: "project-1",
		VPCID:     "vpc-1",
	}
	policyModelVPC, groupsVPC, sharesVPC, err := service.buildNativeSecurityPolicy(sp, common.ResourceTypeSecurityPolicy, vpcInfo)
	require.NoError(t, err)
	require.NotNil(t, policyModelVPC)
	require.NotNil(t, groupsVPC)
	require.NotNil(t, sharesVPC)
	require.NotNil(t, (*groupsVPC)[0].Path)
	assert.Equal(t, "/orgs/default/projects/project-1/vpcs/vpc-1/groups/native-sp-scope_r91b0", *(*groupsVPC)[0].Path)
}
