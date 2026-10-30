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

// SubnetStatus exposes the realized status of a Subnet.
// It includes DHCP reserved IP ranges, unmanaged IP ranges, dynamic DHCP ranges,
// static IP pool ranges, gateway addresses, network addresses, and VLAN extension status.
// The SubnetStatus name is the same as the Subnet CR name.
type SubnetStatus struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// DHCP address ranges used for dynamic IP allocation.
	// +listType=atomic
	DHCPRanges []string `json:"dhcpRanges,omitempty"`

	// DHCP reserved IP ranges excluded from dynamic DHCP assignment.
	// +listType=atomic
	DHCPReservedRanges []string `json:"dhcpReservedRanges,omitempty"`

	// Unmanaged IP addresses or ranges in the Subnet CIDR not managed by IP pools or DHCP.
	// +listType=atomic
	UnmanagedIPs []string `json:"unmanagedIPs,omitempty"`

	// Static IP address ranges used for static IP allocation.
	// +listType=atomic
	StaticIPPoolRanges []string `json:"staticIPPoolRanges,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// SubnetStatusList contains a list of SubnetStatus.
type SubnetStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SubnetStatus `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SubnetStatus{}, &SubnetStatusList{})
}
