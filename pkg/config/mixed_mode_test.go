/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
	hasVDSNamespaces = false
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
