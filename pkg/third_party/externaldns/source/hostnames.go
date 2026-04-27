/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Hostname helpers for Gateway / ListenerSet / routes. Attribution: see package doc.go.
package source

import (
	"strings"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func normalizeHostnamesList(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, h := range in {
		h = strings.TrimSpace(h)
		if h != "" {
			out = append(out, h)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// NormalizeHostnameStrings trims each entry and drops empty strings. Use for comparing or
// deduplicating hostname lists after RouteHostnames or other merges.
func NormalizeHostnameStrings(in []string) []string {
	return normalizeHostnamesList(in)
}

// RouteSpecHostnames converts HTTPRoute/GRPCRoute/TLSRoute Spec.Hostnames to trimmed, non-empty strings.
func RouteSpecHostnames(hostnames []gatewayv1.Hostname) []string {
	raw := make([]string, 0, len(hostnames))
	for i := range hostnames {
		raw = append(raw, string(hostnames[i]))
	}
	return normalizeHostnamesList(raw)
}
