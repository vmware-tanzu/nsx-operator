/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package source

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// objectNamespacedNameFromParentRef resolves a ParentReference to a NamespacedName
// when Group and Kind match the expected values; otherwise ok is false.
func objectNamespacedNameFromParentRef(ref *gatewayv1.ParentReference, routeNamespace, expectedGroup, expectedKind string) (types.NamespacedName, bool) {
	if ref == nil {
		return types.NamespacedName{}, false
	}
	group := gatewayv1.GroupName
	if ref.Group != nil && string(*ref.Group) != "" {
		group = string(*ref.Group)
	}
	if group != expectedGroup {
		return types.NamespacedName{}, false
	}
	kind := "Gateway"
	if ref.Kind != nil && string(*ref.Kind) != "" {
		kind = string(*ref.Kind)
	}
	if kind != expectedKind {
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

// GatewayNamespacedNameFromParentRef returns the resolved Gateway namespaced name when ref points at a
// gateway.networking.k8s.io Gateway; otherwise (nil ref, wrong group/kind, or empty name) it returns false.
func GatewayNamespacedNameFromParentRef(ref *gatewayv1.ParentReference, routeNamespace string) (types.NamespacedName, bool) {
	return objectNamespacedNameFromParentRef(ref, routeNamespace, gatewayv1.GroupName, "Gateway")
}

// ListenerSetNamespacedNameFromParentRef resolves a ParentReference to a ListenerSet in
// gateway.networking.k8s.io when Group/Kind match; otherwise ok is false.
func ListenerSetNamespacedNameFromParentRef(ref *gatewayv1.ParentReference, routeNamespace string) (types.NamespacedName, bool) {
	return objectNamespacedNameFromParentRef(ref, routeNamespace, gatewayv1.GroupName, "ListenerSet")
}

// ParentReferencesSemanticallyEqual compares two ParentReferences with the same default-namespace
// resolution rules used elsewhere in this package.
func ParentReferencesSemanticallyEqual(a, b *gatewayv1.ParentReference, routeNamespace string) bool {
	if a == nil || b == nil {
		return a == b
	}
	ga, okG := GatewayNamespacedNameFromParentRef(a, routeNamespace)
	gb, okG2 := GatewayNamespacedNameFromParentRef(b, routeNamespace)
	if okG && okG2 {
		return ga == gb && ptrEqualComparable(a.SectionName, b.SectionName) && ptrEqualComparable(a.Port, b.Port)
	}
	la, okL := ListenerSetNamespacedNameFromParentRef(a, routeNamespace)
	lb, okL2 := ListenerSetNamespacedNameFromParentRef(b, routeNamespace)
	if okL && okL2 {
		return la == lb && ptrEqualComparable(a.SectionName, b.SectionName) && ptrEqualComparable(a.Port, b.Port)
	}
	return false
}

func ptrEqualComparable[T comparable](a, b *T) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func conditionTrue(conds []metav1.Condition, typ string) bool {
	for j := range conds {
		if conds[j].Type == typ && conds[j].Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}

// RouteAcceptedForParentRef reports whether status.Parents contains an entry for parentRef
// (same group/kind/name/namespace/section/port semantics as ParentReferencesSemanticallyEqual)
// with RouteConditionAccepted=True.
func RouteAcceptedForParentRef(parents []gatewayv1.RouteParentStatus, routeNamespace string, parentRef gatewayv1.ParentReference) bool {
	for i := range parents {
		if !ParentReferencesSemanticallyEqual(&parents[i].ParentRef, &parentRef, routeNamespace) {
			continue
		}
		if conditionTrue(parents[i].Conditions, string(gatewayv1.RouteConditionAccepted)) {
			return true
		}
	}
	return false
}
