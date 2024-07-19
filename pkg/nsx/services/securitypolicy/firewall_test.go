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
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/nsx.vmware.com/v1alpha1"
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
	tagScopeRuleID               = common.TagScopeRuleID
	tagScopeSelectorHash         = common.TagScopeSelectorHash
	spName                       = "sp-ns1-spA"
	spGroupName                  = "ns1-spA-scope"
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
	nsxDirectionIn               = "IN"
	nsxActionAllow               = "ALLOW"
	nsxDirectionOut              = "OUT"
	nsxActionDrop                = "DROP"
	cluster                      = "k8scl-one"
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
)

func TestListSecurityPolicyID(t *testing.T) {
	service := &SecurityPolicyService{
		Service: common.Service{NSXClient: nil},
	}
	service.securityPolicyStore = &SecurityPolicyStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagValueScopeSecurityPolicyUID: indexBySecurityPolicyUID}),
		BindingType: model.SecurityPolicyBindingType(),
	}}
	service.groupStore = &GroupStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagValueScopeSecurityPolicyUID: indexBySecurityPolicyUID}),
		BindingType: model.GroupBindingType(),
	}}
	service.ruleStore = &RuleStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagValueScopeSecurityPolicyUID: indexBySecurityPolicyUID}),
		BindingType: model.RuleBindingType(),
	}}
	service.shareStore = &ShareStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagValueScopeSecurityPolicyUID: indexBySecurityPolicyUID}),
		BindingType: model.ShareBindingType(),
	}}

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

	id3 := "shareId"
	uuid3 := "shareIdUID"
	share := model.Share{}
	share.Id = &id1
	share.UniqueId = &uuid3
	share.Tags = []model.Tag{{Scope: &scope, Tag: &id3}}
	err = service.shareStore.Add(&share)
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := service.ListSecurityPolicyID()
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SecurityPolicyService.ListSecurityPolicyID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestListUpdateRules(t *testing.T) {
	r1 := model.Rule{
		DisplayName:       String("nsxrule1"),
		Id:                String("nsxrule_1"),
		DestinationGroups: []string{"ANY"},
		Direction:         &nsxDirectionIn,
		Scope:             []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_scope"},
		SequenceNumber:    &seq0,
		Services:          []string{"ANY"},
		SourceGroups:      []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_src"},
		Action:            &nsxActionAllow,
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
					Direction:         &nsxDirectionIn,
					Scope:             []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_scope"},
					SequenceNumber:    &seq0,
					Services:          []string{"ANY"},
					SourceGroups:      []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_src"},
					Action:            &nsxActionAllow,
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
					Direction:         &nsxDirectionIn,
					Scope:             []string{"/infra/domains/k8scl-one/groups/sp_uidA_1_scope"},
					SequenceNumber:    &seq0,
					Services:          []string{"ANY"},
					SourceGroups:      []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_src"},
					Action:            &nsxActionAllow,
				},
			},
			finalRulesLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			finalRules := service.updateRules(tt.existingRules, tt.expectedRules)
			assert.Equal(t, tt.finalRulesLen, len(finalRules))
		})
	}
}

func TestListUpdateGroups(t *testing.T) {
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
			finalGroups := service.updateGroups(tt.existingGroups, tt.expectedGroups)
			assert.Equal(t, tt.finalGroupsLen, len(finalGroups))
		})
	}
}

func TestListUpdateShares(t *testing.T) {
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
			finalShares := service.updateShares(tt.existingShares, tt.expectedShares)
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
		Direction:         &nsxDirectionIn,
		Scope:             []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_scope"},
		SequenceNumber:    &seq0,
		Services:          []string{"ANY"},
		SourceGroups:      []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_src"},
		Action:            &nsxActionAllow,
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
				assert.NoError(t, s.shareStore.Apply(s1))

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
						Direction:         &nsxDirectionIn,
						Scope:             []string{"ANY"},
						SequenceNumber:    &seq0,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"ANY"},
						Action:            &nsxActionAllow,
						Tags:              basicTags,
					},
					{
						DisplayName:       &ruleNameWithNsSelector00,
						Id:                &ruleID1,
						DestinationGroups: []string{"ANY"},
						Direction:         &nsxDirectionIn,
						Scope:             []string{"ANY"},
						SequenceNumber:    &seq1,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"/orgs/default/projects/projectQuality/infra/domains/default/groups/sp_uidA_1_src"},
						Action:            &nsxActionAllow,
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
						Direction:         &nsxDirectionIn,
						Scope:             []string{"ANY"},
						SequenceNumber:    &seq0,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"ANY"},
						Action:            &nsxActionAllow,
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
			assert.NoError(t, fakeService.ruleStore.Apply(tt.inputPolicy))

			patches := tt.prepareFunc(t, fakeService)
			defer patches.Reset()

			if err := fakeService.deleteVPCSecurityPolicy(tt.args.uid, tt.args.createdFor); (err != nil) != tt.wantErr {
				t.Errorf("deleteVPCSecurityPolicy error = %v, wantErr %v", err, tt.wantErr)
			}
			assert.Equal(t, tt.wantSecurityPolicyStoreCount, len(fakeService.securityPolicyStore.ListKeys()))
			assert.Equal(t, tt.wantRuleStoreCount, len(fakeService.ruleStore.ListKeys()))
			assert.Equal(t, tt.wantGroupStoreCount, len(fakeService.groupStore.ListKeys()))
			assert.Equal(t, tt.wantProjectGroupStoreCount, len(fakeService.projectGroupStore.ListKeys()))
			assert.Equal(t, tt.wantProjectShareStoreCount, len(fakeService.shareStore.ListKeys()))
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
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(s), "getVpcInfo",
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
				Scope:          []string{"/orgs/default/projects/projectQuality/vpcs/vpc1/groups/sp_uidA_scope"},
				SequenceNumber: &seq0,
				Rules: []model.Rule{
					{
						DisplayName:       &podSelectorRule0Name00,
						Id:                &podSelectorRule0IDPort000,
						DestinationGroups: []string{"ANY"},
						Direction:         &nsxDirectionIn,
						Scope:             []string{"/orgs/default/projects/projectQuality/vpcs/vpc1/groups/sp_uidA_0_scope"},
						SequenceNumber:    &seq0,
						Services:          []string{"ANY"},
						SourceGroups:      []string{"/orgs/default/projects/projectQuality/infra/domains/default/groups/sp_uidA_0_src"},
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
						SourceGroups:      []string{"/orgs/default/projects/projectQuality/infra/domains/default/groups/sp_uidA_1_src"},
						Action:            &nsxActionAllow,
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
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(s), "getVpcInfo",
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
			assert.Equal(t, tt.wantProjectShareStoreCount, len(fakeService.shareStore.ListKeys()))
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
		name                       string
		prepareFunc                func(*testing.T, *SecurityPolicyService) *gomonkey.Patches
		args                       args
		expectedPolicy             *model.SecurityPolicy
		wantErr                    bool
		wantSecurityPolicyChanged  bool
		wantRuleStoreCount         int
		wantGroupStoreCount        int
		wantProjectGroupStoreCount int
	}{
		{
			name: "getFinalSecurityPolicyResouceForVPCMode",
			prepareFunc: func(t *testing.T, s *SecurityPolicyService) *gomonkey.Patches {
				s.NSXConfig.EnableVPCNetwork = true

				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(s), "getVpcInfo",
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
				Id:             common.String("spA-uidA"),
				Scope:          []string{"/orgs/default/projects/projectQuality/vpcs/vpc1/groups/spA-uidA_scope"},
				SequenceNumber: &seq0,
				Rules:          []model.Rule{},
				Tags:           basicTags,
			},
			wantErr:                    false,
			wantSecurityPolicyChanged:  true,
			wantRuleStoreCount:         2,
			wantGroupStoreCount:        2,
			wantProjectGroupStoreCount: 2,
		},
		{
			name: "getFinalSecurityPolicyResouceForT1",
			prepareFunc: func(t *testing.T, s *SecurityPolicyService) *gomonkey.Patches {
				s.NSXConfig.EnableVPCNetwork = false

				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(s), "getVpcInfo",
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
				Scope:          []string{"/infra/domains/k8scl-one:test/groups/sp_uidA_scope"},
				SequenceNumber: &seq0,
				Rules:          []model.Rule{},
				Tags:           basicTags,
			},
			wantErr:                    false,
			wantSecurityPolicyChanged:  true,
			wantRuleStoreCount:         2,
			wantGroupStoreCount:        4,
			wantProjectGroupStoreCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeService.setUpStore(common.TagValueScopeSecurityPolicyUID)

			patches := tt.prepareFunc(t, fakeService)
			defer patches.Reset()

			var finalSecurityPolicy *model.SecurityPolicy
			var finalGroups []model.Group
			var projectShares *[]ProjectShare
			var isChanged bool
			var err error

			if finalSecurityPolicy, finalGroups, projectShares, isChanged, err = fakeService.getFinalSecurityPolicyResource(tt.args.spObj, tt.args.createdFor); (err != nil) != tt.wantErr {
				t.Errorf("getFinalSecurityPolicyResouce error = %v, wantErr %v", err, tt.wantErr)
			}

			assert.Equal(t, *tt.expectedPolicy.Id, *finalSecurityPolicy.Id)
			assert.Equal(t, tt.expectedPolicy.Scope[0], finalSecurityPolicy.Scope[0])
			assert.Equal(t, true, isChanged)
			assert.Equal(t, tt.wantGroupStoreCount, len(finalGroups))
			assert.Equal(t, tt.wantRuleStoreCount, len(finalSecurityPolicy.Rules))

			if fakeService.NSXConfig.EnableVPCNetwork {
				assert.Equal(t, tt.wantProjectGroupStoreCount, len(*projectShares))
			} else {
				assert.Equal(t, (*[]ProjectShare)(nil), projectShares)
			}
		})
	}
}
