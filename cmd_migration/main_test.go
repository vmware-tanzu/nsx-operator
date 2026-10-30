/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package main

import (
	"flag"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
)

func TestMigrationFlagsAndDefaults(t *testing.T) {
	t.Setenv("NSX_OPERATOR_NAMESPACE", "ncp-system")
	flags := flag.NewFlagSet("migration", flag.ContinueOnError)
	opts, err := registerFlags(flags)
	require.NoError(t, err)
	require.False(t, flags.Parsed(), "shared registration must not parse before migration flags exist")
	require.False(t, opts.enabled)
	require.Equal(t, "ncp-system", opts.namespace)
	require.Equal(t, ":9924", config.ProbeAddr)
	require.Equal(t, "0", config.MetricsAddr)
	require.NoError(t, flags.Parse([]string{"--enable-network-migration", "--nsxconfig=/tmp/migration.ini", "--health-probe-bind-address=:9940", "--leader-election-namespace=vmware-system-nsx"}))
	require.True(t, opts.enabled)
	require.Equal(t, ":9940", config.ProbeAddr)
	require.Equal(t, "vmware-system-nsx", opts.namespace)
}

func TestRunRejectsEmptyLeaderElectionNamespace(t *testing.T) {
	originalFlags, originalArgs := flag.CommandLine, os.Args
	t.Cleanup(func() { flag.CommandLine, os.Args = originalFlags, originalArgs })
	flag.CommandLine = flag.NewFlagSet("migration", flag.ContinueOnError)
	os.Args = []string{"migration", "--enable-network-migration", "--leader-election-namespace="}
	require.ErrorContains(t, run(), "leader election namespace must not be empty")
}
