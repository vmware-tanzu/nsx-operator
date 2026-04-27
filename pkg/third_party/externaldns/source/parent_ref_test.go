/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package source

import (
	"testing"

	"github.com/stretchr/testify/require"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestParentReferencesSemanticallyEqual_gateway(t *testing.T) {
	gg := gatewayv1.Group(gatewayv1.GroupName)
	kk := gatewayv1.Kind("Gateway")
	a := gatewayv1.ParentReference{Group: &gg, Kind: &kk, Name: "gw1"}
	b := gatewayv1.ParentReference{Group: &gg, Kind: &kk, Name: "gw1"}
	require.True(t, ParentReferencesSemanticallyEqual(&a, &b, "ns1"))
	b.Name = "gw2"
	require.False(t, ParentReferencesSemanticallyEqual(&a, &b, "ns1"))
}
