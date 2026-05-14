// Copyright 2026 Broadcom, Inc.
// SPDX-License-Identifier: Apache-2.0

package endpoint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSuitableType_table(t *testing.T) {
	tests := []struct {
		target string
		want   string
	}{
		{target: "1.2.3.4", want: RecordTypeA},
		{target: "2001:db8::1", want: RecordTypeAAAA},
		{target: "example.com", want: RecordTypeCNAME},
		{target: "not-an-ip", want: RecordTypeCNAME},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			assert.Equal(t, tt.want, SuitableType(tt.target))
		})
	}
}

func TestEndpointsForHostname_table(t *testing.T) {
	tests := []struct {
		name      string
		hostname  string
		targets   Targets
		ttl       TTL
		wantLen   int
		wantTypes []string
	}{
		{
			name:      "empty targets",
			hostname:  "a.example.com",
			targets:   nil,
			wantLen:   0,
			wantTypes: nil,
		},
		{
			name:      "mixed ipv4 and ipv6",
			hostname:  "a.example.com",
			targets:   Targets{"1.2.3.4", "2001:db8::1"},
			wantLen:   2,
			wantTypes: []string{RecordTypeA, RecordTypeAAAA},
		},
		{
			name:      "cname target",
			hostname:  "a.example.com",
			targets:   Targets{"target.example.com"},
			wantLen:   1,
			wantTypes: []string{RecordTypeCNAME},
		},
		{
			name:      "multiple ipv4 grouped into single A endpoint",
			hostname:  "a.example.com",
			targets:   Targets{"1.2.3.4", "5.6.7.8"},
			wantLen:   1,
			wantTypes: []string{RecordTypeA},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eps := EndpointsForHostname(tt.hostname, tt.targets, tt.ttl)
			require.Len(t, eps, tt.wantLen)
			for i, ep := range eps {
				assert.Equal(t, tt.wantTypes[i], ep.RecordType)
			}
		})
	}
}
