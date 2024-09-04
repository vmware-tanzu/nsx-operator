package securitypolicy

import (
	"fmt"
	"reflect"
	"testing"

	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	core_v1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestSecurityPolicyService_buildRuleIPGroup(t *testing.T) {
	sp := &v1alpha1.SecurityPolicy{
		ObjectMeta: v1.ObjectMeta{Namespace: "ns1", Name: "spA", UID: "uidA"},
	}

	rule := v1alpha1.SecurityPolicyRule{}
	nsxRule := model.Rule{
		DisplayName:       &ruleNameWithPodSelector00,
		Id:                &ruleIDPort000,
		DestinationGroups: []string{"ANY"},
		Direction:         &nsxDirectionIn,
		Scope:             []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_scope"},
		SequenceNumber:    &seq0,
		Services:          []string{"ANY"},
		SourceGroups:      []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_src"},
		Action:            &nsxActionAllow,
		ServiceEntries:    []*data.StructValue{},
		Tags:              basicTags,
	}
	ips := []string{"1.1.1.1", "2.2.2.2"}

	policyGroupID := fmt.Sprintf("%s_ipset", ruleIDPort000)
	policyGroupName := fmt.Sprintf("%s-ipset", ruleNameWithPodSelector00)
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
	var s *SecurityPolicyService
	patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(s), "getNamespaceUID",
		func(s *SecurityPolicyService, ns string) types.UID {
			return types.UID(tagValueNSUID)
		})
	defer patches.Reset()
	ipGroup := model.Group{
		Id:          &policyGroupID,
		DisplayName: &policyGroupName,
		Expression:  []*data.StructValue{blockExpression},
		// build ipset group tags from input securitypolicy and securitypolicy rule
		Tags: service.buildPeerTags(sp, &rule, 0, false, false, common.ResourceTypeSecurityPolicy),
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
			assert.Equalf(t, tt.want, service.buildRuleIPSetGroup(sp, &rule, tt.args.obj, tt.args.ips, 0, common.ResourceTypeSecurityPolicy), "buildRuleIPSetGroup(%v, %v)",
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
		{"2", fields{}, args{&sp, &rule2}, client.ListOptions{LabelSelector: labelSelector2, Namespace: "ns1"}, nil},
		{"3", fields{}, args{&sp, &rule3}, client.ListOptions{LabelSelector: labelSelector, Namespace: "ns1"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &SecurityPolicyService{}
			got, _ := service.getPodSelectors(tt.args.obj, tt.args.rule)
			assert.Equalf(t, tt.want, got[0], "getPodSelector(%v, %v)", tt.args.obj, tt.args.rule)
		})
	}
}
