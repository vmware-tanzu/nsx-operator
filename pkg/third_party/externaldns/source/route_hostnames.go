/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Route hostname merging follows source/gateway.go (*gatewayRouteResolver).hosts():
// spec hostnames, optional FQDN-template hosts, then gateway-hostname-source + hostname annotations.
// Attribution: see package doc.go.

package source

import (
	"log/slog"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	extann "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/annotations"
)

const (
	// GatewayHostnameSourceAnnotationOnly matches external-dns gatewayHostnameSourceAnnotationOnlyValue.
	GatewayHostnameSourceAnnotationOnly = "annotation-only"
	// GatewayHostnameSourceDefinedHostsOnly matches external-dns gatewayHostnameSourceDefinedHostsOnlyValue.
	GatewayHostnameSourceDefinedHostsOnly = "defined-hosts-only"
)

// RouteHostnames is RouteHostnamesForRoute with no FQDN-template hostnames (ExternalDNS gateway-hostname-source rules).
func RouteHostnames(meta *metav1.ObjectMeta, specHostnames []string, gatewayHostnameSourceAnnoKey, hostnameAnnoKey string, ignoreHostnameAnnotation bool) []string {
	return RouteHostnamesForRoute(meta, specHostnames, nil, gatewayHostnameSourceAnnoKey, hostnameAnnoKey, ignoreHostnameAnnotation)
}

// RouteHostnamesForRoute merges spec + optional fqdnTemplate hostnames, then gateway-hostname-source + hostname annotations (see gateway.go hosts()).
// routeSpecHostnamesEmpty must match len(specHostnames)==0 before templates (ExternalDNS empty-hostname branch).
func RouteHostnamesForRoute(meta *metav1.ObjectMeta, specHostnames, fqdnTemplateHostnames []string, gatewayHostnameSourceAnnoKey, hostnameAnnoKey string, ignoreHostnameAnnotation bool) []string {
	hostnames := append([]string(nil), specHostnames...)
	if len(fqdnTemplateHostnames) > 0 {
		hostnames = append(hostnames, fqdnTemplateHostnames...)
	}
	routeSpecHostnamesEmpty := len(specHostnames) == 0
	return mergeGatewayRouteHostnameAnnotations(meta, hostnames, routeSpecHostnamesEmpty, ignoreHostnameAnnotation, gatewayHostnameSourceAnnoKey, hostnameAnnoKey)
}

func mergeGatewayRouteHostnameAnnotations(meta *metav1.ObjectMeta, hostnames []string, routeSpecHostnamesEmpty bool, ignoreHostnameAnnotation bool, gatewayHostnameSourceAnnoKey, hostnameAnnoKey string) []string {
	if meta == nil {
		return hostnames
	}
	ann := meta.GetAnnotations()
	if ann == nil {
		ann = map[string]string{}
	}
	raw, hasKey := ann[gatewayHostnameSourceAnnoKey]
	if !hasKey {
		return appendAnnotationHostsDefault(hostnames, ann, routeSpecHostnamesEmpty, ignoreHostnameAnnotation, hostnameAnnoKey)
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case GatewayHostnameSourceAnnotationOnly:
		if ignoreHostnameAnnotation {
			return []string{}
		}
		h := extann.HostnamesFromAnnotations(ann, hostnameAnnoKey)
		if h == nil {
			return []string{}
		}
		return h
	case GatewayHostnameSourceDefinedHostsOnly:
		return hostnames
	default:
		slog.Default().Warn("invalid gateway-hostname-source, falling back to default behavior",
			"annotation", gatewayHostnameSourceAnnoKey,
			"namespace", meta.Namespace,
			"name", meta.Name,
			"value", raw,
		)
		return appendAnnotationHostsDefault(hostnames, ann, routeSpecHostnamesEmpty, ignoreHostnameAnnotation, hostnameAnnoKey)
	}
}

// appendAnnotationHostsDefault merges hostname annotation values (caller-selected hostnameKey) with
// spec/template hostnames for the default gateway-hostname-source path (same merge order semantics as
// ExternalDNS hosts() default branch; upstream uses fixed annotation key constants).
// Non-empty annotation hostnames are prepended so DNS admission and record generation prefer explicit
// operator overrides over route spec names (nsx-operator; upstream appends annotations after spec).
func appendAnnotationHostsDefault(hostnames []string, ann map[string]string, routeSpecHostnamesEmpty, ignoreHostnameAnnotation bool, hostnameKey string) []string {
	var out []string
	if !ignoreHostnameAnnotation {
		out = append(out, extann.HostnamesFromAnnotations(ann, hostnameKey)...)
	}
	if routeSpecHostnamesEmpty {
		out = append(out, "")
	}
	out = append(out, hostnames...)
	return out
}
