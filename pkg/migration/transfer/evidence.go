/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package transfer

import (
	"fmt"
	"net/netip"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	vpc "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

// IdentityBinding is the previously persisted namespace and resource identity.
// Binding and retention are the caller's responsibility; names do not bind UIDs.
type IdentityBinding struct {
	Ref                     api.NamespacedName
	NamespaceUID, ObjectUID types.UID
}

// CheckIdentity requires a live namespace and object matching a persisted binding.
// A missing source after consumption must instead be verified through the owning
// component's retained lifecycle evidence; it is not a matching live object.
func CheckIdentity(binding IdentityBinding, namespace *corev1.Namespace, object metav1.Object) error {
	if !validRef(binding.Ref) || binding.NamespaceUID == "" || binding.ObjectUID == "" {
		return fmt.Errorf("incomplete identity binding for %s/%s", binding.Ref.Namespace, binding.Ref.Name)
	}
	if namespace == nil || object == nil {
		return fmt.Errorf("missing live namespace or object for %s/%s", binding.Ref.Namespace, binding.Ref.Name)
	}
	if namespace.Name != binding.Ref.Namespace || namespace.UID != binding.NamespaceUID || !namespace.DeletionTimestamp.IsZero() {
		return fmt.Errorf("namespace identity mismatch or deletion for %s", binding.Ref.Namespace)
	}
	if object.GetNamespace() != binding.Ref.Namespace || object.GetName() != binding.Ref.Name || object.GetUID() != binding.ObjectUID || !object.GetDeletionTimestamp().IsZero() {
		return fmt.Errorf("object identity mismatch or deletion for %s/%s", binding.Ref.Namespace, binding.Ref.Name)
	}
	return nil
}

// ObserveSubnetPortReady checks the realized CR and exact addresses. It is not
// proof of workload attachment, live NSX ownership, or fresh observed generation:
// the current SubnetPort status has no observedGeneration field.
func ObserveSubnetPortReady(binding IdentityBinding, namespace *corev1.Namespace, port *vpc.SubnetPort, subnetPath string, expectedIPs []string) (bool, error) {
	if port == nil {
		return false, nil
	}
	if err := CheckIdentity(binding, namespace, port); err != nil {
		return false, err
	}
	if subnetPath == "" {
		return false, fmt.Errorf("missing expected subnet path")
	}
	expected, err := normalizeIPs(expectedIPs)
	if err != nil {
		return false, fmt.Errorf("invalid expected IPs: %w", err)
	}
	ready, err := readyCondition(port.Status.Conditions)
	if err != nil || !ready {
		return false, err
	}
	if port.Status.NetworkInterfaceConfig.SubnetID == "" || len(port.Status.NetworkInterfaceConfig.IPAddresses) == 0 {
		return false, nil
	}
	if port.Status.NetworkInterfaceConfig.SubnetID != subnetPath {
		return false, fmt.Errorf("SubnetPort %s/%s subnet mismatch", port.Namespace, port.Name)
	}
	var addresses []string
	for _, entry := range port.Status.NetworkInterfaceConfig.IPAddresses {
		if entry.IPAddress == "" {
			return false, nil
		}
		if prefix, err := netip.ParsePrefix(entry.IPAddress); err == nil {
			addresses = append(addresses, prefix.Addr().String())
		} else if address, err := netip.ParseAddr(entry.IPAddress); err == nil && address.Zone() == "" {
			addresses = append(addresses, address.String())
		} else {
			return false, fmt.Errorf("invalid SubnetPort address %q", entry.IPAddress)
		}
	}
	actual, err := normalizeIPs(addresses)
	if err != nil || !equal(actual, expected) {
		return false, fmt.Errorf("SubnetPort %s/%s address mismatch", port.Namespace, port.Name)
	}
	return true, nil
}

// DetachmentEvidence comes from the workload owner after checking the actual
// backing and all vNIC consumers. A VIF attachment ID is not a detach signal.
type DetachmentEvidence struct {
	PortUID      types.UID
	NamespaceUID types.UID
	Confirmed    bool
}

func ObserveSubnetPortDetached(binding IdentityBinding, namespace *corev1.Namespace, port *vpc.SubnetPort, evidence DetachmentEvidence) (bool, error) {
	if port == nil {
		return false, nil
	}
	if err := CheckIdentity(binding, namespace, port); err != nil {
		return false, err
	}
	if !evidence.Confirmed {
		return false, nil
	}
	if evidence.PortUID != binding.ObjectUID || evidence.NamespaceUID != binding.NamespaceUID {
		return false, fmt.Errorf("detachment evidence does not match SubnetPort identity")
	}
	return true, nil
}

// ObserveReservationReady checks one reservation's realized exact IPs and
// Subnet CR name. Range/CIDR status entries cannot prove an exact inventory.
func ObserveReservationReady(binding IdentityBinding, namespace *corev1.Namespace, reservation *vpc.SubnetIPReservation, subnetName string, expectedIPs []string) (bool, error) {
	if reservation == nil {
		return false, nil
	}
	if err := CheckIdentity(binding, namespace, reservation); err != nil {
		return false, err
	}
	if subnetName == "" {
		return false, fmt.Errorf("missing expected Subnet name")
	}
	expected, err := normalizeIPs(expectedIPs)
	if err != nil {
		return false, fmt.Errorf("invalid expected IPs: %w", err)
	}
	if reservation.Spec.Subnet != subnetName {
		return false, fmt.Errorf("reservation %s/%s subnet mismatch", reservation.Namespace, reservation.Name)
	}
	ready, err := readyCondition(reservation.Status.Conditions)
	if err != nil || !ready {
		return false, err
	}
	if len(reservation.Status.IPs) == 0 {
		return false, nil
	}
	actual, err := normalizeIPs(reservation.Status.IPs)
	if err != nil || !equal(actual, expected) {
		return false, fmt.Errorf("reservation %s/%s address mismatch or non-exact status", reservation.Namespace, reservation.Name)
	}
	return true, nil
}

// AllocationEvidence must be derived by the caller from a verified live NSX
// allocation identity and the corresponding current CR. Equal IPs alone do not
// prove the two CRs refer to the same allocation.
type AllocationEvidence struct {
	Binding    IdentityBinding
	Namespace  *corev1.Namespace
	Allocation *vpc.IPAddressAllocation
	BackendID  string
}

// ConsumedAllocation is a durable, owner-verified checkpoint retained before
// source cleanup. A missing source without this record cannot be adopted.
type ConsumedAllocation struct {
	Binding   IdentityBinding
	BackendID string
	IP        string
}

// ObserveAllocationReady checks a ready allocation's current exact IP and a
// caller-verified backend identity against the protected inventory.
func ObserveAllocationReady(e AllocationEvidence, expectedIP string) (bool, error) {
	if e.Allocation == nil {
		return false, nil
	}
	expected, err := normalizeIPs([]string{expectedIP})
	if err != nil {
		return false, fmt.Errorf("invalid expected IP: %w", err)
	}
	if err := CheckIdentity(e.Binding, e.Namespace, e.Allocation); err != nil {
		return false, err
	}
	ready, err := readyCondition(e.Allocation.Status.Conditions)
	if err != nil || !ready {
		return false, err
	}
	if e.BackendID == "" {
		return false, fmt.Errorf("backend identity is missing")
	}
	if e.Allocation.Status.AllocationIPs == "" {
		return false, nil
	}
	ip, ready, err := allocationIP(e)
	if err != nil || !ready {
		return false, err
	}
	if ip != expected[0] {
		return false, fmt.Errorf("allocation %s/%s address mismatch", e.Binding.Ref.Namespace, e.Binding.Ref.Name)
	}
	return true, nil
}

// VerifiedMoveIPs checks a live target and either a live source or its retained
// checkpoint against independently verified backend identity. A false ready
// result with no error means the caller should leave this move's verified IPs
// absent so Aggregate reports it as pending. Errors indicate conflicting or
// insufficient identity/address evidence. This function never queries NSX or
// creates/persists the checkpoint itself.
func VerifiedMoveIPs(source AllocationEvidence, consumed *ConsumedAllocation, target AllocationEvidence) ([]string, bool, error) {
	if source.Binding.Ref == target.Binding.Ref {
		return nil, false, fmt.Errorf("source and target allocation references are identical")
	}
	targetIP, ready, err := allocationIP(target)
	if err != nil {
		return nil, false, fmt.Errorf("target: %w", err)
	}
	if !ready {
		return nil, false, nil
	}
	var sourceIP, backendID string
	if source.Allocation != nil {
		if consumed != nil {
			return nil, false, fmt.Errorf("both live source and consumed checkpoint provided")
		}
		sourceIP, ready, err = allocationIP(source)
		if err != nil {
			return nil, false, fmt.Errorf("source: %w", err)
		}
		if !ready {
			return nil, false, nil
		}
		backendID = source.BackendID
	} else {
		if consumed == nil || consumed.Binding != source.Binding || !validRef(consumed.Binding.Ref) || consumed.Binding.NamespaceUID == "" || consumed.Binding.ObjectUID == "" || consumed.BackendID == "" {
			return nil, false, fmt.Errorf("missing or invalid consumed source identity checkpoint")
		}
		if source.Namespace == nil || source.Namespace.Name != source.Binding.Ref.Namespace || source.Namespace.UID != source.Binding.NamespaceUID || !source.Namespace.DeletionTimestamp.IsZero() {
			return nil, false, fmt.Errorf("consumed source namespace identity mismatch or deletion")
		}
		ips, err := normalizeIPs([]string{consumed.IP})
		if err != nil {
			return nil, false, fmt.Errorf("invalid consumed source address: %w", err)
		}
		sourceIP = ips[0]
		backendID = consumed.BackendID
	}
	if backendID == "" || target.BackendID == "" || backendID != target.BackendID {
		return nil, false, fmt.Errorf("allocation backend identity missing or mismatched")
	}
	if sourceIP != targetIP {
		return nil, false, fmt.Errorf("source and target allocation addresses differ")
	}
	return []string{targetIP}, true, nil
}
func allocationIP(e AllocationEvidence) (string, bool, error) {
	if e.Allocation == nil {
		return "", false, nil
	}
	if err := CheckIdentity(e.Binding, e.Namespace, e.Allocation); err != nil {
		return "", false, err
	}
	ready, err := readyCondition(e.Allocation.Status.Conditions)
	if err != nil {
		return "", false, err
	}
	if !ready {
		return "", false, nil
	}
	if e.BackendID == "" {
		return "", false, fmt.Errorf("backend identity is missing")
	}
	if e.Allocation.Status.AllocationIPs == "" {
		return "", false, nil
	}
	value := e.Allocation.Status.AllocationIPs
	if prefix, err := netip.ParsePrefix(value); err == nil && prefix.Bits() == prefix.Addr().BitLen() {
		value = prefix.Addr().String()
	}
	ips, err := normalizeIPs([]string{value})
	if err != nil {
		return "", false, fmt.Errorf("allocation status does not contain a single exact IP: %w", err)
	}
	return ips[0], true, nil
}
func readyCondition(conditions []vpc.Condition) (bool, error) {
	found, ready := false, false
	for _, c := range conditions {
		if c.Type != vpc.Ready {
			continue
		}
		if found {
			return false, fmt.Errorf("duplicate Ready conditions")
		}
		found = true
		ready = c.Status == corev1.ConditionTrue
	}
	return found && ready, nil
}
