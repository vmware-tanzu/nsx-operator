/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package source

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func Test_HTTPRouteParentReadyForGateway_crossNamespaceParentRef(t *testing.T) {
	g := gatewayv1.Group(gatewayv1.GroupName)
	k := gatewayv1.Kind("Gateway")
	gwNS := gatewayv1.Namespace("ns-gw")
	parent := gatewayv1.ParentReference{Group: &g, Kind: &k, Name: "gw1", Namespace: &gwNS}

	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns-route"},
		Status: gatewayv1.HTTPRouteStatus{
			RouteStatus: gatewayv1.RouteStatus{
				Parents: []gatewayv1.RouteParentStatus{
					{
						ParentRef: parent,
						Conditions: []metav1.Condition{
							{Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue},
						},
					},
				},
			},
		},
	}
	gwNN := types.NamespacedName{Namespace: "ns-gw", Name: "gw1"}
	assert.True(t, HTTPRouteParentReadyForGateway(route, gwNN))
}

func Test_HTTPRouteParentReadyForGateway_wrongGatewayNamespaceNotReady(t *testing.T) {
	g := gatewayv1.Group(gatewayv1.GroupName)
	k := gatewayv1.Kind("Gateway")
	// Status parent omits namespace → resolves to route namespace (ns-route), not ns-gw.
	parent := gatewayv1.ParentReference{Group: &g, Kind: &k, Name: "gw1"}

	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns-route"},
		Status: gatewayv1.HTTPRouteStatus{
			RouteStatus: gatewayv1.RouteStatus{
				Parents: []gatewayv1.RouteParentStatus{
					{
						ParentRef: parent,
						Conditions: []metav1.Condition{
							{Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue},
						},
					},
				},
			},
		},
	}
	gwNN := types.NamespacedName{Namespace: "ns-gw", Name: "gw1"}
	assert.False(t, HTTPRouteParentReadyForGateway(route, gwNN))
}
