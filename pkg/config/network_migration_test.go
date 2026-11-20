/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package config

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/ini.v1"
)

func TestNetworkMigrationEnabled(t *testing.T) {
	var nilConfig *NSXOperatorConfig
	require.False(t, nilConfig.NetworkMigrationEnabled())
	require.False(t, (&NSXOperatorConfig{}).NetworkMigrationEnabled())

	for _, tc := range []struct {
		name    string
		content string
		want    bool
	}{
		{name: "omitted", content: "[coe]\nenable_vpc_network = true\n"},
		{name: "disabled", content: "[coe]\nenable_network_migration = false\n"},
		{name: "enabled", content: "[coe]\nenable_network_migration = true\n", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ini.Load([]byte(tc.content))
			require.NoError(t, err)
			operatorConfig := NewNSXOpertorConfig()
			require.NoError(t, cfg.Section("coe").MapTo(operatorConfig.CoeConfig))
			require.Equal(t, tc.want, operatorConfig.NetworkMigrationEnabled())
		})
	}
}
