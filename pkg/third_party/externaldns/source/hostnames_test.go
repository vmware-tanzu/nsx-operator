/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package source

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	extann "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/annotations"
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
		{"valid", &metav1.ObjectMeta{Annotations: map[string]string{extann.HostnameKey: "anno.com"}}, true},
		{"empty", &metav1.ObjectMeta{Annotations: map[string]string{extann.HostnameKey: "  "}}, false},
		{"nil", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var annos map[string]string
			if tc.meta != nil {
				annos = tc.meta.Annotations
			}
			assert.Equal(t, tc.want, routeHasExternalDNSHostnameAnnotation(annos))
		})
	}
}

func TestRouteHostnameWildcardAllowed(t *testing.T) {
	tests := []struct {
		name  string
		annos map[string]string
		want  bool
	}{
		{
			name: "allowed-stable",
			annos: map[string]string{
				extann.GatewayHostnameSourceKey: "annotation-only",
				extann.HostnameKey:              "anno.com",
			},
			want: true,
		},
		{
			name: "allowed-alpha-fallback",
			annos: map[string]string{
				extann.LegacyGatewayHostnameSourceKey: "annotation-only",
				extann.LegacyHostnameKey:              "anno.com",
			},
			want: true,
		},
		{
			name: "wrong-source",
			annos: map[string]string{
				extann.GatewayHostnameSourceKey: "defined-hosts-only",
				extann.HostnameKey:              "anno.com",
			},
			want: false,
		},
		{
			name: "missing-hostname",
			annos: map[string]string{
				extann.GatewayHostnameSourceKey: "annotation-only",
			},
			want: false,
		},
		{name: "nil-annotations", annos: nil, want: false},
		{name: "empty-annotations", annos: map[string]string{}, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, RouteHostnameWildcardAllowed(tc.annos))
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
			"annotation-only-stable",
			&metav1.ObjectMeta{Annotations: map[string]string{
				extann.GatewayHostnameSourceKey: "annotation-only",
				extann.HostnameKey:              "anno.com",
			}},
			[]string{"spec.com"}, false, []string{"anno.com"},
		},
		{
			"annotation-only-ignore",
			&metav1.ObjectMeta{Annotations: map[string]string{
				extann.GatewayHostnameSourceKey: "annotation-only",
				extann.HostnameKey:              "anno.com",
			}},
			[]string{"spec.com"}, true, []string{},
		},
		{
			"defined-hosts-only",
			&metav1.ObjectMeta{Annotations: map[string]string{
				extann.GatewayHostnameSourceKey: "defined-hosts-only",
				extann.HostnameKey:              "anno.com",
			}},
			[]string{"spec.com"}, false, []string{"spec.com"},
		},
		{
			"invalid-annotation",
			&metav1.ObjectMeta{Annotations: map[string]string{
				extann.GatewayHostnameSourceKey: "invalid",
				extann.HostnameKey:              "anno.com",
			}},
			[]string{"spec.com"}, false, []string{"spec.com", "anno.com"},
		},
		{
			"fallback-behavior",
			&metav1.ObjectMeta{Annotations: map[string]string{
				extann.HostnameKey: "anno.com",
			}},
			[]string{"spec.com"}, false, []string{"spec.com", "anno.com"},
		},
		{
			"fallback-empty-spec",
			&metav1.ObjectMeta{Annotations: map[string]string{
				extann.HostnameKey: "anno.com",
			}},
			[]string{}, false, []string{"anno.com"},
		},
		{
			"fallback-ignore-anno",
			&metav1.ObjectMeta{Annotations: map[string]string{
				extann.HostnameKey: "anno.com",
			}},
			[]string{"spec.com"}, true, []string{"spec.com"},
		},
		{
			"nil-meta",
			nil,
			[]string{"spec.com"}, false, []string{"spec.com"},
		},
		{
			"alpha-hostname-only",
			&metav1.ObjectMeta{Annotations: map[string]string{
				extann.LegacyHostnameKey: "alpha.com",
			}},
			[]string{}, false, []string{"alpha.com"},
		},
		{
			"both-hostname-stable-wins",
			&metav1.ObjectMeta{Annotations: map[string]string{
				extann.HostnameKey:       "stable.com",
				extann.LegacyHostnameKey: "alpha.com",
			}},
			[]string{}, false, []string{"stable.com"},
		},
		{
			"alpha-gateway-hostname-source-annotation-only",
			&metav1.ObjectMeta{Annotations: map[string]string{
				extann.LegacyGatewayHostnameSourceKey: "annotation-only",
				extann.LegacyHostnameKey:              "alpha.com",
			}},
			[]string{"spec.com"}, false, []string{"alpha.com"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := RouteHostnames(tc.meta, tc.specHostnames, tc.ignoreGatewayHostnameSource)
			assert.Equal(t, tc.want, res)
		})
	}
}
