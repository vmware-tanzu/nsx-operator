/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package migration

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

func TestClientIsLazyAndSharedAcrossConcurrentHandlers(t *testing.T) {
	var loads, builds atomic.Int32
	want := &nsx.Client{}
	s := NewService()
	s.loadConfig = func() (*config.NSXOperatorConfig, error) { loads.Add(1); return &config.NSXOperatorConfig{}, nil }
	s.newClient = func(*config.NSXOperatorConfig) *nsx.Client { builds.Add(1); return want }
	require.Zero(t, loads.Load(), "construction must not require NSX configuration")
	var wg sync.WaitGroup
	results := make(chan *nsx.Client, 12)
	errors := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); c, err := s.Client(); results <- c; errors <- err }()
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	for client := range results {
		require.Same(t, want, client)
	}
	require.Equal(t, int32(1), loads.Load())
	require.Equal(t, int32(1), builds.Load())
}

func TestFailedInitializationCanRecover(t *testing.T) {
	s := NewService()
	failure := errors.New("configuration not mounted yet")
	ready := false
	s.loadConfig = func() (*config.NSXOperatorConfig, error) {
		if !ready {
			return nil, failure
		}
		return &config.NSXOperatorConfig{}, nil
	}
	want := &nsx.Client{}
	s.newClient = func(*config.NSXOperatorConfig) *nsx.Client { return nil }
	_, err := s.Client()
	require.ErrorIs(t, err, failure)
	ready = true
	_, err = s.Client()
	require.ErrorContains(t, err, "initialization failed")
	s.newClient = func(*config.NSXOperatorConfig) *nsx.Client { return want }
	got, err := s.Client()
	require.NoError(t, err)
	require.Same(t, want, got)
}
