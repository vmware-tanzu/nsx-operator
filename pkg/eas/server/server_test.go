/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package server

import (
	"net"
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
		t.Setenv(easBindAddressEnv, "")
		t.Setenv(tlsCertFileEnv, "")
		t.Setenv(tlsKeyFileEnv, "")
		addr, certFile, keyFile, err := listenerConfigFromEnv()
		require.NoError(t, err)
		assert.Equal(t, net.JoinHostPort("", defaultPort), addr)
		assert.Empty(t, certFile)
		assert.Empty(t, keyFile)
	})

	t.Run("uses explicit tls config", func(t *testing.T) {
		t.Setenv(easPortEnv, "19443")
		t.Setenv(easBindAddressEnv, "")
		t.Setenv(tlsCertFileEnv, "/tmp/tls.crt")
		t.Setenv(tlsKeyFileEnv, "/tmp/tls.key")

		addr, certFile, keyFile, err := listenerConfigFromEnv()
		require.NoError(t, err)
		assert.Equal(t, net.JoinHostPort("", "19443"), addr)
		assert.Equal(t, "/tmp/tls.crt", certFile)
		assert.Equal(t, "/tmp/tls.key", keyFile)
	})

	t.Run("binds to explicit host for pod IP or all interfaces", func(t *testing.T) {
		t.Setenv(easPortEnv, "9553")
		t.Setenv(easBindAddressEnv, "10.0.0.1")
		t.Setenv(tlsCertFileEnv, "")
		t.Setenv(tlsKeyFileEnv, "")
		addr, _, _, err := listenerConfigFromEnv()
		require.NoError(t, err)
		assert.Equal(t, "10.0.0.1:9553", addr)
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
