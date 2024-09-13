/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package securitypolicy

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

var (
	allowAction  = v1alpha1.RuleActionAllow
	allowDrop    = v1alpha1.RuleActionDrop
	directionIn  = v1alpha1.RuleDirectionIn
	directionOut = v1alpha1.RuleDirectionOut

	tagScopeVersion              = common.TagScopeVersion
	tagScopeGroupType            = common.TagScopeGroupType
	tagScopeCluster              = common.TagScopeCluster
	tagScopeNamespace            = common.TagScopeNamespace
	tagScopeNamespaceUID         = common.TagScopeNamespaceUID
	tagScopeSecurityPolicyCRName = common.TagValueScopeSecurityPolicyName
	tagScopeSecurityPolicyCRUID  = common.TagValueScopeSecurityPolicyUID
	tagScopeSecurityPolicyName   = common.TagScopeSecurityPolicyName
	tagScopeSecurityPolicyUID    = common.TagScopeSecurityPolicyUID
	tagScopeRuleID               = common.TagScopeRuleID
	tagScopeSelectorHash         = common.TagScopeSelectorHash
	spName                       = "sp_ns1_spA"
	spGroupName                  = "ns1_spA_scope"
	spID                         = "sp_uidA"
	spID2                        = "sp_uidB"
	spGroupID                    = "sp_uidA_scope"
	seq0                         = int64(0)
	seq1                         = int64(1)
	seq2                         = int64(2)
	ruleNameWithPodSelector00    = "rule-with-pod-selector"
	ruleNameWithNsSelector00     = "rule-with-ns-selector"
	cidr                         = "192.168.1.1/24"
	ruleID0                      = "sp_uidA_0"
	ruleID1                      = "sp_uidA_1"
	ruleIDPort000                = "sp_uidA_0_0_0"
	ruleIDPort100                = "sp_uidA_1_0_0"
	nsxRuleDirectionIn           = "IN"
	nsxRuleActionAllow           = "ALLOW"
	nsxRuleDirectionOut          = "OUT"
	nsxRuleActionDrop            = "DROP"
	clusterName                  = "k8scl-one"
	tagValueVersion              = strings.Join(common.TagValueVersion, ".")
	tagValueGroupScope           = common.TagValueGroupScope
	tagValueGroupSource          = common.TagValueGroupSource
	tagValueNS                   = "ns1"
	tagValueNSUID                = "us1UID"
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
					Name:      "rule-with-pod-ns-selector",
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
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "spB", UID: "uidB"},
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

	basicTagsForSpWithVMSelector = []model.Tag{
		{
			Scope: &tagScopeCluster,
			Tag:   &clusterName,
		},
		{
			Scope: &tagScopeVersion,
			Tag:   &tagValueVersion,
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
			Tag:   String(spWithVMSelector.Name),
		},
		{
			Scope: &tagScopeSecurityPolicyCRUID,
			Tag:   String(string(spWithVMSelector.UID)),
		},
	}

	basicTags = []model.Tag{
		{
			Scope: &tagScopeCluster,
			Tag:   &clusterName,
		},
		{
			Scope: &tagScopeVersion,
			Tag:   &tagValueVersion,
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
	}

	vpcBasicTags = []model.Tag{
		{
			Scope: &tagScopeCluster,
			Tag:   &clusterName,
		},
		{
			Scope: &tagScopeVersion,
			Tag:   &tagValueVersion,
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
			Scope: &tagScopeSecurityPolicyName,
			Tag:   &tagValuePolicyCRName,
		},
		{
			Scope: &tagScopeSecurityPolicyUID,
			Tag:   &tagValuePolicyCRUID,
		},
	}

	vpcBasicTagsForSpWithVMSelector = []model.Tag{
		{
			Scope: &tagScopeCluster,
			Tag:   &clusterName,
		},
		{
			Scope: &tagScopeVersion,
			Tag:   &tagValueVersion,
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
			Scope: &tagScopeSecurityPolicyName,
			Tag:   String(spWithVMSelector.Name),
		},
		{
			Scope: &tagScopeSecurityPolicyUID,
			Tag:   String(string(spWithVMSelector.UID)),
		},
	}

	// Create the NetworkPolicy object
	npWithNsSelecotr = networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "np-app-access",
			Namespace: "ns1",
			UID:       "uidNP",
		},

		Spec: networkingv1.NetworkPolicySpec{
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
			},
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"role": "db",
				},
			},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					From: []networkingv1.NetworkPolicyPeer{
						{
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"app": "coffee",
								},
							},
						},
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"ns-name": "ns-3",
								},
							},
						},
					},
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Protocol: func() *corev1.Protocol {
								proto := corev1.ProtocolTCP
								return &proto
							}(),
							Port: &intstr.IntOrString{
								IntVal: 6001,
							},
						},
					},
				},
			},
		},
	}

	npAllowBasicTags = []model.Tag{
		{
			Scope: &tagScopeCluster,
			Tag:   &clusterName,
		},
		{
			Scope: &tagScopeVersion,
			Tag:   &tagValueVersion,
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
			Scope: common.String(common.TagScopeNetworkPolicyName),
			Tag:   String(npWithNsSelecotr.Name),
		},
		{
			Scope: common.String(common.TagScopeNetworkPolicyUID),
			Tag:   String(string(npWithNsSelecotr.UID + "_allow")),
		},
	}

	npIsolationBasicTags = []model.Tag{
		{
			Scope: &tagScopeCluster,
			Tag:   &clusterName,
		},
		{
			Scope: &tagScopeVersion,
			Tag:   &tagValueVersion,
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
			Scope: common.String(common.TagScopeNetworkPolicyName),
			Tag:   String(npWithNsSelecotr.Name),
		},
		{
			Scope: common.String(common.TagScopeNetworkPolicyUID),
			Tag:   String(string(npWithNsSelecotr.UID + "_isolation")),
		},
	}
)

func TestListSecurityPolicyID(t *testing.T) {
	service := &SecurityPolicyService{
		Service: common.Service{NSXClient: nil},
	}
	service.setUpStore(common.TagValueScopeSecurityPolicyUID)

	group := model.Group{}
	scope := "nsx-op/security_policy_cr_uid"
	uuid := "111111111"
	id := "1234"
	group.Id = &id
	group.UniqueId = &uuid

	group.Tags = []model.Tag{{Scope: &scope, Tag: &id}}
	err := service.groupStore.Add(&group)
	if err != nil {
		t.Fatalf("Failed to add group to store: %v", err)
	}

	id1 := "4567"
	uuid1 := "111111112"
	group1 := model.Group{}
	group1.Id = &id1
	group1.UniqueId = &uuid1
	group1.Tags = []model.Tag{{Scope: &scope, Tag: &id1}}
	err = service.groupStore.Add(&group1)
	if err != nil {
		t.Fatalf("Failed to add group to store: %v", err)
	}

	policy := model.SecurityPolicy{}
	id2 := "1235"
	policy.Id = &id2
	policy.UniqueId = &uuid
	policy.Tags = []model.Tag{{Scope: &scope, Tag: &id2}}
	err = service.securityPolicyStore.Add(&policy)
	if err != nil {
		t.Fatalf("Failed to add policy to store: %v", err)
	}

	id3 := "projectShareId"
	uuid3 := "projectShareIdUID"
	share := model.Share{}
	share.Id = &id3
	share.UniqueId = &uuid3
	share.Tags = []model.Tag{{Scope: &scope, Tag: &id3}}
	err = service.projectShareStore.Add(&share)
	if err != nil {
		t.Fatalf("Failed to add share to store: %v", err)
	}

	id4 := "infraShareId"
	uuid4 := "infraShareIdUID"
	share1 := model.Share{}
	share1.Id = &id4
	share1.UniqueId = &uuid4
	share1.Tags = []model.Tag{{Scope: &scope, Tag: &id4}}
	err = service.infraShareStore.Add(&share1)
	if err != nil {
		t.Fatalf("Failed to add share to store: %v", err)
	}

	tests := []struct {
		name    string
		want    sets.Set[string]
		wantErr bool
	}{
		{
			name:    "test",
			wantErr: false,
		},
	}

	tests[0].want = sets.New[string]()
	tests[0].want.Insert(id)
	tests[0].want.Insert(id1)
	tests[0].want.Insert(id2)
	tests[0].want.Insert(id3)
	tests[0].want.Insert(id4)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := service.ListSecurityPolicyID()
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SecurityPolicyService.ListSecurityPolicyID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetUpdateRules(t *testing.T) {
	r1 := model.Rule{
		DisplayName:       String("nsxrule1"),
		Id:                String("nsxrule_1"),
		DestinationGroups: []string{"ANY"},
		Direction:         &nsxRuleDirectionIn,
		Scope:             []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_scope"},
		SequenceNumber:    &seq0,
		Services:          []string{"ANY"},
		SourceGroups:      []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_src"},
		Action:            &nsxRuleActionAllow,
	}

	tests := []struct {
		name          string
		existingRules []*model.Rule
		expectedRules []model.Rule
		finalRulesLen int
	}{
		{
			name: "test-rule-nochange",
			existingRules: []*model.Rule{
				&r1,
			},
			expectedRules: []model.Rule{
				{
					DisplayName:       String("nsxrule1"),
					Id:                String("nsxrule_1"),
					DestinationGroups: []string{"ANY"},
					Direction:         &nsxRuleDirectionIn,
					Scope:             []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_scope"},
					SequenceNumber:    &seq0,
					Services:          []string{"ANY"},
					SourceGroups:      []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_src"},
					Action:            &nsxRuleActionAllow,
				},
			},
			finalRulesLen: 0,
		},
		{
			name: "test-rule-change",
			existingRules: []*model.Rule{
				&r1,
			},
			expectedRules: []model.Rule{
				{
					DisplayName:       String("nsxrule1"),
					Id:                String("nsxrule_1"),
					DestinationGroups: []string{"ANY"},
					Direction:         &nsxRuleDirectionIn,
					Scope:             []string{"/infra/domains/k8scl-one/groups/sp_uidA_1_scope"},
					SequenceNumber:    &seq0,
					Services:          []string{"ANY"},
					SourceGroups:      []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_src"},
					Action:            &nsxRuleActionAllow,
				},
			},
			finalRulesLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			finalRules := service.getUpdateRules(tt.existingRules, tt.expectedRules)
			assert.Equal(t, tt.finalRulesLen, len(finalRules))
		})
	}
}

func TestGetUpdateGroups(t *testing.T) {
	mId, mTag, mTag2, mScope := "11111", "11111", "22222", "nsx-op/security_policy_cr_uid"
	markDelete := true

	g1 := model.Group{
		Id:              &mId,
		Tags:            []model.Tag{{Tag: &mTag, Scope: &mScope}},
		MarkedForDelete: &markDelete,
	}

	tests := []struct {
		name           string
		existingGroups []*model.Group
		expectedGroups []model.Group
		finalGroupsLen int
	}{
		{
			name: "test-group-nochange",
			existingGroups: []*model.Group{
				&g1,
			},
			expectedGroups: []model.Group{
				{
					Id:              &mId,
					Tags:            []model.Tag{{Tag: &mTag, Scope: &mScope}},
					MarkedForDelete: &markDelete,
				},
			},
			finalGroupsLen: 0,
		},
		{
			name: "test-group-change",
			existingGroups: []*model.Group{
				&g1,
			},
			expectedGroups: []model.Group{
				{
					Id:              String("nsxgroup"),
					Tags:            []model.Tag{{Tag: &mTag, Scope: &mScope}},
					MarkedForDelete: &markDelete,
				},
			},
			finalGroupsLen: 2,
		},
		{
			name: "test-group-change1",
			existingGroups: []*model.Group{
				&g1,
			},
			expectedGroups: []model.Group{
				{
					Id:              &mId,
					Tags:            []model.Tag{{Tag: &mTag2, Scope: &mScope}},
					MarkedForDelete: &markDelete,
				},
			},
			finalGroupsLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			finalGroups := service.getUpdateGroups(tt.existingGroups, tt.expectedGroups)
			assert.Equal(t, tt.finalGroupsLen, len(finalGroups))
		})
	}
}

func TestGetUpdateShares(t *testing.T) {
	mId, mTag, mScope := "11111", "11111", "nsx-op/security_policy_cr_uid"

	s1 := model.Share{
		Id:         &mId,
		Tags:       []model.Tag{{Tag: &mTag, Scope: &mScope}},
		SharedWith: []string{"/org/default/project/default/vpc/vpc1"},
	}

	tests := []struct {
		name           string
		existingShares []*model.Share
		expectedShares []model.Share
		finalSharesLen int
	}{
		{
			name: "test-share-nochange",
			existingShares: []*model.Share{
				&s1,
			},
			expectedShares: []model.Share{
				{
					Id:         &mId,
					Tags:       []model.Tag{{Tag: &mTag, Scope: &mScope}},
					SharedWith: []string{"/org/default/project/default/vpc/vpc1"},
				},
			},
			finalSharesLen: 0,
		},
		{
			name: "test-share-change",
			existingShares: []*model.Share{
				&s1,
			},
			expectedShares: []model.Share{
				{
					Id:         String("nsxshare"),
					Tags:       []model.Tag{{Tag: &mTag, Scope: &mScope}},
					SharedWith: []string{"/org/default/project/default/vpc/vpc1"},
				},
			},
			finalSharesLen: 2,
		},
		{
			name: "test-sahre-change1",
			existingShares: []*model.Share{
				&s1,
			},
			expectedShares: []model.Share{
				{
					Id:         &mId,
					Tags:       []model.Tag{{Tag: &mTag, Scope: &mScope}},
					SharedWith: []string{"/org/default/project/default/vpc/vpc2"},
				},
			},
			finalSharesLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			finalShares := service.getUpdateShares(tt.existingShares, tt.expectedShares)
			assert.Equal(t, tt.finalSharesLen, len(finalShares))
		})
	}
}

func TestListMarkDeleteRules(t *testing.T) {
	var sp types.UID
	sp = "sp_test"
	markNoDelete := false

	r := make([]model.Rule, 0)
	r1 := model.Rule{
		DisplayName:       String("nsxrule1"),
		Id:                String("nsxrule_1"),
		DestinationGroups: []string{"ANY"},
		Direction:         &nsxRuleDirectionIn,
		Scope:             []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_scope"},
		SequenceNumber:    &seq0,
		Services:          []string{"ANY"},
		SourceGroups:      []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_src"},
		Action:            &nsxRuleActionAllow,
		MarkedForDelete:   &markNoDelete,
	}

	tests := []struct {
		name          string
		existingRules []*model.Rule
		deleteRules   *[]model.Rule
	}{
		{
			name: "test-rule-delete",
			existingRules: []*model.Rule{
				&r1,
			},
			deleteRules: &r,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service.markDeleteRules(tt.existingRules, tt.deleteRules, sp)
			for i := len(*tt.deleteRules) - 1; i >= 0; i-- {
				assert.Equal(t, MarkedForDelete, *((*tt.deleteRules)[i].MarkedForDelete))
			}
		})
	}
}

func TestListMarkDeleteGroups(t *testing.T) {
	var sp types.UID
	sp = "sp_test"
	mId, mTag, mScope := "11111", "11111", "nsx-op/security_policy_cr_uid"
	markNoDelete := false

	g := make([]model.Group, 0)
	g1 := model.Group{
		Id:              &mId,
		Tags:            []model.Tag{{Tag: &mTag, Scope: &mScope}},
		MarkedForDelete: &markNoDelete,
	}

	tests := []struct {
		name           string
		existingGroups []*model.Group
		deleteGroups   *[]model.Group
	}{
		{
			name: "test-group-delete",
			existingGroups: []*model.Group{
				&g1,
			},
			deleteGroups: &g,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service.markDeleteGroups(tt.existingGroups, tt.deleteGroups, sp)
			for i := len(*tt.deleteGroups) - 1; i >= 0; i-- {
				assert.Equal(t, MarkedForDelete, *((*tt.deleteGroups)[i].MarkedForDelete))
			}
		})
	}
}

func TestListMarkDeleteShares(t *testing.T) {
	var sp types.UID
	sp = "sp_test"
	mId, mTag, mScope := "11111", "11111", "nsx-op/security_policy_cr_uid"
	markNoDelete := false

	s := make([]model.Share, 0)
	s1 := model.Share{
		Id:              &mId,
		Tags:            []model.Tag{{Tag: &mTag, Scope: &mScope}},
		SharedWith:      []string{"/org/default/project/default/vpc/vpc1"},
		MarkedForDelete: &markNoDelete,
	}

	tests := []struct {
		name           string
		existingShares []*model.Share
		deleteShares   *[]model.Share
	}{
		{
			name: "test-share-nochange",
			existingShares: []*model.Share{
				&s1,
			},
			deleteShares: &s,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service.markDeleteShares(tt.existingShares, tt.deleteShares, sp)
			for i := len(*tt.deleteShares) - 1; i >= 0; i-- {
				assert.Equal(t, MarkedForDelete, *((*tt.deleteShares)[i].MarkedForDelete))
			}
		})
	}
}

func TestDleleteVPCSecurityPolicy(t *testing.T) {
	vpcPath := "/orgs/default/projects/projectQuality/vpcs/vpc1"

	type args struct {
		uid        types.UID
		createdFor string
	}
	tests := []struct {
		name                         string
		prepareFunc                  func(*testing.T, *SecurityPolicyService) *gomonkey.Patches
		args                         args
		inputPolicy                  *model.SecurityPolicy
		wantErr                      bool
		wantSecurityPolicyStoreCount int
		wantRuleStoreCount           int
		wantGroupStoreCount          int
		wantProjectGroupStoreCount   int
		wantProjectShareStoreCount   int
	}{
		{
			name: "successDeleteVPCSecurityPolicy",
			prepareFunc: func(t *testing.T, s *SecurityPolicyService) *gomonkey.Patches {
				mGId := "sp_uidA_0_scope"
				mTag, mScope := tagValuePolicyCRUID, tagScopeSecurityPolicyCRUID
				g := make([]model.Group, 0)
				g1 := &g
				scopeGroup := model.Group{
					Id:   &mGId,
					Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
				}
				*g1 = append(*g1, scopeGroup)
				assert.NoError(t, s.groupStore.Apply(g1))

				mProjGId := "sp_uidA_1_src"
				g = make([]model.Group, 0)
				g2 := &g
				projectGroup := model.Group{
					Id:   &mProjGId,
					Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
				}
				*g2 = append(*g2, projectGroup)
				assert.NoError(t, s.projectGroupStore.Apply(g2))

				mSId := "share-projectQuality-group-sp_uidA_1_src"
				sh := make([]model.Share, 0)
				s1 := &sh
				projectShare := model.Share{
					Id:         &mSId,
					Tags:       []model.Tag{{Tag: &mTag, Scope: &mScope}},
					SharedWith: []string{"/org/default/project/projectQuality/vpcs/vpc1"},
				}
				*s1 = append(*s1, projectShare)
				assert.NoError(t, s.projectShareStore.Apply(s1))

				patches := gomonkey.ApplyMethodSeq(s.NSXClient.OrgRootClient, "Patch", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				return patches
			},
			args: args{
				createdFor: common.ResourceTypeSecurityPolicy,
				uid:        types.UID(tagValuePolicyCRUID),
			},
			inputPolicy: &model.SecurityPolicy{
				DisplayName:    &spName,
				Id:             &tagValuePolicyCRUID,
				Scope:          []string{"/orgs/default/projects/projectQuality/vpcs/vpc1/groups/sp_uidA_scope"},
				SequenceNumber: &seq0,
				Rules: []model.Rule{
					{
						DisplayName:       &ruleNameWithPodSelector00,
						Id:                &ruleID0,
						DestinationGroups: []string{"ANY"},
						Direction:         &nsxRuleDirectionIn,
						Scope:             []string{"ANY"},
						SequenceNumber:    &seq0,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"ANY"},
						Action:            &nsxRuleActionAllow,
						Tags:              basicTags,
					},
					{
						DisplayName:       &ruleNameWithNsSelector00,
						Id:                &ruleID1,
						DestinationGroups: []string{"ANY"},
						Direction:         &nsxRuleDirectionIn,
						Scope:             []string{"ANY"},
						SequenceNumber:    &seq1,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"/orgs/default/projects/projectQuality/infra/domains/default/groups/sp_uidA_1_src"},
						Action:            &nsxRuleActionAllow,
						Tags:              basicTags,
					},
				},
				Tags: basicTags,
				Path: &vpcPath,
			},
			wantErr:                      false,
			wantSecurityPolicyStoreCount: 0,
			wantRuleStoreCount:           0,
			wantGroupStoreCount:          0,
			wantProjectGroupStoreCount:   0,
			wantProjectShareStoreCount:   0,
		},
		{
			name: "errorDeleteVPCSecurityPolicy",
			prepareFunc: func(t *testing.T, s *SecurityPolicyService) *gomonkey.Patches {
				mGId := "sp_uidA_0_scope"
				mTag, mScope := tagValuePolicyCRUID, tagScopeSecurityPolicyCRUID
				g := make([]model.Group, 0)
				g1 := &g
				scopeGroup := model.Group{
					Id:   &mGId,
					Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
				}
				*g1 = append(*g1, scopeGroup)
				assert.NoError(t, s.groupStore.Apply(g1))

				patches := gomonkey.ApplyMethodSeq(s.NSXClient.OrgRootClient, "Patch", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("mock error")},
					Times:  1,
				}})
				return patches
			},
			args: args{
				createdFor: common.ResourceTypeSecurityPolicy,
				uid:        types.UID(tagValuePolicyCRUID),
			},
			inputPolicy: &model.SecurityPolicy{
				DisplayName:    &spName,
				Id:             &tagValuePolicyCRUID,
				Scope:          []string{"/orgs/default/projects/default/vpcs/vpc1/groups/sp_uidA_scope"},
				SequenceNumber: &seq0,
				Rules: []model.Rule{
					{
						DisplayName:       &ruleNameWithPodSelector00,
						Id:                &ruleID0,
						DestinationGroups: []string{"ANY"},
						Direction:         &nsxRuleDirectionIn,
						Scope:             []string{"ANY"},
						SequenceNumber:    &seq0,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"ANY"},
						Action:            &nsxRuleActionAllow,
						Tags:              basicTags,
					},
				},
				Tags: basicTags,
				Path: &vpcPath,
			},
			wantErr:                      true,
			wantSecurityPolicyStoreCount: 1,
			wantRuleStoreCount:           1,
			wantGroupStoreCount:          1,
			wantProjectGroupStoreCount:   0,
			wantProjectShareStoreCount:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeService := fakeSecurityPolicyService()
			fakeService.setUpStore(common.TagValueScopeSecurityPolicyUID)

			assert.NoError(t, fakeService.securityPolicyStore.Apply(tt.inputPolicy))
			assert.NoError(t, fakeService.ruleStore.Apply(&tt.inputPolicy.Rules))

			patches := tt.prepareFunc(t, fakeService)
			defer patches.Reset()

			if err := fakeService.deleteVPCSecurityPolicy(tt.args.uid, false, tt.args.createdFor); (err != nil) != tt.wantErr {
				t.Errorf("deleteVPCSecurityPolicy error = %v, wantErr %v", err, tt.wantErr)
			}
			assert.Equal(t, tt.wantSecurityPolicyStoreCount, len(fakeService.securityPolicyStore.ListKeys()))
			assert.Equal(t, tt.wantRuleStoreCount, len(fakeService.ruleStore.ListKeys()))
			assert.Equal(t, tt.wantGroupStoreCount, len(fakeService.groupStore.ListKeys()))
			assert.Equal(t, tt.wantProjectGroupStoreCount, len(fakeService.projectGroupStore.ListKeys()))
			assert.Equal(t, tt.wantProjectShareStoreCount, len(fakeService.projectShareStore.ListKeys()))
		})
	}
}

func TestCreateOrUpdateSecurityPolicy(t *testing.T) {
	VPCInfo := make([]common.VPCResourceInfo, 1)
	VPCInfo[0].OrgID = "default"
	VPCInfo[0].ProjectID = "projectQuality"
	VPCInfo[0].VPCID = "vpc1"

	fakeService := fakeSecurityPolicyService()
	fakeService.NSXConfig.EnableVPCNetwork = true
	mockVPCService := common.MockVPCServiceProvider{}
	fakeService.vpcService = &mockVPCService

	podSelectorRule0IDPort000 := fakeService.buildExpandedRuleId(fakeService.buildRuleID(&spWithPodSelector, &spWithPodSelector.Spec.Rules[0], 0, common.ResourceTypeSecurityPolicy), 0, 0)
	podSelectorRule1IDPort000 := fakeService.buildExpandedRuleId(fakeService.buildRuleID(&spWithPodSelector, &spWithPodSelector.Spec.Rules[1], 1, common.ResourceTypeSecurityPolicy), 0, 0)

	podSelectorRule0Name00, _ := fakeService.buildRuleDisplayName(&spWithPodSelector.Spec.Rules[0], 0, -1, false, common.ResourceTypeSecurityPolicy)
	podSelectorRule1Name00, _ := fakeService.buildRuleDisplayName(&spWithPodSelector.Spec.Rules[1], 0, -1, false, common.ResourceTypeSecurityPolicy)

	type args struct {
		spObj      *v1alpha1.SecurityPolicy
		createdFor string
	}
	tests := []struct {
		name                         string
		prepareFunc                  func(*testing.T, *SecurityPolicyService) *gomonkey.Patches
		args                         args
		expectedPolicy               *model.SecurityPolicy
		wantErr                      bool
		wantSecurityPolicyStoreCount int
		wantRuleStoreCount           int
		wantGroupStoreCount          int
		wantProjectGroupStoreCount   int
		wantProjectShareStoreCount   int
	}{
		{
			name: "successCreateUpdateVPCSecurityPolicy",
			prepareFunc: func(t *testing.T, s *SecurityPolicyService) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(s), "getVPCInfo",
					func(s *SecurityPolicyService, spNameSpace string) (*common.VPCResourceInfo, error) {
						return &VPCInfo[0], nil
					})

				patches.ApplyMethodSeq(s.NSXClient.OrgRootClient, "Patch", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})

				patches.ApplyPrivateMethod(reflect.TypeOf(s), "getNamespaceUID",
					func(s *SecurityPolicyService, ns string) types.UID {
						return types.UID(tagValueNSUID)
					})

				return patches
			},
			args: args{
				createdFor: common.ResourceTypeSecurityPolicy,
				spObj:      &spWithPodSelector,
			},
			expectedPolicy: &model.SecurityPolicy{
				DisplayName:    &spName,
				Id:             &spID,
				SequenceNumber: &seq0,
				Rules: []model.Rule{
					{
						DisplayName:       &podSelectorRule0Name00,
						Id:                &podSelectorRule0IDPort000,
						DestinationGroups: []string{"ANY"},
						Direction:         &nsxRuleDirectionIn,
						SequenceNumber:    &seq0,
						Services:          []string{"ANY"},
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
						Action:            &nsxRuleActionAllow,
						Tags:              basicTags,
					},
				},
				Tags: basicTags,
			},
			wantErr:                      false,
			wantSecurityPolicyStoreCount: 1,
			wantRuleStoreCount:           2,
			wantGroupStoreCount:          2,
			wantProjectGroupStoreCount:   2,
			wantProjectShareStoreCount:   2,
		},
		{
			name: "errorCreateUpdateVPCSecurityPolicy",
			prepareFunc: func(t *testing.T, s *SecurityPolicyService) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(s), "getVPCInfo",
					func(s *SecurityPolicyService, spNameSpace string) (*common.VPCResourceInfo, error) {
						return &VPCInfo[0], nil
					})

				patches.ApplyMethodSeq(s.NSXClient.OrgRootClient, "Patch", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("mock error")},
					Times:  1,
				}})

				patches.ApplyPrivateMethod(reflect.TypeOf(s), "getNamespaceUID",
					func(s *SecurityPolicyService, ns string) types.UID {
						return types.UID(tagValueNSUID)
					})

				return patches
			},
			args: args{
				createdFor: common.ResourceTypeSecurityPolicy,
				spObj:      &spWithPodSelector,
			},
			expectedPolicy:               &model.SecurityPolicy{},
			wantErr:                      true,
			wantSecurityPolicyStoreCount: 0,
			wantRuleStoreCount:           0,
			wantGroupStoreCount:          0,
			wantProjectGroupStoreCount:   0,
			wantProjectShareStoreCount:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeService.setUpStore(common.TagValueScopeSecurityPolicyUID)

			patches := tt.prepareFunc(t, fakeService)
			patches.ApplyMethodSeq(fakeService.NSXClient.VPCSecurityClient, "Get", []gomonkey.OutputCell{{
				Values: gomonkey.Params{*(tt.expectedPolicy), nil},
				Times:  1,
			}})
			defer patches.Reset()

			if err := fakeService.createOrUpdateVPCSecurityPolicy(tt.args.spObj, tt.args.createdFor); (err != nil) != tt.wantErr {
				t.Errorf("createOrUpdateVPCSecurityPolicy error = %v, wantErr %v", err, tt.wantErr)
			}

			assert.Equal(t, tt.wantSecurityPolicyStoreCount, len(fakeService.securityPolicyStore.ListKeys()))
			assert.Equal(t, tt.wantRuleStoreCount, len(fakeService.ruleStore.ListKeys()))
			assert.Equal(t, tt.wantGroupStoreCount, len(fakeService.groupStore.ListKeys()))
			assert.Equal(t, tt.wantProjectGroupStoreCount, len(fakeService.projectGroupStore.ListKeys()))
			assert.Equal(t, tt.wantProjectShareStoreCount, len(fakeService.projectShareStore.ListKeys()))
		})
	}
}

func TestGetFinalSecurityPolicyResouce(t *testing.T) {
	VPCInfo := make([]common.VPCResourceInfo, 1)
	VPCInfo[0].OrgID = "default"
	VPCInfo[0].ProjectID = "projectQuality"
	VPCInfo[0].VPCID = "vpc1"

	fakeService := fakeSecurityPolicyService()
	mockVPCService := common.MockVPCServiceProvider{}
	fakeService.vpcService = &mockVPCService

	type args struct {
		spObj      *v1alpha1.SecurityPolicy
		createdFor string
	}
	tests := []struct {
		name                      string
		prepareFunc               func(*testing.T, *SecurityPolicyService) *gomonkey.Patches
		args                      args
		expectedPolicy            *model.SecurityPolicy
		wantErr                   bool
		wantSecurityPolicyChanged bool
		wantRuleStoreCount        int
		wantGroupStoreCount       int
		wantShareStoreCount       int
		wantShareGroupStoreCount  int
	}{
		{
			name: "getFinalSecurityPolicyResouceForVPCMode",
			prepareFunc: func(t *testing.T, s *SecurityPolicyService) *gomonkey.Patches {
				s.NSXConfig.EnableVPCNetwork = true

				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(s), "getVPCInfo",
					func(s *SecurityPolicyService, spNameSpace string) (*common.VPCResourceInfo, error) {
						return &VPCInfo[0], nil
					})

				patches.ApplyPrivateMethod(reflect.TypeOf(s), "getNamespaceUID",
					func(s *SecurityPolicyService, ns string) types.UID {
						return types.UID(tagValueNSUID)
					})

				return patches
			},
			args: args{
				createdFor: common.ResourceTypeSecurityPolicy,
				spObj:      &spWithPodSelector,
			},
			expectedPolicy: &model.SecurityPolicy{
				DisplayName:    common.String("spA"),
				Id:             common.String("spA_uidA"),
				Scope:          []string{"/orgs/default/projects/projectQuality/vpcs/vpc1/groups/spA_uidA_scope"},
				SequenceNumber: &seq0,
				Rules:          []model.Rule{},
				Tags:           basicTags,
			},
			wantErr:                   false,
			wantSecurityPolicyChanged: true,
			wantRuleStoreCount:        2,
			wantGroupStoreCount:       2,
			wantShareStoreCount:       2,
			wantShareGroupStoreCount:  2,
		},
		{
			name: "getFinalSecurityPolicyResouceForT1",
			prepareFunc: func(t *testing.T, s *SecurityPolicyService) *gomonkey.Patches {
				s.NSXConfig.EnableVPCNetwork = false

				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(s), "getVPCInfo",
					func(s *SecurityPolicyService, spNameSpace string) (*common.VPCResourceInfo, error) {
						return &VPCInfo[0], nil
					})

				patches.ApplyPrivateMethod(reflect.TypeOf(s), "getNamespaceUID",
					func(s *SecurityPolicyService, ns string) types.UID {
						return types.UID(tagValueNSUID)
					})

				return patches
			},
			args: args{
				createdFor: common.ResourceTypeSecurityPolicy,
				spObj:      &spWithPodSelector,
			},
			expectedPolicy: &model.SecurityPolicy{
				DisplayName:    &spName,
				Id:             &spID,
				Scope:          []string{"/infra/domains/k8scl-one/groups/sp_uidA_scope"},
				SequenceNumber: &seq0,
				Rules:          []model.Rule{},
				Tags:           basicTags,
			},
			wantErr:                   false,
			wantSecurityPolicyChanged: true,
			wantRuleStoreCount:        2,
			wantGroupStoreCount:       4,
			wantShareStoreCount:       0,
			wantShareGroupStoreCount:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeService.setUpStore(common.TagValueScopeSecurityPolicyUID)

			patches := tt.prepareFunc(t, fakeService)
			defer patches.Reset()

			var finalSecurityPolicy *model.SecurityPolicy
			var finalGroups []model.Group
			var finalShares []model.Share
			var finalShareGroups []model.Group
			var isChanged bool
			var err error

			if finalSecurityPolicy, finalGroups, finalShares, finalShareGroups, isChanged, err = fakeService.getFinalSecurityPolicyResource(tt.args.spObj, tt.args.createdFor, false); (err != nil) != tt.wantErr {
				t.Errorf("getFinalSecurityPolicyResouce error = %v, wantErr %v", err, tt.wantErr)
			}

			assert.Equal(t, *tt.expectedPolicy.Id, *finalSecurityPolicy.Id)
			assert.Equal(t, tt.expectedPolicy.Scope[0], finalSecurityPolicy.Scope[0])
			assert.Equal(t, true, isChanged)
			assert.Equal(t, tt.wantGroupStoreCount, len(finalGroups))
			assert.Equal(t, tt.wantRuleStoreCount, len(finalSecurityPolicy.Rules))

			if fakeService.NSXConfig.EnableVPCNetwork {
				assert.Equal(t, tt.wantShareStoreCount, len(finalShares))
				assert.Equal(t, tt.wantShareGroupStoreCount, len(finalShareGroups))
			} else {
				assert.Equal(t, ([]model.Share)(nil), finalShares)
			}
		})
	}
}

func TestConvertNetworkPolicyToInternalSecurityPolicies(t *testing.T) {
	VPCInfo := make([]common.VPCResourceInfo, 1)
	VPCInfo[0].OrgID = "default"
	VPCInfo[0].ProjectID = "projectQuality"
	VPCInfo[0].VPCID = "vpc1"

	fakeService := fakeSecurityPolicyService()
	fakeService.NSXConfig.EnableVPCNetwork = true
	mockVPCService := common.MockVPCServiceProvider{}
	fakeService.vpcService = &mockVPCService

	tests := []struct {
		name                      string
		inputPolicy               *networkingv1.NetworkPolicy
		expPolicyAllowSection     *v1alpha1.SecurityPolicy
		expPolicyIsolationSection *v1alpha1.SecurityPolicy
	}{
		{
			name:        "Convert NetworkPolicy",
			inputPolicy: &npWithNsSelecotr,
			expPolicyAllowSection: &v1alpha1.SecurityPolicy{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "np-app-access", UID: "uidNP_allow"},
				Spec: v1alpha1.SecurityPolicySpec{
					AppliedTo: []v1alpha1.SecurityPolicyTarget{
						{
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"role": "db"},
							},
						},
					},
					Rules: []v1alpha1.SecurityPolicyRule{
						{
							Action:    &allowAction,
							Direction: &directionIn,
							Sources: []v1alpha1.SecurityPolicyPeer{
								{
									PodSelector: &metav1.LabelSelector{
										MatchLabels: map[string]string{"app": "coffee"},
									},
								},
								{
									PodSelector: &metav1.LabelSelector{
										MatchLabels: map[string]string{},
									},
									NamespaceSelector: &metav1.LabelSelector{
										MatchLabels: map[string]string{"ns-name": "ns-3"},
									},
								},
							},
							Ports: []v1alpha1.SecurityPolicyPort{
								{
									Protocol: corev1.ProtocolTCP,
									Port:     intstr.IntOrString{Type: intstr.Int, IntVal: 6001},
								},
							},
						},
					},
					Priority: common.PriorityNetworkPolicyAllowRule,
				},
			},
			expPolicyIsolationSection: &v1alpha1.SecurityPolicy{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "np-app-access", UID: "uidNP_isolation"},
				Spec: v1alpha1.SecurityPolicySpec{
					AppliedTo: []v1alpha1.SecurityPolicyTarget{
						{
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"role": "db"},
							},
						},
					},
					Rules: []v1alpha1.SecurityPolicyRule{
						{
							Action:    &allowDrop,
							Direction: &directionIn,
							Name:      "ingress_isolation",
						},
					},
					Priority: common.PriorityNetworkPolicyIsolationRule,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observedPolicy, err := fakeService.convertNetworkPolicyToInternalSecurityPolicies(tt.inputPolicy)
			assert.Equal(t, nil, err)
			assert.Equal(t, tt.expPolicyAllowSection, observedPolicy[0])
			assert.Equal(t, tt.expPolicyIsolationSection, observedPolicy[1])
		})
	}
}

func TestGetFinalSecurityPolicyResouceFromNetworkPolicy(t *testing.T) {
	VPCInfo := make([]common.VPCResourceInfo, 1)
	VPCInfo[0].OrgID = "default"
	VPCInfo[0].ProjectID = "projectQuality"
	VPCInfo[0].VPCID = "vpc1"

	fakeService := fakeSecurityPolicyService()
	fakeService.NSXConfig.EnableVPCNetwork = true
	mockVPCService := common.MockVPCServiceProvider{}
	fakeService.vpcService = &mockVPCService

	destinationPorts := data.NewListValue()
	destinationPorts.Add(data.NewStringValue("6001"))
	serviceEntry := data.NewStructValue(
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

	patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(fakeService), "getVPCInfo",
		func(s *SecurityPolicyService, spNameSpace string) (*common.VPCResourceInfo, error) {
			return &VPCInfo[0], nil
		})

	patches.ApplyPrivateMethod(reflect.TypeOf(fakeService), "getNamespaceUID",
		func(s *SecurityPolicyService, ns string) types.UID {
			return types.UID(tagValueNSUID)
		})

	defer patches.Reset()

	tests := []struct {
		name               string
		inputPolicy        *networkingv1.NetworkPolicy
		expAllowPolicy     *model.SecurityPolicy
		expIsolationPolicy *model.SecurityPolicy
	}{
		{
			name:        "Get SecurityPolicy from NetworkPolicy",
			inputPolicy: &npWithNsSelecotr,
			expAllowPolicy: &model.SecurityPolicy{
				DisplayName:    common.String("np-app-access"),
				Id:             common.String("np-app-access_uidNP_allow"),
				Scope:          []string{"/orgs/default/projects/projectQuality/vpcs/vpc1/groups/np-app-access_uidNP_allow_scope"},
				SequenceNumber: Int64(int64(common.PriorityNetworkPolicyAllowRule)),
				Rules: []model.Rule{
					{
						DisplayName:       common.String("TCP.6001_ingress_allow"),
						Id:                common.String("np-app-access_uidNP_allow_0_6c2a026ca143812daa72699fb924ee36b33b5cdc_0_0"),
						DestinationGroups: []string{"ANY"},
						Direction:         &nsxRuleDirectionIn,
						Scope:             []string{"ANY"},
						SequenceNumber:    &seq0,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"/orgs/default/projects/projectQuality/infra/domains/default/groups/np-app-access_uidNP_allow_0_src"},
						Action:            &nsxRuleActionAllow,
						ServiceEntries:    []*data.StructValue{serviceEntry},
						Tags:              npAllowBasicTags,
					},
				},
				Tags: npAllowBasicTags,
			},
			expIsolationPolicy: &model.SecurityPolicy{
				DisplayName:    common.String("np-app-access"),
				Id:             common.String("np-app-access_uidNP_isolation"),
				Scope:          []string{"/orgs/default/projects/projectQuality/vpcs/vpc1/groups/np-app-access_uidNP_isolation_scope"},
				SequenceNumber: Int64(int64(common.PriorityNetworkPolicyIsolationRule)),
				Rules: []model.Rule{
					{
						DisplayName:       common.String("ingress_isolation"),
						Id:                common.String("np-app-access_uidNP_isolation_0_114fed106ef3b5eae2a583f312435e84c02ca97f_0_0"),
						DestinationGroups: []string{"ANY"},
						Direction:         &nsxRuleDirectionIn,
						Scope:             []string{"/orgs/default/projects/projectQuality/vpcs/vpc1/groups/np-app-access_uidNP_isolation_scope"},
						SequenceNumber:    &seq0,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"ANY"},
						Action:            &nsxRuleActionDrop,
						Tags:              npIsolationBasicTags,
					},
				},
				Tags: npIsolationBasicTags,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeService.setUpStore(common.TagScopeSecurityPolicyUID)

			convertSecurityPolicy, err := fakeService.convertNetworkPolicyToInternalSecurityPolicies(tt.inputPolicy)
			assert.Equal(t, nil, err)

			finalAllowSecurityPolicy, _, _, _, _, err := fakeService.getFinalSecurityPolicyResource(convertSecurityPolicy[0], common.ResourceTypeNetworkPolicy, false)
			assert.Equal(t, nil, err)

			assert.Equal(t, tt.expAllowPolicy, finalAllowSecurityPolicy)

			finalIsolationSecurityPolicy, _, _, _, _, err := fakeService.getFinalSecurityPolicyResource(convertSecurityPolicy[1], common.ResourceTypeNetworkPolicy, false)
			assert.Equal(t, nil, err)

			assert.Equal(t, tt.expIsolationPolicy, finalIsolationSecurityPolicy)
		})
	}
}
