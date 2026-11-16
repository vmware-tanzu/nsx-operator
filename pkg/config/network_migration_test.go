/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNetworkMigrationConfig(t *testing.T) {
	require.False(t, (*NSXOperatorConfig)(nil).NetworkMigrationEnabled())
	require.False(t, (&NSXOperatorConfig{}).NetworkMigrationEnabled())
	require.False(t, NewNSXOpertorConfig().NetworkMigrationEnabled())
	previous := configFilePath
	t.Cleanup(func() { configFilePath = previous })
	for _, tc := range []struct {
		name, value string
		want        bool
	}{
		{name: "omitted"},
		{name: "disabled", value: "enable_network_migration = false"},
		{name: "enabled", value: "enable_network_migration = true", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configFilePath = filepath.Join(t.TempDir(), "nsxop.ini")
			// Existing VPC options must not implicitly enable migration support.
			contents := "[coe]\ncluster=test\nenable_vpc_network=true\n" + tc.value + "\n[nsx_v3]\nnsx_api_managers=127.0.0.1\ninsecure=true\nvpc_wcp_enhance=true\n"
			require.NoError(t, os.WriteFile(configFilePath, []byte(contents), 0600))
			cfg, err := LoadConfigFromFile()
			require.NoError(t, err)
			require.Equal(t, tc.want, cfg.NetworkMigrationEnabled())
			require.True(t, cfg.EnableVPCNetwork)
			require.True(t, cfg.VpcWcpEnhanceEnabled())
		})
	}
}
