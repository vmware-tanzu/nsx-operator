// Copyright 2026 Broadcom, Inc.
// SPDX-License-Identifier: Apache-2.0

package annotations

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHostnamesFromAnnotations(t *testing.T) {
	tests := []struct {
		name         string
		input        map[string]string
		hostnameKeys []string
		want         []string
	}{
		{name: "nil-input", input: nil, hostnameKeys: []string{"hostname"}, want: nil},
		{name: "empty-key", input: map[string]string{"hostname": "foo.com"}, hostnameKeys: []string{""}, want: nil},
		{name: "missing-key", input: map[string]string{"other": "foo.com"}, hostnameKeys: []string{"hostname"}, want: nil},
		{name: "single-hostname", input: map[string]string{"hostname": "foo.com"}, hostnameKeys: []string{"hostname"}, want: []string{"foo.com"}},
		{name: "multiple-hostnames", input: map[string]string{"hostname": "foo.com, bar.com"}, hostnameKeys: []string{"hostname"}, want: []string{"foo.com", "bar.com"}},
		{name: "hostnames-with-spaces", input: map[string]string{"hostname": " foo.com , bar.com "}, hostnameKeys: []string{"hostname"}, want: []string{"foo.com", "bar.com"}},
		{name: "empty-value", input: map[string]string{"hostname": ""}, hostnameKeys: []string{"hostname"}, want: []string{""}},

		// Stable vs Alpha hostname annotation tests
		{
			name:  "stable-annotation-only",
			input: map[string]string{HostnameKey: "stable.example.com"},
			want:  []string{"stable.example.com"},
		},
		{
			name:  "alpha-annotation-only-fallback",
			input: map[string]string{LegacyHostnameKey: "alpha.example.com"},
			want:  []string{"alpha.example.com"},
		},
		{
			name: "stable-and-alpha-both-present-stable-wins",
			input: map[string]string{
				HostnameKey:       "stable.example.com",
				LegacyHostnameKey: "alpha.example.com",
			},
			want: []string{"stable.example.com"},
		},
		{
			name: "stable-empty-overrides-alpha-fallback",
			input: map[string]string{
				HostnameKey:       "",
				LegacyHostnameKey: "alpha.example.com",
			},
			want: []string{""},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := HostnamesFromAnnotations(tc.input, tc.hostnameKeys...)
			assert.Equal(t, tc.want, got)
		})
	}
}
