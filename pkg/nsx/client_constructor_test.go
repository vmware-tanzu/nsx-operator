/*
Copyright © 2026 Broadcom, Inc. All Rights Reserved.

	SPDX-License-Identifier: Apache-2.0
*/
package nsx

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
)

func TestNewClientInvalidConfiguration(t *testing.T) {
	for _, cfg := range []*config.NSXOperatorConfig{nil, {}, config.NewNSXOpertorConfig()} {
		require.NotPanics(t, func() { client, err := NewClient(cfg); require.Error(t, err); require.Nil(t, client) })
	}
}

func TestNewClientMalformedEndpoint(t *testing.T) {
	cfg := config.NewNSXOpertorConfig()
	cfg.NsxApiManagers = []string{"https://[invalid"}
	cfg.Insecure = true
	client, err := NewClient(cfg)
	require.Nil(t, client)
	require.ErrorContains(t, err, "creating NSX cluster")
	require.Nil(t, GetClient(cfg), "legacy constructor must also handle initialization errors")
}
