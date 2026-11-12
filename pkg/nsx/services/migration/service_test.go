/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package migration

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	vapierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	vapidata "github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	nsxmodel "github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

func TestClientIsLazyAndSharedAcrossConcurrentHandlers(t *testing.T) {
	var loads, builds atomic.Int32
	want := &nsx.Client{}
	s := NewService()
	s.loadConfig = func() (*config.NSXOperatorConfig, error) { loads.Add(1); return &config.NSXOperatorConfig{}, nil }
	s.newClient = func(*config.NSXOperatorConfig) (*nsx.Client, error) { builds.Add(1); return want, nil }
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

type workflowClientStub struct {
	delete func(string) error
}

func (s workflowClientStub) Delete(id string) error                  { return s.delete(id) }
func (workflowClientStub) Get(string) (*vapidata.StructValue, error) { return nil, nil }
func (workflowClientStub) List(*string, *string, *int64, *bool, *string) (nsxmodel.PolicyWorkflowListResult, error) {
	return nsxmodel.PolicyWorkflowListResult{}, nil
}
func (workflowClientStub) Update(string, *vapidata.StructValue) (*vapidata.StructValue, error) {
	return nil, nil
}

func TestDeleteWorkflowIsIdempotentForRecordedMissingWorkflow(t *testing.T) {
	notFound := vapierrors.NewNotFound()
	var deleted string
	s := NewService()
	s.loadConfig = func() (*config.NSXOperatorConfig, error) { return &config.NSXOperatorConfig{}, nil }
	s.newClient = func(*config.NSXOperatorConfig) (*nsx.Client, error) {
		return &nsx.Client{WorkflowsClient: workflowClientStub{delete: func(id string) error {
			deleted = id
			return notFound
		}}}, nil
	}

	require.NoError(t, s.DeleteWorkflow(context.Background(), "workflow-1"))
	require.Equal(t, "workflow-1", deleted)
	require.ErrorContains(t, s.DeleteWorkflow(context.Background(), ""), "workflow ID is required")
}

func TestDeleteWorkflowRetriesUnexpectedErrors(t *testing.T) {
	failure := errors.New("NSX unavailable")
	s := NewService()
	s.loadConfig = func() (*config.NSXOperatorConfig, error) { return &config.NSXOperatorConfig{}, nil }
	s.newClient = func(*config.NSXOperatorConfig) (*nsx.Client, error) {
		return &nsx.Client{WorkflowsClient: workflowClientStub{delete: func(string) error { return failure }}}, nil
	}

	require.ErrorIs(t, s.DeleteWorkflow(context.Background(), "workflow-1"), failure)
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
	s.newClient = func(*config.NSXOperatorConfig) (*nsx.Client, error) { return nil, nil }
	_, err := s.Client()
	require.ErrorIs(t, err, failure)
	ready = true
	_, err = s.Client()
	require.ErrorContains(t, err, "initialization failed")
	s.newClient = func(*config.NSXOperatorConfig) (*nsx.Client, error) { return want, nil }
	got, err := s.Client()
	require.NoError(t, err)
	require.Same(t, want, got)
}

func TestConstructorErrorCanRecover(t *testing.T) {
	s := NewService()
	s.loadConfig = func() (*config.NSXOperatorConfig, error) { return &config.NSXOperatorConfig{}, nil }
	failure := errors.New("constructor failed")
	s.newClient = func(*config.NSXOperatorConfig) (*nsx.Client, error) { return &nsx.Client{}, failure }
	_, err := s.Client()
	require.ErrorIs(t, err, failure)
	require.Nil(t, s.client, "a partially initialized client must not be cached")
	want := &nsx.Client{}
	s.newClient = func(*config.NSXOperatorConfig) (*nsx.Client, error) { return want, nil }
	got, err := s.Client()
	require.NoError(t, err)
	require.Same(t, want, got)
}

func TestRealConstructorRejectsMalformedEndpoint(t *testing.T) {
	s := NewService()
	cfg := config.NewNSXOpertorConfig()
	cfg.NsxApiManagers = []string{"https://[invalid"}
	cfg.Insecure = true
	s.loadConfig = func() (*config.NSXOperatorConfig, error) { return cfg, nil }
	require.NotPanics(t, func() {
		client, err := s.Client()
		require.Nil(t, client)
		require.ErrorContains(t, err, "creating NSX cluster")
	})
	require.Nil(t, s.client)
}
