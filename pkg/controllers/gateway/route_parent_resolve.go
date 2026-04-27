/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package gateway

import (
	"slices"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	extdnssrc "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/source"
)

const routeParentListenerSetIndex = "routeParentListenerSet" // Route -> ListenerSet parent refs

func routeParentListenerSetIndexFunc(obj client.Object) []string {
	var ns string
	var refs []gatewayv1.ParentReference
	switch o := obj.(type) {
	case *gatewayv1.HTTPRoute:
		ns, refs = o.Namespace, o.Spec.ParentRefs
	case *gatewayv1.GRPCRoute:
		ns, refs = o.Namespace, o.Spec.ParentRefs
	case *gatewayv1.TLSRoute:
		ns, refs = o.Namespace, o.Spec.ParentRefs
	default:
		return nil
	}
	seen := sets.New[string]()
	for i := range refs {
		lsNN, ok := extdnssrc.ListenerSetNamespacedNameFromParentRef(&refs[i], ns)
		if !ok {
			continue
		}
		key := lsNN.String()
		if seen.Has(key) {
			continue
		}
		seen.Insert(key)
	}
	if seen.Len() == 0 {
		return nil
	}
	out := seen.UnsortedList()
	slices.Sort(out)
	return out
}

// resolveParentRefToRootGatewayNN returns root Gateway NN for ref, or (_, false) if unknown or not in ipCache.
func (r *GatewayReconciler) resolveParentRefToRootGatewayNN(routeNS string, ref *gatewayv1.ParentReference) (types.NamespacedName, bool) {
	if ref == nil {
		return types.NamespacedName{}, false
	}
	if gwNN, ok := extdnssrc.GatewayNamespacedNameFromParentRef(ref, routeNS); ok {
		return gwNN, true
	}
	lsNN, ok := extdnssrc.ListenerSetNamespacedNameFromParentRef(ref, routeNS)
	if !ok {
		return types.NamespacedName{}, false
	}
	if r.ipCache == nil {
		return types.NamespacedName{}, false
	}
	gwNN, ok := r.ipCache.rootGatewayForCachedListenerSet(lsNN)
	if !ok {
		return types.NamespacedName{}, false
	}
	return gwNN, true
}

// routeParentManagedInIPCache reports whether ref is a known parent kind and present in ipCache (for status patches).
func (r *GatewayReconciler) routeParentManagedInIPCache(routeNS string, ref *gatewayv1.ParentReference) (managed, supportedKind bool) {
	if ref == nil {
		return false, false
	}
	if gwNN, ok := extdnssrc.GatewayNamespacedNameFromParentRef(ref, routeNS); ok {
		_, managed = r.ipCache.get(gwNN)
		return managed, true
	}
	if lsNN, ok := extdnssrc.ListenerSetNamespacedNameFromParentRef(ref, routeNS); ok {
		return r.ipCache.listenerSetInLSToRootIndex(lsNN), true
	}
	return false, false
}
