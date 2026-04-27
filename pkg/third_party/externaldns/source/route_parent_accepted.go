/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package source

import (
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

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
