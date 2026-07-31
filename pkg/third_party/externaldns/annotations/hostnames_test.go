// Copyright 2026 Broadcom, Inc.
// SPDX-License-Identifier: Apache-2.0
package annotations

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHostnamesFromAnnotations(t *testing.T) {
	tests := []struct {
		name        string
		input       map[string]string
		hostnameKey string
		want        []string
	}{
		{"nil-input", nil, "hostname", nil},
		{"empty-key", map[string]string{"hostname": "foo.com"}, "", nil},
		{"missing-key", map[string]string{"other": "foo.com"}, "hostname", nil},
		{"single-hostname", map[string]string{"hostname": "foo.com"}, "hostname", []string{"foo.com"}},
		{"multiple-hostnames", map[string]string{"hostname": "foo.com, bar.com"}, "hostname", []string{"foo.com", "bar.com"}},
		{"hostnames-with-spaces", map[string]string{"hostname": " foo.com , bar.com "}, "hostname", []string{"foo.com", "bar.com"}},
		{"empty-value", map[string]string{"hostname": ""}, "hostname", []string{""}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, HostnamesFromAnnotations(tc.input, tc.hostnameKey))
		})
	}
}
