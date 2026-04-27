/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package source

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRouteHostnamesMatchingAdmission_table(t *testing.T) {
	meta := func(ann map[string]string) *metav1.ObjectMeta {
		return &metav1.ObjectMeta{Name: "r1", Namespace: "ns", Annotations: ann}
	}
	tests := []struct {
		name          string
		routeHosts    []string
		m             *metav1.ObjectMeta
		listenerHosts []string
		want          []string
		wantNil       bool
		elementsMatch bool
	}{
		{name: "skipsBothEmpty", routeHosts: []string{""}, m: meta(nil), listenerHosts: []string{""}, wantNil: true},
		{name: "emptyRouteHostMultiListener", routeHosts: []string{"a.example.com", "b.example.com"}, m: meta(nil), listenerHosts: []string{""}, want: []string{"a.example.com", "b.example.com"}, elementsMatch: true},
		{name: "wildcardDroppedWithoutAnnotation", routeHosts: []string{""}, m: meta(nil), listenerHosts: []string{"*.example.com"}, wantNil: true},
		{
			name: "wildcardDroppedWhenHostnameAnnoButNotAnnotationOnlySource", routeHosts: []string{""},
			m: meta(map[string]string{
				testDNSRouteKeyGatewayHostnameSource: GatewayHostnameSourceDefinedHostsOnly,
				testDNSRouteKeyHostname:              "explicit.example.org",
			}),
			listenerHosts: []string{"*.example.com"}, wantNil: true,
		},
		{
			name: "wildcardAllowedWithHostnameAnnotation", routeHosts: []string{""},
			m: meta(map[string]string{
				testDNSRouteKeyGatewayHostnameSource: GatewayHostnameSourceAnnotationOnly,
				testDNSRouteKeyHostname:              "explicit.example.org",
			}),
			listenerHosts: []string{"*.example.com"}, want: []string{"*.example.com"},
		},
		{name: "noAllowed", routeHosts: nil, m: meta(nil), listenerHosts: []string{"foo.com"}, wantNil: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RouteHostnamesMatchingAdmission(tt.routeHosts, tt.m, tt.listenerHosts, testDNSRouteKeyGatewayHostnameSource, testDNSRouteKeyHostname)
			require.NoError(t, err)
			if tt.wantNil {
				assert.Nil(t, got)
				return
			}
			if tt.elementsMatch {
				assert.ElementsMatch(t, tt.want, got)
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRouteHostnameWildcardAllowed_table(t *testing.T) {
	tests := []struct {
		name     string
		meta     *metav1.ObjectMeta
		wantTrue bool
	}{
		{name: "nil_meta", meta: nil},
		{name: "no_annotations", meta: &metav1.ObjectMeta{Name: "r"}},
		{
			name: "annotation_only_with_nonempty_hostname",
			meta: &metav1.ObjectMeta{Annotations: map[string]string{
				testDNSRouteKeyGatewayHostnameSource: GatewayHostnameSourceAnnotationOnly,
				testDNSRouteKeyHostname:              "*.example.com",
			}},
			wantTrue: true,
		},
		{
			name: "annotation_only_but_empty_hostname_anno",
			meta: &metav1.ObjectMeta{Annotations: map[string]string{
				testDNSRouteKeyGatewayHostnameSource: GatewayHostnameSourceAnnotationOnly,
				testDNSRouteKeyHostname:              "  ",
			}},
		},
		{
			name: "defined_hosts_only_with_hostname",
			meta: &metav1.ObjectMeta{Annotations: map[string]string{
				testDNSRouteKeyGatewayHostnameSource: GatewayHostnameSourceDefinedHostsOnly,
				testDNSRouteKeyHostname:              "x.example.com",
			}},
		},
		{
			name: "hostname_only_no_source_key",
			meta: &metav1.ObjectMeta{Annotations: map[string]string{
				testDNSRouteKeyHostname: "x.example.com",
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RouteHostnameWildcardAllowed(tt.meta, testDNSRouteKeyGatewayHostnameSource, testDNSRouteKeyHostname)
			assert.Equal(t, tt.wantTrue, got)
		})
	}
}

func TestBestMatchingAdmissionFilter_table(t *testing.T) {
	tests := []struct {
		name       string
		filters    []string
		host       string
		wantFilter string
		wantOK     bool
	}{
		{name: "empty_filter_matches_any", filters: []string{""}, host: "shared.example.com", wantFilter: "", wantOK: true},
		{name: "no_match", filters: []string{"other.example.com"}, host: "shared.example.com", wantOK: false},
		// GwMatchingHost treats equal canonical wildcard hosts as a match; BestMatchingAdmissionFilter does not forbid
		// wildcard FQDNs as rtHost — that policy lives in RouteHostnamesMatchingAdmission (allowWildcardDNSName) and in
		// dns.ValidateEndpointsByDNSZone (skip literal "*.…" DNS names for NSX rows).
		{name: "wildcard_filter_matches_concrete_host", filters: []string{"*.example.com"}, host: "app.example.com", wantFilter: "*.example.com", wantOK: true},
		{name: "wildcard_filter_matches_same_wildcard_host", filters: []string{"*.example.com"}, host: "*.example.com", wantFilter: "*.example.com", wantOK: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, ok := BestMatchingAdmissionFilter(tt.filters, tt.host)
			require.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.wantFilter, f)
			}
		})
	}
}
