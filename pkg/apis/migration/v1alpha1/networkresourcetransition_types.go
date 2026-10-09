/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NamespacedObjectReference identifies a resource by its namespace and name.
type NamespacedObjectReference struct {
	// Namespace is the Kubernetes namespace containing the referenced resource.
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`

	// Name is the name of the referenced resource.
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// CreateSubnetPortSpec specifies the atomic cutover of one or more SubnetIPReservations into a single SubnetPort.
type CreateSubnetPortSpec struct {
	// SubnetIPReservations is the list of source SubnetIPReservation CRs (namespace and name)
	// whose IP allocations are to be bound into the target SubnetPort.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	SubnetIPReservations []NamespacedObjectReference `json:"subnetIPReservations"`

	// SubnetPort is the destination SubnetPort CR (namespace and name) to create.
	// +kubebuilder:validation:Required
	SubnetPort NamespacedObjectReference `json:"subnetPort"`
}

// MoveIPAddressAllocationSpec specifies the in-place adoption of an LB VIP across namespaces.
type MoveIPAddressAllocationSpec struct {
	// Source is the reference to the existing staging IPAddressAllocation CR.
	// +kubebuilder:validation:Required
	Source NamespacedObjectReference `json:"source"`

	// Target is the reference to the destination IPAddressAllocation CR in the user namespace.
	// +kubebuilder:validation:Required
	Target NamespacedObjectReference `json:"target"`
}

// NetworkResourceTransitionSpec defines the desired migration operations.
type NetworkResourceTransitionSpec struct {
	// CreateSubnetPort specifies workload cutover(s) from reservations to SubnetPort(s).
	// +optional
	CreateSubnetPort []CreateSubnetPortSpec `json:"createSubnetPort,omitempty"`

	// MoveIPAddressAllocation specifies Load Balancer VIP(s) to adopt into tenant namespaces.
	// +optional
	MoveIPAddressAllocation []MoveIPAddressAllocationSpec `json:"moveIPAddressAllocation,omitempty"`
}

type MigrationPhase string

const (
	MigrationPhasePending         MigrationPhase = "Pending"
	MigrationPhaseMigrating       MigrationPhase = "Migrating"
	MigrationPhaseSucceeded       MigrationPhase = "Succeeded"
	MigrationPhaseFailed          MigrationPhase = "Failed"
	MigrationPhasePartiallyFailed MigrationPhase = "PartiallyFailed"
)

type ItemMigrationPhase string

const (
	ItemPhasePending   ItemMigrationPhase = "Pending"
	ItemPhaseMigrating ItemMigrationPhase = "Migrating"
	ItemPhaseSucceeded ItemMigrationPhase = "Succeeded"
	ItemPhaseFailed    ItemMigrationPhase = "Failed"
	ItemPhaseSkipped   ItemMigrationPhase = "Skipped"
)

type CreateSubnetPortStatus struct {
	SubnetPort           NamespacedObjectReference   `json:"subnetPort"`
	SubnetIPReservations []NamespacedObjectReference `json:"subnetIPReservations"`
	Phase                ItemMigrationPhase          `json:"phase"`
	Message              string                      `json:"message,omitempty"`
	AttachmentID         string                      `json:"attachmentID,omitempty"`
	IPAddresses          []string                    `json:"ipAddresses,omitempty"`
	MACAddress           string                      `json:"macAddress,omitempty"`
}

type MoveIPAddressAllocationStatus struct {
	Source  NamespacedObjectReference `json:"source"`
	Target  NamespacedObjectReference `json:"target"`
	Phase   ItemMigrationPhase        `json:"phase"`
	Message string                    `json:"message,omitempty"`
}

// NetworkResourceTransitionStatus defines the observed state of NetworkResourceTransition.
type NetworkResourceTransitionStatus struct {
	Phase MigrationPhase `json:"phase,omitempty"`

	TotalResources     int `json:"totalResources,omitempty"`
	SucceededResources int `json:"succeededResources,omitempty"`
	FailedResources    int `json:"failedResources,omitempty"`

	// CreateSubnetPort mirrors spec field.
	// +optional
	CreateSubnetPort []CreateSubnetPortStatus `json:"createSubnetPort,omitempty"`

	// MoveIPAddressAllocation mirrors spec field.
	// +optional
	MoveIPAddressAllocation []MoveIPAddressAllocationStatus `json:"moveIPAddressAllocation,omitempty"`

	// Conditions track overall migration progress.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=nrt
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Total",type=integer,JSONPath=`.status.totalResources`
// +kubebuilder:printcolumn:name="Succeeded",type=integer,JSONPath=`.status.succeededResources`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=`.metadata.creationTimestamp`

// NetworkResourceTransition is the Schema for cluster-scoped migration resource transition operations.
type NetworkResourceTransition struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NetworkResourceTransitionSpec   `json:"spec,omitempty"`
	Status NetworkResourceTransitionStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// NetworkResourceTransitionList contains a list of NetworkResourceTransition.
type NetworkResourceTransitionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NetworkResourceTransition `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NetworkResourceTransition{}, &NetworkResourceTransitionList{})
}
