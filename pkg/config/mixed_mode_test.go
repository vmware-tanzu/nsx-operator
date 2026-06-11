/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// makeCapabilitiesObj builds an unstructured SupervisorCapabilities object
// with the given supports_per_namespace_network_provider.activated value.
func makeCapabilitiesObj(activated bool) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "iaas.vmware.com/v1alpha1",
			"kind":       "SupervisorCapabilities",
			"metadata": map[string]interface{}{
				"name": supervisorCapabilitiesName,
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
