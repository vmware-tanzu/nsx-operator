/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package source

import (
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// AdmissionHostCacheRow is one listener's admission hostname filter for a root Gateway (Gateway
// listeners first in spec order, then each attached ListenerSet's entries in list order).
// FromListenerSet false means Gateway.spec.listeners; true means ListenerSet.spec.listeners for ListenerSet.
type AdmissionHostCacheRow struct {
	FromListenerSet bool
	ListenerSet     types.NamespacedName
	Section         gatewayv1.SectionName
	Filter          string
}

func admissionFilterStringForHostname(h *gatewayv1.Hostname) (string, bool) {
	if h == nil {
		return "", true
	}
	s := strings.TrimSpace(string(*h))
	if s == "" {
		return "", true
	}
	if c, ok := GatewayCanonicalHost(s); ok {
		return c, true
	}
	return "", false
}

// BuildAdmissionHostCacheRows returns per-listener rows for gw and listenerSets.
func BuildAdmissionHostCacheRows(gw *gatewayv1.Gateway, listenerSets []gatewayv1.ListenerSet) []AdmissionHostCacheRow {
	if gw == nil {
		return nil
	}
	var out []AdmissionHostCacheRow
	for i := range gw.Spec.Listeners {
		l := gw.Spec.Listeners[i]
		f, ok := admissionFilterStringForHostname(l.Hostname)
		if !ok {
			continue
		}
		out = append(out, AdmissionHostCacheRow{Section: l.Name, Filter: f})
	}
	for lsIdx := range listenerSets {
		ls := &listenerSets[lsIdx]
		lsNN := types.NamespacedName{Namespace: ls.Namespace, Name: ls.Name}
		for i := range ls.Spec.Listeners {
			e := &ls.Spec.Listeners[i]
			f, ok := admissionFilterStringForHostname(e.Hostname)
			if !ok {
				continue
			}
			out = append(out, AdmissionHostCacheRow{
				FromListenerSet: true,
				ListenerSet:     lsNN,
				Section:         e.Name,
				Filter:          f,
			})
		}
	}
	return out
}

// parentRefSectionName returns ref.SectionName when set and non-empty; otherwise zero means all sections.
func parentRefSectionName(ref *gatewayv1.ParentReference) gatewayv1.SectionName {
	if ref.SectionName != nil && *ref.SectionName != "" {
		return *ref.SectionName
	}
	return ""
}

// AdmissionHostnameFiltersForRouteParentFromRows returns admission hostname filters for ref using precomputed rows.
func AdmissionHostnameFiltersForRouteParentFromRows(rows []AdmissionHostCacheRow, ref *gatewayv1.ParentReference, routeNamespace string) []string {
	if ref == nil {
		return nil
	}
	section := parentRefSectionName(ref)
	seen := make(map[string]struct{})
	var out []string
	if _, ok := GatewayNamespacedNameFromParentRef(ref, routeNamespace); ok {
		for i := range rows {
			row := rows[i]
			if row.FromListenerSet {
				continue
			}
			if section != "" && row.Section != section {
				continue
			}
			out = appendAdmissionHostnameFilterString(row.Filter, seen, out)
		}
		return out
	}
	if lsNN, ok := ListenerSetNamespacedNameFromParentRef(ref, routeNamespace); ok {
		for i := range rows {
			row := rows[i]
			if !row.FromListenerSet {
				continue
			}
			if row.ListenerSet != lsNN {
				continue
			}
			if section != "" && row.Section != section {
				continue
			}
			out = appendAdmissionHostnameFilterString(row.Filter, seen, out)
		}
		return out
	}
	return nil
}

func appendAdmissionHostnameFilterString(s string, seen map[string]struct{}, out []string) []string {
	if _, ok := seen[s]; ok {
		return out
	}
	seen[s] = struct{}{}
	return append(out, s)
}

// RouteHostnamesMatchingAdmission maps raw route hosts to admitted DNS names via GwMatchingHost.
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
	return normalizeHostnamesList(out), nil
}

// admissionMatchesForRouteHost returns allowed canonical hosts for one route hostname token.
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

// HostnameMoreSpecific reports whether a should be preferred over b for DNS admission tie-breaking.
func HostnameMoreSpecific(a, b string) bool {
	return hostnameMoreSpecific(a, b)
}

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

// BestMatchingAdmissionFilter returns the winning filter for rtHost via GwMatchingHost; (_, false) if none.
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
