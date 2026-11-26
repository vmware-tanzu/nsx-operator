/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package transfer

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestObserveVDNSubnetLinkExample(t *testing.T) {
	mapping := LinkMapping{VDN: "primary", Subnet: "/orgs/default/projects/project-quality/vpcs/vpc1/subnets/subnet1", PortConfig: "/orgs/default/projects/project-quality/vpcs/vpc1/subnets/subnet1/portconfigs/portconfig1"}
	link := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "netoperator.vmware.com/v1alpha1", "kind": "VDNSubnetLink", "metadata": map[string]interface{}{"name": "primary", "generation": int64(2)},
		"spec":   map[string]interface{}{"vdn_network": mapping.VDN, "subnet": mapping.Subnet, "portConfig": mapping.PortConfig},
		"status": map[string]interface{}{"conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": true}, map[string]interface{}{"type": "IPReserved", "status": true}}},
	}}
	got, err := ObserveVDNSubnetLink(link, mapping)
	require.NoError(t, err)
	require.True(t, got.Ready)
	require.True(t, got.IPReserved)
	missing, err := ObserveVDNSubnetLink(nil, mapping)
	require.NoError(t, err)
	require.False(t, missing.Ready)
	wrong := mapping
	wrong.Subnet = "different"
	_, err = ObserveVDNSubnetLink(link, wrong)
	require.ErrorContains(t, err, "mismatch")
	stale := link.DeepCopy()
	require.NoError(t, unstructured.SetNestedSlice(stale.Object, []interface{}{map[string]interface{}{"type": "Ready", "status": "True", "observedGeneration": int64(1)}}, "status", "conditions"))
	got, err = ObserveVDNSubnetLink(stale, mapping)
	require.NoError(t, err)
	require.False(t, got.Ready)
	current := stale.DeepCopy()
	require.NoError(t, unstructured.SetNestedSlice(current.Object, []interface{}{map[string]interface{}{"type": "Ready", "status": "True", "observedGeneration": int64(2)}}, "status", "conditions"))
	got, err = ObserveVDNSubnetLink(current, mapping)
	require.NoError(t, err)
	require.True(t, got.Ready)
	require.False(t, got.IPReserved)
	independentConditions := link.DeepCopy()
	require.NoError(t, unstructured.SetNestedSlice(independentConditions.Object, []interface{}{map[string]interface{}{"type": "Ready", "status": false}, map[string]interface{}{"type": "IPReserved", "status": true}}, "status", "conditions"))
	got, err = ObserveVDNSubnetLink(independentConditions, mapping)
	require.NoError(t, err)
	require.False(t, got.Ready)
	require.True(t, got.IPReserved)
	require.Contains(t, got.Pending, "VDNSubnetLink Ready condition is not true")
	duplicate := link.DeepCopy()
	require.NoError(t, unstructured.SetNestedSlice(duplicate.Object, []interface{}{map[string]interface{}{"type": "Ready", "status": true}, map[string]interface{}{"type": "Ready", "status": true}}, "status", "conditions"))
	_, err = ObserveVDNSubnetLink(duplicate, mapping)
	require.ErrorContains(t, err, "duplicate")
	incomplete := link.DeepCopy()
	unstructured.RemoveNestedField(incomplete.Object, "spec", "subnet")
	got, err = ObserveVDNSubnetLink(incomplete, mapping)
	require.NoError(t, err)
	require.False(t, got.Ready)
	require.False(t, got.IPReserved)
	missingFields := link.DeepCopy()
	unstructured.RemoveNestedField(missingFields.Object, "spec", "vdn_network")
	unstructured.RemoveNestedField(missingFields.Object, "spec", "subnet")
	got, err = ObserveVDNSubnetLink(missingFields, mapping)
	require.NoError(t, err)
	require.Equal(t, []string{"VDNSubnetLink primary missing spec.vdn_network", "VDNSubnetLink primary missing spec.subnet"}, got.Pending)
	require.False(t, got.IPReserved)
}
