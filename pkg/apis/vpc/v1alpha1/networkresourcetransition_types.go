/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MigrationPhase defines the overall or per-resource phase of network resource transition.
// +kubebuilder:validation:Enum=Pending;Migrating;Succeeded;Failed;PartiallyFailed
type MigrationPhase string

const (
	MigrationPhasePending         MigrationPhase = "Pending"
	MigrationPhaseMigrating       MigrationPhase = "Migrating"
	MigrationPhaseSucceeded       MigrationPhase = "Succeeded"
	MigrationPhaseFailed          MigrationPhase = "Failed"
	MigrationPhasePartiallyFailed MigrationPhase = "PartiallyFailed"
)

// NamespacedObjectReference contains namespace and name to uniquely identify a namespaced Kubernetes resource.
type NamespacedObjectReference struct {
	// Namespace of the referent.
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`

	// Name of the referent.
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// CreateSubnetPortSpec specifies the source SubnetIPReservation(s) and target SubnetPort to realize.
type CreateSubnetPortSpec struct {
	// SubnetIPReservations specifies the list of source SubnetIPReservations in the staging namespace to cut over.
	// Supports dual-stack (IPv4 and IPv6) reservations mapped to a single SubnetPort.
	// +kubebuilder:validation:MinItems=1
	SubnetIPReservations []NamespacedObjectReference `json:"subnetIPReservations"`

	// SubnetPort specifies the target SubnetPort to create in the destination workload namespace.
	// +kubebuilder:validation:Required
	SubnetPort NamespacedObjectReference `json:"subnetPort"`
}

// MoveIPAddressAllocationSpec specifies the source and target for transferring an IPAddressAllocation (LB VIP).
type MoveIPAddressAllocationSpec struct {
	// Source specifies the existing IPAddressAllocation in the staging namespace.
	// +kubebuilder:validation:Required
	Source NamespacedObjectReference `json:"source"`

	// Target specifies the target IPAddressAllocation in the destination namespace.
	// +kubebuilder:validation:Required
	Target NamespacedObjectReference `json:"target"`
}

// NetworkResourceTransitionSpec defines the desired transition of network resources from staging to target namespaces.
type NetworkResourceTransitionSpec struct {
	// CreateSubnetPort specifies workload cutover(s) from SubnetIPReservation(s) to SubnetPort(s).
	// +optional
	CreateSubnetPort []CreateSubnetPortSpec `json:"createSubnetPort,omitempty"`

	// MoveIPAddressAllocation specifies Load Balancer VIP(s) to adopt into tenant namespaces.
	// +optional
	MoveIPAddressAllocation []MoveIPAddressAllocationSpec `json:"moveIPAddressAllocation,omitempty"`
}

// CreateSubnetPortStatus records the observed transition status for a SubnetPort cutover.
type CreateSubnetPortStatus struct {
	// SubnetPort mirrors the target SubnetPort reference.
	SubnetPort NamespacedObjectReference `json:"subnetPort"`

	// SubnetIPReservations mirrors the source SubnetIPReservation references.
	// +optional
	SubnetIPReservations []NamespacedObjectReference `json:"subnetIPReservations,omitempty"`

	// Phase of this specific cutover operation.
	Phase MigrationPhase `json:"phase"`

	// Message describes details or error information.
	// +optional
	Message string `json:"message,omitempty"`

	// AttachmentID of the realized SubnetPort on NSX.
	// +optional
	AttachmentID string `json:"attachmentID,omitempty"`

	// IPAddresses realized on the target SubnetPort.
	// +optional
	IPAddresses []string `json:"ipAddresses,omitempty"`

	// MACAddress preserved and programmed on the target SubnetPort.
	// +optional
	MACAddress string `json:"macAddress,omitempty"`
}

// MoveIPAddressAllocationStatus records the observed transition status for an IPAddressAllocation move.
type MoveIPAddressAllocationStatus struct {
	// Source mirrors the source IPAddressAllocation reference.
	Source NamespacedObjectReference `json:"source"`

	// Target mirrors the target IPAddressAllocation reference.
	Target NamespacedObjectReference `json:"target"`

	// Phase of this specific move operation.
	Phase MigrationPhase `json:"phase"`

	// Message describes details or error information.
	// +optional
	Message string `json:"message,omitempty"`
}

// NetworkResourceTransitionStatus defines the observed state of NetworkResourceTransition.
type NetworkResourceTransitionStatus struct {
	// Phase defines the overall migration phase.
	// +optional
	Phase MigrationPhase `json:"phase,omitempty"`

	// TotalResources is the total number of target resources to migrate.
	// +optional
	TotalResources int `json:"totalResources,omitempty"`

	// SucceededResources is the count of successfully transitioned resources.
	// +optional
	SucceededResources int `json:"succeededResources,omitempty"`

	// FailedResources is the count of failed resources.
	// +optional
	FailedResources int `json:"failedResources,omitempty"`

	// CreateSubnetPort mirrors the status for each SubnetPort cutover.
	// +optional
	CreateSubnetPort []CreateSubnetPortStatus `json:"createSubnetPort,omitempty"`

	// MoveIPAddressAllocation mirrors the status for each IPAddressAllocation move.
	// +optional
	MoveIPAddressAllocation []MoveIPAddressAllocationStatus `json:"moveIPAddressAllocation,omitempty"`

	// Conditions track overall transition conditions.
	// +optional
	Conditions []Condition `json:"conditions,omitempty"`
}

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion

// NetworkResourceTransition is the Schema for the networkresourcetransitions API.
// +kubebuilder:resource:scope="Cluster",shortName=nrt;nrts
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`,description="Migration phase of NetworkResourceTransition"
// +kubebuilder:printcolumn:name="Total",type=integer,JSONPath=`.status.totalResources`,description="Total resources to migrate"
// +kubebuilder:printcolumn:name="Succeeded",type=integer,JSONPath=`.status.succeededResources`,description="Number of successfully migrated resources"
// +kubebuilder:printcolumn:name="Failed",type=integer,JSONPath=`.status.failedResources`,description="Number of failed resources"
type NetworkResourceTransition struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NetworkResourceTransitionSpec   `json:"spec,omitempty"`
	Status NetworkResourceTransitionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NetworkResourceTransitionList contains a list of NetworkResourceTransition.
type NetworkResourceTransitionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NetworkResourceTransition `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NetworkResourceTransition{}, &NetworkResourceTransitionList{})
}
