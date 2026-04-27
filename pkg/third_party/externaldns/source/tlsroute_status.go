/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */
// Attribution: see package doc.go.

package source

import (
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// TLSRouteParentReadyForGateway is the TLSRoute equivalent of HTTPRouteParentReadyForGateway
// (Accepted=True for the parent Gateway ref).
func TLSRouteParentReadyForGateway(route *gatewayv1.TLSRoute, gw types.NamespacedName) bool {
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
