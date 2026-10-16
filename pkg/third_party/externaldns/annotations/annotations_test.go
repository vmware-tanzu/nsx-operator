// Copyright 2025 The Kubernetes Authors.
// Copyright 2026 Broadcom, Inc.
//
// SPDX-License-Identifier: Apache-2.0
//
// Derived from sigs.k8s.io/external-dns/source/annotations/annotations_test.go.
// Attribution: see package doc.go.

package annotations

import (
	"strings"
	"testing"

	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
)

func TestResolveLegacyAnnotations(t *testing.T) {
	for _, tc := range []struct {
		name        string
		annotations map[string]string
		expected    map[string]string
		resolved    bool
		warning     string
	}{
		{
			name: "nil annotations",
		},
		{
			name:        "no legacy annotations",
			annotations: map[string]string{HostnameKey: "example.org", "description": "kept"},
			expected:    map[string]string{HostnameKey: "example.org", "description": "kept"},
		},
		{
			name:        "legacy annotation is copied to the configured prefix and kept",
			annotations: map[string]string{LegacyAnnotationPrefix + "hostname": "example.org"},
			expected:    map[string]string{LegacyAnnotationPrefix + "hostname": "example.org", HostnameKey: "example.org"},
			resolved:    true,
		},
		{
			name: "every legacy annotation is copied, other annotations are untouched",
			annotations: map[string]string{
				LegacyAnnotationPrefix + "hostname":                "example.org",
				LegacyAnnotationPrefix + "gateway-hostname-source": "annotation-only",
				"description": "kept",
			},
			expected: map[string]string{
				LegacyAnnotationPrefix + "hostname":                "example.org",
				LegacyAnnotationPrefix + "gateway-hostname-source": "annotation-only",
				HostnameKey:              "example.org",
				GatewayHostnameSourceKey: "annotation-only",
				"description":            "kept",
			},
			resolved: true,
		},
		{
			name: "same value under both prefixes is not a conflict and nothing changes",
			annotations: map[string]string{
				LegacyAnnotationPrefix + "hostname": "example.org",
				HostnameKey:                         "example.org",
			},
			expected: map[string]string{LegacyAnnotationPrefix + "hostname": "example.org", HostnameKey: "example.org"},
		},
		{
			name: "the configured prefix wins on conflict and the conflict is logged",
			annotations: map[string]string{
				LegacyAnnotationPrefix + "hostname": "legacy.example.org",
				HostnameKey:                         "example.org",
			},
			expected: map[string]string{LegacyAnnotationPrefix + "hostname": "legacy.example.org", HostnameKey: "example.org"},
			warning:  `ignoring annotation external-dns.alpha.kubernetes.io/hostname="legacy.example.org" because external-dns.kubernetes.io/hostname="example.org" is set`,
		},
		{
			name: "stable empty string wins over legacy alpha on conflict",
			annotations: map[string]string{
				LegacyAnnotationPrefix + "hostname": "legacy.example.org",
				HostnameKey:                         "",
			},
			expected: map[string]string{LegacyAnnotationPrefix + "hostname": "legacy.example.org", HostnameKey: ""},
			warning:  `ignoring annotation external-dns.alpha.kubernetes.io/hostname="legacy.example.org" because external-dns.kubernetes.io/hostname="" is set`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hook := test.NewGlobal()
			defer hook.Reset()

			assert.Equal(t, tc.resolved, resolveLegacyAnnotations(tc.annotations))
			assert.Equal(t, tc.expected, tc.annotations)
			// A second pass must be a no-op.
			assert.False(t, resolveLegacyAnnotations(tc.annotations))
			assert.Equal(t, tc.expected, tc.annotations)
			if tc.warning != "" {
				found := false
				for _, entry := range hook.AllEntries() {
					if entry.Message == tc.warning {
						found = true
						break
					}
				}
				assert.True(t, found, "expected warning %q in logs", tc.warning)
			} else {
				assert.Empty(t, hook.AllEntries())
			}
		})
	}
}

func TestResolveAnnotations(t *testing.T) {
	t.Run("nil or empty map", func(t *testing.T) {
		assert.Nil(t, ResolveAnnotations(nil))
		empty := map[string]string{}
		assert.Equal(t, empty, ResolveAnnotations(empty))
	})

	t.Run("no legacy annotations returns original map without copy", func(t *testing.T) {
		orig := map[string]string{HostnameKey: "example.org"}
		got := ResolveAnnotations(orig)
		assert.Equal(t, orig, got)
	})

	t.Run("legacy annotation is resolved without mutating original map", func(t *testing.T) {
		orig := map[string]string{LegacyAnnotationPrefix + "hostname": "legacy.org"}
		origCopy := map[string]string{LegacyAnnotationPrefix + "hostname": "legacy.org"}

		got := ResolveAnnotations(orig)
		assert.Equal(t, origCopy, orig, "original map must not be mutated")
		assert.Equal(t, "legacy.org", got[HostnameKey])
		assert.Equal(t, "legacy.org", got[LegacyAnnotationPrefix+"hostname"])
	})

	t.Run("stable wins on conflict and original map is untouched", func(t *testing.T) {
		hook := test.NewGlobal()
		defer hook.Reset()

		orig := map[string]string{
			LegacyAnnotationPrefix + "hostname": "legacy.org",
			HostnameKey:                         "stable.org",
		}
		origCopy := map[string]string{
			LegacyAnnotationPrefix + "hostname": "legacy.org",
			HostnameKey:                         "stable.org",
		}

		got := ResolveAnnotations(orig)
		assert.Equal(t, origCopy, orig, "original map must not be mutated")
		assert.Equal(t, "stable.org", got[HostnameKey])

		found := false
		for _, entry := range hook.AllEntries() {
			if strings.Contains(entry.Message, "ignoring annotation") {
				found = true
				break
			}
		}
		assert.True(t, found, "warning log expected on conflict")
	})
}

func TestHasHostnameAnnotationChanged(t *testing.T) {
	tests := []struct {
		name     string
		oldAnnos map[string]string
		newAnnos map[string]string
		want     bool
	}{
		{
			name:     "both nil",
			oldAnnos: nil,
			newAnnos: nil,
			want:     false,
		},
		{
			name:     "stable hostname changed",
			oldAnnos: map[string]string{HostnameKey: "old.com"},
			newAnnos: map[string]string{HostnameKey: "new.com"},
			want:     true,
		},
		{
			name:     "alpha hostname changed",
			oldAnnos: map[string]string{LegacyHostnameKey: "old.com"},
			newAnnos: map[string]string{LegacyHostnameKey: "new.com"},
			want:     true,
		},
		{
			name: "update from alpha hostname to empty stable hostname",
			oldAnnos: map[string]string{
				LegacyHostnameKey: "app.example.com",
			},
			newAnnos: map[string]string{
				LegacyHostnameKey: "app.example.com",
				HostnameKey:       "",
			},
			want: true,
		},
		{
			name: "update from empty stable hostname back to alpha hostname",
			oldAnnos: map[string]string{
				LegacyHostnameKey: "app.example.com",
				HostnameKey:       "",
			},
			newAnnos: map[string]string{
				LegacyHostnameKey: "app.example.com",
			},
			want: true,
		},
		{
			name:     "stable hostname added with empty value",
			oldAnnos: nil,
			newAnnos: map[string]string{HostnameKey: ""},
			want:     true,
		},
		{
			name:     "stable hostname removed with empty value",
			oldAnnos: map[string]string{HostnameKey: ""},
			newAnnos: nil,
			want:     true,
		},
		{
			name:     "stable hostname unchanged with empty value",
			oldAnnos: map[string]string{HostnameKey: ""},
			newAnnos: map[string]string{HostnameKey: ""},
			want:     false,
		},
		{
			name:     "alpha hostname added with empty value",
			oldAnnos: map[string]string{},
			newAnnos: map[string]string{LegacyHostnameKey: ""},
			want:     true,
		},
		{
			name:     "alpha hostname removed with empty value",
			oldAnnos: map[string]string{LegacyHostnameKey: ""},
			newAnnos: map[string]string{},
			want:     true,
		},
		{
			name:     "unrelated annotation changed",
			oldAnnos: map[string]string{"unrelated": "1"},
			newAnnos: map[string]string{"unrelated": "2"},
			want:     false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, HasHostnameAnnotationChanged(tc.oldAnnos, tc.newAnnos))
		})
	}
}

func TestHasRouteDNSAnnotationsChanged(t *testing.T) {
	tests := []struct {
		name     string
		oldAnnos map[string]string
		newAnnos map[string]string
		want     bool
	}{
		{
			name:     "stable hostname changed",
			oldAnnos: map[string]string{HostnameKey: "old.com"},
			newAnnos: map[string]string{HostnameKey: "new.com"},
			want:     true,
		},
		{
			name:     "alpha hostname changed",
			oldAnnos: map[string]string{LegacyHostnameKey: "old.com"},
			newAnnos: map[string]string{LegacyHostnameKey: "new.com"},
			want:     true,
		},
		{
			name:     "gateway hostname source changed",
			oldAnnos: map[string]string{GatewayHostnameSourceKey: "annotation-only"},
			newAnnos: map[string]string{GatewayHostnameSourceKey: "defined-hosts-only"},
			want:     true,
		},
		{
			name:     "alpha gateway hostname source changed",
			oldAnnos: map[string]string{LegacyGatewayHostnameSourceKey: "annotation-only"},
			newAnnos: map[string]string{LegacyGatewayHostnameSourceKey: "defined-hosts-only"},
			want:     true,
		},
		{
			name: "update from alpha hostname to empty stable hostname",
			oldAnnos: map[string]string{
				LegacyHostnameKey: "app.example.com",
			},
			newAnnos: map[string]string{
				LegacyHostnameKey: "app.example.com",
				HostnameKey:       "",
			},
			want: true,
		},
		{
			name: "update from empty stable hostname back to alpha hostname",
			oldAnnos: map[string]string{
				LegacyHostnameKey: "app.example.com",
				HostnameKey:       "",
			},
			newAnnos: map[string]string{
				LegacyHostnameKey: "app.example.com",
			},
			want: true,
		},
		{
			name: "update from alpha gateway-hostname-source to empty stable gateway-hostname-source",
			oldAnnos: map[string]string{
				LegacyGatewayHostnameSourceKey: "annotation-only",
			},
			newAnnos: map[string]string{
				LegacyGatewayHostnameSourceKey: "annotation-only",
				GatewayHostnameSourceKey:       "",
			},
			want: true,
		},
		{
			name: "update from empty stable gateway-hostname-source back to alpha",
			oldAnnos: map[string]string{
				LegacyGatewayHostnameSourceKey: "annotation-only",
				GatewayHostnameSourceKey:       "",
			},
			newAnnos: map[string]string{
				LegacyGatewayHostnameSourceKey: "annotation-only",
			},
			want: true,
		},
		{
			name:     "unrelated annotation changed",
			oldAnnos: map[string]string{"unrelated": "1"},
			newAnnos: map[string]string{"unrelated": "2"},
			want:     false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, HasRouteDNSAnnotationsChanged(tc.oldAnnos, tc.newAnnos))
		})
	}
}
