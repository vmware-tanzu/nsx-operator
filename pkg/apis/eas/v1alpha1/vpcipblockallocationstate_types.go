// Copyright (c) 2026 Broadcom. All Rights Reserved.
// Broadcom Confidential. The term "Broadcom" refers to Broadcom Inc.
// and/or its subsidiaries.

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:onlyVerbs=get
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:resource:scope=Namespaced

// VPCIPBlockAllocationState represents the allocation state of all CIDRs allocated from an IPBlock.
// VPCIPBlockAllocationState name is the same as the IPBlock name.
type VPCIPBlockAllocationState struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Visibility of IPBlock.
	// Must be External or Private.
	// +kubebuilder:validation:Enum=External;Private
	Visibility IPAddressVisibility `json:"visibility"`
	// State of each CIDR allocated from an IPBlock.
	AllocationStates []VPCIPAddressBlockAllocationStateForCIDR `json:"allocationStates"`
	// IP family of IP addresses: must be IPv4 or IPv6.
	// +kubebuilder:validation:Enum=IPv4;IPv6
	// +optional
	IPAddressType IPAddressType `json:"ipAddressType,omitempty"`
}

// VPCIPAddressBlockAllocationStateForCIDR represents the state for a CIDR allocated from an IPBlock.
// The CIDR can be a single IP which is represented as a /32. Or it can be a larger CIDR.
type VPCIPAddressBlockAllocationStateForCIDR struct {
	// Allocated CIDR from an IPBlock.
	CIDR string `json:"cidr"`
	// IPAddressAllocation CR in this Namespace that is associated with this allocated IP/CIDR.
	// +optional
	VPCIPAddressAllocationName string `json:"vpcIPAddressAllocationName,omitempty"`
}

func init() {
	SchemeBuilder.Register(&VPCIPBlockAllocationState{})
}
