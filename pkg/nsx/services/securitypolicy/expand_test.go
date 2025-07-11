/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package securitypolicy

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	core_v1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	pkg_mock "github.com/vmware-tanzu/nsx-operator/pkg/mock"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestSecurityPolicyService_buildRuleIPGroup(t *testing.T) {
	sp := &v1alpha1.SecurityPolicy{
		ObjectMeta: v1.ObjectMeta{Namespace: "ns1", Name: "spA", UID: "uidA"},
		Spec: v1alpha1.SecurityPolicySpec{
			Rules: []v1alpha1.SecurityPolicyRule{
				{
					Action:    &allowAction,
					Direction: &directionIn,
					Sources: []v1alpha1.SecurityPolicyPeer{
						{
							PodSelector: &v1.LabelSelector{
								MatchLabels: map[string]string{"pod_selector_1": "pod_value_1"},
							},
						},
					},
				},
			},
		},
	}

	nsxRule := model.Rule{
		DisplayName:       &ruleNameWithPodSelector00,
		Id:                &ruleIDPort000,
		DestinationGroups: []string{"ANY"},
		Direction:         &nsxRuleDirectionIn,
		Scope:             []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_scope"},
		SequenceNumber:    &seq0,
		Services:          []string{"ANY"},
		SourceGroups:      []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_src"},
		Action:            &nsxRuleActionAllow,
		ServiceEntries:    []*data.StructValue{},
		Tags:              basicTags,
	}
	ips := []string{"1.1.1.1", "2.2.2.2"}

	policyGroupID := fmt.Sprintf("%s_ipset", ruleIDPort000)
	policyGroupName := fmt.Sprintf("%s_ipset", ruleNameWithPodSelector00)
	addresses := data.NewListValue()
	for _, ip := range ips {
		addresses.Add(data.NewStringValue(ip))
	}
	blockExpression := data.NewStructValue(
		"",
		map[string]data.DataValue{
			"resource_type": data.NewStringValue("IPAddressExpression"),
			"ip_addresses":  addresses,
		},
	)
	s := &SecurityPolicyService{Service: common.Service{}}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(&s.Service), "GetNamespaceUID",
		func(s *common.Service, ns string) types.UID {
			return types.UID(tagValueNSUID)
		})
	defer patches.Reset()
	ipGroup := model.Group{
		Id:          &policyGroupID,
		DisplayName: &policyGroupName,
		Expression:  []*data.StructValue{blockExpression},
		// build ipset group tags from input securitypolicy and securitypolicy rule
		Tags: service.buildPeerTags(sp, &sp.Spec.Rules[0], 0, false, false, false, common.ResourceTypeSecurityPolicy),
	}

	type args struct {
		obj *model.Rule
		ips []string
	}
	tests := []struct {
		name string
		args args
		want *model.Group
	}{
		{"1", args{&nsxRule, ips}, &ipGroup},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, service.buildRuleIPSetGroup(sp, &sp.Spec.Rules[0], tt.args.obj, tt.args.ips, 0, common.ResourceTypeSecurityPolicy), "buildRuleIPSetGroup(%v, %v)",
				tt.args.obj, tt.args.ips)
		})
	}
}

func TestSecurityPolicyService_getPodSelectors(t *testing.T) {
	podSelector2 := &v1.LabelSelector{
		MatchLabels:      map[string]string{"pod_selector_1": "pod_value_1"},
		MatchExpressions: podSelectorMatchExpression,
	}
	sp := v1alpha1.SecurityPolicy{
		ObjectMeta: v1.ObjectMeta{Namespace: "ns1", Name: "spA", UID: "uidA"},
		Spec:       v1alpha1.SecurityPolicySpec{},
	}

	sp1 := v1alpha1.SecurityPolicy{
		ObjectMeta: v1.ObjectMeta{Namespace: "ns1", Name: "spA", UID: "uidA"},
		Spec: v1alpha1.SecurityPolicySpec{
			AppliedTo: []v1alpha1.SecurityPolicyTarget{
				{
					PodSelector: podSelector2,
				},
			},
		},
	}

	podSelector := &v1.LabelSelector{
		MatchLabels:      map[string]string{"pod_selector_1": "pod_value_1"},
		MatchExpressions: podSelectorMatchExpression,
	}
	rule := v1alpha1.SecurityPolicyRule{
		Action:    &allowAction,
		Direction: &directionIn,
		Name:      "rule-with-pod-selector",
		AppliedTo: []v1alpha1.SecurityPolicyTarget{
			{
				PodSelector: podSelector,
			},
		},
		Sources: []v1alpha1.SecurityPolicyPeer{
			{
				PodSelector: &v1.LabelSelector{
					MatchLabels:      map[string]string{"pod_selector_1": "pod_value_1"},
					MatchExpressions: podSelectorMatchExpression,
				},
				NamespaceSelector: &v1.LabelSelector{
					MatchLabels:      map[string]string{"ns1": "spA"},
					MatchExpressions: nsSelectorMatchExpression,
				},
			},
		},
	}
	rule2 := v1alpha1.SecurityPolicyRule{
		Action:    &allowAction,
		Direction: &directionIn,
		Name:      "rule-with-pod-selector-2",
		AppliedTo: []v1alpha1.SecurityPolicyTarget{},
		Sources: []v1alpha1.SecurityPolicyPeer{
			{
				PodSelector: &v1.LabelSelector{
					MatchLabels:      map[string]string{"pod_selector_1": "pod_value_1"},
					MatchExpressions: podSelectorMatchExpression,
				},
				NamespaceSelector: &v1.LabelSelector{
					MatchLabels:      map[string]string{"ns1": "spA"},
					MatchExpressions: nsSelectorMatchExpression,
				},
			},
		},
	}
	rule3 := v1alpha1.SecurityPolicyRule{
		Action:    &allowAction,
		Direction: &directionOut,
		Name:      "rule-with-pod-selector-3",
		AppliedTo: []v1alpha1.SecurityPolicyTarget{},
		Destinations: []v1alpha1.SecurityPolicyPeer{
			{
				PodSelector: podSelector,
				NamespaceSelector: &v1.LabelSelector{
					MatchLabels:      map[string]string{"ns1": "spA"},
					MatchExpressions: nsSelectorMatchExpression,
				},
			},
		},
	}
	type fields struct {
		Client                     client.Client
		NSXClient                  *nsx.Client
		NSXConfig                  *config.NSXOperatorConfig
		GroupCacheIndexer          cache.Indexer
		SecurityPolicyCacheIndexer cache.Indexer
		RuleCacheIndexer           cache.Indexer
	}
	type args struct {
		obj  *v1alpha1.SecurityPolicy
		rule *v1alpha1.SecurityPolicyRule
	}

	labelSelector, _ := v1.LabelSelectorAsSelector(podSelector)
	labelSelector2, _ := v1.LabelSelectorAsSelector(podSelector2)
	var s *SecurityPolicyService
	patches := gomonkey.ApplyMethod(reflect.TypeOf(s), "ResolveNamespace",
		func(s *SecurityPolicyService, _ *v1.LabelSelector) (*core_v1.NamespaceList, error) {
			ns := core_v1.NamespaceList{
				Items: []core_v1.Namespace{
					{
						TypeMeta: v1.TypeMeta{},
						ObjectMeta: v1.ObjectMeta{
							Name:   "ns1",
							Labels: map[string]string{"ns1": "spA"},
						},
					},
					{
						TypeMeta: v1.TypeMeta{},
						ObjectMeta: v1.ObjectMeta{
							Name:   "ns2",
							Labels: map[string]string{"ns2": "spA"},
						},
					},
				},
			}
			return &ns, nil
		})
	defer patches.Reset()

	tests := []struct {
		name    string
		fields  fields
		args    args
		want    client.ListOptions
		wantErr assert.ErrorAssertionFunc
	}{
		{"1", fields{}, args{&sp, &rule}, client.ListOptions{LabelSelector: labelSelector, Namespace: "ns1"}, nil},
		{"2", fields{}, args{&sp1, &rule2}, client.ListOptions{LabelSelector: labelSelector2, Namespace: "ns1"}, nil},
		{"3", fields{}, args{&sp1, &rule3}, client.ListOptions{LabelSelector: labelSelector, Namespace: "ns1"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &SecurityPolicyService{}
			got, _ := service.getPodSelectors(tt.args.obj, tt.args.rule)
			assert.Equalf(t, tt.want, got[0], "getPodSelector(%v, %v)", tt.args.obj, tt.args.rule)
		})
	}
}

var secPolicy = &v1alpha1.SecurityPolicy{
	ObjectMeta: v1.ObjectMeta{Namespace: "ns1", Name: "p1", UID: "uid1"},
	Spec: v1alpha1.SecurityPolicySpec{
		Rules: []v1alpha1.SecurityPolicyRule{
			{
				Name:      "rule1",
				Action:    &allowAction,
				Direction: &directionIn,
				Ports:     []v1alpha1.SecurityPolicyPort{},
			},
			{
				Name:      "rule2",
				Action:    &allowAction,
				Direction: &directionIn,
				Ports: []v1alpha1.SecurityPolicyPort{
					{
						Protocol: "TCP",
						Port:     intstr.IntOrString{Type: intstr.Int, IntVal: 1000},
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
						Port:     intstr.IntOrString{Type: intstr.String, StrVal: "http"}, // http port is 443
					},
					{
						Protocol: "UDP",
						Port:     intstr.IntOrString{Type: intstr.Int, IntVal: 1236},
						EndPort:  1237,
					},
				},
			},
		},
		AppliedTo: []v1alpha1.SecurityPolicyTarget{
			{
				PodSelector: &v1.LabelSelector{
					MatchLabels: map[string]string{"pod_selector_1": "pod_value_1"},
				},
			},
		},
	},
}

func Test_ExpandRule(t *testing.T) {
	ruleTagsFn := func(policyType string) []model.Tag {
		return []model.Tag{
			{Scope: common.String("nsx-op/cluster"), Tag: common.String("")},
			{Scope: common.String("nsx-op/version"), Tag: common.String("1.0.0")},
			{Scope: common.String("nsx-op/namespace"), Tag: common.String("ns1")},
			{Scope: common.String("nsx-op/namespace_uid"), Tag: common.String("ns1-uid")},
			{Scope: common.String(fmt.Sprintf("nsx-op/%s_name", policyType)), Tag: common.String("p1")},
			{Scope: common.String(fmt.Sprintf("nsx-op/%s_uid", policyType)), Tag: common.String("uid1")},
		}
	}
	npRuleTags := ruleTagsFn("network_policy")
	spT1RuleTags := ruleTagsFn("security_policy_cr")
	spVPCRuleTags := ruleTagsFn("security_policy")

	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(
		func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
			namespace, _ := obj.(*core_v1.Namespace)
			namespace.UID = types.UID("ns1-uid")
			return nil
		}).AnyTimes()
	k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(
		func(_ context.Context, objList client.ObjectList, opts ...*client.ListOptions) error {
			podsList, _ := objList.(*core_v1.PodList)
			podsList.Items = []core_v1.Pod{
				{
					Spec: core_v1.PodSpec{
						Containers: []core_v1.Container{
							{
								Ports: []core_v1.ContainerPort{
									{
										Name:          "http",
										Protocol:      "TCP",
										ContainerPort: 8080,
									},
								},
							},
						},
					},
					Status: core_v1.PodStatus{
						Phase: core_v1.PodRunning,
						PodIP: "1.1.1.1",
					},
				},
			}
			return nil
		},
	).AnyTimes()

	mockVPCService := pkg_mock.MockVPCServiceProvider{}
	mockVPCService.On("ListVPCInfo", mock.Anything).Return([]common.VPCResourceInfo{{
		OrgID:     "default",
		ProjectID: "pro1",
		VPCID:     "vpc1",
	}}).Maybe()

	getTestIPsetGroup := func(id string, displayName string, ruleId string, isVPC bool, policyType string) *model.Group {
		groupTags := []model.Tag{
			{Scope: common.String("nsx-op/group_type"), Tag: common.String("destination")},
			{Scope: common.String("nsx-op/rule_id"), Tag: common.String(ruleId)},
			{Scope: common.String("nsx-op/selector_hash"), Tag: common.String("2be88ca4242c76e8253ac62474851065032d6833")},
		}
		groupTags = append(groupTags, ruleTagsFn(policyType)...)
		if isVPC {
			groupTags = append(groupTags,
				model.Tag{Scope: common.String("nsx-op/nsx_share_created_for"), Tag: common.String("notShared")})
		}

		addresses := data.NewListValue()
		addresses.Add(data.NewStringValue("1.1.1.1"))

		return &model.Group{
			Id:          common.String(id),
			DisplayName: common.String(displayName),
			Tags:        groupTags,
			Expression: []*data.StructValue{
				data.NewStructValue(
					"",
					map[string]data.DataValue{
						"resource_type": data.NewStringValue("IPAddressExpression"),
						"ip_addresses":  addresses,
					},
				),
			},
		}
	}

	for _, tc := range []struct {
		name       string
		vpcEnabled bool
		ruleIdx    int
		createdFor string
		expGroups  []*model.Group
		expRules   []*model.Rule
		expErr     string
	}{
		{
			name:       "VPC: rule without and ports for NetworkPolicy",
			vpcEnabled: true,
			ruleIdx:    0,
			createdFor: common.ResourceTypeNetworkPolicy,
			expRules: []*model.Rule{
				{
					Id:             common.String("p1_uid1_26e848bd_all"),
					DisplayName:    common.String("rule1"),
					Direction:      common.String(string("IN")),
					SequenceNumber: Int64(int64(0)),
					Action:         common.String(string("ALLOW")),
					Services:       []string{"ANY"},
					Tags:           npRuleTags,
				},
			},
		}, {
			name:       "VPC: named rule without named ports for NetworkPolicy",
			vpcEnabled: true,
			ruleIdx:    1,
			createdFor: common.ResourceTypeNetworkPolicy,
			expRules: []*model.Rule{
				{
					Id:             common.String("p1_uid1_2a54787a_1000_1234.1235"),
					DisplayName:    common.String("rule2"),
					Direction:      common.String(string("IN")),
					SequenceNumber: Int64(int64(1)),
					Action:         common.String(string("ALLOW")),
					Services:       []string{"ANY"},
					ServiceEntries: []*data.StructValue{
						getRuleServiceEntries(1000, 0, "TCP"),
						getRuleServiceEntries(1234, 1235, "UDP"),
					},
					Tags: npRuleTags,
				},
			},
		}, {
			name:       "VPC: rule with named ports for NetworkPolicy",
			vpcEnabled: true,
			ruleIdx:    2,
			createdFor: common.ResourceTypeNetworkPolicy,
			expGroups: []*model.Group{
				getTestIPsetGroup("p1_uid1_94b44028_8080_ipset", "TCP.http_UDP.1236.1237.TCP.8080_ingress_allow_ipset", "p1_uid1_94b44028", true, "network_policy"),
			},
			expRules: []*model.Rule{
				{
					Id:                common.String("p1_uid1_94b44028_8080"),
					DisplayName:       common.String("TCP.http_UDP.1236.1237.TCP.8080_ingress_allow"),
					Direction:         common.String("IN"),
					SequenceNumber:    Int64(int64(2)),
					Action:            common.String("ALLOW"),
					Services:          []string{"ANY"},
					ServiceEntries:    []*data.StructValue{getRuleServiceEntries(8080, 0, "TCP")},
					Tags:              npRuleTags,
					DestinationGroups: []string{"/orgs/default/projects/pro1/vpcs/vpc1/groups/p1_uid1_94b44028_8080_ipset"},
				}, {
					Id:             common.String("p1_uid1_94b44028_1236.1237"),
					DisplayName:    common.String("TCP.http_UDP.1236.1237.UDP.1236.1237_ingress_allow"),
					Direction:      common.String("IN"),
					SequenceNumber: Int64(int64(2)),
					Action:         common.String("ALLOW"),
					Services:       []string{"ANY"},
					ServiceEntries: []*data.StructValue{getRuleServiceEntries(1236, 1237, "UDP")},
					Tags:           npRuleTags,
				},
			},
		}, {
			name:       "VPC: rule with named ports for SecurityPolicy",
			vpcEnabled: true,
			ruleIdx:    2,
			createdFor: common.ResourceTypeSecurityPolicy,
			expGroups: []*model.Group{
				getTestIPsetGroup("p1_uid1_94b44028_8080_ipset", "TCP.http_UDP.1236.1237.TCP.8080_ingress_allow_ipset", "p1_uid1_94b44028", true, "security_policy"),
			},
			expRules: []*model.Rule{
				{
					Id:                common.String("p1_uid1_94b44028_8080"),
					DisplayName:       common.String("TCP.http_UDP.1236.1237.TCP.8080_ingress_allow"),
					Direction:         common.String("IN"),
					SequenceNumber:    Int64(int64(2)),
					Action:            common.String("ALLOW"),
					Services:          []string{"ANY"},
					ServiceEntries:    []*data.StructValue{getRuleServiceEntries(8080, 0, "TCP")},
					Tags:              spVPCRuleTags,
					DestinationGroups: []string{"/orgs/default/projects/pro1/vpcs/vpc1/groups/p1_uid1_94b44028_8080_ipset"},
				}, {
					Id:             common.String("p1_uid1_94b44028_1236.1237"),
					DisplayName:    common.String("TCP.http_UDP.1236.1237.UDP.1236.1237_ingress_allow"),
					Direction:      common.String("IN"),
					SequenceNumber: Int64(int64(2)),
					Action:         common.String("ALLOW"),
					Services:       []string{"ANY"},
					ServiceEntries: []*data.StructValue{getRuleServiceEntries(1236, 1237, "UDP")},
					Tags:           spVPCRuleTags,
				},
			},
		}, {
			name:       "T1: rule with named ports for SecurityPolicy",
			vpcEnabled: false,
			ruleIdx:    2,
			createdFor: common.ResourceTypeSecurityPolicy,
			expGroups: []*model.Group{
				getTestIPsetGroup("sp_uid1_94b44028488f3e719879abbc27c75e5cb44872b7_2_0_0_ipset", "TCP.http_UDP.1236.1237.TCP.8080_ingress_allow_ipset", "sp_uid1_94b44028488f3e719879abbc27c75e5cb44872b7_2", false, "security_policy_cr"),
			},
			expRules: []*model.Rule{
				{
					Id:                common.String("sp_uid1_94b44028488f3e719879abbc27c75e5cb44872b7_2_0_0"),
					DisplayName:       common.String("TCP.http_UDP.1236.1237.TCP.8080_ingress_allow"),
					Direction:         common.String("IN"),
					SequenceNumber:    Int64(int64(2)),
					Action:            common.String("ALLOW"),
					Services:          []string{"ANY"},
					ServiceEntries:    []*data.StructValue{getRuleServiceEntries(8080, 0, "TCP")},
					Tags:              spT1RuleTags,
					DestinationGroups: []string{"/infra/domains//groups/sp_uid1_94b44028488f3e719879abbc27c75e5cb44872b7_2_0_0_ipset"},
				}, {
					Id:             common.String("sp_uid1_94b44028488f3e719879abbc27c75e5cb44872b7_2_1_0"),
					DisplayName:    common.String("TCP.http_UDP.1236.1237.UDP.1236.1237_ingress_allow"),
					Direction:      common.String("IN"),
					SequenceNumber: Int64(int64(2)),
					Action:         common.String("ALLOW"),
					Services:       []string{"ANY"},
					ServiceEntries: []*data.StructValue{getRuleServiceEntries(1236, 1237, "UDP")},
					Tags:           spT1RuleTags,
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Initial the security policy related tags. This is executed in `InitializeSecurityPolicy` in
			// function logic.
			if tc.vpcEnabled {
				common.TagValueScopeSecurityPolicyName = common.TagScopeSecurityPolicyName
				common.TagValueScopeSecurityPolicyUID = common.TagScopeSecurityPolicyUID
			} else {
				common.TagValueScopeSecurityPolicyName = common.TagScopeSecurityPolicyCRName
				common.TagValueScopeSecurityPolicyUID = common.TagScopeSecurityPolicyCRUID
			}
			svc := &SecurityPolicyService{
				Service: common.Service{
					Client: k8sClient,
					NSXConfig: &config.NSXOperatorConfig{
						CoeConfig: &config.CoeConfig{EnableVPCNetwork: tc.vpcEnabled},
					},
				},
				vpcService: &mockVPCService,
			}
			rule := secPolicy.Spec.Rules[tc.ruleIdx]
			nsxGroups, nsxRules, err := svc.expandRule(secPolicy, &rule, tc.ruleIdx, tc.createdFor)
			if tc.expErr != "" {
				require.EqualError(t, err, tc.expErr)
			} else {
				require.NoError(t, err)
			}
			assert.ElementsMatch(t, tc.expGroups, nsxGroups)
			assert.ElementsMatch(t, tc.expRules, nsxRules)
		})
	}
}

// TestResolveNamespace tests the ResolveNamespace function
func Test_ResolveNamespace(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()

	// Create a mock client
	k8sClient := mock_client.NewMockClient(mockCtl)

	// Prepare test data
	labelSelector := &v1.LabelSelector{
		MatchLabels: map[string]string{"env": "test"},
	}
	expectedNamespaceList := &core_v1.NamespaceList{
		Items: []core_v1.Namespace{
			{ObjectMeta: v1.ObjectMeta{Name: "test-namespace"}},
		},
	}

	// Convert LabelSelector to map and to Selector
	labelMap, err := v1.LabelSelectorAsMap(labelSelector)
	assert.NoError(t, err)
	expectedSelector := labels.SelectorFromSet(labelMap)

	// Set up the mock to expect a List call with the correct options
	k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&client.ListOptions{})).
		DoAndReturn(func(ctx context.Context, list *core_v1.NamespaceList, opts ...client.ListOption) error {
			// Verify the selector matches
			listOpts := opts[0].(*client.ListOptions)
			assert.Equal(t, expectedSelector.String(), listOpts.LabelSelector.String())

			// Return the expected namespace list
			*list = *expectedNamespaceList
			return nil
		})

	// Create the service and call the function
	service := &SecurityPolicyService{
		Service: common.Service{
			Client: k8sClient,
		},
	}

	nsList, err := service.ResolveNamespace(labelSelector)

	// Verify results
	assert.NoError(t, err)
	assert.Equal(t, expectedNamespaceList, nsList)
}

func getRuleServiceEntries(portStart, portEnd int, protocol string) *data.StructValue {
	return buildRuleServiceEntries(v1alpha1.SecurityPolicyPort{
		Protocol: core_v1.Protocol(protocol),
		Port:     intstr.FromInt32(int32(portStart)),
		EndPort:  portEnd,
	})
}
