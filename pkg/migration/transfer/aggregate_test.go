/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package transfer

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	vpcapi "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

func TestAggregateProtectedInventory(t *testing.T) {
	reservation := api.NamespacedName{Namespace: "staging", Name: "r"}
	port := api.NamespacedName{Namespace: "tenant", Name: "p"}
	source := api.NamespacedName{Namespace: "staging", Name: "vip"}
	target := api.NamespacedName{Namespace: "tenant", Name: "vip"}
	move := api.IPAddressAllocationMoveSpec{Source: source, Target: target}
	expected := []Operation{{Type: SubnetPortCutover, Source: reservation, Target: port, IPs: []string{"10.0.0.2"}}, {Type: IPAddressAllocationMove, Source: source, Target: target, IPs: []string{"10.0.0.3"}}}
	child := &api.NetworkResourceTransition{ObjectMeta: metav1.ObjectMeta{Name: "child", UID: "uid", Generation: 1}, Spec: api.NetworkResourceTransitionSpec{SubnetPortCutovers: []api.SubnetPortCutoverSpec{{SubnetIPReservation: reservation, SubnetPort: port}}, IPAddressAllocationMoves: []api.IPAddressAllocationMoveSpec{move}}, Status: api.NetworkResourceTransitionStatus{ObservedGeneration: 1, SubnetPortCutovers: []api.SubnetPortCutoverStatus{{SubnetIPReservation: reservation, SubnetPort: port, Phase: api.TransitionPhaseSucceeded, IPAddresses: []string{"10.0.0.2"}}}, IPAddressAllocationMoves: []api.IPAddressAllocationMoveStatus{{Source: source, Target: target, Phase: api.TransitionPhaseSucceeded}}}}
	observation := Observation{Child: child, VerifiedMoveIPs: map[api.IPAddressAllocationMoveSpec][]string{move: {"10.0.0.3"}}}
	require.True(t, Aggregate(true, expected, []Observation{observation}).Ready)
	require.False(t, Aggregate(false, expected, []Observation{observation}).Ready)
	require.False(t, Aggregate(true, expected, nil).Ready)
	noEvidence := observation
	noEvidence.VerifiedMoveIPs = nil
	require.Contains(t, Aggregate(true, expected, []Observation{noEvidence}).Pending[0], "verified addresses")
	wrong := observation
	wrong.VerifiedMoveIPs = map[api.IPAddressAllocationMoveSpec][]string{move: {"10.0.0.4"}}
	require.Contains(t, Aggregate(true, expected, []Observation{wrong}).Failures[0], "incorrect addresses")
	stale := child.DeepCopy()
	stale.Status.ObservedGeneration = 0
	require.NotEmpty(t, Aggregate(true, expected, []Observation{{Child: stale}}).Pending)
	failed := child.DeepCopy()
	failed.Status.SubnetPortCutovers[0].Phase = api.TransitionPhaseFailed
	failed.Status.SubnetPortCutovers[0].Message = "backend unavailable"
	require.Contains(t, Aggregate(true, expected, []Observation{{Child: failed}}).Failures[0], "backend unavailable")
	duplicate := child.DeepCopy()
	duplicate.Name = "second"
	duplicate.UID = "second-uid"
	require.NotEmpty(t, Aggregate(true, expected, []Observation{observation, {Child: duplicate}}).Failures)
	unexpected := child.DeepCopy()
	unexpected.Status.IPAddressAllocationMoves = append(unexpected.Status.IPAddressAllocationMoves, api.IPAddressAllocationMoveStatus{Source: source, Target: api.NamespacedName{Namespace: "tenant", Name: "other"}})
	require.Contains(t, Aggregate(true, expected, []Observation{{Child: unexpected}}).Failures[0], "unexpected allocation move status")
}

func TestAggregateChildFailureAndInvalidInventory(t *testing.T) {
	source := api.NamespacedName{Namespace: "staging", Name: "reservation"}
	target := api.NamespacedName{Namespace: "tenant", Name: "port"}
	expected := []Operation{{Type: SubnetPortCutover, Source: source, Target: target, IPs: []string{"10.0.0.2"}}}
	child := &api.NetworkResourceTransition{ObjectMeta: metav1.ObjectMeta{Name: "child", UID: "uid", Generation: 1}, Spec: api.NetworkResourceTransitionSpec{SubnetPortCutovers: []api.SubnetPortCutoverSpec{{SubnetIPReservation: source, SubnetPort: target}}}, Status: api.NetworkResourceTransitionStatus{ObservedGeneration: 1, Phase: api.TransitionPhaseFailed, Conditions: []vpcapi.Condition{{Type: vpcapi.Ready, Reason: "BackendError", Message: "backend unavailable"}}}}
	result := Aggregate(true, expected, []Observation{{Child: child}})
	require.False(t, result.Ready)
	require.Len(t, result.Failures, 1)
	require.Contains(t, result.Failures[0], "BackendError backend unavailable")
	child.Status.Conditions[0].Reason = ""
	result = Aggregate(true, expected, []Observation{{Child: child}})
	require.Equal(t, []string{"child child failed: backend unavailable"}, result.Failures)
	child.Status.ObservedGeneration = 0
	result = Aggregate(true, expected, []Observation{{Child: child}})
	require.Empty(t, result.Failures)
	require.NotEmpty(t, result.Pending)
	child.Status.ObservedGeneration = 1
	child.Status.SubnetPortCutovers = []api.SubnetPortCutoverStatus{{SubnetIPReservation: source, SubnetPort: target, Phase: api.TransitionPhaseFailed, Message: "operation failed"}}
	result = Aggregate(true, expected, []Observation{{Child: child}})
	require.Len(t, result.Failures, 1)
	require.Contains(t, result.Failures[0], "operation failed")
	child.Status.SubnetPortCutovers[0].Phase = api.TransitionPhaseSucceeded
	child.Status.SubnetPortCutovers[0].IPAddresses = []string{"10.0.0.2"}
	require.NotEmpty(t, Aggregate(true, expected, []Observation{{Child: child}}).Failures)
	expected[0].IPs = []string{"invalid"}
	result = Aggregate(true, expected, []Observation{{Child: child}})
	require.Len(t, result.Failures, 1)
	require.Contains(t, result.Failures[0], "invalid expected addresses")
}

func TestAggregateAcrossSplitChildren(t *testing.T) {
	var expected []Operation
	var observations []Observation
	for batch := 0; batch < 2; batch++ {
		child := &api.NetworkResourceTransition{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("batch-%d", batch), UID: types.UID(fmt.Sprintf("uid-%d", batch)), Generation: 1}, Status: api.NetworkResourceTransitionStatus{ObservedGeneration: 1}}
		count := 100
		if batch == 1 {
			count = 1
		}
		for i := 0; i < count; i++ {
			name := fmt.Sprintf("item-%d-%d", batch, i)
			source := api.NamespacedName{Namespace: "staging", Name: name}
			target := api.NamespacedName{Namespace: "tenant", Name: name}
			child.Spec.SubnetPortCutovers = append(child.Spec.SubnetPortCutovers, api.SubnetPortCutoverSpec{SubnetIPReservation: source, SubnetPort: target})
			child.Status.SubnetPortCutovers = append(child.Status.SubnetPortCutovers, api.SubnetPortCutoverStatus{SubnetIPReservation: source, SubnetPort: target, Phase: api.TransitionPhaseSucceeded, IPAddresses: []string{"10.0.0.2"}})
			expected = append(expected, Operation{Type: SubnetPortCutover, Source: source, Target: target, IPs: []string{"10.0.0.2"}})
		}
		observations = append(observations, Observation{Child: child})
	}
	require.True(t, Aggregate(true, expected, observations).Ready)
	require.False(t, Aggregate(true, expected, observations[:1]).Ready)
}

func TestAggregateReverseAndSplit(t *testing.T) {
	port := api.NamespacedName{Namespace: "tenant", Name: "port"}
	reservation := api.NamespacedName{Namespace: "staging", Name: "reservation"}
	op := api.SubnetPortRevertSpec{SubnetPort: port, SubnetIPReservation: reservation, ReservedIPs: []string{"10.0.0.2"}}
	child := &api.NetworkResourceTransition{ObjectMeta: metav1.ObjectMeta{Name: "reverse", UID: "uid", Generation: 2}, Spec: api.NetworkResourceTransitionSpec{SubnetPortReverts: []api.SubnetPortRevertSpec{op}}, Status: api.NetworkResourceTransitionStatus{ObservedGeneration: 2, SubnetPortReverts: []api.SubnetPortRevertStatus{{SubnetPort: port, SubnetIPReservation: &reservation, ReservedIPs: []string{"10.0.0.2"}, Phase: api.TransitionPhaseSucceeded}}}}
	expected := []Operation{{Type: SubnetPortRevert, Source: port, Target: reservation, IPs: []string{"10.0.0.2"}}}
	require.True(t, Aggregate(true, expected, []Observation{{Child: child}}).Ready)
	child.Spec.SubnetPortReverts[0].ReservedIPs = []string{"10.0.0.9"}
	require.NotEmpty(t, Aggregate(true, expected, []Observation{{Child: child}}).Failures)
	child.Spec.SubnetPortCutovers = []api.SubnetPortCutoverSpec{{}}
	require.NotEmpty(t, Aggregate(true, expected, []Observation{{Child: child}}).Failures)
	child.Spec.SubnetPortCutovers = nil
	child.Spec.SubnetPortReverts = make([]api.SubnetPortRevertSpec, 101)
	require.Contains(t, Aggregate(true, expected, []Observation{{Child: child}}).Failures[0], "splitting contract")
	child.Spec.SubnetPortReverts = []api.SubnetPortRevertSpec{op}
	result := Aggregate(true, nil, []Observation{{Child: child}})
	require.Len(t, result.Failures, 1)
	require.Contains(t, result.Failures[0], "is not in inventory")
}
