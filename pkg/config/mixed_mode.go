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
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
)

const (
	VPCNetworkConfigAnnotation     = "nsx.vmware.com/vpc_network_config"
	T1DefaultConfigAnnotation      = "nsx.vmware.com/t1_default_config"
	T1VmwareSystemSharedAnnotation = "vmware-system-shared-t1"
	T1NsxNetworkConfigAnnotation   = "ncp/nsx_network_config_crd"

	capabilitiesName = "supervisor-capabilities"
)

func hasT1ActivationAnnotation(annotations map[string]string) bool {
	if val, ok := annotations[T1DefaultConfigAnnotation]; ok {
		if strings.ToLower(strings.TrimSpace(val)) == "true" {
			return true
		}
	}
	if val, ok := annotations[T1VmwareSystemSharedAnnotation]; ok {
		if strings.ToLower(strings.TrimSpace(val)) == "true" {
			return true
		}
	}
	if val, ok := annotations[T1NsxNetworkConfigAnnotation]; ok {
		if strings.TrimSpace(val) != "" {
			return true
		}
	}
	return false
}

var (
	capabilitiesGVR = schema.GroupVersionResource{
		Group:    "iaas.vmware.com",
		Version:  "v1alpha1",
		Resource: "capabilities",
	}

	stateMu                        sync.RWMutex
	hasT1Namespaces                bool
	hasVPCNamespaces               bool
	perNamespaceProvidersSupported *bool
	stateInitialized               bool

	// retryInitialInterval and retryMaxInterval control the exponential
	// backoff used when a transient error prevents reading
	// Capabilities or listing namespaces. Overridable in tests.
	retryInitialInterval = 2 * time.Second
	retryMaxInterval     = 30 * time.Second

	// storedClientset and storedEnableVPCNetwork are kept from InitMixedMode so that
	// RefreshMixedModeState can re-scan without requiring the caller to pass them each time.
	storedClientset        kubernetes.Interface
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

// scanNamespaceProvidersFromAPI uses kubernetes.Interface to list Namespaces
// from the API Server and discover the active network providers (T1 or VPC)
// based on explicit Namespace annotations.
// Used during initialization before Informer caches are ready.
func scanNamespaceProvidersFromAPI(ctx context.Context, clientset kubernetes.Interface) (hasT1 bool, hasVPC bool, err error) {
	hasT1 = false
	hasVPC = false

	if clientset != nil {
		list, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
		if err == nil {
			for _, item := range list.Items {
				annotations := item.GetAnnotations()
				if _, ok := annotations[VPCNetworkConfigAnnotation]; ok {
					hasVPC = true
				} else if hasT1ActivationAnnotation(annotations) {
					hasT1 = true
				}
				if hasT1 && hasVPC {
					break
				}
			}
		} else {
			log.Debug("Failed to list Namespaces during mixed-mode scan", "error", err)
			return false, false, err
		}
	}

	if !hasT1 && !hasVPC {
		// Note: For the VDS to VPC Migration case, when the component starts,
		// the enable_vpc_network config will be true, safely falling back to VPC mode.
		if storedEnableVPCNetwork {
			hasVPC = true
		} else {
			hasT1 = true
		}
	}

	return hasT1, hasVPC, nil
}

// scanNamespaceProvidersFromCache uses controller-runtime client.Reader to list Namespaces
// from the local Informer cache. Used during periodic refresh to avoid API Server load.
func scanNamespaceProvidersFromCache(ctx context.Context, reader client.Reader) (hasT1 bool, hasVPC bool, err error) {
	hasT1 = false
	hasVPC = false

	nsList := &v1.NamespaceList{}
	if err := reader.List(ctx, nsList); err != nil {
		log.Debug("Failed to list Namespaces with reader during mixed-mode rescan", "error", err)
		return false, false, err
	}
	for _, item := range nsList.Items {
		annotations := item.GetAnnotations()
		if _, ok := annotations[VPCNetworkConfigAnnotation]; ok {
			hasVPC = true
		} else if hasT1ActivationAnnotation(annotations) {
			hasT1 = true
		}
		if hasT1 && hasVPC {
			break
		}
	}

	if !hasT1 && !hasVPC {
		// Note: For the VDS to VPC Migration case, when the component starts,
		// the enable_vpc_network config will be true, safely falling back to VPC mode.
		if storedEnableVPCNetwork {
			hasVPC = true
		} else {
			hasT1 = true
		}
	}

	return hasT1, hasVPC, nil
}

// StartNamespaceInformer registers a cache-backed client.Reader
// (typically mgr.GetClient()) for periodic mixed-mode rescans and ensures
// the required Informers are started.
// Call once from cmd after controllers are set up on the manager.
func StartNamespaceInformer(mgr manager.Manager) {
	if !IsPerNamespaceProvidersSupported() {
		return
	}

	// Ensure Namespace is cached in the controller-runtime manager and the
	// informer is synced before enabling the cache reader. Until the reader is set,
	// RefreshMixedModeState falls back to a direct API list, so a not-yet-synced
	// cache cannot make refresh observe an empty Namespace set and trigger a
	// spurious mode change/restart.
	go func() {
		nsObj := &v1.Namespace{}
		interval := retryInitialInterval
		for {
			informer, err := mgr.GetCache().GetInformer(context.TODO(), nsObj)
			if err != nil {
				log.Info("Failed to start informer for Namespaces, will retry", "error", err, "retryIn", interval)
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
			log.Info("Informer for Namespaces synced successfully; enabling cache reader")
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
func waitForNamespaceProviders(ctx context.Context, clientset kubernetes.Interface) (bool, bool) {
	interval := retryInitialInterval
	for {
		hasT1, hasVPC, err := scanNamespaceProvidersFromAPI(ctx, clientset)
		if err == nil {
			return hasT1, hasVPC
		}
		log.Info("Failed to list providers for mixed-mode scan, will retry", "error", err, "retryIn", interval)
		select {
		case <-ctx.Done():
			log.Info("Context cancelled during mixed-mode scan, returning empty state")
			return false, false
		case <-time.After(interval):
		}
		interval = min(interval*2, retryMaxInterval)
	}
}

// InitMixedMode initializes mixed-mode state by checking Capabilities
// and scanning Namespaces for T1 and VPC annotations.
// If per-namespace providers are not activated, falls back to the legacy EnableVPCNetwork flag.
//
// The Capabilities lookup is performed outside the state mutex so
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
	supported, _ := checkPerNamespaceProvidersSupported(ctx, dynClient)

	var t1, vpc bool
	if supported {
		log.Info("Per-namespace network providers are supported, scanning namespaces for mixed-mode")
		t1, vpc = waitForNamespaceProviders(ctx, clientset)
		if enableVPCNetwork {
			vpc = true
		}
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
	storedEnableVPCNetwork = enableVPCNetwork
	// The capability can be changed in day2. However, we intentionally do NOT poll or watch this capability during runtime to save API Server overhead.
	// By design, if it changes from deactivated to activated, an external component will restart the nsx-operator pod; changing from activated to deactivated is not a valid scenario.
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
	if storedClientset == nil {
		log.Debug("Skipping mixed-mode refresh: storedClientset is nil")
		return false
	}

	if perNamespaceProvidersSupported != nil && !*perNamespaceProvidersSupported {
		log.Debug("Skipping mixed-mode refresh: per-namespace network providers are not supported")
		return false
	}

	var newT1, newVPC bool
	var err error
	if r := currentNamespaceRefreshReader(); r != nil {
		newT1, newVPC, err = scanNamespaceProvidersFromCache(ctx, r)
	} else {
		newT1, newVPC, err = scanNamespaceProvidersFromAPI(ctx, storedClientset)
	}
	if storedEnableVPCNetwork {
		newVPC = true
	}
	if err != nil {
		log.Warn("Failed to scan namespaces during mixed-mode refresh, keeping current state", "error", err)
		return false
	}

	stateMu.Lock()
	defer stateMu.Unlock()

	oldT1, oldVPC := hasT1Namespaces, hasVPCNamespaces
	hasT1Namespaces = newT1
	hasVPCNamespaces = newVPC

	changed := oldT1 != hasT1Namespaces || oldVPC != hasVPCNamespaces
	if changed {
		log.Info("Mixed-mode state changed",
			"oldT1", oldT1, "newT1", hasT1Namespaces,
			"oldVPC", oldVPC, "newVPC", hasVPCNamespaces)
	}
	return changed
}

// HasT1Namespaces returns true when at least one Namespace has a T1 activation annotation.
func HasT1Namespaces() bool {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return hasT1Namespaces
}

// HasVPCNamespaces returns true when enable_vpc_network is true or at least one Namespace has the VPC config annotation.
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
