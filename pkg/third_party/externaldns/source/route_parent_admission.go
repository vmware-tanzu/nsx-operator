/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Route parent admission filters (Gateway / ListenerSet + SectionName).
// Mirrors sigs.k8s.io/external-dns/source/gateway.go gatewaySections and listenerSetSections:
// ParentReference.SectionName selects one listener entry; absent/empty SectionName means all
// listeners on that parent object.

package source

import (
	"strings"

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

// BuildAdmissionHostCacheRows returns per-listener rows for gw and listenerSets (same order as
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

// AdmissionHostnameFiltersForRouteParentFromRows returns admission hostname filters for ref using
// precomputed rows from BuildAdmissionHostCacheRows (same semantics as AdmissionHostnameFiltersForRouteParent).
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

// AdmissionHostnameFiltersForRouteParent returns admission hostname filters for listeners on the
// Gateway or ListenerSet referenced by ref. When SectionName is set, only that listener section is
// included (same resolution as ExternalDNS gatewayRouteResolver.resolveParentRef). gw must be set
// when ref targets a Gateway; ls must be set when ref targets a ListenerSet.
func AdmissionHostnameFiltersForRouteParent(gw *gatewayv1.Gateway, ls *gatewayv1.ListenerSet, ref *gatewayv1.ParentReference, routeNamespace string) []string {
	if ref == nil {
		return nil
	}
	section := parentRefSectionName(ref)
	if _, ok := GatewayNamespacedNameFromParentRef(ref, routeNamespace); ok {
		if gw == nil {
			return nil
		}
		listeners := gatewayListenersForSection(gw.Spec.Listeners, section)
		seen := make(map[string]struct{})
		var out []string
		return appendAdmissionHostnameFiltersFromGatewayListeners(listeners, seen, out)
	}
	if _, ok := ListenerSetNamespacedNameFromParentRef(ref, routeNamespace); ok {
		if ls == nil {
			return nil
		}
		entries := listenerEntriesForSection(ls.Spec.Listeners, section)
		seen := make(map[string]struct{})
		var out []string
		for i := range entries {
			out = appendAdmissionHostnameFiltersFromListenerEntry(&entries[i], seen, out)
		}
		return out
	}
	return nil
}

func gatewayListenersForSection(listeners []gatewayv1.Listener, section gatewayv1.SectionName) []gatewayv1.Listener {
	if section == "" {
		return listeners
	}
	for i := range listeners {
		if listeners[i].Name == section {
			return []gatewayv1.Listener{listeners[i]}
		}
	}
	return nil
}

func listenerEntriesForSection(entries []gatewayv1.ListenerEntry, section gatewayv1.SectionName) []gatewayv1.ListenerEntry {
	if section == "" {
		return entries
	}
	for i := range entries {
		if entries[i].Name == section {
			return []gatewayv1.ListenerEntry{entries[i]}
		}
	}
	return nil
}
