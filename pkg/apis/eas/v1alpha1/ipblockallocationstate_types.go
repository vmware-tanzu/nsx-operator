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

// IPBlockAllocationState represents the allocation state of all CIDRs allocated from an IPBlock.
type IPBlockAllocationState struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Name of the IPBlock.
	IPBlockName string `json:"ipBlockName,omitempty"`

	// Visibility of IPBlock.
	// Must be External or Private.
	// +kubebuilder:validation:Enum=External;Private
	Visibility IPAddressVisibility `json:"visibility" protobuf:"bytes,3,opt,name=visibility"`
	// State of each CIDR allocated from an IPBlock.
	AllocationStates []IPAddressBlockAllocationStateForCIDR `json:"allocationStates" protobuf:"bytes,4,rep,name=allocationStates"`
	// IP family of IP addresses: must be IPv4 or IPv6.
	// +kubebuilder:validation:Enum=IPv4;IPv6
	// +optional
	IPAddressType IPAddressType `json:"ipAddressType,omitempty" protobuf:"bytes,5,opt,name=ipAddressType"`
}

// IPAddressBlockAllocationStateForCIDR represents the state for a CIDR allocated from an IPBlock.
// The CIDR can be a single IP which is represented as a /32. Or it can be a larger CIDR.
type IPAddressBlockAllocationStateForCIDR struct {
	// Allocated CIDR from an IPBlock.
	CIDR string `json:"cidr" protobuf:"bytes,1,opt,name=cidr"`
	// IPAddressAllocation CR in this Namespace that is associated with this allocated IP/CIDR.
	// +optional
	VPCIPAddressAllocationName string `json:"vpcIPAddressAllocationName,omitempty" protobuf:"bytes,3,opt,name=vpcIPAddressAllocationName"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type IPBlockAllocationStateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	// The maximum number of items returned in one page is 10.
	Items []IPBlockAllocationState `json:"items" protobuf:"bytes,2,rep,name=items"`
}

func init() {
	SchemeBuilder.Register(&IPBlockAllocationState{}, &IPBlockAllocationStateList{})
}
