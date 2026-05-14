// Copyright 2026 Broadcom, Inc.
// SPDX-License-Identifier: Apache-2.0

package endpoint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTargets_table(t *testing.T) {
	tests := []struct {
		name    string
		targets []string
		want    Targets
	}{
		{
			name:    "empty input",
			targets: nil,
			want:    nil,
		},
		{
			name:    "single target",
			targets: []string{"1.2.3.4"},
			want:    Targets{"1.2.3.4"},
		},
		{
			name:    "deduplicated and sorted",
			targets: []string{"z.example.com", "a.example.com", "z.example.com"},
			want:    Targets{"a.example.com", "z.example.com"},
		},
		{
			name:    "whitespace trimmed and blank filtered",
			targets: []string{"  1.2.3.4  ", "", "   "},
			want:    Targets{"1.2.3.4"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewTargets(tt.targets...)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestEndpointKey_String(t *testing.T) {
	k := EndpointKey{DNSName: "a.example.com", RecordType: "A", SetIdentifier: "s1"}
	assert.Equal(t, "a.example.com/A/s1", k.String())
}

func TestEndpoint_Key(t *testing.T) {
	ep := &Endpoint{DNSName: "b.example.com", RecordType: "CNAME", SetIdentifier: "x"}
	k := ep.Key()
	assert.Equal(t, EndpointKey{DNSName: "b.example.com", RecordType: "CNAME", SetIdentifier: "x"}, k)
}

func TestEndpoint_ProviderSpecific_table(t *testing.T) {
	ep := NewEndpoint("a.example.com", RecordTypeA, "1.2.3.4")
	require.NotNil(t, ep)

	t.Run("set and get present key", func(t *testing.T) {
		ep.SetProviderSpecificProperty("foo", "bar")
		val, ok := ep.GetProviderSpecificProperty("foo")
		require.True(t, ok)
		assert.Equal(t, "bar", val)
	})

	t.Run("get absent key", func(t *testing.T) {
		val, ok := ep.GetProviderSpecificProperty("missing")
		assert.False(t, ok)
		assert.Empty(t, val)
	})

	t.Run("update existing key", func(t *testing.T) {
		ep.SetProviderSpecificProperty("foo", "baz")
		val, ok := ep.GetProviderSpecificProperty("foo")
		require.True(t, ok)
		assert.Equal(t, "baz", val)
	})
}

func TestEndpoint_WithLabel(t *testing.T) {
	ep := NewEndpoint("a.example.com", RecordTypeA, "1.2.3.4")
	require.NotNil(t, ep)
	ep2 := ep.WithLabel("owner", "me")
	assert.Same(t, ep, ep2)
	assert.Equal(t, "me", ep.Labels["owner"])

	// Second call updates existing key without re-allocating
	ep.WithLabel("owner", "you")
	assert.Equal(t, "you", ep.Labels["owner"])
}

func TestEndpoint_GetBoolProviderSpecificProperty_table(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		wantBool  bool
		wantFound bool
	}{
		{name: "absent", value: "", wantBool: false, wantFound: false},
		{name: "true", value: "true", wantBool: true, wantFound: true},
		{name: "false", value: "false", wantBool: false, wantFound: true},
		{name: "invalid", value: "yes", wantBool: false, wantFound: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ep := NewEndpoint("a.example.com", RecordTypeA, "1.2.3.4")
			require.NotNil(t, ep)
			if tt.value != "" {
				ep.SetProviderSpecificProperty("alias", tt.value)
			}
			got, found := ep.GetBoolProviderSpecificProperty("alias")
			assert.Equal(t, tt.wantBool, got)
			assert.Equal(t, tt.wantFound, found)
		})
	}
}

func TestEndpoint_supportsAlias_and_isAlias_table(t *testing.T) {
	tests := []struct {
		name         string
		recordType   string
		aliasValue   string
		wantSupports bool
		wantIsAlias  bool
	}{
		{name: "A supports alias, not set", recordType: RecordTypeA, aliasValue: "", wantSupports: true, wantIsAlias: false},
		{name: "AAAA supports alias", recordType: RecordTypeAAAA, aliasValue: "true", wantSupports: true, wantIsAlias: true},
		{name: "CNAME supports alias", recordType: RecordTypeCNAME, aliasValue: "true", wantSupports: true, wantIsAlias: true},
		{name: "TXT does not support alias", recordType: RecordTypeTXT, aliasValue: "", wantSupports: false, wantIsAlias: false},
		{name: "MX does not support alias", recordType: RecordTypeMX, aliasValue: "", wantSupports: false, wantIsAlias: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ep := &Endpoint{DNSName: "a.example.com", RecordType: tt.recordType}
			if tt.aliasValue != "" {
				ep.SetProviderSpecificProperty(providerSpecificAlias, tt.aliasValue)
			}
			assert.Equal(t, tt.wantSupports, ep.supportsAlias())
			assert.Equal(t, tt.wantIsAlias, ep.isAlias())
		})
	}
}

func TestNewEndpointWithTTL_table(t *testing.T) {
	tests := []struct {
		name       string
		dnsName    string
		recordType string
		ttl        TTL
		targets    []string
		wantNil    bool
		wantName   string
	}{
		{
			name:       "normal A record",
			dnsName:    "a.example.com",
			recordType: RecordTypeA,
			targets:    []string{"1.2.3.4"},
			wantName:   "a.example.com",
		},
		{
			name:       "trailing dot stripped",
			dnsName:    "a.example.com.",
			recordType: RecordTypeA,
			targets:    []string{"1.2.3.4."},
			wantName:   "a.example.com",
		},
		{
			name:       "TXT target not trimmed",
			dnsName:    "a.example.com",
			recordType: RecordTypeTXT,
			targets:    []string{"some text."},
			wantName:   "a.example.com",
		},
		{
			name:       "label longer than 63 chars returns nil",
			dnsName:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.example.com",
			recordType: RecordTypeA,
			targets:    []string{"1.2.3.4"},
			wantNil:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ep := NewEndpointWithTTL(tt.dnsName, tt.recordType, tt.ttl, tt.targets...)
			if tt.wantNil {
				assert.Nil(t, ep)
			} else {
				require.NotNil(t, ep)
				assert.Equal(t, tt.wantName, ep.DNSName)
			}
		})
	}
}

func TestTTLIsConfigured(t *testing.T) {
	assert.False(t, TTL(0).IsConfigured())
	assert.True(t, TTL(1).IsConfigured())
}
