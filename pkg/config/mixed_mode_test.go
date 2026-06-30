/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package config

import (
	"testing"

	"context"
	"time"

	"errors"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
			name: "mixed VPC and VDS",
			items: []unstructured.Unstructured{
				makeNetworkSettings("vpc"),
				makeNetworkSettings("vsphere-distributed"),
			},
			expectT1:  false,
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

	// Scenario 3: Per-namespace supported = true, EnableVPCNetwork = false, has VDS and VPC
	capObjTrue := makeCapabilitiesObj(true)
	ns1 := makeNetworkSettingsCR("ns1", "ns1", "vpc")
	ns2 := makeNetworkSettingsCR("ns2", "ns2", "vsphere-distributed")
	dynClientMixed := setupFakeDynamicClient(t, capObjTrue, ns1, ns2)

	initMixedModeWithClients(ctx, dynClientMixed, false)
	assert.False(t, HasT1Namespaces())
	assert.True(t, HasVPCNamespaces())
	assert.True(t, HasVDSNamespaces())

	// Scenario 4: Per-namespace supported = true, EnableVPCNetwork = true, has T1
	nsT1 := makeNetworkSettingsCR("ns3", "ns3", "nsx-tier1")
	dynClientVPC := setupFakeDynamicClient(t, capObjTrue, nsT1)
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

// stubReader is a minimal client.Reader for testing cache-backed scans.
type stubReader struct {
	items []unstructured.Unstructured
	err   error
}

func (s *stubReader) Get(_ context.Context, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
	return nil
}

func (s *stubReader) List(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
	if s.err != nil {
		return s.err
	}
	ul, ok := list.(*unstructured.UnstructuredList)
	if ok {
		ul.Items = s.items
	}
	return nil
}

func TestScanNamespaceProvidersFromAPI(t *testing.T) {
	ctx := context.TODO()

	// nil client returns empty state, no error
	t1, vpc, vds, err := scanNamespaceProvidersFromAPI(ctx, nil)
	assert.NoError(t, err)
	assert.False(t, t1)
	assert.False(t, vpc)
	assert.False(t, vds)

	// empty list returns empty state, no error
	dynClientEmpty := setupFakeDynamicClient(t)
	t1, vpc, vds, err = scanNamespaceProvidersFromAPI(ctx, dynClientEmpty)
	assert.NoError(t, err)
	assert.False(t, t1)
	assert.False(t, vpc)
	assert.False(t, vds)

	// list with T1 and VPC providers
	ns1 := makeNetworkSettingsCR("ns1", "ns1", "nsx-tier1")
	ns2 := makeNetworkSettingsCR("ns2", "ns2", "vpc")
	dynClient := setupFakeDynamicClient(t, ns1, ns2)
	t1, vpc, vds, err = scanNamespaceProvidersFromAPI(ctx, dynClient)
	assert.NoError(t, err)
	assert.True(t, t1)
	assert.True(t, vpc)
	assert.False(t, vds)
}

func TestScanNamespaceProvidersFromCache(t *testing.T) {
	ctx := context.TODO()

	// empty cache returns empty state, no error
	t1, vpc, vds, err := scanNamespaceProvidersFromCache(ctx, &stubReader{})
	assert.NoError(t, err)
	assert.False(t, t1)
	assert.False(t, vpc)
	assert.False(t, vds)

	// cache with VPC and T1 providers
	reader := &stubReader{items: []unstructured.Unstructured{
		makeNetworkSettings("vpc"),
		makeNetworkSettings("nsx-tier1"),
	}}
	t1, vpc, vds, err = scanNamespaceProvidersFromCache(ctx, reader)
	assert.NoError(t, err)
	assert.True(t, t1)
	assert.True(t, vpc)
	assert.False(t, vds)

	// list error is propagated
	errReader := &stubReader{err: errors.New("list failed")}
	t1, vpc, vds, err = scanNamespaceProvidersFromCache(ctx, errReader)
	assert.Error(t, err)
	assert.False(t, t1)
	assert.False(t, vpc)
	assert.False(t, vds)
}

func TestRefreshMixedModeState(t *testing.T) {
	resetMixedModeGlobals := func() {
		stateMu.Lock()
		hasT1Namespaces = false
		hasVPCNamespaces = false
		hasVDSNamespaces = false
		perNamespaceProvidersSupported = nil
		stateInitialized = false
		storedDynClient = nil
		storedEnableVPCNetwork = false
		stateMu.Unlock()
		refreshReaderMu.Lock()
		namespaceRefreshReader = nil
		refreshReaderMu.Unlock()
	}

	// storedDynClient is nil -> skip
	resetMixedModeGlobals()
	assert.False(t, RefreshMixedModeState(context.TODO()))

	// per-namespace providers not supported -> skip
	resetMixedModeGlobals()
	stateMu.Lock()
	storedDynClient = setupFakeDynamicClient(t)
	supportedFalse := false
	perNamespaceProvidersSupported = &supportedFalse
	stateMu.Unlock()
	assert.False(t, RefreshMixedModeState(context.TODO()))

	// no change via API path -> false
	resetMixedModeGlobals()
	stateMu.Lock()
	storedDynClient = setupFakeDynamicClient(t)
	supportedTrue := true
	perNamespaceProvidersSupported = &supportedTrue
	hasT1Namespaces = false
	stateMu.Unlock()
	assert.False(t, RefreshMixedModeState(context.TODO()))

	// state change via API path -> true
	resetMixedModeGlobals()
	ns1 := makeNetworkSettingsCR("ns1", "ns1", "nsx-tier1")
	stateMu.Lock()
	storedDynClient = setupFakeDynamicClient(t, ns1)
	perNamespaceProvidersSupported = &supportedTrue
	stateMu.Unlock()
	assert.True(t, RefreshMixedModeState(context.TODO()))
	assert.True(t, HasT1Namespaces())
	assert.False(t, HasVPCNamespaces())

	// cache reader takes precedence over API; enableVPCNetwork forces VPC=true
	resetMixedModeGlobals()
	stateMu.Lock()
	storedDynClient = setupFakeDynamicClient(t)
	perNamespaceProvidersSupported = &supportedTrue
	storedEnableVPCNetwork = true
	stateMu.Unlock()
	refreshReaderMu.Lock()
	namespaceRefreshReader = &stubReader{items: []unstructured.Unstructured{makeNetworkSettings("nsx-tier1")}}
	refreshReaderMu.Unlock()
	assert.True(t, RefreshMixedModeState(context.TODO()))
	assert.True(t, HasT1Namespaces())
	assert.True(t, HasVPCNamespaces())

	// scan error keeps current state, returns false
	resetMixedModeGlobals()
	stateMu.Lock()
	storedDynClient = setupFakeDynamicClient(t)
	perNamespaceProvidersSupported = &supportedTrue
	hasT1Namespaces = true
	stateMu.Unlock()
	refreshReaderMu.Lock()
	namespaceRefreshReader = &stubReader{err: errors.New("list failed")}
	refreshReaderMu.Unlock()
	assert.False(t, RefreshMixedModeState(context.TODO()))
	assert.True(t, HasT1Namespaces())

	resetMixedModeGlobals()
}
