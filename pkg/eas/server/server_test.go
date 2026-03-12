/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNeedLeaderElection(t *testing.T) {
	s := &EASServer{}
	assert.False(t, s.NeedLeaderElection(), "EAS is read-only, NeedLeaderElection must always return false")
}

func TestListenerConfigFromEnv(t *testing.T) {
	t.Run("uses default port without tls", func(t *testing.T) {
		t.Setenv(easPortEnv, "")
		t.Setenv(tlsCertFileEnv, "")
		t.Setenv(tlsKeyFileEnv, "")
		addr, certFile, keyFile, err := listenerConfigFromEnv()
		require.NoError(t, err)
		assert.Equal(t, ":"+defaultPort, addr)
		assert.Empty(t, certFile)
		assert.Empty(t, keyFile)
	})

	t.Run("uses explicit tls config", func(t *testing.T) {
		t.Setenv(easPortEnv, "19443")
		t.Setenv(tlsCertFileEnv, "/tmp/tls.crt")
		t.Setenv(tlsKeyFileEnv, "/tmp/tls.key")

		addr, certFile, keyFile, err := listenerConfigFromEnv()
		require.NoError(t, err)
		assert.Equal(t, ":19443", addr)
		assert.Equal(t, "/tmp/tls.crt", certFile)
		assert.Equal(t, "/tmp/tls.key", keyFile)
	})

	t.Run("rejects partial tls config", func(t *testing.T) {
		t.Setenv(easPortEnv, "")
		t.Setenv(tlsCertFileEnv, "/tmp/tls.crt")
		t.Setenv(tlsKeyFileEnv, "")

		_, _, _, err := listenerConfigFromEnv()
		require.Error(t, err)
		assert.Contains(t, err.Error(), tlsCertFileEnv)
		assert.Contains(t, err.Error(), tlsKeyFileEnv)
	})
}
