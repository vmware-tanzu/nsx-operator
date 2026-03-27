/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package config

import (
	"context"
	"sync"

	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
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
	stateLog                       *zap.SugaredLogger
)

func init() {
	zapLogger, _ := zap.NewProduction()
	stateLog = zapLogger.Sugar()
}

func checkPerNamespaceProvidersSupported(ctx context.Context, dynClient dynamic.Interface) bool {
	obj, err := dynClient.Resource(supervisorCapabilitiesGVR).Get(
		ctx, supervisorCapabilitiesName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			stateLog.Info("SupervisorCapabilities CRD not found; " +
				"falling back to legacy config")
		} else {
			stateLog.Infof("Failed to get SupervisorCapabilities: %v; "+
				"falling back to legacy config", err)
		}
		return false
	}
	return extractCapability(obj)
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

func scanNamespaceLabels(ctx context.Context, clientset kubernetes.Interface) (hasT1 bool, hasVPC bool) {
	nsList, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		stateLog.Errorf("Failed to list namespaces for mixed-mode state detection: %v", err)
		return false, false
	}
	for _, ns := range nsList.Items {
		provider := ns.Labels[NetworkProviderLabel]
		switch provider {
		case ProviderNSXT1:
			hasT1 = true
		case ProviderNSXVPC:
			hasVPC = true
		case ProviderVSphereNetwork:
			hasVPC = true
		}
	}
	return hasT1, hasVPC
}

// InitMixedMode initializes mixed-mode state by checking SupervisorCapabilities
// and scanning namespace labels. If per-namespace providers are not supported,
// falls back to the legacy EnableVPCNetwork flag.
func InitMixedMode(ctx context.Context, clientset kubernetes.Interface, dynClient dynamic.Interface, enableVPCNetwork bool) {
	stateMu.Lock()
	defer stateMu.Unlock()

	supported := checkPerNamespaceProvidersSupported(ctx, dynClient)
	perNamespaceProvidersSupported = &supported

	if supported {
		stateLog.Info("Per-namespace network providers are supported " +
			"(SupervisorCapabilities); scanning namespace labels")
		hasT1Namespaces, hasVPCNamespaces = scanNamespaceLabels(ctx, clientset)
	} else {
		stateLog.Infof("Per-namespace network providers not supported; "+
			"using legacy EnableVPCNetwork=%v", enableVPCNetwork)
		if enableVPCNetwork {
			hasT1Namespaces = false
			hasVPCNamespaces = true
		} else {
			hasT1Namespaces = true
			hasVPCNamespaces = false
		}
	}

	stateInitialized = true
	stateLog.Infof("Mixed-mode state initialized: HasT1Namespaces=%v, "+
		"HasVPCNamespaces=%v", hasT1Namespaces, hasVPCNamespaces)
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
	hasT1Namespaces, hasVPCNamespaces = scanNamespaceLabels(ctx, clientset)

	changed := oldT1 != hasT1Namespaces || oldVPC != hasVPCNamespaces
	if changed {
		stateLog.Infof("Mixed-mode state changed: HasT1Namespaces=%v->%v, "+
			"HasVPCNamespaces=%v->%v", oldT1, hasT1Namespaces, oldVPC, hasVPCNamespaces)
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

// GetNamespaceNetworkProvider returns the network provider string from a
// namespace's labels.
func GetNamespaceNetworkProvider(ns *v1.Namespace) string {
	if ns == nil || ns.Labels == nil {
		return ""
	}
	return ns.Labels[NetworkProviderLabel]
}
