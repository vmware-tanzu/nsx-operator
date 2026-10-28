/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

func TestNamespaceCountsFromDesignExamples(t *testing.T) {
	for _, tc := range []struct {
		file   string
		counts map[string]int32
	}{
		{"design-namespaces-success", map[string]int32{PhaseCompleted: 3}},
		{"design-namespaces-failure", map[string]int32{PhaseCompleted: 2, PhaseFailed: 1}},
		{"design-namespaces-incompatible", map[string]int32{PhaseEvaluationPassed: 1, PhaseEvaluationFailed: 1}},
		{"design-namespaces-plan", map[string]int32{PhasePlanReady: 1}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			data, err := os.ReadFile("testdata/" + tc.file + ".yaml")
			require.NoError(t, err)
			var obj NamespaceNetworkTransition
			require.NoError(t, yaml.Unmarshal(data, &obj))
			before := obj.DeepCopy()
			require.Equal(t, tc.counts, obj.NamespacePhaseCounts())
			require.Equal(t, before, &obj, "deriving progress must not mutate the CR")
		})
	}
}

func TestNamespaceCountsUseBatchMembership(t *testing.T) {
	obj := &NamespaceNetworkTransition{Spec: NamespaceNetworkTransitionSpec{Namespaces: []string{"a", "b", "c", "d"}}}
	require.Equal(t, map[string]int32{PhasePending: 4}, obj.NamespacePhaseCounts())
	obj.Status = &NamespaceNetworkTransitionStatus{NamespaceStatuses: []NamespaceStatus{
		{Name: "a", Phase: PhaseFailed}, {Name: "b", Phase: PhaseRolledBack}, {Name: "c", Phase: "FuturePhase"},
		{Name: "outside", Phase: PhaseCompleted},
	}}
	require.Equal(t, map[string]int32{PhaseFailed: 1, PhaseRolledBack: 1, "FuturePhase": 1, PhasePending: 1}, obj.NamespacePhaseCounts())
	// A retry replaces the per-namespace outcome rather than accumulating counts.
	obj.Status.NamespaceStatuses[0].Phase = PhaseCompleted
	obj.Status.NamespaceStatuses[1].Phase = PhaseCommitted
	require.Equal(t, map[string]int32{PhaseCompleted: 1, PhaseCommitted: 1, "FuturePhase": 1, PhasePending: 1}, obj.NamespacePhaseCounts())
	// Defensive behavior for unvalidated client objects: no duplicate counting.
	obj.Spec.Namespaces = append(obj.Spec.Namespaces, "a")
	obj.Status.NamespaceStatuses = append(obj.Status.NamespaceStatuses, NamespaceStatus{Name: "a", Phase: PhaseRolledBack}, NamespaceStatus{Name: "d"})
	require.Equal(t, map[string]int32{PhaseRolledBack: 1, PhaseCommitted: 1, "FuturePhase": 1, PhasePending: 1}, obj.NamespacePhaseCounts())
	var missing *NamespaceNetworkTransition
	require.Empty(t, missing.NamespacePhaseCounts())
}
