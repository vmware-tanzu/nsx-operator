/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package transfer

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	vpc "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

func TestSubnetPortReadinessAndDetach(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant", UID: "namespace-uid"}}
	binding := IdentityBinding{Ref: api.NamespacedName{Namespace: "tenant", Name: "port"}, NamespaceUID: "namespace-uid", ObjectUID: "port-uid"}
	port := &vpc.SubnetPort{ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "port", UID: "port-uid"}, Status: vpc.SubnetPortStatus{Conditions: []vpc.Condition{{Type: vpc.Ready, Status: corev1.ConditionTrue}}, Attachment: vpc.PortAttachment{ID: "vif-id"}, NetworkInterfaceConfig: vpc.NetworkInterfaceConfig{SubnetID: "/subnet", IPAddresses: []vpc.NetworkInterfaceIPAddress{{IPAddress: "10.0.0.2/24"}}}}}
	ready, err := ObserveSubnetPortReady(binding, ns, port, "/subnet", []string{"10.0.0.2"})
	require.NoError(t, err)
	require.True(t, ready)
	port.Status.NetworkInterfaceConfig.IPAddresses = []vpc.NetworkInterfaceIPAddress{{IPAddress: "10.0.0.2"}, {IPAddress: "2001:db8::1"}}
	ready, err = ObserveSubnetPortReady(binding, ns, port, "/subnet", []string{"10.0.0.2", "2001:db8::1"})
	require.NoError(t, err)
	require.True(t, ready)
	port.Status.NetworkInterfaceConfig.IPAddresses = []vpc.NetworkInterfaceIPAddress{{IPAddress: "10.0.0.2/24"}}
	ready, err = ObserveSubnetPortReady(binding, ns, port, "/subnet", []string{"10.0.0.3"})
	require.ErrorContains(t, err, "address mismatch")
	require.False(t, ready)
	ready, err = ObserveSubnetPortReady(binding, ns, port, "/wrong", []string{"10.0.0.2"})
	require.ErrorContains(t, err, "subnet mismatch")
	require.False(t, ready)
	port.Status.Conditions[0].Status = corev1.ConditionFalse
	ready, err = ObserveSubnetPortReady(binding, ns, port, "/subnet", []string{"10.0.0.2"})
	require.NoError(t, err)
	require.False(t, ready)
	detached, err := ObserveSubnetPortDetached(binding, ns, port, DetachmentEvidence{})
	require.NoError(t, err)
	require.False(t, detached)
	detached, err = ObserveSubnetPortDetached(binding, ns, port, DetachmentEvidence{Confirmed: true, NamespaceUID: ns.UID, PortUID: port.UID})
	require.NoError(t, err)
	require.True(t, detached)
	_, err = ObserveSubnetPortDetached(binding, ns, port, DetachmentEvidence{Confirmed: true, NamespaceUID: ns.UID, PortUID: "replacement"})
	require.ErrorContains(t, err, "evidence")
	replacement := port.DeepCopy()
	replacement.UID = "new-port"
	_, err = ObserveSubnetPortDetached(binding, ns, replacement, DetachmentEvidence{})
	require.ErrorContains(t, err, "identity mismatch")
	replacementNS := ns.DeepCopy()
	replacementNS.UID = "new-namespace"
	_, err = ObserveSubnetPortReady(binding, replacementNS, port, "/subnet", []string{"10.0.0.2"})
	require.ErrorContains(t, err, "namespace identity mismatch")
}

func TestVerifiedMoveIPs(t *testing.T) {
	srcNS := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "staging", UID: "source-ns"}}
	dstNS := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant", UID: "target-ns"}}
	source := AllocationEvidence{Binding: IdentityBinding{Ref: api.NamespacedName{Namespace: "staging", Name: "vip"}, NamespaceUID: srcNS.UID, ObjectUID: "source"}, Namespace: srcNS, BackendID: "/allocation/id", Allocation: &vpc.IPAddressAllocation{ObjectMeta: metav1.ObjectMeta{Namespace: "staging", Name: "vip", UID: "source"}, Status: vpc.IPAddressAllocationStatus{AllocationIPs: "10.0.0.4", Conditions: []vpc.Condition{{Type: vpc.Ready, Status: corev1.ConditionTrue}}}}}
	target := AllocationEvidence{Binding: IdentityBinding{Ref: api.NamespacedName{Namespace: "tenant", Name: "vip"}, NamespaceUID: dstNS.UID, ObjectUID: "target"}, Namespace: dstNS, BackendID: "/allocation/id", Allocation: &vpc.IPAddressAllocation{ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "vip", UID: "target"}, Status: vpc.IPAddressAllocationStatus{AllocationIPs: "10.0.0.4", Conditions: []vpc.Condition{{Type: vpc.Ready, Status: corev1.ConditionTrue}}}}}
	ips, ready, err := VerifiedMoveIPs(source, nil, target)
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, []string{"10.0.0.4"}, ips)
	pendingTarget := target
	pendingTarget.Allocation = target.Allocation.DeepCopy()
	pendingTarget.BackendID = ""
	pendingTarget.Allocation.Status.Conditions[0].Status = corev1.ConditionFalse
	ips, ready, err = VerifiedMoveIPs(source, nil, pendingTarget)
	require.NoError(t, err)
	require.False(t, ready)
	require.Nil(t, ips)
	pendingTarget.Allocation.Status.Conditions[0].Status = corev1.ConditionTrue
	pendingTarget.BackendID = target.BackendID
	pendingTarget.Allocation.Status.AllocationIPs = ""
	ips, ready, err = VerifiedMoveIPs(source, nil, pendingTarget)
	require.NoError(t, err)
	require.False(t, ready)
	require.Nil(t, ips)
	ready, err = ObserveAllocationReady(source, "10.0.0.4")
	require.NoError(t, err)
	require.True(t, ready)
	pending := source
	pending.Allocation = source.Allocation.DeepCopy()
	pending.Allocation.Status.Conditions = nil
	ready, err = ObserveAllocationReady(pending, "10.0.0.4")
	require.NoError(t, err)
	require.False(t, ready)
	pending.Allocation.Status.Conditions = []vpc.Condition{{Type: vpc.Ready, Status: corev1.ConditionFalse}}
	ready, err = ObserveAllocationReady(pending, "10.0.0.4")
	require.NoError(t, err)
	require.False(t, ready)
	pending.BackendID = ""
	ready, err = ObserveAllocationReady(pending, "10.0.0.4")
	require.NoError(t, err)
	require.False(t, ready)
	pending.BackendID = source.BackendID
	pending.Allocation.Status.Conditions[0].Status = corev1.ConditionTrue
	pending.Allocation.Status.AllocationIPs = ""
	ready, err = ObserveAllocationReady(pending, "10.0.0.4")
	require.NoError(t, err)
	require.False(t, ready)
	pending.BackendID = ""
	_, err = ObserveAllocationReady(pending, "10.0.0.4")
	require.ErrorContains(t, err, "backend identity")
	pending.BackendID = source.BackendID
	pending.Allocation.Status.Conditions = append(pending.Allocation.Status.Conditions, pending.Allocation.Status.Conditions[0])
	_, err = ObserveAllocationReady(pending, "10.0.0.4")
	require.ErrorContains(t, err, "duplicate Ready")
	_, err = ObserveAllocationReady(source, "10.0.0.9")
	require.ErrorContains(t, err, "address mismatch")
	target.BackendID = "/other"
	_, _, err = VerifiedMoveIPs(source, nil, target)
	require.ErrorContains(t, err, "backend identity")
	target.BackendID = source.BackendID
	target.Allocation.Status.AllocationIPs = "10.0.0.5"
	_, _, err = VerifiedMoveIPs(source, nil, target)
	require.ErrorContains(t, err, "addresses differ")
	target.Allocation.Status.AllocationIPs = "10.0.0.4/24"
	_, _, err = VerifiedMoveIPs(source, nil, target)
	require.ErrorContains(t, err, "single exact IP")
	target.Allocation.Status.AllocationIPs = "10.0.0.4/32"
	ips, ready, err = VerifiedMoveIPs(source, nil, target)
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, []string{"10.0.0.4"}, ips)
	target.Allocation.Status.AllocationIPs = "10.0.0.4"
	target.Allocation.UID = "replacement"
	_, _, err = VerifiedMoveIPs(source, nil, target)
	require.ErrorContains(t, err, "identity mismatch")
	target.Allocation.UID = target.Binding.ObjectUID
	checkpoint := &ConsumedAllocation{Binding: source.Binding, BackendID: source.BackendID, IP: "10.0.0.4"}
	source.Allocation = nil
	ips, ready, err = VerifiedMoveIPs(source, checkpoint, target)
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, []string{"10.0.0.4"}, ips)
	_, _, err = VerifiedMoveIPs(source, nil, target)
	require.ErrorContains(t, err, "checkpoint")
	source.Namespace = srcNS.DeepCopy()
	source.Namespace.UID = "replacement"
	_, _, err = VerifiedMoveIPs(source, checkpoint, target)
	require.ErrorContains(t, err, "namespace identity")
}

func TestObserveReservationReady(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "staging", UID: "namespace"}}
	binding := IdentityBinding{Ref: api.NamespacedName{Namespace: "staging", Name: "reservation"}, NamespaceUID: ns.UID, ObjectUID: "reservation-uid"}
	reservation := &vpc.SubnetIPReservation{ObjectMeta: metav1.ObjectMeta{Namespace: "staging", Name: "reservation", UID: binding.ObjectUID}, Spec: vpc.SubnetIPReservationSpec{Subnet: "subnet"}, Status: vpc.SubnetIPReservationStatus{Conditions: []vpc.Condition{{Type: vpc.Ready, Status: corev1.ConditionTrue}}, IPs: []string{"10.0.0.2"}}}
	ready, err := ObserveReservationReady(binding, ns, reservation, "subnet", []string{"10.0.0.2"})
	require.NoError(t, err)
	require.True(t, ready)
	reservation.Status.IPs = []string{"10.0.0.3"}
	_, err = ObserveReservationReady(binding, ns, reservation, "subnet", []string{"10.0.0.2"})
	require.ErrorContains(t, err, "address mismatch")
	reservation.Status.IPs = nil
	ready, err = ObserveReservationReady(binding, ns, reservation, "subnet", []string{"10.0.0.2"})
	require.NoError(t, err)
	require.False(t, ready)
	reservation.UID = "replacement"
	_, err = ObserveReservationReady(binding, ns, reservation, "subnet", []string{"10.0.0.2"})
	require.ErrorContains(t, err, "identity mismatch")
}
