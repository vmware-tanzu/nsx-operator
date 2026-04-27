/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package source

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	testDNSRouteKeyGatewayHostnameSource = "external-dns.alpha.kubernetes.io/gateway-hostname-source"
	testDNSRouteKeyHostname              = "external-dns.alpha.kubernetes.io/hostname"
)

func TestRouteHostnames_andForRoute_table(t *testing.T) {
	type mode int
	const (
		hostnames mode = iota
		forRoute
	)
	tests := []struct {
		mode            mode
		name            string
		ann             map[string]string
		spec, templates []string
		wantEqual       []string
		wantContains    []string
		wantFirst       string
		wantMinLen      int
	}{
		{hostnames, "defaultWithAnnotation", map[string]string{testDNSRouteKeyHostname: "ann.example.com"}, nil, nil, nil, []string{"", "ann.example.com"}, "ann.example.com", 0},
		{hostnames, "defaultAnnotationPrependedBeforeSpec", map[string]string{testDNSRouteKeyHostname: "ann.example.com"}, []string{"spec.example.com"}, nil, nil, []string{"ann.example.com", "spec.example.com"}, "ann.example.com", 2},
		{hostnames, "definedHostsOnlyIgnoresHostnameAnnotation", map[string]string{testDNSRouteKeyGatewayHostnameSource: GatewayHostnameSourceDefinedHostsOnly, testDNSRouteKeyHostname: "ignored.example.com"}, []string{"spec.example.com"}, nil, []string{"spec.example.com"}, nil, "", 0},
		{hostnames, "annotationOnly", map[string]string{testDNSRouteKeyGatewayHostnameSource: GatewayHostnameSourceAnnotationOnly, testDNSRouteKeyHostname: "only.example.com"}, []string{"spec.example.com"}, nil, []string{"only.example.com"}, nil, "", 0},
		{forRoute, "templateAppendsBeforeSource", map[string]string{testDNSRouteKeyGatewayHostnameSource: GatewayHostnameSourceDefinedHostsOnly}, []string{"spec.example.com"}, []string{"tpl.example.com"}, []string{"spec.example.com", "tpl.example.com"}, nil, "", 0},
		{forRoute, "emptySpecPlaceholderUsesSpecOnly", map[string]string{testDNSRouteKeyHostname: "ann.example.com"}, nil, []string{"from.template.example.com"}, nil, []string{"", "ann.example.com", "from.template.example.com"}, "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := &metav1.ObjectMeta{Namespace: "ns1", Name: "r1", Annotations: tt.ann}
			var h []string
			if tt.mode == hostnames {
				h = RouteHostnames(meta, tt.spec, testDNSRouteKeyGatewayHostnameSource, testDNSRouteKeyHostname, false)
			} else {
				h = RouteHostnamesForRoute(meta, tt.spec, tt.templates, testDNSRouteKeyGatewayHostnameSource, testDNSRouteKeyHostname, false)
			}
			if len(tt.wantEqual) > 0 {
				assert.Equal(t, tt.wantEqual, h)
				return
			}
			for _, w := range tt.wantContains {
				assert.Contains(t, h, w)
			}
			if tt.wantMinLen > 0 {
				require.GreaterOrEqual(t, len(h), tt.wantMinLen)
			}
			if tt.wantFirst != "" {
				require.NotEmpty(t, h)
				assert.Equal(t, tt.wantFirst, h[0])
			}
		})
	}
}

func TestRouteHostnames_invalidGatewayHostnameSourceLogsAndFallsBack(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	meta := &metav1.ObjectMeta{Namespace: "ns1", Name: "r1", Annotations: map[string]string{
		testDNSRouteKeyGatewayHostnameSource: "not-a-valid-mode", testDNSRouteKeyHostname: "ann.example.com",
	}}
	h := RouteHostnames(meta, []string{"spec.example.com"}, testDNSRouteKeyGatewayHostnameSource, testDNSRouteKeyHostname, false)
	assert.Contains(t, h, "spec.example.com")
	assert.Contains(t, h, "ann.example.com")
	assert.Contains(t, buf.String(), "invalid gateway-hostname-source")
	assert.Contains(t, buf.String(), "not-a-valid-mode")
}
