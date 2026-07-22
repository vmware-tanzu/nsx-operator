/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package source

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestRouteSpecHostnames(t *testing.T) {
	h1 := gatewayv1.Hostname("foo.com")
	h2 := gatewayv1.Hostname("  bar.com  ")
	h3 := gatewayv1.Hostname("")

	res := RouteSpecHostnames([]gatewayv1.Hostname{h1, h2, h3})
	assert.Equal(t, []string{"foo.com", "bar.com"}, res)

	resEmpty := RouteSpecHostnames([]gatewayv1.Hostname{h3})
	assert.Nil(t, resEmpty)
}

func TestRouteHasExternalDNSHostnameAnnotation(t *testing.T) {
	tests := []struct {
		name string
		meta *metav1.ObjectMeta
		want bool
	}{
		{"valid", &metav1.ObjectMeta{Annotations: map[string]string{"hostname": "anno.com"}}, true},
		{"empty", &metav1.ObjectMeta{Annotations: map[string]string{"hostname": "  "}}, false},
		{"nil", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, routeHasExternalDNSHostnameAnnotation(tc.meta, "hostname"))
		})
	}
}

func TestRouteHostnameWildcardAllowed(t *testing.T) {
	tests := []struct {
		name string
		meta *metav1.ObjectMeta
		want bool
	}{
		{"allowed", &metav1.ObjectMeta{Annotations: map[string]string{"gateway-hostname-source": "annotation-only", "hostname": "anno.com"}}, true},
		{"wrong-source", &metav1.ObjectMeta{Annotations: map[string]string{"gateway-hostname-source": "defined-hosts-only", "hostname": "anno.com"}}, false},
		{"missing-hostname", &metav1.ObjectMeta{Annotations: map[string]string{"gateway-hostname-source": "annotation-only"}}, false},
		{"nil-meta", nil, false},
		{"empty-annotations", &metav1.ObjectMeta{Annotations: map[string]string{}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, RouteHostnameWildcardAllowed(tc.meta, "gateway-hostname-source", "hostname"))
		})
	}
}

func TestToLowerCaseASCII(t *testing.T) {
	assert.Equal(t, "foo.com", ToLowerCaseASCII("Foo.com"))
	assert.Equal(t, "foo.com", ToLowerCaseASCII("foo.com"))
}

func TestGwMatchingHost(t *testing.T) {
	tests := []struct {
		a, b string
		want string
		ok   bool
	}{
		{"*.example.com", "foo.example.com", "foo.example.com", true},
		{"foo.example.com", "*.example.com", "foo.example.com", true},
		{"foo.com", "bar.com", "", false},
		{"", "foo.com", "foo.com", true},
	}
	for _, tt := range tests {
		got, ok := GwMatchingHost(tt.a, tt.b)
		assert.Equal(t, tt.ok, ok)
		assert.Equal(t, tt.want, got)
	}
}

func TestRouteHostnames(t *testing.T) {
	tests := []struct {
		name                        string
		meta                        *metav1.ObjectMeta
		specHostnames               []string
		ignoreGatewayHostnameSource bool
		want                        []string
	}{
		{
			"annotation-only",
			&metav1.ObjectMeta{Annotations: map[string]string{"gateway-hostname-source": "annotation-only", "hostname": "anno.com"}},
			[]string{"spec.com"}, false, []string{"anno.com"},
		},
		{
			"annotation-only-ignore",
			&metav1.ObjectMeta{Annotations: map[string]string{"gateway-hostname-source": "annotation-only", "hostname": "anno.com"}},
			[]string{"spec.com"}, true, []string{},
		},
		{
			"defined-hosts-only",
			&metav1.ObjectMeta{Annotations: map[string]string{"gateway-hostname-source": "defined-hosts-only", "hostname": "anno.com"}},
			[]string{"spec.com"}, false, []string{"spec.com"},
		},
		{
			"invalid-annotation",
			&metav1.ObjectMeta{Annotations: map[string]string{"gateway-hostname-source": "invalid", "hostname": "anno.com"}},
			[]string{"spec.com"}, false, []string{"spec.com", "anno.com"},
		},
		{
			"fallback-behavior",
			&metav1.ObjectMeta{Annotations: map[string]string{"hostname": "anno.com"}},
			[]string{"spec.com"}, false, []string{"spec.com", "anno.com"},
		},
		{
			"fallback-empty-spec",
			&metav1.ObjectMeta{Annotations: map[string]string{"hostname": "anno.com"}},
			[]string{}, false, []string{"anno.com"},
		},
		{
			"fallback-ignore-anno",
			&metav1.ObjectMeta{Annotations: map[string]string{"hostname": "anno.com"}},
			[]string{"spec.com"}, true, []string{"spec.com"},
		},
		{
			"nil-meta",
			nil,
			[]string{"spec.com"}, false, []string{"spec.com"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := RouteHostnames(tc.meta, tc.specHostnames, "gateway-hostname-source", "hostname", tc.ignoreGatewayHostnameSource)
			assert.Equal(t, tc.want, res)
		})
	}
}
