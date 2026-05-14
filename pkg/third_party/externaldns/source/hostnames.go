/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package source

import (
	"strings"
	"unicode/utf8"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	extann "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/annotations"
	extendpoint "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
)

// ToLowerCaseASCII returns a lower-case version of s. See RFC 6125 6.4.1. This is an explicitly ASCII
// function to avoid sharp corners from performing Unicode operations on DNS labels (ExternalDNS gateway path).
func ToLowerCaseASCII(s string) string {
	isAlreadyLowerCase := true
	for _, c := range s {
		if c == utf8.RuneError {
			isAlreadyLowerCase = false
			break
		}
		if 'A' <= c && c <= 'Z' {
			isAlreadyLowerCase = false
			break
		}
	}
	if isAlreadyLowerCase {
		return s
	}
	out := []byte(s)
	for i, c := range out {
		if 'A' <= c && c <= 'Z' {
			out[i] += 'a' - 'A'
		}
	}
	return string(out)
}

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

// RouteSpecHostnames converts HTTPRoute/GRPCRoute/TLSRoute Spec.Hostnames to trimmed, non-empty strings.
func RouteSpecHostnames(hostnames []gatewayv1.Hostname) []string {
	raw := make([]string, 0, len(hostnames))
	for i := range hostnames {
		raw = append(raw, string(hostnames[i]))
	}
	return normalizeHostnamesList(raw)
}

// GwMatchingHost returns the most-specific overlapping host and whether a match exists.
// Hostnames prefixed with a wildcard label (`*.`) are interpreted as a suffix match:
// "*.example.com" matches "test.example.com" and "foo.test.example.com", but not "example.com".
// An empty string matches anything (same semantics as ExternalDNS gateway source).
func GwMatchingHost(a, b string) (string, bool) {
	var ok bool
	if a, ok = gatewayCanonicalHost(a); !ok {
		return "", false
	}
	if b, ok = gatewayCanonicalHost(b); !ok {
		return "", false
	}
	if a == "" {
		return b, true
	}
	if b == "" || a == b {
		return a, true
	}
	if na, nb := len(a), len(b); nb < na || (na == nb && strings.HasPrefix(b, "*.")) {
		a, b = b, a
	}
	if strings.HasPrefix(a, "*.") && strings.HasSuffix(b, a[1:]) {
		return b, true
	}
	return "", false
}

// GatewayCanonicalHost returns the canonical lower-case ASCII hostname, or false if the value is an IP
// or not a valid DNS-1123 domain (after stripping a leading "*." for label validation).
func GatewayCanonicalHost(host string) (string, bool) {
	return gatewayCanonicalHost(host)
}

func gatewayCanonicalHost(host string) (string, bool) {
	if host == "" {
		return "", true
	}
	if isGatewayHostIP(host) || !isDNS1123Domain(strings.TrimPrefix(host, "*.")) {
		return "", false
	}
	return ToLowerCaseASCII(host), true
}

func isGatewayHostIP(s string) bool {
	return extendpoint.SuitableType(s) != extendpoint.RecordTypeCNAME
}

func isDNS1123Domain(s string) bool {
	if n := len(s); n == 0 || n > 255 {
		return false
	}
	for lbl, rest := "", s; rest != ""; {
		if lbl, rest, _ = strings.Cut(rest, "."); !isDNS1123Label(lbl) {
			return false
		}
	}
	return true
}

func isDNS1123Label(s string) bool {
	n := len(s)
	if n == 0 || n > 63 {
		return false
	}
	if !isAlphaNum(s[0]) || !isAlphaNum(s[n-1]) {
		return false
	}
	for i, k := 1, n-1; i < k; i++ {
		if b := s[i]; b != '-' && !isAlphaNum(b) {
			return false
		}
	}
	return true
}

func isAlphaNum(b byte) bool {
	switch {
	case 'a' <= b && b <= 'z',
		'A' <= b && b <= 'Z',
		'0' <= b && b <= '9':
		return true
	default:
		return false
	}
}

const (
	GatewayHostnameSourceAnnotationOnly        = "annotation-only"
	GatewayHostnameSourceDefinedHostsOnlyValue = "defined-hosts-only"
)

// RouteHostnameWildcardAllowed is true when the Route may emit a leading "*." DNS name: gateway-hostname-source
// must be explicitly [GatewayHostnameSourceAnnotationOnly] and the hostname annotation (hostnameAnnoKey) must list
// at least one non-empty hostname (annotation-only DNS hostname policy).
func RouteHostnameWildcardAllowed(meta *metav1.ObjectMeta, gatewayHostnameSourceAnnoKey, hostnameAnnoKey string) bool {
	if meta == nil || meta.Annotations == nil {
		return false
	}
	raw, ok := meta.Annotations[gatewayHostnameSourceAnnoKey]
	if !ok {
		return false
	}
	if strings.ToLower(strings.TrimSpace(raw)) != GatewayHostnameSourceAnnotationOnly {
		return false
	}
	return routeHasExternalDNSHostnameAnnotation(meta, hostnameAnnoKey)
}

// RouteHostnames resolves the desired hostnames for a route based on annotations and spec hostnames.
// If ignoreGatewayHostnameSource is false, it respects the annotation-only policy.
func RouteHostnames(meta *metav1.ObjectMeta, specHostnames []string, gatewayHostnameSourceAnnoKey, hostnameAnnoKey string, ignoreGatewayHostnameSource bool) []string {
	if meta == nil {
		return normalizeHostnamesList(specHostnames)
	}

	var hostnames []string
	hostnames = append(hostnames, specHostnames...)

	hostNameAnnotation, hostNameAnnotationExists := meta.Annotations[gatewayHostnameSourceAnnoKey]
	if !hostNameAnnotationExists {
		if len(hostnames) == 0 {
			hostnames = append(hostnames, "")
		}
		if !ignoreGatewayHostnameSource {
			hostnames = append(hostnames, extann.HostnamesFromAnnotations(meta.Annotations, hostnameAnnoKey)...)
		}
		return normalizeHostnamesList(hostnames)
	}

	switch strings.ToLower(hostNameAnnotation) {
	case GatewayHostnameSourceAnnotationOnly:
		if ignoreGatewayHostnameSource {
			return []string{}
		}
		return normalizeHostnamesList(extann.HostnamesFromAnnotations(meta.Annotations, hostnameAnnoKey))
	case GatewayHostnameSourceDefinedHostsOnlyValue:
		return normalizeHostnamesList(hostnames)
	default:
		// Invalid value provided: fall back to default behavior (as if the annotation is absent)
		if len(hostnames) == 0 {
			hostnames = append(hostnames, "")
		}
		if !ignoreGatewayHostnameSource {
			hostnames = append(hostnames, extann.HostnamesFromAnnotations(meta.Annotations, hostnameAnnoKey)...)
		}
		return normalizeHostnamesList(hostnames)
	}
}

func routeHasExternalDNSHostnameAnnotation(meta *metav1.ObjectMeta, hostnameKey string) bool {
	if meta == nil {
		return false
	}
	for _, h := range extann.HostnamesFromAnnotations(meta.GetAnnotations(), hostnameKey) {
		if strings.TrimSpace(h) != "" {
			return true
		}
	}
	return false
}
