/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

type startupManager struct {
	ctrl.Manager
	failAt      string
	failure     error
	healthNames []string
	readyNames  []string
	started     bool
	ctx         context.Context
}

func (m *startupManager) AddHealthzCheck(name string, check healthz.Checker) error {
	if m.failAt == "health" {
		return m.failure
	}
	m.healthNames = append(m.healthNames, name)
	return check(nil)
}

func (m *startupManager) AddReadyzCheck(name string, check healthz.Checker) error {
	if m.failAt == "ready" {
		return m.failure
	}
	m.readyNames = append(m.readyNames, name)
	return check(nil)
}

func (m *startupManager) Start(ctx context.Context) error {
	m.started, m.ctx = true, ctx
	if m.failAt == "start" {
		return m.failure
	}
	return nil
}

func TestManagerStartupWiring(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cfg := &rest.Config{Host: "https://kubernetes.invalid", Timeout: 30 * time.Second}
			mgr := &startupManager{}
			registered := false
			deps := managerDependencies{
				loadConfig: func() (*rest.Config, error) {
					require.False(t, enabled, "enabled startup must retain API health checking")
					return cfg, nil
				},
				getConfig: func() (*rest.Config, error) {
					require.True(t, enabled, "disabled startup must not probe API health")
					return cfg, nil
				},
				newManager: func(got *rest.Config, opts ctrl.Options) (ctrl.Manager, error) {
					require.Same(t, cfg, got)
					require.Equal(t, enabled, opts.LeaderElection)
					require.Equal(t, migrationLeaseID, opts.LeaderElectionID)
					if enabled {
						require.Equal(t, "ncp-system", opts.LeaderElectionNamespace)
					}
					require.NotSame(t, cfg, opts.LeaderElectionConfig)
					require.Equal(t, 5*time.Second, opts.LeaderElectionConfig.Timeout)
					for _, kind := range []string{"ControlPlaneNetworkTransition", "NamespaceNetworkTransition"} {
						require.True(t, opts.Scheme.Recognizes(api.GroupVersion.WithKind(kind)))
					}
					require.True(t, opts.Scheme.Recognizes(corev1.SchemeGroupVersion.WithKind("Pod")))
					return mgr, nil
				},
				setupControllers: func(got ctrl.Manager) error {
					require.Same(t, mgr, got)
					registered = true
					return nil
				},
			}
			namespace := "ncp-system"
			if !enabled {
				namespace = ""
			}
			require.NoError(t, startManager(ctx, &options{enabled: enabled, namespace: namespace}, deps))
			require.Equal(t, enabled, registered)
			require.True(t, mgr.started)
			require.Equal(t, ctx, mgr.ctx)
			require.Equal(t, []string{"healthz"}, mgr.healthNames)
			require.Equal(t, []string{"readyz"}, mgr.readyNames)
			require.Equal(t, 30*time.Second, cfg.Timeout, "leader-election timeout must not mutate the shared REST config")
		})
	}
}

func TestManagerStartupFailures(t *testing.T) {
	for _, failAt := range []string{"namespace", "config", "manager", "controllers", "health", "ready", "start"} {
		t.Run(failAt, func(t *testing.T) {
			failure := errors.New("startup failure")
			mgr := &startupManager{failAt: failAt, failure: failure}
			deps := managerDependencies{
				getConfig: func() (*rest.Config, error) {
					if failAt == "config" {
						return nil, failure
					}
					return &rest.Config{}, nil
				},
				newManager: func(*rest.Config, ctrl.Options) (ctrl.Manager, error) {
					if failAt == "manager" {
						return nil, failure
					}
					return mgr, nil
				},
				setupControllers: func(ctrl.Manager) error {
					if failAt == "controllers" {
						return failure
					}
					return nil
				},
			}
			opts := &options{enabled: true, namespace: "ncp-system"}
			if failAt == "namespace" {
				opts.namespace = ""
			}
			err := startManager(context.Background(), opts, deps)
			if failAt == "namespace" {
				require.ErrorContains(t, err, "namespace must not be empty")
			} else {
				require.ErrorIs(t, err, failure)
			}
			require.Equal(t, failAt == "start", mgr.started, "startup must stop at the first failed prerequisite")
		})
	}
}

func TestDisabledStartupSkipsUnhealthyAPI(t *testing.T) {
	cfg := &rest.Config{Host: "https://unhealthy-api.invalid"}
	mgr := &startupManager{}
	probed := false
	deps := managerDependencies{
		loadConfig: func() (*rest.Config, error) { return cfg, nil },
		getConfig: func() (*rest.Config, error) {
			probed = true
			return nil, errors.New("Kubernetes API Server is unhealthy, status: 503 Service Unavailable")
		},
		newManager: func(got *rest.Config, opts ctrl.Options) (ctrl.Manager, error) {
			require.Same(t, cfg, got)
			require.False(t, opts.LeaderElection)
			return mgr, nil
		},
		setupControllers: func(ctrl.Manager) error { t.Fatal("disabled startup must not register controllers"); return nil },
	}
	require.NoError(t, startManager(context.Background(), &options{}, deps))
	require.False(t, probed)
	require.True(t, mgr.started)
	require.Equal(t, []string{"healthz"}, mgr.healthNames)
	require.Equal(t, []string{"readyz"}, mgr.readyNames)
}

func TestDisabledStartupReportsInvalidLocalConfig(t *testing.T) {
	failure := errors.New("invalid kubeconfig")
	deps := managerDependencies{
		loadConfig: func() (*rest.Config, error) { return nil, failure },
		getConfig: func() (*rest.Config, error) {
			t.Fatal("must not probe or fall back after invalid local configuration")
			return nil, nil
		},
		newManager: func(*rest.Config, ctrl.Options) (ctrl.Manager, error) {
			t.Fatal("must not construct manager with invalid local configuration")
			return nil, nil
		},
	}
	require.ErrorIs(t, startManager(context.Background(), &options{}, deps), failure)
}
