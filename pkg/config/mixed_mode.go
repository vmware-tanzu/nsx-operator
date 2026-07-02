/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
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
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
)

const (
	VPCNetworkConfigAnnotation = "nsx.vmware.com/vpc_network_config"

	capabilitiesName = "supervisor-capabilities"
)

var (
	capabilitiesGVR = schema.GroupVersionResource{
		Group:    "iaas.vmware.com",
		Version:  "v1alpha1",
		Resource: "capabilities",
	}
	networkSettingsGVR = schema.GroupVersionResource{
		Group:    "netoperator.vmware.com",
		Version:  "v1alpha1",
		Resource: "networksettings",
	}

	stateMu                        sync.RWMutex
	hasT1Namespaces                bool
	hasVPCNamespaces               bool
	hasVDSNamespaces               bool
	perNamespaceProvidersSupported *bool
	stateInitialized               bool

	// retryInitialInterval and retryMaxInterval control the exponential
	// backoff used when a transient error prevents reading
	// Capabilities or listing namespaces. Overridable in tests.
	retryInitialInterval = 2 * time.Second
	retryMaxInterval     = 30 * time.Second

	// storedDynClient and storedEnableVPCNetwork are kept from InitMixedMode so that
	// RefreshMixedModeState can re-scan without requiring the caller to pass them each time.
	storedDynClient        dynamic.Interface
	storedEnableVPCNetwork bool

	// namespaceRefreshReader, when non-nil, is used by RefreshMixedModeState to list
	// namespaces from the controller-runtime cache (mgr.GetClient()) instead of
	// a direct API list on storedClientset — reducing apiserver load on the 30s
	// refresh ticker. Set via SetMixedModeNamespaceRefreshReader from cmd after
	// controllers are registered on the manager.
	namespaceRefreshReader client.Reader
	refreshReaderMu        sync.RWMutex
)

var log = logger.Log

// checkPerNamespaceProvidersSupported fetches the Capabilities object and
// returns whether per-namespace network providers are activated. It retries
// all errors with exponential backoff (starting at retryInitialInterval,
// doubling each attempt, capped at retryMaxInterval). The Capabilities
// CR is guaranteed to exist; all failures are treated as transient (e.g. API
// server not yet ready at operator startup). Returns false only when the
// context is cancelled.
func checkPerNamespaceProvidersSupported(ctx context.Context, dynClient dynamic.Interface) (bool, error) {
	interval := retryInitialInterval
	for {
		obj, err := dynClient.Resource(capabilitiesGVR).Get(
			ctx, capabilitiesName, metav1.GetOptions{})
		if err == nil {
			return extractCapability(obj), nil
		}
		log.Info("Failed to get Capabilities, will retry", "error", err, "retryIn", interval)
		select {
		case <-ctx.Done():
			log.Info("Context cancelled while waiting for Capabilities, falling back to legacy config")
			return false, ctx.Err()
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
	supervisor, found, err := unstructured.NestedMap(status, "supervisor")
	if err != nil || !found {
		return false
	}
	cap, ok := supervisor["supports_per_namespace_network_provider"]
	if !ok {
		return false
	}
	capMap, ok := cap.(map[string]interface{})
	if !ok {
		return false
	}
	activated, ok := capMap["activated"]
	if ok {
		if b, ok := activated.(bool); ok && b {
			return true
		}
	}
	return false
}

func parseProvidersFromList(items []unstructured.Unstructured) (hasT1 bool, hasVPC bool, hasVDS bool) {
	for _, item := range items {
		provider, _, _ := unstructured.NestedString(item.Object, "provider")
		switch provider {
		case "nsx-tier1":
			hasT1 = true
		case "vpc":
			hasVPC = true
		case "vsphere-distributed":
			hasVDS = true
		}
		if hasT1 && hasVPC && hasVDS {
			break
		}
	}
	return hasT1, hasVPC, hasVDS
}

// scanNamespaceProvidersFromAPI uses dynamic.Interface to directly list NetworkSettings
// from the API Server. Used during initialization before Informer caches are ready.
func scanNamespaceProvidersFromAPI(ctx context.Context, dynClient dynamic.Interface) (hasT1 bool, hasVPC bool, hasVDS bool, err error) {
	if dynClient != nil {
		list, err := dynClient.Resource(networkSettingsGVR).List(ctx, metav1.ListOptions{})
		if err == nil && len(list.Items) > 0 {
			t1, vpc, vds := parseProvidersFromList(list.Items)
			return t1, vpc, vds, nil
		} else if err != nil {
			log.V(1).Info("Failed to list NetworkSettings", "error", err)
			return false, false, false, err
		}
	}
	return false, false, false, nil
}

// scanNamespaceProvidersFromCache uses controller-runtime client.Reader to list NetworkSettings
// from the local Informer cache. Used during periodic refresh to avoid API Server load.
func scanNamespaceProvidersFromCache(ctx context.Context, reader client.Reader) (hasT1 bool, hasVPC bool, hasVDS bool, err error) {
	nsList := &unstructured.UnstructuredList{}
	nsList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "netoperator.vmware.com",
		Version: "v1alpha1",
		Kind:    "NetworkSettingsList",
	})
	if err := reader.List(ctx, nsList); err == nil && len(nsList.Items) > 0 {
		t1, vpc, vds := parseProvidersFromList(nsList.Items)
		return t1, vpc, vds, nil
	} else if err != nil {
		log.V(1).Info("Failed to list NetworkSettings with reader", "error", err)
		return false, false, false, err
	}
	return false, false, false, nil
}

// StartNetworkSettingsInformer registers a cache-backed client.Reader
// (typically mgr.GetClient()) for periodic mixed-mode rescans and ensures
// the required Informers are started.
// Call once from cmd after controllers are set up on the manager.
func StartNetworkSettingsInformer(mgr manager.Manager) {
	if !IsPerNamespaceProvidersSupported() {
		return
	}

	// Ensure NetworkSettings is cached in the controller-runtime manager and the
	// informer is synced before enabling the cache reader. Until the reader is set,
	// RefreshMixedModeState falls back to a direct API list, so a not-yet-synced
	// cache cannot make refresh observe an empty NetworkSettings set and trigger a
	// spurious mode change/restart.
	go func() {
		nsObj := &unstructured.Unstructured{}
		nsObj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "netoperator.vmware.com",
			Version: "v1alpha1",
			Kind:    "NetworkSettings",
		})
		interval := retryInitialInterval
		for {
			informer, err := mgr.GetCache().GetInformer(context.TODO(), nsObj)
			if err != nil {
				log.Info("Failed to start informer for NetworkSettings, will retry", "error", err, "retryIn", interval)
				time.Sleep(interval)
				interval = min(interval*2, retryMaxInterval)
				continue
			}
			if !informer.HasSynced() {
				time.Sleep(time.Second)
				continue
			}
			refreshReaderMu.Lock()
			namespaceRefreshReader = mgr.GetClient()
			refreshReaderMu.Unlock()
			log.Info("Informer for NetworkSettings synced successfully; enabling cache reader")
			break
		}
	}()
}

func currentNamespaceRefreshReader() client.Reader {
	refreshReaderMu.RLock()
	defer refreshReaderMu.RUnlock()
	return namespaceRefreshReader
}

// waitForNamespaceProviders retries scanNamespaceProvidersFromAPI with exponential
// backoff on transient errors (e.g. API server not yet ready at operator startup).
func waitForNamespaceProviders(ctx context.Context, dynClient dynamic.Interface) (bool, bool, bool) {
	interval := retryInitialInterval
	for {
		hasT1, hasVPC, hasVDS, err := scanNamespaceProvidersFromAPI(ctx, dynClient)
		if err == nil {
			return hasT1, hasVPC, hasVDS
		}
		log.Info("Failed to list providers for mixed-mode scan, will retry", "error", err, "retryIn", interval)
		select {
		case <-ctx.Done():
			log.Info("Context cancelled during mixed-mode scan, returning empty state")
			return false, false, false
		case <-time.After(interval):
		}
		interval = min(interval*2, retryMaxInterval)
	}
}

// InitMixedMode initializes mixed-mode state by checking Capabilities
// and scanning NetworkSettings CRs for T1, VPC, and VDS network providers.
// If per-namespace providers are not activated, falls back to the legacy EnableVPCNetwork flag.
//
// The Capabilities lookup is performed outside the state mutex so
// that transient API errors can be retried without blocking readers for an
// extended period.
func InitMixedMode(ctx context.Context, cfg *rest.Config, enableVPCNetwork bool) error {
	dynClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return err
	}
	initMixedModeWithClients(ctx, dynClient, enableVPCNetwork)
	return nil
}

func initMixedModeWithClients(ctx context.Context, dynClient dynamic.Interface, enableVPCNetwork bool) {
	// checkPerNamespaceProvidersSupported retries on transient errors; runs outside
	// the mutex to avoid holding the lock during potentially many retries.
	supported, _ := checkPerNamespaceProvidersSupported(ctx, dynClient)

	var t1, vpc, vds bool
	if supported {
		log.Info("Per-namespace network providers are supported, scanning namespaces for mixed-mode")
		t1, vpc, vds = waitForNamespaceProviders(ctx, dynClient)
		if enableVPCNetwork {
			vpc = true
		}
	} else {
		log.Info("Per-namespace network providers not supported, using legacy config", "enableVPCNetwork", enableVPCNetwork)
		if enableVPCNetwork {
			t1, vpc, vds = false, true, false
		} else {
			t1, vpc, vds = true, false, false
		}
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	storedDynClient = dynClient
	storedEnableVPCNetwork = enableVPCNetwork
	// The capability can be changed in day2. However, we intentionally do NOT poll or watch this capability during runtime to save API Server overhead.
	// By design, if it changes from deactivated to activated, an external component will restart the nsx-operator pod; changing from activated to deactivated is not a valid scenario.
	perNamespaceProvidersSupported = &supported
	hasT1Namespaces = t1
	hasVPCNamespaces = vpc
	hasVDSNamespaces = vds
	stateInitialized = true
	log.Info("Mixed-mode state initialized", "hasT1Namespaces", t1, "hasVPCNamespaces", vpc, "hasVDSNamespaces", vds)
}

// RefreshMixedModeState re-scans namespaces using the dynamic client stored during
// InitMixedMode and updates the global state. Returns true if the state
// changed; the caller should then restart the operator so that VPC services
// and controllers are initialized for the new mode.
func RefreshMixedModeState(ctx context.Context) bool {
	stateMu.Lock()
	defer stateMu.Unlock()

	if storedDynClient == nil {
		log.V(1).Info("Skipping mixed-mode refresh: storedDynClient is nil")
		return false
	}

	if perNamespaceProvidersSupported != nil && !*perNamespaceProvidersSupported {
		log.V(1).Info("Skipping mixed-mode refresh: per-namespace network providers are not supported")
		return false
	}

	oldT1, oldVPC, oldVDS := hasT1Namespaces, hasVPCNamespaces, hasVDSNamespaces
	var newT1, newVPC, newVDS bool
	var err error
	if r := currentNamespaceRefreshReader(); r != nil {
		newT1, newVPC, newVDS, err = scanNamespaceProvidersFromCache(ctx, r)
	} else {
		newT1, newVPC, newVDS, err = scanNamespaceProvidersFromAPI(ctx, storedDynClient)
	}
	if storedEnableVPCNetwork {
		newVPC = true
	}
	if err != nil {
		log.Warn("Failed to scan namespaces during mixed-mode refresh, keeping current state", "error", err)
		return false
	}
	hasT1Namespaces = newT1
	hasVPCNamespaces = newVPC
	hasVDSNamespaces = newVDS

	changed := oldT1 != hasT1Namespaces || oldVPC != hasVPCNamespaces || oldVDS != hasVDSNamespaces
	if changed {
		log.Info("Mixed-mode state changed",
			"oldT1", oldT1, "newT1", hasT1Namespaces,
			"oldVPC", oldVPC, "newVPC", hasVPCNamespaces,
			"oldVDS", oldVDS, "newVDS", hasVDSNamespaces)
	}
	return changed
}

// HasT1Namespaces returns true when at least one NetworkSettings CR uses T1.
func HasT1Namespaces() bool {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return hasT1Namespaces
}

// HasVPCNamespaces returns true when enable_vpc_network is true or at least one NetworkSettings CR uses VPC.
func HasVPCNamespaces() bool {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return hasVPCNamespaces
}

// HasVDSNamespaces returns true when at least one NetworkSettings CR uses VDS.
func HasVDSNamespaces() bool {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return hasVDSNamespaces
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
	hasVDSNamespaces = false
	stateInitialized = true
}

// IsPerNamespaceProvidersSupported returns true when Capabilities
// advertises per-namespace network providers.
func IsPerNamespaceProvidersSupported() bool {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return perNamespaceProvidersSupported != nil && *perNamespaceProvidersSupported
}

// IsVPCNamespace reports whether ns should be treated as a VPC namespace.
// In mixed mode (when per-namespace providers are supported), a non-empty
// VPCNetworkConfigAnnotation marks a VPC namespace.
// In legacy mode (pre-9.2), the whole cluster runs a single provider, so the
// cluster-level HasVPCNamespaces flag (derived from EnableVPCNetwork) is
// returned regardless of the namespace.
func IsVPCNamespace(ns *v1.Namespace) bool {
	if ns == nil {
		return false
	}
	if IsPerNamespaceProvidersSupported() {
		return strings.TrimSpace(ns.Annotations[VPCNetworkConfigAnnotation]) != ""
	}
	return HasVPCNamespaces()
}
