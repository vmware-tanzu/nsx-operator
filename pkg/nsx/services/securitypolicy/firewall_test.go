/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package securitypolicy

import (
	"reflect"
	"testing"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

var (
	allowAction  = v1alpha1.RuleActionAllow
	allowDrop    = v1alpha1.RuleActionDrop
	directionIn  = v1alpha1.RuleDirectionIn
	directionOut = v1alpha1.RuleDirectionOut

	tagScopeGroupType = common.TagScopeGroupType

	tagScopeCluster              = common.TagScopeCluster
	tagScopeNamespace            = common.TagScopeNamespace
	tagScopeSecurityPolicyCRName = common.TagScopeSecurityPolicyCRName
	tagScopeSecurityPolicyCRUID  = common.TagScopeSecurityPolicyCRUID
	tagScopeRuleID               = common.TagScopeRuleID
	tagScopeSelectorHash         = common.TagScopeSelectorHash
	spName                       = "ns1-spA"
	spGroupName                  = "ns1-spA-scope"
	spID                         = "sp_uidA"
	spID2                        = "sp_uidB"
	spGroupID                    = "sp_uidA_scope"
	seq0                         = int64(0)
	seq1                         = int64(1)
	seq2                         = int64(2)
	ruleNameWithPodSelector00    = "rule-with-pod-selector-0-0"
	ruleNameWithVMSelector00     = "rule-with-VM-selector-0-0"
	ruleNameWithNsSelector00     = "rule-with-ns-selector-0-0"
	ruleNameWithIpBlock00        = "rule-with-ip-block-0-0"
	cidr                         = "192.168.1.1/24"
	ruleID0                      = "sp_uidA_0"
	ruleID1                      = "sp_uidA_1"
	ruleIDPort000                = "sp_uidA_0_0_0"
	ruleIDPort100                = "sp_uidA_1_0_0"
	ruleIDPort200                = "sp_uidA_2_0_0"
	nsxDirectionIn               = "IN"
	nsxActionAllow               = "ALLOW"
	nsxDirectionOut              = "OUT"
	nsxActionDrop                = "DROP"
	cluster                      = "k8scl-one"
	tagValueScope                = "scope"
	tagValueNS                   = "ns1"
	tagValuePolicyCRName         = "spA"
	tagValuePolicyCRUID          = "uidA"
	tagValuePodSelectorHash      = "a42321575d78a6c340c6963c7a82c86c7217f847"
	tagValueRuleSrcHash          = "52ec44a8f417d08f05720333292c24acfb108dab"
	timeStamp                    = int64(1641892699021)

	podSelectorMatchExpression = []metav1.LabelSelectorRequirement{
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
	}

	vmSelectorMatchExpression = []metav1.LabelSelectorRequirement{
		{
			Key:      "k3",
			Operator: metav1.LabelSelectorOpDoesNotExist,
		},
		{
			Key:      "k4",
			Operator: metav1.LabelSelectorOpExists,
		},
	}

	nsSelectorMatchExpression = []metav1.LabelSelectorRequirement{
		{
			Key:      "k5",
			Operator: metav1.LabelSelectorOpDoesNotExist,
		},
		{
			Key:      "k6",
			Operator: metav1.LabelSelectorOpExists,
		},
	}

	service = &SecurityPolicyService{
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "k8scl-one",
				},
			},
		},
	}

	spWithPodSelector = v1alpha1.SecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "spA", UID: "uidA"},
		Spec: v1alpha1.SecurityPolicySpec{
			AppliedTo: []v1alpha1.SecurityPolicyTarget{
				{
					PodSelector: &metav1.LabelSelector{
						MatchLabels:      map[string]string{"pod_selector_1": "pod_value_1"},
						MatchExpressions: podSelectorMatchExpression,
					},
				},
			},
			Rules: []v1alpha1.SecurityPolicyRule{
				{
					Action:    &allowAction,
					Direction: &directionIn,
					Name:      "rule-with-pod-selector",
					AppliedTo: []v1alpha1.SecurityPolicyTarget{
						{
							PodSelector: &metav1.LabelSelector{
								MatchLabels:      map[string]string{"pod_selector_1": "pod_value_1"},
								MatchExpressions: podSelectorMatchExpression,
							},
						},
					},
					Sources: []v1alpha1.SecurityPolicyPeer{
						{
							PodSelector: &metav1.LabelSelector{
								MatchLabels:      map[string]string{"pod_selector_1": "pod_value_1"},
								MatchExpressions: podSelectorMatchExpression,
							},
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels:      map[string]string{"ns1": "spA"},
								MatchExpressions: nsSelectorMatchExpression,
							},
						},
					},
				},
				{
					Action:    &allowAction,
					Direction: &directionIn,
					Name:      "rule-with-ns-selector",
					Ports: []v1alpha1.SecurityPolicyPort{
						{
							Protocol: corev1.ProtocolUDP,
							Port:     intstr.IntOrString{Type: intstr.Int, IntVal: 53},
						},
					},
					Sources: []v1alpha1.SecurityPolicyPeer{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels:      map[string]string{"ns1": "spA"},
								MatchExpressions: nsSelectorMatchExpression,
							},
						},
					},
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
						MatchLabels:      map[string]string{"VM_selector_1": "VM_value_1"},
						MatchExpressions: vmSelectorMatchExpression,
					},
				},
			},
			Rules: []v1alpha1.SecurityPolicyRule{
				{
					Action:    &allowDrop,
					Direction: &directionOut,
					Name:      "rule-with-VM-selector",
					AppliedTo: []v1alpha1.SecurityPolicyTarget{
						{
							VMSelector: &metav1.LabelSelector{
								MatchLabels:      map[string]string{"VM_selector_1": "VM_value_1"},
								MatchExpressions: vmSelectorMatchExpression,
							},
						},
					},
					Destinations: []v1alpha1.SecurityPolicyPeer{
						{
							VMSelector: &metav1.LabelSelector{
								MatchLabels:      map[string]string{"VM_selector_1": "VM_value_1"},
								MatchExpressions: vmSelectorMatchExpression,
							},
						},
					},
				},
				{
					Action:    &allowDrop,
					Direction: &directionOut,
					Name:      "rule-with-ns-selector",
					Destinations: []v1alpha1.SecurityPolicyPeer{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{},
							},
						},
					},
				},
				{
					Action:    &allowDrop,
					Direction: &directionOut,
					Name:      "rule-with-ip-block",
					Destinations: []v1alpha1.SecurityPolicyPeer{
						{
							IPBlocks: []v1alpha1.IPBlock{
								{CIDR: cidr},
							},
						},
					},
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

func TestListSecurityPolicyID(t *testing.T) {
	service := &SecurityPolicyService{
		Service: common.Service{NSXClient: nil},
	}
	service.securityPolicyStore = &SecurityPolicyStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeSecurityPolicyCRUID: indexFunc}),
		BindingType: model.SecurityPolicyBindingType(),
	}}
	service.groupStore = &GroupStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeSecurityPolicyCRUID: indexFunc}),
		BindingType: model.GroupBindingType(),
	}}
	service.ruleStore = &RuleStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeSecurityPolicyCRUID: indexFunc}),
		BindingType: model.RuleBindingType(),
	}}

	group := model.Group{}
	scope := "nsx-op/security_policy_cr_uid"
	uuid := "111111111"
	id := "1234"
	group.Id = &id
	group.UniqueId = &uuid

	group.Tags = []model.Tag{{Scope: &scope, Tag: &id}}
	err := service.groupStore.Add(group)
	if err != nil {
		t.Fatalf("Failed to add group to store: %v", err)
	}

	id1 := "4567"
	uuid1 := "111111112"
	group1 := model.Group{}
	group1.Id = &id1
	group1.UniqueId = &uuid1
	group1.Tags = []model.Tag{{Scope: &scope, Tag: &id1}}
	err = service.groupStore.Add(group1)
	if err != nil {
		t.Fatalf("Failed to add group to store: %v", err)
	}

	policy := model.SecurityPolicy{}
	id2 := "1235"
	policy.Id = &id2
	policy.UniqueId = &uuid
	policy.Tags = []model.Tag{{Scope: &scope, Tag: &id2}}
	err = service.securityPolicyStore.Add(policy)
	if err != nil {
		t.Fatalf("Failed to add policy to store: %v", err)
	}

	tests := []struct {
		name    string
		want    sets.String
		wantErr bool
	}{
		{
			name:    "test",
			wantErr: false,
		},
	}

	tests[0].want = sets.NewString()
	tests[0].want.Insert(id)
	tests[0].want.Insert(id1)
	tests[0].want.Insert(id2)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := service.ListSecurityPolicyID()
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SecurityPolicyService.ListSecurityPolicyID() = %v, want %v", got, tt.want)
			}
		})
	}
}
