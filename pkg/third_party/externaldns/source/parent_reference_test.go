/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package source

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestObjectNamespacedNameFromParentRef(t *testing.T) {
	gwGroup := gatewayv1.Group(gatewayv1.GroupName)
	gwKind := gatewayv1.Kind("Gateway")
	lsKind := gatewayv1.Kind("ListenerSet")

	refGW := &gatewayv1.ParentReference{Group: &gwGroup, Kind: &gwKind, Name: "gw1"}
	nn, ok := GatewayNamespacedNameFromParentRef(refGW, "ns1")
	assert.True(t, ok)
	assert.Equal(t, "ns1/gw1", nn.String())

	refLS := &gatewayv1.ParentReference{Group: &gwGroup, Kind: &lsKind, Name: "ls1"}
	nn, ok = ListenerSetNamespacedNameFromParentRef(refLS, "ns1")
	assert.True(t, ok)
	assert.Equal(t, "ns1/ls1", nn.String())

	// negative test
	_, ok = GatewayNamespacedNameFromParentRef(refLS, "ns1")
	assert.False(t, ok)
	_, ok = ListenerSetNamespacedNameFromParentRef(refGW, "ns1")
	assert.False(t, ok)

	nilRef := (*gatewayv1.ParentReference)(nil)
	_, ok = GatewayNamespacedNameFromParentRef(nilRef, "ns1")
	assert.False(t, ok)
}

func TestParentReferencesSemanticallyEqual(t *testing.T) {
	group1 := gatewayv1.Group(gatewayv1.GroupName)
	group2 := gatewayv1.Group("other")
	kind1 := gatewayv1.Kind("Gateway")
	kind2 := gatewayv1.Kind("ListenerSet")
	ns1 := gatewayv1.Namespace("ns1")
	ns2 := gatewayv1.Namespace("ns2")
	sec1 := gatewayv1.SectionName("sec1")
	sec2 := gatewayv1.SectionName("sec2")
	port1 := gatewayv1.PortNumber(80)
	port2 := gatewayv1.PortNumber(443)

	base := gatewayv1.ParentReference{
		Group:       &group1,
		Kind:        &kind1,
		Namespace:   &ns1,
		Name:        "name1",
		SectionName: &sec1,
		Port:        &port1,
	}

	tests := []struct {
		name string
		mod  func(*gatewayv1.ParentReference)
		want bool
	}{
		{"identical", func(pr *gatewayv1.ParentReference) {}, true},
		{"differing-name", func(pr *gatewayv1.ParentReference) { pr.Name = "name2" }, false},
		{"differing-group", func(pr *gatewayv1.ParentReference) { pr.Group = &group2 }, false},
		{"differing-kind", func(pr *gatewayv1.ParentReference) { pr.Kind = &kind2 }, false},
		{"differing-namespace", func(pr *gatewayv1.ParentReference) { pr.Namespace = &ns2 }, false},
		{"differing-section", func(pr *gatewayv1.ParentReference) { pr.SectionName = &sec2 }, false},
		{"differing-port", func(pr *gatewayv1.ParentReference) { pr.Port = &port2 }, false},
		{"nil-port", func(pr *gatewayv1.ParentReference) { pr.Port = nil }, false},
		{"listener-set", func(pr *gatewayv1.ParentReference) {
			base.Kind = &kind2
			pr.Kind = &kind2
		}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := base
			tc.mod(&b)
			assert.Equal(t, tc.want, ParentReferencesSemanticallyEqual(&base, &b, "ns1"))
		})
	}

	t.Run("nil-cases", func(t *testing.T) {
		assert.False(t, ParentReferencesSemanticallyEqual(&base, nil, "ns1"))
		assert.True(t, ParentReferencesSemanticallyEqual(nil, nil, "ns1"))
	})
}

func TestRouteAcceptedForParentRef(t *testing.T) {
	gwGroup := gatewayv1.Group(gatewayv1.GroupName)
	gwKind := gatewayv1.Kind("Gateway")
	refGW := gatewayv1.ParentReference{Group: &gwGroup, Kind: &gwKind, Name: "gw1"}

	tests := []struct {
		name    string
		parents []gatewayv1.RouteParentStatus
		ref     gatewayv1.ParentReference
		want    bool
	}{
		{
			"accepted",
			[]gatewayv1.RouteParentStatus{{ParentRef: refGW, Conditions: []metav1.Condition{{Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue}}}},
			refGW, true,
		},
		{
			"diff-parent",
			[]gatewayv1.RouteParentStatus{{ParentRef: refGW, Conditions: []metav1.Condition{{Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue}}}},
			gatewayv1.ParentReference{Group: &gwGroup, Kind: &gwKind, Name: "gw2"}, false,
		},
		{
			"condition-false",
			[]gatewayv1.RouteParentStatus{{ParentRef: refGW, Conditions: []metav1.Condition{{Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionFalse}}}},
			refGW, false,
		},
		{
			"missing-condition",
			[]gatewayv1.RouteParentStatus{{ParentRef: refGW, Conditions: []metav1.Condition{{Type: string(gatewayv1.RouteConditionResolvedRefs), Status: metav1.ConditionTrue}}}},
			refGW, false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, RouteAcceptedForParentRef(tc.parents, "ns1", tc.ref))
		})
	}
}
