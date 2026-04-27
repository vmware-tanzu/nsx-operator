/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */
// Attribution: see package doc.go.

package source

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// HTTPRouteParentReadyForGateway returns true when the HTTPRoute status includes a parent entry
// for the given Gateway with RouteConditionAccepted set to True.
//
// ExternalDNS gateway resolver (source/gateway.go) treats the parent route status entry as ready when
// Accepted is true for the matching Gateway ref; this helper mirrors that check for the HTTPRoute type.
func HTTPRouteParentReadyForGateway(route *gatewayv1.HTTPRoute, gw types.NamespacedName) bool {
	if route == nil {
		return false
	}
	for i := range route.Status.Parents {
		ps := &route.Status.Parents[i]
		if !ParentRefMatchesGateway(&ps.ParentRef, route.Namespace, gw) {
			continue
		}
		if conditionTrue(ps.Conditions, string(gatewayv1.RouteConditionAccepted)) {
			return true
		}
	}
	return false
}

// GatewayNamespacedNameFromParentRef returns the resolved Gateway namespaced name when ref points at a
// gateway.networking.k8s.io Gateway; otherwise (nil ref, wrong group/kind, or empty name) it returns false.
func GatewayNamespacedNameFromParentRef(ref *gatewayv1.ParentReference, routeNamespace string) (types.NamespacedName, bool) {
	if ref == nil {
		return types.NamespacedName{}, false
	}
	group := gatewayv1.GroupName
	if ref.Group != nil && string(*ref.Group) != "" {
		group = string(*ref.Group)
	}
	if group != gatewayv1.GroupName {
		return types.NamespacedName{}, false
	}
	kind := "Gateway"
	if ref.Kind != nil && string(*ref.Kind) != "" {
		kind = string(*ref.Kind)
	}
	if kind != "Gateway" {
		return types.NamespacedName{}, false
	}
	if ref.Name == "" {
		return types.NamespacedName{}, false
	}
	ns := routeNamespace
	if ref.Namespace != nil && string(*ref.Namespace) != "" {
		ns = string(*ref.Namespace)
	}
	return types.NamespacedName{Namespace: ns, Name: string(ref.Name)}, true
}

// ParentRefMatchesGateway reports whether ref identifies the given Gateway (group/kind/name/namespace).
func ParentRefMatchesGateway(ref *gatewayv1.ParentReference, routeNamespace string, gw types.NamespacedName) bool {
	nn, ok := GatewayNamespacedNameFromParentRef(ref, routeNamespace)
	if !ok {
		return false
	}
	return nn == gw
}

func conditionTrue(conds []metav1.Condition, typ string) bool {
	for j := range conds {
		if conds[j].Type == typ && conds[j].Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}
