/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package source

import (
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// ListenerSetNamespacedNameFromParentRef resolves a ParentReference to a ListenerSet in
// gateway.networking.k8s.io when Group/Kind match; otherwise ok is false.
func ListenerSetNamespacedNameFromParentRef(ref *gatewayv1.ParentReference, routeNamespace string) (types.NamespacedName, bool) {
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
	kind := "ListenerSet"
	if ref.Kind != nil && string(*ref.Kind) != "" {
		kind = string(*ref.Kind)
	}
	if kind != "ListenerSet" {
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

// ParentRefMatchesListenerSet reports whether ref identifies the given ListenerSet.
func ParentRefMatchesListenerSet(ref *gatewayv1.ParentReference, routeNamespace string, ls types.NamespacedName) bool {
	nn, ok := ListenerSetNamespacedNameFromParentRef(ref, routeNamespace)
	if !ok {
		return false
	}
	return nn == ls
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
