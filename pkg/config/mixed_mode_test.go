/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package config

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
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

func TestStateGetters(t *testing.T) {
	// reset state
	stateMu.Lock()
	hasT1Namespaces = false
	hasVPCNamespaces = false
	perNamespaceProvidersSupported = nil
	stateInitialized = false
	stateMu.Unlock()

	assert.False(t, HasT1Namespaces())
	assert.False(t, HasVPCNamespaces())
	assert.False(t, IsMixedModeStateInitialized())
	assert.False(t, IsPerNamespaceProvidersSupported())

	SetMixedModeStateForTest(true, true)
	assert.True(t, HasT1Namespaces())
	assert.True(t, HasVPCNamespaces())
	assert.True(t, IsMixedModeStateInitialized())

	stateMu.Lock()
	supported := true
	perNamespaceProvidersSupported = &supported
	stateMu.Unlock()

	assert.True(t, IsPerNamespaceProvidersSupported())
}

func setupFakeDynamicClient(t *testing.T, objects ...*unstructured.Unstructured) *fake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		capabilitiesGVR: "CapabilitiesList",
	}
	client := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds)

	ctx := context.TODO()
	for _, obj := range objects {
		gvr := capabilitiesGVR

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
	clientset := kubernetesfake.NewClientset()

	initMixedModeWithClients(ctx, clientset, dynClientLegacy, false)
	assert.True(t, HasT1Namespaces())
	assert.False(t, HasVPCNamespaces())

	// Scenario 2: Per-namespace supported = false, EnableVPCNetwork = true
	initMixedModeWithClients(ctx, clientset, dynClientLegacy, true)
	assert.False(t, HasT1Namespaces())
	assert.True(t, HasVPCNamespaces())

	// Scenario 3: Per-namespace supported = true, EnableVPCNetwork = false, has T1 and VPC namespaces
	capObjTrue := makeCapabilitiesObj(true)
	dynClientMixed := setupFakeDynamicClient(t, capObjTrue)
	ns1 := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ns1",
			Annotations: map[string]string{
				"nsx.vmware.com/vpc_network_config": "{}",
			},
		},
	}
	ns2 := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ns2",
			Annotations: map[string]string{
				"nsx.vmware.com/t1_default_config": "true",
			},
		},
	}
	clientsetMixed := kubernetesfake.NewClientset(ns1, ns2)

	initMixedModeWithClients(ctx, clientsetMixed, dynClientMixed, false)
	assert.True(t, HasT1Namespaces())
	assert.True(t, HasVPCNamespaces())

	// Scenario 4: Per-namespace supported = true, EnableVPCNetwork = true, has T1
	dynClientVPC := setupFakeDynamicClient(t, capObjTrue)
	ns3 := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ns3",
			Annotations: map[string]string{
				"vmware-system-shared-t1": "true",
			},
		},
	}
	clientsetVPC := kubernetesfake.NewClientset(ns3)
	initMixedModeWithClients(ctx, clientsetVPC, dynClientVPC, true)
	assert.True(t, HasT1Namespaces())
	assert.True(t, HasVPCNamespaces())
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
	items []v1.Namespace
	err   error
}

func (s *stubReader) Get(_ context.Context, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
	return nil
}

func (s *stubReader) List(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
	if s.err != nil {
		return s.err
	}
	nl, ok := list.(*v1.NamespaceList)
	if ok {
		nl.Items = s.items
	}
	return nil
}

func TestScanNamespaceProvidersFromAPI(t *testing.T) {
	ctx := context.TODO()
	stateMu.Lock()
	storedEnableVPCNetwork = false
	stateMu.Unlock()

	// nil client returns fallback default state
	t1, vpc, err := scanNamespaceProvidersFromAPI(ctx, nil)
	assert.NoError(t, err)
	assert.True(t, t1)
	assert.False(t, vpc)

	// empty list returns fallback T1 state, no error
	clientsetEmpty := kubernetesfake.NewClientset()
	t1, vpc, err = scanNamespaceProvidersFromAPI(ctx, clientsetEmpty)
	assert.NoError(t, err)
	assert.True(t, t1)
	assert.False(t, vpc)

	// list with T1 and VPC providers via annotations
	ns1 := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ns1",
			Annotations: map[string]string{
				"nsx.vmware.com/vpc_network_config": "{}",
			},
		},
	}
	ns2 := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ns2",
			Annotations: map[string]string{
				"ncp/nsx_network_config_crd": "nnc-override",
			},
		},
	}
	clientset := kubernetesfake.NewClientset(ns1, ns2)
	t1, vpc, err = scanNamespaceProvidersFromAPI(ctx, clientset)
	assert.NoError(t, err)
	assert.True(t, t1)
	assert.True(t, vpc)
}

func TestScanNamespaceProvidersFromCache(t *testing.T) {
	ctx := context.TODO()
	stateMu.Lock()
	storedEnableVPCNetwork = false
	stateMu.Unlock()

	// empty cache returns default fallback state, no error
	t1, vpc, err := scanNamespaceProvidersFromCache(ctx, &stubReader{})
	assert.NoError(t, err)
	assert.True(t, t1)
	assert.False(t, vpc)

	// cache with VPC and T1 providers
	reader := &stubReader{items: []v1.Namespace{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "ns1",
				Annotations: map[string]string{
					"nsx.vmware.com/vpc_network_config": "{}",
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "ns2",
				Annotations: map[string]string{
					"nsx.vmware.com/t1_default_config": "true",
				},
			},
		},
	}}
	t1, vpc, err = scanNamespaceProvidersFromCache(ctx, reader)
	assert.NoError(t, err)
	assert.True(t, t1)
	assert.True(t, vpc)

	// list error is propagated
	errReader := &stubReader{err: errors.New("list failed")}
	t1, vpc, err = scanNamespaceProvidersFromCache(ctx, errReader)
	assert.Error(t, err)
	assert.False(t, t1)
	assert.False(t, vpc)
}

func TestRefreshMixedModeState(t *testing.T) {
	resetMixedModeGlobals := func() {
		stateMu.Lock()
		hasT1Namespaces = false
		hasVPCNamespaces = false
		perNamespaceProvidersSupported = nil
		stateInitialized = false
		storedClientset = nil
		storedEnableVPCNetwork = false
		stateMu.Unlock()
		refreshReaderMu.Lock()
		namespaceRefreshReader = nil
		refreshReaderMu.Unlock()
	}

	// storedClientset is nil -> skip
	resetMixedModeGlobals()
	assert.False(t, RefreshMixedModeState(context.TODO()))

	// per-namespace providers not supported -> skip
	resetMixedModeGlobals()
	stateMu.Lock()
	storedClientset = kubernetesfake.NewClientset()
	supportedFalse := false
	perNamespaceProvidersSupported = &supportedFalse
	stateMu.Unlock()
	assert.False(t, RefreshMixedModeState(context.TODO()))

	// no change via API path -> false
	resetMixedModeGlobals()
	stateMu.Lock()
	storedClientset = kubernetesfake.NewClientset()
	supportedTrue := true
	perNamespaceProvidersSupported = &supportedTrue
	hasT1Namespaces = true // default fallback
	stateMu.Unlock()
	assert.False(t, RefreshMixedModeState(context.TODO()))

	// state change via API path -> true
	resetMixedModeGlobals()
	ns1 := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ns1",
			Annotations: map[string]string{
				"nsx.vmware.com/vpc_network_config": "{}",
			},
		},
	}
	stateMu.Lock()
	storedClientset = kubernetesfake.NewClientset(ns1)
	perNamespaceProvidersSupported = &supportedTrue
	hasT1Namespaces = true
	stateMu.Unlock()
	assert.True(t, RefreshMixedModeState(context.TODO()))
	assert.False(t, HasT1Namespaces())
	assert.True(t, HasVPCNamespaces())

	// cache reader takes precedence over API; enableVPCNetwork forces VPC=true
	resetMixedModeGlobals()
	stateMu.Lock()
	storedClientset = kubernetesfake.NewClientset()
	perNamespaceProvidersSupported = &supportedTrue
	storedEnableVPCNetwork = true
	stateMu.Unlock()
	refreshReaderMu.Lock()
	namespaceRefreshReader = &stubReader{items: []v1.Namespace{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "ns1",
				Annotations: map[string]string{
					"nsx.vmware.com/t1_default_config": "true",
				},
			},
		},
	}}
	refreshReaderMu.Unlock()
	assert.True(t, RefreshMixedModeState(context.TODO()))
	assert.True(t, HasT1Namespaces())
	assert.True(t, HasVPCNamespaces())

	// scan error keeps current state, returns false
	resetMixedModeGlobals()
	stateMu.Lock()
	storedClientset = kubernetesfake.NewClientset()
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

func TestIsVPCNamespace(t *testing.T) {
	t.Run("nil namespace", func(t *testing.T) {
		resetMixedModeState()
		assert.False(t, IsVPCNamespace(nil))
	})

	t.Run("per-namespace on vpc annotation", func(t *testing.T) {
		resetMixedModeState()
		supported := true
		stateMu.Lock()
		perNamespaceProvidersSupported = &supported
		stateMu.Unlock()
		ns := makeNamespace("x", "default")
		assert.True(t, IsVPCNamespace(ns))
	})

	t.Run("per-namespace on no annotation counts as T1", func(t *testing.T) {
		resetMixedModeState()
		supported := true
		stateMu.Lock()
		perNamespaceProvidersSupported = &supported
		stateMu.Unlock()
		ns := makeNamespace("y", "")
		assert.False(t, IsVPCNamespace(ns))
	})

	t.Run("per-namespace off IsVPCNamespace uses cluster flags", func(t *testing.T) {
		resetMixedModeState()
		supported := false
		stateMu.Lock()
		perNamespaceProvidersSupported = &supported
		hasVPCNamespaces = true
		hasT1Namespaces = false
		stateMu.Unlock()
		ns := makeNamespace("any", "")
		assert.True(t, IsVPCNamespace(ns))
		stateMu.Lock()
		hasVPCNamespaces = false
		hasT1Namespaces = true
		stateMu.Unlock()
		assert.False(t, IsVPCNamespace(ns))
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

func resetMixedModeState() {
	stateMu.Lock()
	defer stateMu.Unlock()
	hasT1Namespaces = false
	hasVPCNamespaces = false
	stateInitialized = false
	perNamespaceProvidersSupported = nil
}

func makeNamespace(name, vpcAnnotation string) *v1.Namespace {
	ns := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: map[string]string{},
		},
	}
	if vpcAnnotation != "" {
		ns.Annotations[VPCNetworkConfigAnnotation] = vpcAnnotation
	}
	return ns
}
