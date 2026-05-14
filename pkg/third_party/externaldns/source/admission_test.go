/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package source

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestAdmissionFilterStringForHostname(t *testing.T) {
	tests := []struct {
		name string
		h    *gatewayv1.Hostname
		want string
		ok   bool
	}{
		{"nil-hostname", nil, "", true},
		{"empty-string", (*gatewayv1.Hostname)(ptr("   ")), "", true},
		{"valid-hostname", (*gatewayv1.Hostname)(ptr("foo.com")), "foo.com", true},
		{"invalid-hostname", (*gatewayv1.Hostname)(ptr("invalid_hostname")), "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := admissionFilterStringForHostname(tc.h)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func ptr[T any](v T) *T {
	return &v
}

func TestBuildAdmissionHostCacheRows(t *testing.T) {
	h1 := gatewayv1.Hostname("foo.com")
	h2 := gatewayv1.Hostname("*.bar.com")
	h3 := gatewayv1.Hostname("invalid_hostname")
	gw := &gatewayv1.Gateway{
		Spec: gatewayv1.GatewaySpec{
			Listeners: []gatewayv1.Listener{
				{Name: "l1", Hostname: &h1},
				{Name: "l2", Hostname: &h2},
				{Name: "l_invalid", Hostname: &h3},
			},
		},
	}
	ls := []gatewayv1.ListenerSet{
		{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "ls1"},
			Spec: gatewayv1.ListenerSetSpec{
				Listeners: []gatewayv1.ListenerEntry{
					{Name: "l3", Hostname: &h1},
					{Name: "l_invalid_ls", Hostname: &h3},
				},
			},
		},
	}

	// test with nil gateway
	assert.Nil(t, BuildAdmissionHostCacheRows(nil, ls))

	rows := BuildAdmissionHostCacheRows(gw, ls)
	require.Len(t, rows, 3) // invalid hostnames should be skipped
	assert.Equal(t, "foo.com", rows[0].Filter)
	assert.Equal(t, "*.bar.com", rows[1].Filter)
	assert.Equal(t, "foo.com", rows[2].Filter)
	assert.True(t, rows[2].FromListenerSet)
	assert.Equal(t, "ns1", rows[2].ListenerSet.Namespace)
}

func TestAdmissionHostnameFiltersForRouteParentFromRows(t *testing.T) {
	rows := []AdmissionHostCacheRow{
		{FromListenerSet: false, Section: "l1", Filter: "foo.com"},
		{FromListenerSet: true, ListenerSet: types.NamespacedName{Namespace: "ns1", Name: "ls1"}, Section: "l2", Filter: "bar.com"},
		{FromListenerSet: true, ListenerSet: types.NamespacedName{Namespace: "ns1", Name: "ls1"}, Section: "l2", Filter: "bar.com"}, // duplicate
	}
	gwGroup := gatewayv1.Group(gatewayv1.GroupName)
	gwKind := gatewayv1.Kind("Gateway")
	lsKind := gatewayv1.Kind("ListenerSet")

	refGW := &gatewayv1.ParentReference{Group: &gwGroup, Kind: &gwKind, Name: "gw1"}
	assert.Equal(t, []string{"foo.com"}, AdmissionHostnameFiltersForRouteParentFromRows(rows, refGW, "ns1"))

	refLS := &gatewayv1.ParentReference{Group: &gwGroup, Kind: &lsKind, Name: "ls1"}
	assert.Equal(t, []string{"bar.com"}, AdmissionHostnameFiltersForRouteParentFromRows(rows, refLS, "ns1"))

	// test with nil ref
	assert.Nil(t, AdmissionHostnameFiltersForRouteParentFromRows(rows, nil, "ns1"))

	// test with section name
	secName := gatewayv1.SectionName("l1")
	refGWWithSec := &gatewayv1.ParentReference{Group: &gwGroup, Kind: &gwKind, Name: "gw1", SectionName: &secName}
	assert.Equal(t, []string{"foo.com"}, AdmissionHostnameFiltersForRouteParentFromRows(rows, refGWWithSec, "ns1"))

	secNameEmpty := gatewayv1.SectionName("")
	refGWWithEmptySec := &gatewayv1.ParentReference{Group: &gwGroup, Kind: &gwKind, Name: "gw1", SectionName: &secNameEmpty}
	assert.Equal(t, []string{"foo.com"}, AdmissionHostnameFiltersForRouteParentFromRows(rows, refGWWithEmptySec, "ns1"))

	secNameMismatch := gatewayv1.SectionName("nomatch")
	refGWWithSecMismatch := &gatewayv1.ParentReference{Group: &gwGroup, Kind: &gwKind, Name: "gw1", SectionName: &secNameMismatch}
	assert.Empty(t, AdmissionHostnameFiltersForRouteParentFromRows(rows, refGWWithSecMismatch, "ns1"))

	// test with mismatching listener set name
	refLSMismatch := &gatewayv1.ParentReference{Group: &gwGroup, Kind: &lsKind, Name: "ls2"}
	assert.Empty(t, AdmissionHostnameFiltersForRouteParentFromRows(rows, refLSMismatch, "ns1"))

	// test with unknown kind
	unknownKind := gatewayv1.Kind("Unknown")
	refUnknown := &gatewayv1.ParentReference{Group: &gwGroup, Kind: &unknownKind, Name: "unknown"}
	assert.Nil(t, AdmissionHostnameFiltersForRouteParentFromRows(rows, refUnknown, "ns1"))
}

func TestRouteHostnamesMatchingAdmission(t *testing.T) {
	allowed := []string{"*.example.com", "foo.bar.com"}
	meta := &metav1.ObjectMeta{
		Annotations: map[string]string{
			"gateway-hostname-source": "annotation-only",
			"hostname":                "*.example.com",
		},
	}
	tests := []struct {
		hosts   []string
		allowed []string
		want    []string
	}{
		{[]string{"test.example.com"}, allowed, []string{"test.example.com"}},
		{[]string{"foo.bar.com"}, allowed, []string{"foo.bar.com"}},
		{[]string{"baz.com"}, allowed, nil},
		{[]string{"*.example.com"}, allowed, []string{"*.example.com"}},
		{[]string{"*.example.com"}, []string{"foo.bar.com"}, nil},                                 // no match
		{[]string{"test.example.com", "test.example.com"}, allowed, []string{"test.example.com"}}, // duplicate
		{nil, allowed, nil},
		{[]string{"test.example.com"}, nil, nil},
	}
	for _, tc := range tests {
		got, err := RouteHostnamesMatchingAdmission(tc.allowed, meta, tc.hosts, "gateway-hostname-source", "hostname")
		require.NoError(t, err)
		assert.Equal(t, tc.want, got)
	}

	// test wildcard not allowed
	got, err := RouteHostnamesMatchingAdmission(allowed, nil, []string{"*.example.com"}, "gateway-hostname-source", "hostname")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestHostnameMoreSpecific(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"foo.com", "foo.com", false},
		{"foo.com", "*.com", true},
		{"*.com", "foo.com", false},
		{"a.com", "b.com", true},
		{"b.com", "a.com", false},
		{"", "a.com", false},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, HostnameMoreSpecific(tc.a, tc.b), "a: %s, b: %s", tc.a, tc.b)
	}
}

func TestBestMatchingAdmissionFilter(t *testing.T) {
	allowed := []string{"*.example.com", "test.example.com"}
	tests := []struct {
		allowed []string
		rtHost  string
		want    string
		wantOk  bool
	}{
		{allowed, "test.example.com", "test.example.com", true},
		{allowed, "foo.example.com", "*.example.com", true},
		{allowed, "baz.com", "", false},
		{[]string{""}, "", "", false},
		{nil, "foo.com", "", false},
		{allowed, "", "*.example.com", true},
	}
	for _, tc := range tests {
		got, ok := BestMatchingAdmissionFilter(tc.allowed, tc.rtHost)
		assert.Equal(t, tc.wantOk, ok)
		assert.Equal(t, tc.want, got)
	}
}

func TestAdmissionMatchesForRouteHost(t *testing.T) {
	allowed := []string{"*.example.com", "test.example.com", "foo.bar.com"}
	tests := []struct {
		name    string
		allowed []string
		rtHost  string
		want    []string
	}{
		{"exact-match", allowed, "test.example.com", []string{"test.example.com"}},
		{"wildcard-match", allowed, "foo.example.com", []string{"foo.example.com"}},
		{"no-match", allowed, "baz.com", nil},
		{"empty-allowed", nil, "test.example.com", nil},
		{"empty-rtHost", allowed, "", []string{"*.example.com", "test.example.com", "foo.bar.com"}},
		{"duplicate-candidates", []string{"*.example.com", "*.example.com"}, "foo.example.com", []string{"foo.example.com"}},
		{"multiple-candidates", []string{"*.example.com", "*.test.example.com"}, "foo.test.example.com", []string{"foo.test.example.com"}},
		{"both-empty", []string{""}, "", nil},
		{"empty-filter-not-empty-host", []string{""}, "foo.com", []string{"foo.com"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, admissionMatchesForRouteHost(tc.allowed, tc.rtHost))
		})
	}
}
