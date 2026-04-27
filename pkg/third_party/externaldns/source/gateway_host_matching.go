// Copyright 2021 The Kubernetes Authors.
// Copyright 2026 Broadcom, Inc.
//
// SPDX-License-Identifier: Apache-2.0
//
// Derived from sigs.k8s.io/external-dns/source/gateway.go (gwMatchingHost, gwHost, isIPAddr, DNS1123 helpers).
// Attribution: see package doc.go.

package source

import (
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"

	extendpoint "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
)

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

// --- nsx-operator: Gateway vs ListenerSet direct-DNS batch precedence (not upstream as a named API) ---

// ClaimGwMatchingDNSName inserts trimmed h into seen if it does not overlap any existing name under
// GwMatchingHost; returns false if h is empty or overlaps (including duplicate of an exact name in seen).
func ClaimGwMatchingDNSName(seen sets.Set[string], h string) bool {
	h = strings.TrimSpace(h)
	if h == "" {
		return false
	}
	if hostnameOverlapsGwMatchingSeen(seen, h) {
		return false
	}
	seen.Insert(h)
	return true
}

func hostnameOverlapsGwMatchingSeen(seen sets.Set[string], h string) bool {
	for s := range seen {
		if _, ok := GwMatchingHost(s, h); ok {
			return true
		}
	}
	return false
}
