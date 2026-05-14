/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestZonePathForHostnameFromMap_table(t *testing.T) {
	tests := []struct {
		name       string
		zones      map[string]string // zonePath → domainName
		hostname   string
		wantRecord string
		wantPath   string
		errSub     string // non-empty => require error containing substring
	}{
		{
			name: "nested_host_longest_suffix_zone",
			zones: map[string]string{
				"/z1": "example.com",
				"/z2": "foo.example.com",
			},
			hostname:   "a.foo.example.com",
			wantRecord: "a",
			wantPath:   "/z2",
		},
		{
			name: "shorter_zone_when_longer_suffix_not_configured",
			zones: map[string]string{
				"/z1": "example.com",
				"/z2": "foo.example.com",
			},
			hostname:   "bar.example.com",
			wantRecord: "bar",
			wantPath:   "/z1",
		},
		{
			name:       "wildcard_prefix_stripped_before_FindZone",
			zones:      map[string]string{"/z": "example.com"},
			hostname:   "*.apps.example.com",
			wantRecord: "apps",
			wantPath:   "/z",
		},
		{
			name:     "apex_hostname_rejected_even_with_trailing_dot_and_mixed_case",
			zones:    map[string]string{"/z": "example.com"},
			hostname: "EXAMPLE.COM.",
			errSub:   "must not equal to the allowed DNS domain",
		},
		{
			name:     "leading_dot_only_zone_suffix_rejected_normalized_equals_suffix",
			zones:    map[string]string{"/z": "example.com"},
			hostname: ".example.com",
			errSub:   "does not lie under matched zone",
		},
		{
			name:     "hostname_not_under_any_zone",
			zones:    map[string]string{"/z": "example.com"},
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
			zones:    map[string]string{"/z": "example.com"},
			hostname: "  ",
			errSub:   "empty hostname",
		},
		{
			name: "punycode_hostname_matches_unicode_zone_longest_suffix",
			zones: map[string]string{
				"/z1": "example.com",
				"/z2": "xn--testcass-e1ae.fr",
				"/z3": "app.xn--testcass-e1ae.fr",
			},
			hostname:   "svc.app.xn--testcass-e1ae.fr",
			wantRecord: "svc",
			wantPath:   "/z3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &DNSRecordService{}
			zid := generateZoneIdFromMap(tt.zones)
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

func TestParseProjectDNSZonePath_table(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantOrg    string
		wantProj   string
		wantDNSSvc string
		wantZone   string
		wantErr    bool
	}{
		{
			name:       "valid path",
			path:       "/orgs/default/projects/project-1/dns-services/dns-svc-1/zones/zone-1",
			wantOrg:    "default",
			wantProj:   "project-1",
			wantDNSSvc: "dns-svc-1",
			wantZone:   "zone-1",
		},
		{
			name:    "wrong segment (vpcs instead of dns-services)",
			path:    "/orgs/a/projects/p/vpcs/vpc1/zones/z",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			org, proj, dnsSvc, zone, err := parseProjectDNSZonePath(tc.path)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantOrg, org)
			require.Equal(t, tc.wantProj, proj)
			require.Equal(t, tc.wantDNSSvc, dnsSvc)
			require.Equal(t, tc.wantZone, zone)
		})
	}
}
