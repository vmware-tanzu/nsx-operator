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

	plain, overflow := joinAndPackStrings(keys)

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
}

func TestJoinAndPackStrings_EdgeCases(t *testing.T) {
	// Empty slice
	plain, overflow := joinAndPackStrings(nil)
	require.Empty(t, plain)
	require.Empty(t, overflow)

	// Small slice (no overflow)
	keys := []string{"http_route/ns1/name1", "http_route/ns2/name2"}
	plain, overflow = joinAndPackStrings(keys)
	require.Equal(t, "http_route/ns1/name1,http_route/ns2/name2", plain)
	require.Empty(t, overflow)
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
	out := appendGatewayAndContributionTags(tags, "", nil)
	require.Len(t, out, 1)

	// With gateway, empty contrib
	out = appendGatewayAndContributionTags(tags, "gw-key", nil)
	require.Len(t, out, 2)
	require.Equal(t, common.TagScopeDNSRecordGatewayIndexList, *out[1].Scope)
	require.Equal(t, "gw-key", *out[1].Tag)

	// With contrib (small)
	contribKeys := []string{"key1", "key2"}
	out = appendGatewayAndContributionTags(tags, "", contribKeys)
	require.Len(t, out, 2)
	require.Equal(t, common.TagScopeDNSRecordContributingOwners, *out[1].Scope)
	require.Equal(t, "key1,key2", *out[1].Tag)
}

func TestParseContributingOwnersFromRecord(t *testing.T) {
	var keys []string
	for i := 0; i < 50; i++ {
		keys = append(keys, fmt.Sprintf("http_route/namespace-%d/name-%d", i, i))
	}

	plain, overflow := joinAndPackStrings(keys)

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

func TestReplaceContributingOwnersInTagsWithOverflow(t *testing.T) {
	var keys []string
	for i := 0; i < 50; i++ {
		keys = append(keys, fmt.Sprintf("http_route/namespace-%d/name-%d", i, i))
	}

	tags := []model.Tag{
		{Scope: common.String(common.TagScopeCluster), Tag: common.String("c1")},
	}

	out := replaceContributingOwnersInTags(tags, keys)

	// Should have Cluster, ContributingOwners, and AdditionalContributingOwners tags
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
}
