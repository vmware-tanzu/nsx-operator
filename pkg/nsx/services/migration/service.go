/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Package migration provides shared NSX connectivity for migration handlers.
package migration

import (
	"fmt"
	"sync"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

// Service lazily initializes a process-local NSX client using the standard config,
// TLS, authentication, token refresh, endpoint failover, and transport implementation.
// Initialization failures are retried on subsequent calls, not cached permanently.
type Service struct {
	mu         sync.Mutex
	client     *nsx.Client
	loadConfig func() (*config.NSXOperatorConfig, error)
	newClient  func(*config.NSXOperatorConfig) (*nsx.Client, error)
}

// NewService defers reading configuration and contacting NSX until a handler needs it.
func NewService() *Service {
	return &Service{loadConfig: config.NewNSXOperatorConfigFromFile, newClient: nsx.NewClient}
}

// Client returns the shared client for this migration process. It does not share
// sessions or rate-limit state with the NSX operator container.
func (s *Service) Client() (*nsx.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		return s.client, nil
	}
	cfg, err := s.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("loading NSX configuration: %w", err)
	}
	if cfg == nil {
		return nil, fmt.Errorf("NSX configuration is unavailable")
	}
	client, err := s.newClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("initializing NSX client: %w", err)
	}
	if client == nil {
		return nil, fmt.Errorf("NSX client initialization failed")
	}
	s.client = client
	return client, nil
}
