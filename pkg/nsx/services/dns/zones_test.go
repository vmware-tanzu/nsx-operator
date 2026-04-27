/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	extprovider "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/provider"
)

// zoneIDNameFromDomainPathMap builds extprovider.ZoneIDName from domain -> zone path (policy ID)
// in deterministic domain key order.
func zoneIDNameFromDomainPathMap(m map[string]string) extprovider.ZoneIDName {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	z := make(extprovider.ZoneIDName)
	for _, dom := range keys {
		d := strings.ToLower(strings.TrimSpace(dom))
		p := strings.TrimSpace(m[dom])
		z.Add(p, d)
	}
	return z
}

func TestZonePathForHostnameFromMap_table(t *testing.T) {
	tests := []struct {
		name       string
		zones      map[string]string
		hostname   string
		wantRecord string
		wantPath   string
		errSub     string // non-empty => require error containing substring
	}{
		{
			name: "nested_host_longest_suffix_zone",
			zones: map[string]string{
				"example.com":     "/z1",
				"foo.example.com": "/z2",
			},
			hostname:   "a.foo.example.com",
			wantRecord: "a",
			wantPath:   "/z2",
		},
		{
			name: "shorter_zone_when_longer_suffix_not_configured",
			zones: map[string]string{
				"example.com":     "/z1",
				"foo.example.com": "/z2",
			},
			hostname:   "bar.example.com",
			wantRecord: "bar",
			wantPath:   "/z1",
		},
		{
			name:       "wildcard_prefix_stripped_before_FindZone",
			zones:      map[string]string{"example.com": "/z"},
			hostname:   "*.apps.example.com",
			wantRecord: "apps",
			wantPath:   "/z",
		},
		{
			name:     "apex_hostname_rejected_even_with_trailing_dot_and_mixed_case",
			zones:    map[string]string{"example.com": "/z"},
			hostname: "EXAMPLE.COM.",
			errSub:   "must not equal allowed DNS domain",
		},
		{
			name:     "hostname_not_under_any_zone",
			zones:    map[string]string{"example.com": "/z"},
			hostname: "svc.other.example",
			errSub:   "does not match any allowed DNS domain",
		},
		{
			name:     "no_zones_configured",
			zones:    map[string]string{},
			hostname: "a.example.com",
			errSub:   "does not match any allowed DNS domain",
		},
		{
			name:     "empty_hostname_after_trim",
			zones:    map[string]string{"example.com": "/z"},
			hostname: "  ",
			errSub:   "empty hostname",
		},
		{
			name: "punycode_hostname_matches_unicode_zone_longest_suffix",
			zones: map[string]string{
				"example.com":              "/z1",
				"xn--testcass-e1ae.fr":     "/z2",
				"app.xn--testcass-e1ae.fr": "/z3",
			},
			hostname:   "svc.app.xn--testcass-e1ae.fr",
			wantRecord: "svc",
			wantPath:   "/z3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &DNSRecordService{}
			zid := zoneIDNameFromDomainPathMap(tt.zones)
			rec, zp, err := svc.getZonePathForHostname(zid, tt.hostname)
			if tt.errSub != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errSub)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantPath, zp, "zone path")
			require.Equal(t, tt.wantRecord, rec, "relative record name")
		})
	}
}
