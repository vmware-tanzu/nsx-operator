/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Gateway admission hostname filtering (Gateway + ListenerSet scope vs route hostnames).
// Semantics mirror sigs.k8s.io/external-dns/source/gateway.go matchRouteToListener (gwMatchingHost,
// skip when listener host and route host are both empty). Attribution: see package doc.go.

package source

import (
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	extann "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/annotations"
)

func appendAdmissionHostnameFiltersFromGatewayListeners(listeners []gatewayv1.Listener, seen map[string]struct{}, out []string) []string {
	for i := range listeners {
		out = appendAdmissionHostnameFiltersFromListenerHostname(listeners[i].Hostname, seen, out)
	}
	return out
}

func appendAdmissionHostnameFiltersFromListenerEntry(e *gatewayv1.ListenerEntry, seen map[string]struct{}, out []string) []string {
	if e == nil {
		return out
	}
	return appendAdmissionHostnameFiltersFromListenerHostname(e.Hostname, seen, out)
}

func appendAdmissionHostnameFiltersFromListenerHostname(hostname *gatewayv1.Hostname, seen map[string]struct{}, out []string) []string {
	if hostname == nil {
		return appendAdmissionHostnameFilterString("", seen, out)
	}
	h := strings.TrimSpace(string(*hostname))
	if h == "" {
		return appendAdmissionHostnameFilterString("", seen, out)
	}
	if c, ok := GatewayCanonicalHost(h); ok {
		return appendAdmissionHostnameFilterString(c, seen, out)
	}
	return out
}

func appendAdmissionHostnameFilterString(s string, seen map[string]struct{}, out []string) []string {
	if _, ok := seen[s]; ok {
		return out
	}
	seen[s] = struct{}{}
	return append(out, s)
}

// RouteHostnamesMatchingAdmission maps raw route hosts to admitted DNS names via GwMatchingHost (empty+empty pair skipped).
// Non-empty route host: pick most specific canonical host; empty route host: keep distinct matches. Wildcards dropped unless RouteHostnameWildcardAllowed.
func RouteHostnamesMatchingAdmission(allowed []string, routeMeta *metav1.ObjectMeta, rawRouteHostnames []string, gatewayHostnameSourceAnnoKey, hostnameAnnoKey string) ([]string, error) {
	if len(allowed) == 0 || len(rawRouteHostnames) == 0 {
		return nil, nil
	}
	allowWildcardDNSName := RouteHostnameWildcardAllowed(routeMeta, gatewayHostnameSourceAnnoKey, hostnameAnnoKey)
	var out []string
	seen := make(map[string]struct{})
	for _, rtHost := range rawRouteHostnames {
		for _, best := range admissionMatchesForRouteHost(allowed, rtHost) {
			if strings.HasPrefix(best, "*.") && !allowWildcardDNSName {
				continue
			}
			if _, ok := seen[best]; ok {
				continue
			}
			seen[best] = struct{}{}
			out = append(out, best)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return NormalizeHostnameStrings(out), nil
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

// admissionMatchesForRouteHost returns allowed canonical hosts for one route hostname token (ExternalDNS matchRouteToListener semantics).
func admissionMatchesForRouteHost(allowed []string, rtHost string) []string {
	seen := make(map[string]struct{})
	var candidates []string
	for _, f := range allowed {
		if f == "" && rtHost == "" {
			continue
		}
		h, ok := GwMatchingHost(f, rtHost)
		if !ok || h == "" {
			continue
		}
		if _, dup := seen[h]; dup {
			continue
		}
		seen[h] = struct{}{}
		candidates = append(candidates, h)
	}
	if len(candidates) <= 1 {
		return candidates
	}
	if rtHost == "" {
		return candidates
	}
	best := candidates[0]
	for i := 1; i < len(candidates); i++ {
		if hostnameMoreSpecific(candidates[i], best) {
			best = candidates[i]
		}
	}
	return []string{best}
}

// HostnameMoreSpecific reports whether a should be preferred over b for DNS admission tie-breaking
// (listener filter strings or canonical hostnames).
func HostnameMoreSpecific(a, b string) bool {
	return hostnameMoreSpecific(a, b)
}

// hostnameMoreSpecific reports whether a should be preferred over b for DNS admission tie-breaking.
func hostnameMoreSpecific(a, b string) bool {
	if a == b {
		return false
	}
	ra, rb := hostnameSpecificityRank(a), hostnameSpecificityRank(b)
	if ra != rb {
		return ra > rb
	}
	return a < b
}

// BestMatchingAdmissionFilter returns the winning filter for rtHost via GwMatchingHost; (_, false) if none. Matched filter may be "".
func BestMatchingAdmissionFilter(allowed []string, rtHost string) (string, bool) {
	type cand struct {
		f  string
		ch string
	}
	seen := make(map[string]struct{})
	var cands []cand
	for _, f := range allowed {
		if f == "" && rtHost == "" {
			continue
		}
		ch, ok := GwMatchingHost(f, rtHost)
		if !ok || ch == "" {
			continue
		}
		key := f + "\x00" + ch
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		cands = append(cands, cand{f: f, ch: ch})
	}
	if len(cands) == 0 {
		return "", false
	}
	if rtHost == "" {
		return cands[0].f, true
	}
	best := cands[0]
	for i := 1; i < len(cands); i++ {
		c := cands[i]
		if hostnameMoreSpecific(c.ch, best.ch) {
			best = c
		} else if c.ch == best.ch && hostnameMoreSpecific(c.f, best.f) {
			best = c
		}
	}
	return best.f, true
}

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

func hostnameSpecificityRank(h string) int {
	if h == "" {
		return -1
	}
	labels := strings.Count(h, ".") + 1
	r := labels * 4
	if strings.HasPrefix(h, "*.") {
		r -= 2
	}
	return r
}
