/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package source

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/sets"
)

func TestToLowerCaseASCII(t *testing.T) {
	assert.Equal(t, "abc", ToLowerCaseASCII("AbC"))
	assert.Equal(t, "already", ToLowerCaseASCII("already"))
}

func TestGwMatchingHost(t *testing.T) {
	t.Run("empty matches concrete", func(t *testing.T) {
		got, ok := GwMatchingHost("", "Foo.EXAMPLE.com")
		require.True(t, ok)
		assert.Equal(t, "foo.example.com", got)
	})
	t.Run("wildcard suffix", func(t *testing.T) {
		got, ok := GwMatchingHost("*.example.com", "a.example.com")
		require.True(t, ok)
		assert.Equal(t, "a.example.com", got)
	})
	t.Run("exact", func(t *testing.T) {
		got, ok := GwMatchingHost("X.example.com", "x.example.com")
		require.True(t, ok)
		assert.Equal(t, "x.example.com", got)
	})
	t.Run("no match", func(t *testing.T) {
		_, ok := GwMatchingHost("*.example.com", "other.org")
		assert.False(t, ok)
	})
	t.Run("invalid IP", func(t *testing.T) {
		_, ok := GwMatchingHost("10.0.0.1", "foo.com")
		assert.False(t, ok)
	})
}

func TestGatewayCanonicalHost(t *testing.T) {
	got, ok := GatewayCanonicalHost("*.Example.COM")
	require.True(t, ok)
	assert.Equal(t, "*.example.com", got)
}

func TestClaimGwMatchingDNSName(t *testing.T) {
	seen := sets.New[string]()
	assert.True(t, ClaimGwMatchingDNSName(seen, "dup.example.com"))
	assert.False(t, ClaimGwMatchingDNSName(seen, "dup.example.com"))
}
