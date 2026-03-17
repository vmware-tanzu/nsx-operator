/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package config

import (
	"context"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
)

const (
	NetworkProviderLabel   = "iaas.vmware.com/network-provider"
	ProviderNSXT1          = "nsx-t1"
	ProviderNSXVPC         = "nsx-vpc"
	ProviderVSphereNetwork = "vsphere-network"

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
)

var log = logger.Log

// checkPerNamespaceProvidersSupported fetches the SupervisorCapabilities object
// once and returns whether per-namespace network providers are activated.
// Any error is returned to the caller for retry; the SupervisorCapabilities CR
// is guaranteed to exist and all failures are treated as transient.
func checkPerNamespaceProvidersSupported(ctx context.Context, dynClient dynamic.Interface) (bool, error) {
	obj, err := dynClient.Resource(supervisorCapabilitiesGVR).Get(
		ctx, supervisorCapabilitiesName, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	return extractCapability(obj), nil
}

// waitForSupervisorCapabilities calls checkPerNamespaceProvidersSupported and
// retries all errors with exponential backoff (starting at retryInitialInterval,
// doubling each attempt, capped at retryMaxInterval). The SupervisorCapabilities
// CR is guaranteed to exist; all failures are treated as transient (e.g. API
// server not yet ready at operator startup). Returns false only when the
// context is cancelled.
func waitForSupervisorCapabilities(ctx context.Context, dynClient dynamic.Interface) bool {
	interval := retryInitialInterval
	for {
		supported, err := checkPerNamespaceProvidersSupported(ctx, dynClient)
		if err == nil {
			return supported
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

func scanNamespaceLabels(ctx context.Context, clientset kubernetes.Interface) (hasT1 bool, hasVPC bool, err error) {
	nsList, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, false, err
	}
	for _, ns := range nsList.Items {
		provider := ns.Labels[NetworkProviderLabel]
		switch provider {
		case ProviderNSXT1:
			hasT1 = true
		case ProviderNSXVPC:
			hasVPC = true
		}
	}
	return hasT1, hasVPC, nil
}

// waitForNamespaceLabels retries scanNamespaceLabels with exponential backoff
// on transient errors (e.g. API server not yet ready at operator startup).
func waitForNamespaceLabels(ctx context.Context, clientset kubernetes.Interface) (bool, bool) {
	interval := retryInitialInterval
	for {
		hasT1, hasVPC, err := scanNamespaceLabels(ctx, clientset)
		if err == nil {
			return hasT1, hasVPC
		}
		log.Warn("Failed to list namespaces for mixed-mode detection, will retry", "error", err, "retryIn", interval)
		select {
		case <-ctx.Done():
			log.Info("Context cancelled while scanning namespace labels, returning empty state")
			return false, false
		case <-time.After(interval):
		}
		interval = min(interval*2, retryMaxInterval)
	}
}

// InitMixedMode initializes mixed-mode state by checking SupervisorCapabilities
// and scanning namespace labels. If per-namespace providers are not activated,
// falls back to the legacy EnableVPCNetwork flag.
//
// The SupervisorCapabilities lookup is performed outside the state mutex so
// that transient API errors can be retried without blocking readers for an
// extended period.
func InitMixedMode(ctx context.Context, clientset kubernetes.Interface, dynClient dynamic.Interface, enableVPCNetwork bool) {
	// waitForSupervisorCapabilities retries on transient errors; runs outside
	// the mutex to avoid holding the lock during potentially many retries.
	supported := waitForSupervisorCapabilities(ctx, dynClient)

	var t1, vpc bool
	if supported {
		log.Info("Per-namespace network providers are supported, scanning namespace labels")
		t1, vpc = waitForNamespaceLabels(ctx, clientset)
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

// RefreshMixedMode re-scans namespace labels and updates state.
// Returns true if the state changed (caller should consider restarting).
func RefreshMixedMode(ctx context.Context, clientset kubernetes.Interface) bool {
	stateMu.Lock()
	defer stateMu.Unlock()

	if perNamespaceProvidersSupported == nil || !*perNamespaceProvidersSupported {
		return false
	}

	oldT1, oldVPC := hasT1Namespaces, hasVPCNamespaces
	newT1, newVPC, err := scanNamespaceLabels(ctx, clientset)
	if err != nil {
		log.Warn("Failed to scan namespace labels during refresh, keeping current state", "error", err)
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

// RefreshMixedModeState re-scans namespace labels using the clientset stored
// during InitMixedMode and updates the global state.  Returns true if the
// state changed; the caller should then restart the operator so that VPC
// services and controllers are initialized for the new mode.
//
// This is a convenience wrapper around RefreshMixedMode for callers that do
// not hold the clientset themselves (e.g. the background goroutine in main).
func RefreshMixedModeState(ctx context.Context) bool {
	stateMu.RLock()
	c := storedClientset
	stateMu.RUnlock()
	if c == nil {
		return false
	}
	return RefreshMixedMode(ctx, c)
}

// IsPerNamespaceProvidersSupported returns true when SupervisorCapabilities
// advertises per-namespace network providers.
func IsPerNamespaceProvidersSupported() bool {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return perNamespaceProvidersSupported != nil && *perNamespaceProvidersSupported
}

// GetNamespaceNetworkProvider returns the network provider string from a
// namespace's labels.
func GetNamespaceNetworkProvider(ns *v1.Namespace) string {
	if ns == nil || ns.Labels == nil {
		return ""
	}
	return ns.Labels[NetworkProviderLabel]
}
