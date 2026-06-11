/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package config

import (
	"testing"

	"context"
	"time"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
)

// makeCapabilitiesObj builds an unstructured Capabilities object
// with the given supports_per_namespace_network_provider.activated value.
func makeCapabilitiesObj(activated bool) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "iaas.vmware.com/v1alpha1",
			"kind":       "Capabilities",
			"metadata": map[string]interface{}{
				"name": capabilitiesName,
			},
			"status": map[string]interface{}{
				"supervisor": map[string]interface{}{
					"supports_per_namespace_network_provider": map[string]interface{}{
						"activated": activated,
					},
				},
			},
		},
	}
}

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
			name: "status without supervisor",
			obj: &unstructured.Unstructured{Object: map[string]interface{}{
				"status": map[string]interface{}{},
			}},
			expect: false,
		},
		{
			name: "supervisor map is empty",
			obj: &unstructured.Unstructured{Object: map[string]interface{}{
				"status": map[string]interface{}{
					"supervisor": map[string]interface{}{},
				},
			}},
			expect: false,
		},
		{
			name: "supervisor has no matching capability key",
			obj: &unstructured.Unstructured{Object: map[string]interface{}{
				"status": map[string]interface{}{
					"supervisor": map[string]interface{}{
						"other_capability": map[string]interface{}{"activated": true},
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
					"supervisor": map[string]interface{}{
						"supports_per_namespace_network_provider": map[string]interface{}{
							"activated": "yes",
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
					"supervisor": map[string]interface{}{
						"supports_per_namespace_network_provider": "true",
					},
				},
			}},
			expect: false,
		},
		{
			name: "supervisor entry is not a map",
			obj: &unstructured.Unstructured{Object: map[string]interface{}{
				"status": map[string]interface{}{
					"supervisor": "not-a-map",
				},
			}},
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, extractCapability(tt.obj))
		})
	}
}

func makeNetworkSettings(provider string) unstructured.Unstructured {
	return unstructured.Unstructured{
		Object: map[string]interface{}{
			"provider": provider,
		},
	}
}

func TestParseProvidersFromList(t *testing.T) {
	tests := []struct {
		name      string
		items     []unstructured.Unstructured
		expectT1  bool
		expectVPC bool
		expectVDS bool
	}{
		{
			name:      "empty list",
			items:     []unstructured.Unstructured{},
			expectT1:  false,
			expectVPC: false,
			expectVDS: false,
		},
		{
			name: "only T1",
			items: []unstructured.Unstructured{
				makeNetworkSettings("nsx-tier1"),
			},
			expectT1:  true,
			expectVPC: false,
			expectVDS: false,
		},
		{
			name: "only VPC",
			items: []unstructured.Unstructured{
				makeNetworkSettings("vpc"),
			},
			expectT1:  false,
			expectVPC: true,
			expectVDS: false,
		},
		{
			name: "only VDS",
			items: []unstructured.Unstructured{
				makeNetworkSettings("vsphere-distributed"),
			},
			expectT1:  false,
			expectVPC: false,
			expectVDS: true,
		},
		{
			name: "mixed T1, VPC and VDS",
			items: []unstructured.Unstructured{
				makeNetworkSettings("nsx-tier1"),
				makeNetworkSettings("vpc"),
				makeNetworkSettings("vsphere-distributed"),
			},
			expectT1:  true,
			expectVPC: true,
			expectVDS: true,
		},
		{
			name: "unknown provider",
			items: []unstructured.Unstructured{
				makeNetworkSettings("unknown-provider"),
			},
			expectT1:  false,
			expectVPC: false,
			expectVDS: false,
		},
		{
			name: "multiple same providers",
			items: []unstructured.Unstructured{
				makeNetworkSettings("vpc"),
				makeNetworkSettings("vpc"),
			},
			expectT1:  false,
			expectVPC: true,
			expectVDS: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t1, vpc, vds := parseProvidersFromList(tt.items)
			assert.Equal(t, tt.expectT1, t1)
			assert.Equal(t, tt.expectVPC, vpc)
			assert.Equal(t, tt.expectVDS, vds)
		})
	}
}

func TestStateGetters(t *testing.T) {
	// reset state
	stateMu.Lock()
	hasT1Namespaces = false
	hasVPCNamespaces = false
	hasVDSNamespaces = false
	perNamespaceProvidersSupported = nil
	stateInitialized = false
	stateMu.Unlock()

	assert.False(t, HasT1Namespaces())
	assert.False(t, HasVPCNamespaces())
	assert.False(t, HasVDSNamespaces())
	assert.False(t, IsMixedModeStateInitialized())
	assert.False(t, IsPerNamespaceProvidersSupported())

	SetMixedModeStateForTest(true, true)
	assert.True(t, HasT1Namespaces())
	assert.True(t, HasVPCNamespaces())
	assert.False(t, HasVDSNamespaces())
	assert.True(t, IsMixedModeStateInitialized())

	stateMu.Lock()
	hasVDSNamespaces = true
	supported := true
	perNamespaceProvidersSupported = &supported
	stateMu.Unlock()

	assert.True(t, HasVDSNamespaces())
	assert.True(t, IsPerNamespaceProvidersSupported())
}

func makeNetworkSettingsCR(name, namespace, provider string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "netoperator.vmware.com/v1alpha1",
			"kind":       "NetworkSettings",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"provider": provider,
		},
	}
}

func setupFakeDynamicClient(t *testing.T, objects ...*unstructured.Unstructured) *fake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		capabilitiesGVR:    "CapabilitiesList",
		networkSettingsGVR: "NetworkSettingsList",
	}
	client := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds)

	ctx := context.TODO()
	for _, obj := range objects {
		gvr := capabilitiesGVR
		if obj.GetKind() == "NetworkSettings" {
			gvr = networkSettingsGVR
		}

		ns := obj.GetNamespace()
		var err error
		if ns != "" {
			_, err = client.Resource(gvr).Namespace(ns).Create(ctx, obj, metav1.CreateOptions{})
		} else {
			_, err = client.Resource(gvr).Create(ctx, obj, metav1.CreateOptions{})
		}
		assert.NoError(t, err)
	}
	return client
}

func TestInitMixedModeWithClients(t *testing.T) {
	origRetryInitial := retryInitialInterval
	retryInitialInterval = 1 * time.Millisecond
	defer func() { retryInitialInterval = origRetryInitial }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Scenario 1: Per-namespace supported = false, EnableVPCNetwork = false
	capObjFalse := makeCapabilitiesObj(false)
	dynClientLegacy := setupFakeDynamicClient(t, capObjFalse)

	initMixedModeWithClients(ctx, dynClientLegacy, false)
	assert.True(t, HasT1Namespaces())
	assert.False(t, HasVPCNamespaces())
	assert.False(t, HasVDSNamespaces())

	// Scenario 2: Per-namespace supported = false, EnableVPCNetwork = true
	initMixedModeWithClients(ctx, dynClientLegacy, true)
	assert.False(t, HasT1Namespaces())
	assert.True(t, HasVPCNamespaces())
	assert.False(t, HasVDSNamespaces())

	// Scenario 3: Per-namespace supported = true, EnableVPCNetwork = false, has VDS and T1
	capObjTrue := makeCapabilitiesObj(true)
	ns1 := makeNetworkSettingsCR("ns1", "ns1", "nsx-tier1")
	ns2 := makeNetworkSettingsCR("ns2", "ns2", "vsphere-distributed")
	dynClientMixed := setupFakeDynamicClient(t, capObjTrue, ns1, ns2)

	initMixedModeWithClients(ctx, dynClientMixed, false)
	assert.True(t, HasT1Namespaces())
	assert.False(t, HasVPCNamespaces())
	assert.True(t, HasVDSNamespaces())

	// Scenario 4: Per-namespace supported = true, EnableVPCNetwork = true, has T1
	dynClientVPC := setupFakeDynamicClient(t, capObjTrue, ns1)
	initMixedModeWithClients(ctx, dynClientVPC, true)
	assert.True(t, HasT1Namespaces())
	assert.True(t, HasVPCNamespaces())
	assert.False(t, HasVDSNamespaces())
}

func TestCheckPerNamespaceProvidersSupported(t *testing.T) {
	origRetryInitial := retryInitialInterval
	retryInitialInterval = 1 * time.Millisecond
	defer func() { retryInitialInterval = origRetryInitial }()

	capObj := makeCapabilitiesObj(true)
	dynClient := setupFakeDynamicClient(t, capObj)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	supported, err := checkPerNamespaceProvidersSupported(ctx, dynClient)

	assert.NoError(t, err)
	assert.True(t, supported)

	capObjFalse := makeCapabilitiesObj(false)
	dynClientFalse := setupFakeDynamicClient(t, capObjFalse)
	supportedFalse, errFalse := checkPerNamespaceProvidersSupported(ctx, dynClientFalse)
	assert.NoError(t, errFalse)
	assert.False(t, supportedFalse)

	ctxCancel, cancelCtx := context.WithCancel(context.Background())
	cancelCtx()

	dynClientEmpty := setupFakeDynamicClient(t)
	supportedCancel, errCancel := checkPerNamespaceProvidersSupported(ctxCancel, dynClientEmpty)
	assert.Error(t, errCancel)
	assert.False(t, supportedCancel)
}
