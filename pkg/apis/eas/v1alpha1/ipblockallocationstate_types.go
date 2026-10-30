// Copyright (c) 2026 Broadcom. All Rights Reserved.
// Broadcom Confidential. The term "Broadcom" refers to Broadcom Inc.
// and/or its subsidiaries.

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:resource:scope=Namespaced

// IPBlockAllocationState represents the allocation state of IP Blocks for a VPC.
type IPBlockAllocationState struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Name of the IPBlock.
	IPBlockName string `json:"ipBlockName,omitempty"`

	// Visibility of the IPBlock. Must be External, Private, or PrivateTGW.
	// +kubebuilder:validation:Enum=External;Private;PrivateTGW
	Visibility IPAddressVisibility `json:"visibility,omitempty"`

	// IP address type of the IPBlock. Must be IPv4 or IPv6.
	// +kubebuilder:validation:Enum=IPv4;IPv6
	IPAddressType IPAddressType `json:"addressType,omitempty"`

	// Allocations is the list of allocated IPs/CIDRs from this IP block and their associated CR names.
	// +listType=atomic
	Allocations []AllocatedCIDR `json:"allocations,omitempty"`
}

// AllocatedCIDR represents an allocated IP or CIDR and its corresponding IPAddressAllocation CR name.
type AllocatedCIDR struct {
	// CIDR represents the allocated CIDR or IP address (e.g., "10.0.0.0/24" or "192.168.1.10/32").
	CIDR string `json:"cidr,omitempty"`

	// IPAddressAllocationName is the name of the Kubernetes IPAddressAllocation CR
	// associated with this allocated IP/CIDR.
	IPAddressAllocationName string `json:"ipAddressAllocationName,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// IPBlockAllocationStateList contains a list of IPBlockAllocationState.
type IPBlockAllocationStateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IPBlockAllocationState `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IPBlockAllocationState{}, &IPBlockAllocationStateList{})
}
