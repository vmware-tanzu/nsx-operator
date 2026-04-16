/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package config

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	k8stesting "k8s.io/client-go/testing"
	crfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// resetMixedModeState resets all global mixed-mode state for test isolation.
func resetMixedModeState() {
	refreshReaderMu.Lock()
	namespaceRefreshReader = nil
	refreshReaderMu.Unlock()
	stateMu.Lock()
	defer stateMu.Unlock()
	hasT1Namespaces = false
	hasVPCNamespaces = false
	perNamespaceProvidersSupported = nil
	stateInitialized = false
	storedClientset = nil
}

// makeCapabilitiesObj builds an unstructured SupervisorCapabilities object
// with the given supports_per_namespace_network_providers.activated value.
func makeCapabilitiesObj(activated bool) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "iaas.vmware.com/v1alpha1",
			"kind":       "SupervisorCapabilities",
			"metadata": map[string]interface{}{
				"name": supervisorCapabilitiesName,
			},
			"status": map[string]interface{}{
				"services": map[string]interface{}{
					"wcp": map[string]interface{}{
						"supports_per_namespace_network_providers": map[string]interface{}{
							"activated": activated,
						},
					},
				},
			},
		},
	}
}

// makeDynClientWith returns a fake dynamic client pre-seeded with a
// SupervisorCapabilities object whose activated flag is set as specified.
func makeDynClientWith(activated bool) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	fc := dynamicfake.NewSimpleDynamicClient(scheme)
	obj := makeCapabilitiesObj(activated)
	if err := fc.Tracker().Create(supervisorCapabilitiesGVR, obj, ""); err != nil {
		panic(fmt.Sprintf("test setup: could not seed capabilities object: %v", err))
	}
	return fc
}

// makeNamespace creates a Namespace. If vpcNetworkConfigValue is non-empty
// (after trim), the namespace is treated as VPC for mixed-mode discovery;
// otherwise it counts as T1.
func makeNamespace(name, vpcNetworkConfigValue string) *v1.Namespace {
	ns := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if strings.TrimSpace(vpcNetworkConfigValue) != "" {
		ns.Annotations = map[string]string{
			VPCNetworkConfigAnnotation: vpcNetworkConfigValue,
		}
	}
	return ns
}

// ---------- extractCapability ----------

func TestExtractCapability(t *testing.T) {
	tests := []struct {
		name   string
		obj    *unstructured.Unstructured
		expect bool
	}{
		{
			name:   "no status field",
			obj:    &unstructured.Unstructured{Object: map[string]interface{}{}},
			expect: false,
		},
		{
			name: "status without services",
			obj: &unstructured.Unstructured{Object: map[string]interface{}{
				"status": map[string]interface{}{},
			}},
			expect: false,
		},
		{
			name: "services map is empty",
			obj: &unstructured.Unstructured{Object: map[string]interface{}{
				"status": map[string]interface{}{
					"services": map[string]interface{}{},
				},
			}},
			expect: false,
		},
		{
			name: "service has no matching capability key",
			obj: &unstructured.Unstructured{Object: map[string]interface{}{
				"status": map[string]interface{}{
					"services": map[string]interface{}{
						"wcp": map[string]interface{}{
							"other_capability": map[string]interface{}{"activated": true},
						},
					},
				},
			}},
			expect: false,
		},
		{
			name:   "capability activated=false",
			obj:    makeCapabilitiesObj(false),
			expect: false,
		},
		{
			name:   "capability activated=true",
			obj:    makeCapabilitiesObj(true),
			expect: true,
		},
		{
			name: "activated is not a bool",
			obj: &unstructured.Unstructured{Object: map[string]interface{}{
				"status": map[string]interface{}{
					"services": map[string]interface{}{
						"wcp": map[string]interface{}{
							"supports_per_namespace_network_providers": map[string]interface{}{
								"activated": "yes",
							},
						},
					},
				},
			}},
			expect: false,
		},
		{
			name: "capability field is not a map",
			obj: &unstructured.Unstructured{Object: map[string]interface{}{
				"status": map[string]interface{}{
					"services": map[string]interface{}{
						"wcp": map[string]interface{}{
							"supports_per_namespace_network_providers": "true",
						},
					},
				},
			}},
			expect: false,
		},
		{
			name: "service entry is not a map",
			obj: &unstructured.Unstructured{Object: map[string]interface{}{
				"status": map[string]interface{}{
					"services": map[string]interface{}{
						"wcp": "not-a-map",
					},
				},
			}},
			expect: false,
		},
		{
			name: "multiple services, second has activated=true",
			obj: &unstructured.Unstructured{Object: map[string]interface{}{
				"status": map[string]interface{}{
					"services": map[string]interface{}{
						"svc-a": map[string]interface{}{
							"supports_per_namespace_network_providers": map[string]interface{}{
								"activated": false,
							},
						},
						"svc-b": map[string]interface{}{
							"supports_per_namespace_network_providers": map[string]interface{}{
								"activated": true,
							},
						},
					},
				},
			}},
			expect: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, extractCapability(tt.obj))
		})
	}
}

// ---------- checkPerNamespaceProvidersSupported ----------

func TestCheckPerNamespaceProvidersSupported(t *testing.T) {
	ctx := context.Background()

	// Speed up retries so tests complete in milliseconds.
	origInit, origMax := retryInitialInterval, retryMaxInterval
	retryInitialInterval = 1 * time.Millisecond
	retryMaxInterval = 4 * time.Millisecond
	defer func() { retryInitialInterval, retryMaxInterval = origInit, origMax }()

	t.Run("returns immediately on success (activated=true)", func(t *testing.T) {
		assert.True(t, checkPerNamespaceProvidersSupported(ctx, makeDynClientWith(true)))
	})

	t.Run("returns immediately on success (activated=false)", func(t *testing.T) {
		assert.False(t, checkPerNamespaceProvidersSupported(ctx, makeDynClientWith(false)))
	})

	t.Run("retries on transient error and eventually succeeds", func(t *testing.T) {
		scheme := runtime.NewScheme()
		dynClient := dynamicfake.NewSimpleDynamicClient(scheme)
		callCount := 0
		dynClient.PrependReactor("get", "*", func(_ k8stesting.Action) (bool, runtime.Object, error) {
			callCount++
			if callCount < 3 {
				return true, nil, fmt.Errorf("transient error %d", callCount)
			}
			return true, makeCapabilitiesObj(true), nil
		})
		result := checkPerNamespaceProvidersSupported(ctx, dynClient)
		assert.True(t, result)
		assert.Equal(t, 3, callCount)
	})

	t.Run("returns false on context cancellation during retry", func(t *testing.T) {
		cancelCtx, cancel := context.WithCancel(context.Background())
		scheme := runtime.NewScheme()
		dynClient := dynamicfake.NewSimpleDynamicClient(scheme)
		dynClient.PrependReactor("get", "*", func(_ k8stesting.Action) (bool, runtime.Object, error) {
			cancel()
			return true, nil, fmt.Errorf("still failing")
		})
		result := checkPerNamespaceProvidersSupported(cancelCtx, dynClient)
		assert.False(t, result)
	})
}

// ---------- waitForNamespaceProviders ----------

func TestWaitForNamespaceProviders(t *testing.T) {
	ctx := context.Background()

	// Speed up retries so tests complete in milliseconds.
	origInit, origMax := retryInitialInterval, retryMaxInterval
	retryInitialInterval = 1 * time.Millisecond
	retryMaxInterval = 4 * time.Millisecond
	defer func() { retryInitialInterval, retryMaxInterval = origInit, origMax }()

	t.Run("returns immediately on success", func(t *testing.T) {
		cs := kubefake.NewClientset(
			makeNamespace("ns-t1", ""),
			makeNamespace("ns-vpc", "default"),
		)
		hasT1, hasVPC := waitForNamespaceProviders(ctx, cs)
		assert.True(t, hasT1)
		assert.True(t, hasVPC)
	})

	t.Run("retries on transient error and eventually succeeds", func(t *testing.T) {
		cs := kubefake.NewClientset(makeNamespace("ns-t1", ""))
		callCount := 0
		cs.PrependReactor("list", "namespaces", func(_ k8stesting.Action) (bool, runtime.Object, error) {
			callCount++
			if callCount < 3 {
				return true, nil, fmt.Errorf("list failed %d", callCount)
			}
			return false, nil, nil // fall through to real clientset
		})
		hasT1, hasVPC := waitForNamespaceProviders(ctx, cs)
		assert.True(t, hasT1)
		assert.False(t, hasVPC)
		assert.Equal(t, 3, callCount)
	})

	t.Run("returns false false on context cancellation", func(t *testing.T) {
		cancelCtx, cancel := context.WithCancel(context.Background())
		cs := kubefake.NewClientset()
		cs.PrependReactor("list", "namespaces", func(_ k8stesting.Action) (bool, runtime.Object, error) {
			cancel()
			return true, nil, fmt.Errorf("still failing")
		})
		hasT1, hasVPC := waitForNamespaceProviders(cancelCtx, cs)
		assert.False(t, hasT1)
		assert.False(t, hasVPC)
	})
}

// ---------- scanNamespaceProviders ----------

func TestScanNamespaceProviders(t *testing.T) {
	ctx := context.Background()

	t.Run("error listing namespaces returns error", func(t *testing.T) {
		cs := kubefake.NewClientset()
		cs.PrependReactor("list", "namespaces", func(_ k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, fmt.Errorf("apiserver unavailable")
		})
		hasT1, hasVPC, err := scanNamespaceProviders(ctx, cs)
		assert.False(t, hasT1)
		assert.False(t, hasVPC)
		assert.Error(t, err)
	})

	t.Run("empty namespace list returns false false no error", func(t *testing.T) {
		hasT1, hasVPC, err := scanNamespaceProviders(ctx, kubefake.NewClientset())
		assert.False(t, hasT1)
		assert.False(t, hasVPC)
		assert.NoError(t, err)
	})

	t.Run("T1-only namespace", func(t *testing.T) {
		cs := kubefake.NewClientset(makeNamespace("ns-t1", ""))
		hasT1, hasVPC, err := scanNamespaceProviders(ctx, cs)
		assert.True(t, hasT1)
		assert.False(t, hasVPC)
		assert.NoError(t, err)
	})

	t.Run("VPC-only namespace", func(t *testing.T) {
		cs := kubefake.NewClientset(makeNamespace("ns-vpc", "default"))
		hasT1, hasVPC, err := scanNamespaceProviders(ctx, cs)
		assert.False(t, hasT1)
		assert.True(t, hasVPC)
		assert.NoError(t, err)
	})

	t.Run("vsphere-style namespace without vpc annotation counts as T1", func(t *testing.T) {
		cs := kubefake.NewClientset(makeNamespace("ns-vsphere", ""))
		hasT1, hasVPC, err := scanNamespaceProviders(ctx, cs)
		assert.True(t, hasT1)
		assert.False(t, hasVPC)
		assert.NoError(t, err)
	})

	t.Run("mixed T1 and VPC namespaces", func(t *testing.T) {
		cs := kubefake.NewClientset(
			makeNamespace("ns-t1", ""),
			makeNamespace("ns-vpc", "default"),
		)
		hasT1, hasVPC, err := scanNamespaceProviders(ctx, cs)
		assert.True(t, hasT1)
		assert.True(t, hasVPC)
		assert.NoError(t, err)
	})

	t.Run("plain namespace without vpc annotation counts as T1", func(t *testing.T) {
		cs := kubefake.NewClientset(makeNamespace("ns-plain", ""))
		hasT1, hasVPC, err := scanNamespaceProviders(ctx, cs)
		assert.True(t, hasT1)
		assert.False(t, hasVPC)
		assert.NoError(t, err)
	})
}

// ---------- scanNamespaceProvidersWithClient ----------

func TestScanNamespaceProvidersWithClient(t *testing.T) {
	ctx := context.Background()
	cl := crfake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).WithObjects(
		makeNamespace("ns-vpc", "default"),
	).Build()
	hasT1, hasVPC, err := scanNamespaceProvidersWithClient(ctx, cl)
	assert.NoError(t, err)
	assert.False(t, hasT1)
	assert.True(t, hasVPC)
}

// ---------- InitMixedMode ----------

func TestInitMixedModeWithClients(t *testing.T) {
	ctx := context.Background()

	t.Run("capability not activated enableVPCNetwork=true uses legacy config", func(t *testing.T) {
		resetMixedModeState()
		initMixedModeWithClients(ctx, kubefake.NewClientset(), makeDynClientWith(false), true)
		assert.True(t, IsMixedModeStateInitialized())
		assert.False(t, IsPerNamespaceProvidersSupported())
		assert.True(t, HasVPCNamespaces())
		assert.False(t, HasT1Namespaces())
	})

	t.Run("capability not activated enableVPCNetwork=false uses legacy config", func(t *testing.T) {
		resetMixedModeState()
		initMixedModeWithClients(ctx, kubefake.NewClientset(), makeDynClientWith(false), false)
		assert.True(t, IsMixedModeStateInitialized())
		assert.False(t, IsPerNamespaceProvidersSupported())
		assert.False(t, HasVPCNamespaces())
		assert.True(t, HasT1Namespaces())
	})

	t.Run("per-namespace supported scans namespaces for mixed-mode", func(t *testing.T) {
		resetMixedModeState()
		cs := kubefake.NewClientset(
			makeNamespace("ns-t1", ""),
			makeNamespace("ns-vpc", "default"),
		)
		initMixedModeWithClients(ctx, cs, makeDynClientWith(true), false)
		assert.True(t, IsMixedModeStateInitialized())
		assert.True(t, IsPerNamespaceProvidersSupported())
		assert.True(t, HasT1Namespaces())
		assert.True(t, HasVPCNamespaces())
	})

	t.Run("per-namespace supported but no namespaces", func(t *testing.T) {
		resetMixedModeState()
		initMixedModeWithClients(ctx, kubefake.NewClientset(), makeDynClientWith(true), true)
		assert.True(t, IsMixedModeStateInitialized())
		assert.True(t, IsPerNamespaceProvidersSupported())
		assert.False(t, HasT1Namespaces())
		assert.False(t, HasVPCNamespaces())
	})
}

// ---------- RefreshMixedModeState ----------

func TestRefreshMixedModeState(t *testing.T) {
	ctx := context.Background()

	t.Run("returns false when perNamespaceProvidersSupported is nil", func(t *testing.T) {
		resetMixedModeState()
		storedClientset = kubefake.NewClientset()
		assert.False(t, RefreshMixedModeState(ctx))
	})

	t.Run("returns false when storedClientset is nil", func(t *testing.T) {
		resetMixedModeState()
		supported := true
		stateMu.Lock()
		perNamespaceProvidersSupported = &supported
		stateMu.Unlock()
		assert.False(t, RefreshMixedModeState(ctx))
	})

	t.Run("returns false when per-namespace providers not supported", func(t *testing.T) {
		resetMixedModeState()
		supported := false
		stateMu.Lock()
		perNamespaceProvidersSupported = &supported
		storedClientset = kubefake.NewClientset()
		stateMu.Unlock()
		assert.False(t, RefreshMixedModeState(ctx))
	})

	t.Run("returns false when state is unchanged", func(t *testing.T) {
		resetMixedModeState()
		supported := true
		stateMu.Lock()
		perNamespaceProvidersSupported = &supported
		hasT1Namespaces = true
		hasVPCNamespaces = false
		storedClientset = kubefake.NewClientset(makeNamespace("ns-t1", ""))
		stateMu.Unlock()
		assert.False(t, RefreshMixedModeState(ctx))
		assert.True(t, HasT1Namespaces())
		assert.False(t, HasVPCNamespaces())
	})

	t.Run("returns true when state changes", func(t *testing.T) {
		resetMixedModeState()
		supported := true
		stateMu.Lock()
		perNamespaceProvidersSupported = &supported
		hasT1Namespaces = true
		hasVPCNamespaces = false
		storedClientset = kubefake.NewClientset(makeNamespace("ns-vpc", "default"))
		stateMu.Unlock()
		assert.True(t, RefreshMixedModeState(ctx))
		assert.False(t, HasT1Namespaces())
		assert.True(t, HasVPCNamespaces())
	})

	t.Run("returns true when new namespace added and state grows", func(t *testing.T) {
		resetMixedModeState()
		supported := true
		stateMu.Lock()
		perNamespaceProvidersSupported = &supported
		storedClientset = kubefake.NewClientset(
			makeNamespace("ns-t1", ""),
			makeNamespace("ns-vpc", "default"),
		)
		stateMu.Unlock()
		assert.True(t, RefreshMixedModeState(ctx))
		assert.True(t, HasT1Namespaces())
		assert.True(t, HasVPCNamespaces())
	})

	t.Run("namespace list error preserves old state and returns false", func(t *testing.T) {
		resetMixedModeState()
		supported := true
		stateMu.Lock()
		perNamespaceProvidersSupported = &supported
		hasT1Namespaces = true
		hasVPCNamespaces = false
		cs := kubefake.NewClientset()
		cs.PrependReactor("list", "namespaces", func(_ k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, fmt.Errorf("list failed")
		})
		storedClientset = cs
		stateMu.Unlock()
		assert.False(t, RefreshMixedModeState(ctx))
		// State must be preserved despite the error.
		assert.True(t, HasT1Namespaces())
		assert.False(t, HasVPCNamespaces())
	})

	t.Run("uses cache-backed reader when set (clientset would miss the namespace)", func(t *testing.T) {
		resetMixedModeState()
		supported := true
		stateMu.Lock()
		perNamespaceProvidersSupported = &supported
		hasT1Namespaces = true
		hasVPCNamespaces = false
		// Empty clientset: only the cache reader sees the test namespace.
		storedClientset = kubefake.NewClientset()
		stateMu.Unlock()
		cl := crfake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).WithObjects(
			makeNamespace("ns-vpc", "default"),
		).Build()
		SetMixedModeNamespaceRefreshReader(cl)
		t.Cleanup(func() { SetMixedModeNamespaceRefreshReader(nil) })
		assert.True(t, RefreshMixedModeState(ctx))
		assert.False(t, HasT1Namespaces())
		assert.True(t, HasVPCNamespaces())
	})
}

// ---------- Getters and SetMixedModeStateForTest ----------

func TestGettersAndSetMixedModeStateForTest(t *testing.T) {
	t.Run("SetMixedModeStateForTest sets T1=true VPC=false", func(t *testing.T) {
		SetMixedModeStateForTest(true, false)
		assert.True(t, HasT1Namespaces())
		assert.False(t, HasVPCNamespaces())
		assert.True(t, IsMixedModeStateInitialized())
	})

	t.Run("SetMixedModeStateForTest sets T1=false VPC=true", func(t *testing.T) {
		SetMixedModeStateForTest(false, true)
		assert.False(t, HasT1Namespaces())
		assert.True(t, HasVPCNamespaces())
		assert.True(t, IsMixedModeStateInitialized())
	})

	t.Run("IsPerNamespaceProvidersSupported false when nil", func(t *testing.T) {
		resetMixedModeState()
		assert.False(t, IsPerNamespaceProvidersSupported())
	})

	t.Run("IsPerNamespaceProvidersSupported false when explicitly false", func(t *testing.T) {
		resetMixedModeState()
		supported := false
		stateMu.Lock()
		perNamespaceProvidersSupported = &supported
		stateMu.Unlock()
		assert.False(t, IsPerNamespaceProvidersSupported())
	})

	t.Run("IsPerNamespaceProvidersSupported true when set", func(t *testing.T) {
		resetMixedModeState()
		supported := true
		stateMu.Lock()
		perNamespaceProvidersSupported = &supported
		stateMu.Unlock()
		assert.True(t, IsPerNamespaceProvidersSupported())
	})

	t.Run("IsMixedModeStateInitialized false before init", func(t *testing.T) {
		resetMixedModeState()
		assert.False(t, IsMixedModeStateInitialized())
	})
}
