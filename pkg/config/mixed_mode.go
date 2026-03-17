/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package config

import (
	"context"
	"strings"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
)

const (
	VPCNetworkConfigAnnotation = "nsx.vmware.com/vpc_network_config"

	supervisorCapabilitiesName = "supervisor-capabilities"
)

var (
	supervisorCapabilitiesGVR = schema.GroupVersionResource{
		Group:    "iaas.vmware.com",
		Version:  "v1alpha1",
		Resource: "supervisorcapabilities",
	}

	stateMu                        sync.RWMutex
	hasT1Namespaces                bool
	hasVPCNamespaces               bool
	perNamespaceProvidersSupported *bool
	stateInitialized               bool

	// retryInitialInterval and retryMaxInterval control the exponential
	// backoff used when a transient error prevents reading
	// SupervisorCapabilities or listing namespaces. Overridable in tests.
	retryInitialInterval = 2 * time.Second
	retryMaxInterval     = 30 * time.Second

	// storedClientset is kept from InitMixedMode so that RefreshMixedModeState
	// can re-scan without requiring the caller to pass it each time.
	storedClientset kubernetes.Interface

	// namespaceRefreshReader, when non-nil, is used by RefreshMixedModeState to list
	// namespaces from the controller-runtime cache (mgr.GetClient()) instead of
	// a direct API list on storedClientset — reducing apiserver load on the 30s
	// refresh ticker. Set via SetMixedModeNamespaceRefreshReader from cmd after
	// controllers are registered on the manager.
	namespaceRefreshReader client.Reader
	refreshReaderMu        sync.RWMutex
)

var log = logger.Log

// checkPerNamespaceProvidersSupported fetches the SupervisorCapabilities object and
// returns whether per-namespace network providers are activated. It retries
// all errors with exponential backoff (starting at retryInitialInterval,
// doubling each attempt, capped at retryMaxInterval). The SupervisorCapabilities
// CR is guaranteed to exist; all failures are treated as transient (e.g. API
// server not yet ready at operator startup). Returns false only when the
// context is cancelled.
func checkPerNamespaceProvidersSupported(ctx context.Context, dynClient dynamic.Interface) bool {
	interval := retryInitialInterval
	for {
		obj, err := dynClient.Resource(supervisorCapabilitiesGVR).Get(
			ctx, supervisorCapabilitiesName, metav1.GetOptions{})
		if err == nil {
			return extractCapability(obj)
		}
		log.Info("Failed to get SupervisorCapabilities, will retry", "error", err, "retryIn", interval)
		select {
		case <-ctx.Done():
			log.Info("Context cancelled while waiting for SupervisorCapabilities, falling back to legacy config")
			return false
		case <-time.After(interval):
		}
		interval = min(interval*2, retryMaxInterval)
	}
}

func extractCapability(obj *unstructured.Unstructured) bool {
	status, found, err := unstructured.NestedMap(obj.Object, "status")
	if err != nil || !found {
		return false
	}
	services, found, err := unstructured.NestedMap(status, "services")
	if err != nil || !found {
		return false
	}
	for _, svcCaps := range services {
		capsMap, ok := svcCaps.(map[string]interface{})
		if !ok {
			continue
		}
		cap, ok := capsMap["supports_per_namespace_network_providers"]
		if !ok {
			continue
		}
		capMap, ok := cap.(map[string]interface{})
		if !ok {
			continue
		}
		activated, ok := capMap["activated"]
		if ok {
			if b, ok := activated.(bool); ok && b {
				return true
			}
		}
	}
	return false
}

func namespaceHasVPCNetworkConfig(ns *v1.Namespace) bool {
	if ns == nil {
		return false
	}
	v := strings.TrimSpace(ns.Annotations[VPCNetworkConfigAnnotation])
	return v != ""
}

func accumulateMixedModeFlagsFromNamespaces(items []v1.Namespace) (hasT1 bool, hasVPC bool) {
	for i := range items {
		if namespaceHasVPCNetworkConfig(&items[i]) {
			hasVPC = true
		} else {
			hasT1 = true
		}
	}
	return hasT1, hasVPC
}

func scanNamespaceProviders(ctx context.Context, clientset kubernetes.Interface) (hasT1 bool, hasVPC bool, err error) {
	nsList, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, false, err
	}
	hasT1, hasVPC = accumulateMixedModeFlagsFromNamespaces(nsList.Items)
	return hasT1, hasVPC, nil
}

func scanNamespaceProvidersWithClient(ctx context.Context, reader client.Reader) (hasT1 bool, hasVPC bool, err error) {
	nsList := &v1.NamespaceList{}
	if err := reader.List(ctx, nsList); err != nil {
		return false, false, err
	}
	hasT1, hasVPC = accumulateMixedModeFlagsFromNamespaces(nsList.Items)
	return hasT1, hasVPC, nil
}

// SetMixedModeNamespaceRefreshReader registers a cache-backed client.Reader
// (typically mgr.GetClient()) for periodic mixed-mode rescans. When nil,
// RefreshMixedModeState keeps using the kubernetes.Interface from InitMixedMode.
// Call once from cmd after controllers are set up on the manager.
func SetMixedModeNamespaceRefreshReader(r client.Reader) {
	refreshReaderMu.Lock()
	defer refreshReaderMu.Unlock()
	namespaceRefreshReader = r
}

func currentNamespaceRefreshReader() client.Reader {
	refreshReaderMu.RLock()
	defer refreshReaderMu.RUnlock()
	return namespaceRefreshReader
}

// waitForNamespaceProviders retries scanNamespaceProviders with exponential
// backoff on transient errors (e.g. API server not yet ready at operator startup).
func waitForNamespaceProviders(ctx context.Context, clientset kubernetes.Interface) (bool, bool) {
	interval := retryInitialInterval
	for {
		hasT1, hasVPC, err := scanNamespaceProviders(ctx, clientset)
		if err == nil {
			return hasT1, hasVPC
		}
		log.Warn("Failed to list namespaces for mixed-mode scan, will retry", "error", err, "retryIn", interval)
		select {
		case <-ctx.Done():
			log.Info("Context cancelled during mixed-mode namespace scan, returning empty state")
			return false, false
		case <-time.After(interval):
		}
		interval = min(interval*2, retryMaxInterval)
	}
}

// InitMixedMode initializes mixed-mode state by checking SupervisorCapabilities
// and scanning namespaces (non-empty nsx.vmware.com/vpc_network_config to VPC,
// otherwise T1 for mixed-mode aggregation). If per-namespace providers are not
// activated, falls back to the legacy EnableVPCNetwork flag.
//
// The SupervisorCapabilities lookup is performed outside the state mutex so
// that transient API errors can be retried without blocking readers for an
// extended period.
func InitMixedMode(ctx context.Context, cfg *rest.Config, enableVPCNetwork bool) error {
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}
	dynClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return err
	}
	initMixedModeWithClients(ctx, clientset, dynClient, enableVPCNetwork)
	return nil
}

func initMixedModeWithClients(ctx context.Context, clientset kubernetes.Interface, dynClient dynamic.Interface, enableVPCNetwork bool) {
	// checkPerNamespaceProvidersSupported retries on transient errors; runs outside
	// the mutex to avoid holding the lock during potentially many retries.
	supported := checkPerNamespaceProvidersSupported(ctx, dynClient)

	var t1, vpc bool
	if supported {
		log.Info("Per-namespace network providers are supported, scanning namespaces for mixed-mode")
		t1, vpc = waitForNamespaceProviders(ctx, clientset)
	} else {
		log.Info("Per-namespace network providers not supported, using legacy config", "enableVPCNetwork", enableVPCNetwork)
		if enableVPCNetwork {
			t1, vpc = false, true
		} else {
			t1, vpc = true, false
		}
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	storedClientset = clientset
	perNamespaceProvidersSupported = &supported
	hasT1Namespaces = t1
	hasVPCNamespaces = vpc
	stateInitialized = true
	log.Info("Mixed-mode state initialized", "hasT1Namespaces", t1, "hasVPCNamespaces", vpc)
}

// RefreshMixedModeState re-scans namespaces using the clientset stored during
// InitMixedMode and updates the global state. Returns true if the state
// changed; the caller should then restart the operator so that VPC services
// and controllers are initialized for the new mode.
func RefreshMixedModeState(ctx context.Context) bool {
	stateMu.Lock()
	defer stateMu.Unlock()

	if perNamespaceProvidersSupported == nil || !*perNamespaceProvidersSupported {
		return false
	}
	if storedClientset == nil {
		return false
	}

	oldT1, oldVPC := hasT1Namespaces, hasVPCNamespaces
	var newT1, newVPC bool
	var err error
	if r := currentNamespaceRefreshReader(); r != nil {
		newT1, newVPC, err = scanNamespaceProvidersWithClient(ctx, r)
	} else {
		newT1, newVPC, err = scanNamespaceProviders(ctx, storedClientset)
	}
	if err != nil {
		log.Warn("Failed to scan namespaces during mixed-mode refresh, keeping current state", "error", err)
		return false
	}
	hasT1Namespaces = newT1
	hasVPCNamespaces = newVPC

	changed := oldT1 != hasT1Namespaces || oldVPC != hasVPCNamespaces
	if changed {
		log.Info("Mixed-mode state changed",
			"oldHasT1Namespaces", oldT1, "hasT1Namespaces", hasT1Namespaces,
			"oldHasVPCNamespaces", oldVPC, "hasVPCNamespaces", hasVPCNamespaces)
	}
	return changed
}

// HasT1Namespaces returns true when at least one namespace uses T1 networking.
func HasT1Namespaces() bool {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return hasT1Namespaces
}

// HasVPCNamespaces returns true when at least one namespace uses VPC (or VDS in migration).
func HasVPCNamespaces() bool {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return hasVPCNamespaces
}

// IsMixedModeStateInitialized returns true after InitMixedMode has been called.
func IsMixedModeStateInitialized() bool {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return stateInitialized
}

// SetMixedModeStateForTest sets hasT1Namespaces and hasVPCNamespaces for unit tests.
// Must only be used from test code so production always goes through InitMixedMode.
func SetMixedModeStateForTest(hasT1, hasVPC bool) {
	stateMu.Lock()
	defer stateMu.Unlock()
	hasT1Namespaces = hasT1
	hasVPCNamespaces = hasVPC
	stateInitialized = true
}

// IsPerNamespaceProvidersSupported returns true when SupervisorCapabilities
// advertises per-namespace network providers.
func IsPerNamespaceProvidersSupported() bool {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return perNamespaceProvidersSupported != nil && *perNamespaceProvidersSupported
}
