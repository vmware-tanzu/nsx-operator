/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TransitionPhase defines the phase of a network resource transition.
// +kubebuilder:validation:Enum=InProgress;Succeeded;Failed
type TransitionPhase string

const (
	TransitionPhaseInProgress TransitionPhase = "InProgress"
	TransitionPhaseSucceeded  TransitionPhase = "Succeeded"
	TransitionPhaseFailed     TransitionPhase = "Failed"
)

// NamespacedName identifies a Kubernetes resource by Namespace and name.
// This API type preserves lowercase JSON fields and admission validation.
type NamespacedName struct {
	// Namespace of the referent.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Namespace string `json:"namespace"`

	// Name of the referent.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	Name string `json:"name"`
}

// SubnetPortCutoverSpec transfers one source SubnetIPReservation to a target SubnetPort.
// The reservation holds the workload interface's IPv4 address and, when configured,
// its IPv6 address. The target claims these addresses without releasing them, and
// the source SubnetIPReservation is deleted after successful cutover.
type SubnetPortCutoverSpec struct {
	// SubnetIPReservation identifies the source reservation containing the interface addresses.
	// +kubebuilder:validation:Required
	SubnetIPReservation NamespacedName `json:"subnetIPReservation"`

	// SubnetPort identifies the target SubnetPort to create with the claimed addresses.
	// +kubebuilder:validation:Required
	SubnetPort NamespacedName `json:"subnetPort"`
}

// SubnetPortRevertSpec transfers a detached SubnetPort's addresses back to one reservation.
// The source SubnetPort must be detached from all network interfaces before transfer.
// The destination reservation must restore exactly the source port's current addresses
// on the same backing subnet. Transfer must be atomic and must not release addresses.
type SubnetPortRevertSpec struct {
	// SubnetPort identifies the detached source SubnetPort.
	// +kubebuilder:validation:Required
	SubnetPort NamespacedName `json:"subnetPort"`

	// SubnetIPReservation identifies the destination reservation by Namespace and name.
	// Derive the resolved Subnet CR name from the source SubnetPort and resolve the
	// same-named Subnet in this Namespace, requiring matching backing identity.
	// +kubebuilder:validation:Required
	SubnetIPReservation NamespacedName `json:"subnetIPReservation"`

	// ReservedIPs contains the exact addresses to restore, with at most one address
	// per family. Ranges, CIDRs, and requests for new addresses are not supported.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=2
	// +kubebuilder:validation:items:MaxLength=45
	// +kubebuilder:validation:XValidation:rule="self.all(address, isIP(address)) && (size(self) < 2 || ip(self[0]).family() != ip(self[1]).family())",message="reservedIPs must contain at most one individual IPv4 and one individual IPv6 address"
	// +listType=set
	ReservedIPs []string `json:"reservedIPs"`
}

// IPAddressAllocationMoveSpec specifies the source and target for transferring an IPAddressAllocation across Namespaces.
// Ownership of the allocated IP addresses is transferred without releasing the addresses.
// Rollback uses the same operation in a new transition, reversing source and target references.
type IPAddressAllocationMoveSpec struct {
	// Source specifies the existing IPAddressAllocation to transfer from.
	// +kubebuilder:validation:Required
	Source NamespacedName `json:"source"`

	// Target specifies the target IPAddressAllocation in the destination Namespace.
	// +kubebuilder:validation:Required
	Target NamespacedName `json:"target"`
}

// +kubebuilder:validation:XValidation:rule="(has(self.subnetPortCutovers) && size(self.subnetPortCutovers) > 0) || (has(self.ipAddressAllocationMoves) && size(self.ipAddressAllocationMoves) > 0) || (has(self.subnetPortReverts) && size(self.subnetPortReverts) > 0)",message="at least one transition operation list must be nonempty"
// +kubebuilder:validation:XValidation:rule="!has(self.subnetPortCutovers) || self.subnetPortCutovers.all(x, self.subnetPortCutovers.exists_one(y, y.subnetPort == x.subnetPort))",message="target subnetPort must be unique across all subnetPortCutovers items"
// +kubebuilder:validation:XValidation:rule="!has(self.ipAddressAllocationMoves) || self.ipAddressAllocationMoves.all(x, self.ipAddressAllocationMoves.exists_one(y, y.target == x.target))",message="target IPAddressAllocation must be unique across all ipAddressAllocationMoves items"
// +kubebuilder:validation:XValidation:rule="!has(self.ipAddressAllocationMoves) || self.ipAddressAllocationMoves.all(x, self.ipAddressAllocationMoves.exists_one(y, y.source == x.source))",message="source IPAddressAllocation must be unique across all ipAddressAllocationMoves items"
// +kubebuilder:validation:XValidation:rule="!has(self.subnetPortReverts) || self.subnetPortReverts.all(x, self.subnetPortReverts.exists_one(y, y.subnetPort == x.subnetPort))",message="source subnetPorts must be unique across reverts"
// +kubebuilder:validation:XValidation:rule="!has(self.subnetPortReverts) || size(self.subnetPortReverts) == 0 || !has(self.subnetPortCutovers) || size(self.subnetPortCutovers) == 0",message="forward cutovers and reverse cutovers require separate transitions"
// +kubebuilder:validation:XValidation:rule="!has(self.subnetPortCutovers) || self.subnetPortCutovers.all(x, self.subnetPortCutovers.exists_one(y, y.subnetIPReservation == x.subnetIPReservation))",message="source reservations must be unique across cutovers"
// +kubebuilder:validation:XValidation:rule="!has(self.subnetPortReverts) || self.subnetPortReverts.all(x, self.subnetPortReverts.exists_one(y, y.subnetIPReservation == x.subnetIPReservation))",message="destination reservations must be unique across reverts"
// NetworkResourceTransitionSpec defines the desired transition of network resources across Namespaces.
type NetworkResourceTransitionSpec struct {
	// SubnetPortCutovers specifies workload cutovers from one reservation to one SubnetPort each.
	// +kubebuilder:validation:MaxItems=100
	// +listType=atomic
	// +optional
	SubnetPortCutovers []SubnetPortCutoverSpec `json:"subnetPortCutovers,omitempty"`

	// SubnetPortReverts specifies rollback from each detached SubnetPort to one exact-IP reservation.
	// Use a new transition request; the original forward transition remains immutable.
	// +kubebuilder:validation:MaxItems=100
	// +listType=atomic
	// +optional
	SubnetPortReverts []SubnetPortRevertSpec `json:"subnetPortReverts,omitempty"`

	// IPAddressAllocationMoves specifies IPAddressAllocation resources to transfer across Namespaces.
	// +kubebuilder:validation:MaxItems=100
	// +listType=atomic
	// +optional
	IPAddressAllocationMoves []IPAddressAllocationMoveSpec `json:"ipAddressAllocationMoves,omitempty"`
}

// SubnetPortCutoverStatus records the observed transition status for a SubnetPort cutover.
type SubnetPortCutoverStatus struct {
	// SubnetPort mirrors the target SubnetPort reference.
	SubnetPort NamespacedName `json:"subnetPort"`

	// SubnetIPReservation mirrors the source reservation's Namespace and name.
	SubnetIPReservation NamespacedName `json:"subnetIPReservation"`

	// Phase of this specific cutover operation.
	Phase TransitionPhase `json:"phase"`

	// Message describes details or error information.
	// +optional
	Message string `json:"message,omitempty"`

	// AttachmentID of the realized SubnetPort.
	// +optional
	AttachmentID string `json:"attachmentID,omitempty"`

	// IPAddresses realized on the target SubnetPort.
	// +listType=set
	// +optional
	IPAddresses []string `json:"ipAddresses,omitempty"`

	// MACAddress preserved and programmed on the target SubnetPort.
	// +optional
	MACAddress string `json:"macAddress,omitempty"`
}

// SubnetPortRevertStatus reports allocation-preserving restoration of one source port.
type SubnetPortRevertStatus struct {
	// SubnetPort records the source port, retained after verified consumption.
	SubnetPort NamespacedName `json:"subnetPort"`

	// SubnetIPReservation mirrors the requested destination reservation.
	// Success means it is realized and port cleanup cannot release its addresses.
	// +optional
	SubnetIPReservation *NamespacedName `json:"subnetIPReservation,omitempty"`

	// ReservedIPs records the exact addresses restored to the destination reservation.
	// +kubebuilder:validation:MaxItems=2
	// +kubebuilder:validation:items:MaxLength=45
	// +kubebuilder:validation:XValidation:rule="self.all(address, isIP(address)) && (size(self) < 2 || ip(self[0]).family() != ip(self[1]).family())",message="reservedIPs must contain at most one individual IPv4 and one individual IPv6 address"
	// +listType=set
	// +optional
	ReservedIPs []string `json:"reservedIPs,omitempty"`

	// Phase and Message report this operation's outcome, not whole-Namespace rollback.
	Phase TransitionPhase `json:"phase"`
	// +optional
	Message string `json:"message,omitempty"`
}

// IPAddressAllocationMoveStatus records the observed transition status for an IPAddressAllocation move.
type IPAddressAllocationMoveStatus struct {
	// Source mirrors the source IPAddressAllocation reference.
	Source NamespacedName `json:"source"`

	// Target mirrors the target IPAddressAllocation reference.
	Target NamespacedName `json:"target"`

	// Phase of this specific move operation.
	Phase TransitionPhase `json:"phase"`

	// Message describes details or error information.
	// +optional
	Message string `json:"message,omitempty"`
}

// NetworkResourceTransitionStatus defines the observed state of NetworkResourceTransition.
type NetworkResourceTransitionStatus struct {
	// ObservedGeneration represents the .metadata.generation that the condition/status was set based upon.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Phase defines the overall transition phase.
	// +optional
	Phase TransitionPhase `json:"phase,omitempty"`

	// TotalResources counts operations across cutovers, reverts, and allocation moves.
	// Each cutover or revert transfers one reservation for one SubnetPort.
	// +optional
	TotalResources int `json:"totalResources,omitempty"`

	// SucceededResources is the count of successfully transitioned resources.
	// +optional
	SucceededResources int `json:"succeededResources,omitempty"`

	// FailedResources is the count of failed resources.
	// +optional
	FailedResources int `json:"failedResources,omitempty"`

	// SubnetPortCutovers mirrors the status for each SubnetPort cutover.
	// +listType=atomic
	// +optional
	SubnetPortCutovers []SubnetPortCutoverStatus `json:"subnetPortCutovers,omitempty"`

	// SubnetPortReverts mirrors the rollback status for each source SubnetPort.
	// +kubebuilder:validation:MaxItems=100
	// +listType=atomic
	// +optional
	SubnetPortReverts []SubnetPortRevertStatus `json:"subnetPortReverts,omitempty"`

	// IPAddressAllocationMoves mirrors the status for each IPAddressAllocation move.
	// +listType=atomic
	// +optional
	IPAddressAllocationMoves []IPAddressAllocationMoveStatus `json:"ipAddressAllocationMoves,omitempty"`

	// Conditions track overall transition conditions.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []vpcv1alpha1.Condition `json:"conditions,omitempty"`
}

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion

// NetworkResourceTransition is the Schema for the networkresourcetransitions API.
// +kubebuilder:resource:scope="Cluster",shortName=nrt;nrts
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`,description="Transition phase of NetworkResourceTransition"
// +kubebuilder:printcolumn:name="Total",type=integer,JSONPath=`.status.totalResources`,description="Total resources to transition"
// +kubebuilder:printcolumn:name="Succeeded",type=integer,JSONPath=`.status.succeededResources`,description="Number of successfully transitioned resources"
// +kubebuilder:printcolumn:name="Failed",type=integer,JSONPath=`.status.failedResources`,description="Number of failed resources"
// +kubebuilder:validation:XValidation:rule="self.spec == oldSelf.spec",message="spec is immutable"
type NetworkResourceTransition struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   NetworkResourceTransitionSpec   `json:"spec"`
	Status NetworkResourceTransitionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NetworkResourceTransitionList contains a list of NetworkResourceTransition.
type NetworkResourceTransitionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NetworkResourceTransition `json:"items"`
}
