/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestJoinAndPackStrings(t *testing.T) {
	// Generate a list of keys that will exceed 255 characters when joined
	var keys []string
	for i := 0; i < 50; i++ {
		keys = append(keys, fmt.Sprintf("http_route/namespace-%d/name-%d", i, i))
	}

	plain, overflow, truncated := joinAndPackStrings(keys)

	// Plain string should not exceed 255 characters
	require.LessOrEqual(t, len(plain), 255)

	// Overflow should not exceed 255 characters
	require.LessOrEqual(t, len(overflow), 255)
	require.NotEmpty(t, overflow)

	// Verify that the overflow string contains valid keys
	// It might be truncated, so we just check if the first few overflow keys are present
	overflowKeys := strings.Split(overflow, ",")
	require.True(t, len(overflowKeys) > 0)

	// Ensure no corrupted partial keys are present (they should start with "http_route")
	for _, k := range overflowKeys {
		require.True(t, strings.HasPrefix(k, "http_route/"))
	}

	// Since we generated 50 keys and each is ~30 chars, total is ~1500 chars.
	// Two 255-char buffers can only hold ~510 chars, so it MUST be truncated.
	require.True(t, truncated)
}

func TestJoinAndPackStrings_EdgeCases(t *testing.T) {
	// Empty slice
	plain, overflow, truncated := joinAndPackStrings(nil)
	require.Empty(t, plain)
	require.Empty(t, overflow)
	require.False(t, truncated)

	// Small slice (no overflow)
	keys := []string{"http_route/ns1/name1", "http_route/ns2/name2"}
	plain, overflow, truncated = joinAndPackStrings(keys)
	require.Equal(t, "http_route/ns1/name1,http_route/ns2/name2", plain)
	require.Empty(t, overflow)
	require.False(t, truncated)

	// Single item exceeding 255 bytes (must not be truncated silently, must mark truncated=true)
	longKey := "http_route/ns/" + strings.Repeat("a", 260)
	plain, overflow, truncated = joinAndPackStrings([]string{longKey})
	require.Empty(t, plain)
	require.Empty(t, overflow)
	require.True(t, truncated)
}

func TestFormatContributingOwnersTag(t *testing.T) {
	// Empty keys
	tag1, tag2, err := formatContributingOwnersTag(nil)
	require.NoError(t, err)
	require.Empty(t, tag1)
	require.Empty(t, tag2)

	// Small keys
	tag1, tag2, err = formatContributingOwnersTag([]string{"key1", "key2"})
	require.NoError(t, err)
	require.Equal(t, "key1,key2", tag1)
	require.Empty(t, tag2)

	// Large keys that cause truncation
	var keys []string
	for i := 0; i < 50; i++ {
		keys = append(keys, fmt.Sprintf("http_route/namespace-%d/name-%d", i, i))
	}
	_, _, err = formatContributingOwnersTag(keys)
	require.Error(t, err)
	require.ErrorContains(t, err, "contributing owners count exceeds maximum tag capacity")
}

func TestParseContributingOwnersTag(t *testing.T) {
	require.Empty(t, parseContributingOwnersTag(""))
	require.Empty(t, parseContributingOwnersTag("   "))

	parsed := parseContributingOwnersTag("key1, key2 ,key3,  key1 ")
	require.ElementsMatch(t, []string{"key1", "key2", "key3"}, parsed)
}

func TestMergeContributingOwnerKeys(t *testing.T) {
	existing := []string{"key1", "key2"}

	// Normal merge
	merged := mergeContributingOwnerKeys(existing, "key3", "primaryKey")
	require.Equal(t, []string{"key1", "key2", "key3"}, merged)

	// Deduplication and exclude primaryNNKey
	merged = mergeContributingOwnerKeys(existing, "key2", "key1")
	require.Equal(t, []string{"key2"}, merged)

	// Add primaryNNKey (should be excluded)
	merged = mergeContributingOwnerKeys(existing, "primaryKey", "primaryKey")
	require.Equal(t, []string{"key1", "key2"}, merged)
}

func TestAppendGatewayAndContributionTags(t *testing.T) {
	tags := []model.Tag{{Scope: common.String("existing"), Tag: common.String("value")}}

	// Empty gateway, empty contrib
	out, err := appendGatewayAndContributionTags(tags, "", nil)
	require.NoError(t, err)
	require.Len(t, out, 1)

	// With gateway, empty contrib
	out, err = appendGatewayAndContributionTags(tags, "gw-key", nil)
	require.NoError(t, err)
	require.Len(t, out, 2)
	require.Equal(t, common.TagScopeDNSRecordGatewayIndexList, *out[1].Scope)
	require.Equal(t, "gw-key", *out[1].Tag)

	// With contrib (small)
	contribKeys := []string{"key1", "key2"}
	out, err = appendGatewayAndContributionTags(tags, "", contribKeys)
	require.NoError(t, err)
	require.Len(t, out, 2)
	require.Equal(t, common.TagScopeDNSRecordContributingOwners, *out[1].Scope)
	require.Equal(t, "key1,key2", *out[1].Tag)
}

func TestParseContributingOwnersFromRecord(t *testing.T) {
	var keys []string
	for i := 0; i < 50; i++ {
		keys = append(keys, fmt.Sprintf("http_route/namespace-%d/name-%d", i, i))
	}

	plain, overflow, _ := joinAndPackStrings(keys)

	rec := &model.DnsRecord{
		Tags: []model.Tag{
			{Scope: common.String(common.TagScopeDNSRecordContributingOwners), Tag: common.String(plain)},
			{Scope: common.String(common.TagScopeDNSRecordAdditionalContributingOwners), Tag: common.String(overflow)},
		},
	}

	parsedKeys := parseContributingOwnersFromRecord(rec)

	// We might lose some keys due to truncation, but we should definitely have more than what fits in the plain tag
	plainKeys := strings.Split(plain, ",")
	require.Greater(t, len(parsedKeys), len(plainKeys))

	// All parsed keys should be valid
	for _, k := range parsedKeys {
		require.True(t, strings.HasPrefix(k, "http_route/"))
	}
}

func TestReplaceContributingOwnersInTags(t *testing.T) {
	tags := []model.Tag{
		{Scope: common.String(common.TagScopeCluster), Tag: common.String("c1")},
		{Scope: common.String(common.TagScopeDNSRecordContributingOwners), Tag: common.String("old_plain")},
		{Scope: common.String(common.TagScopeDNSRecordAdditionalContributingOwners), Tag: common.String("old_overflow")},
	}

	t.Run("normal replacement without overflow", func(t *testing.T) {
		keys := []string{"http_route/ns1/name1"}
		out, err := replaceContributingOwnersInTags(tags, keys)
		require.NoError(t, err)
		require.Len(t, out, 2)
		require.Equal(t, common.TagScopeCluster, *out[0].Scope)
		require.Equal(t, common.TagScopeDNSRecordContributingOwners, *out[1].Scope)
		require.Equal(t, "http_route/ns1/name1", *out[1].Tag)
	})

	t.Run("replacement with overflow but no truncation", func(t *testing.T) {
		// Generate enough keys to spill into the second tag, but not exceed both 255-byte limits.
		// 10 keys of ~30 chars = ~300 chars.
		var keys []string
		for i := 0; i < 10; i++ {
			keys = append(keys, fmt.Sprintf("http_route/namespace-%d/name-%d", i, i))
		}
		out, err := replaceContributingOwnersInTags(tags, keys)
		require.NoError(t, err)
		require.Len(t, out, 3)

		hasPlain := false
		hasOverflow := false
		for _, tag := range out {
			if *tag.Scope == common.TagScopeDNSRecordContributingOwners {
				hasPlain = true
				require.LessOrEqual(t, len(*tag.Tag), 255)
			}
			if *tag.Scope == common.TagScopeDNSRecordAdditionalContributingOwners {
				hasOverflow = true
				require.LessOrEqual(t, len(*tag.Tag), 255)
			}
		}
		require.True(t, hasPlain)
		require.True(t, hasOverflow)
	})

	t.Run("replacement with truncation returns error", func(t *testing.T) {
		// Generate huge number of keys to force truncation
		var keys []string
		for i := 0; i < 50; i++ {
			keys = append(keys, fmt.Sprintf("http_route/namespace-%d/name-%d", i, i))
		}
		out, err := replaceContributingOwnersInTags(tags, keys)
		require.Error(t, err)
		require.Contains(t, err.Error(), "contributing owners count exceeds maximum tag capacity")
		require.Nil(t, out)
	})
}

func TestParseOwnerNNIndexKey(t *testing.T) {
	cf, ns, n, ok := parseOwnerNNIndexKey("httproute/ns1/r1")
	require.True(t, ok)
	require.Equal(t, common.TagValueDNSRecordForHTTPRoute, cf)
	require.Equal(t, "ns1", ns)
	require.Equal(t, "r1", n)

	// Invalid format
	_, _, _, ok = parseOwnerNNIndexKey("invalid")
	require.False(t, ok)
}
