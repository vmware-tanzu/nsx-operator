// Copyright (c) 2026 Broadcom. All Rights Reserved.
// Broadcom Confidential. The term "Broadcom" refers to Broadcom Inc.
// and/or its subsidiaries.

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// VLANExtensionStatus describes the VLAN extension status for the Subnet.
type VLANExtensionStatus struct {
	// VLAN ID used for extending to the VPC subnet.
	VLANID int64 `json:"vlanId,omitempty"`
	// Indicates whether the VPC gateway connection is enabled for this subnet.
	VPCGatewayConnectionEnable bool `json:"vpcGatewayConnectionEnable,omitempty"`
}

// VPCSubnetStatusDetails represents the realized status details for an address family of the Subnet.
type VPCSubnetStatusDetails struct {
	// IP address type (IPv4 or IPv6).
	// Must be IPv4, IPv6, or IPv4IPv6.
	// +kubebuilder:validation:Enum=IPv4;IPv6;IPv4IPv6
	AddressType string `json:"addressType,omitempty"`

	// Network address (CIDR) of the VPC Subnet.
	NetworkAddress string `json:"networkAddress,omitempty"`

	// Gateway address of the VPC Subnet.
	GatewayAddress string `json:"gatewayAddress,omitempty"`

	// DHCP server IP address.
	DHCPServerAddress string `json:"dhcpServerAddress,omitempty"`

	// DHCP address ranges used for dynamic IP allocation.
	// +listType=atomic
	DHCPRanges []string `json:"dhcpRanges,omitempty"`

	// DHCP reserved IP ranges excluded from dynamic DHCP assignment.
	// +listType=atomic
	DHCPReservedRanges []string `json:"dhcpReservedRanges,omitempty"`

	// Unmanaged IP addresses or ranges in the subnet CIDR.
	// +listType=atomic
	UnmanagedIPs []string `json:"unmanagedIPs,omitempty"`

	// Static IP address ranges used for static IP allocation (e.g. LB VIP, DHCP static binding).
	// +listType=atomic
	StaticIPPoolRanges []string `json:"staticIPPoolRanges,omitempty"`

	// Policy path to the static IP pool in NSX.
	StaticIPPoolPath string `json:"staticIPPoolPath,omitempty"`

	// VLAN extension configuration and status for the Subnet.
	VLANExtension *VLANExtensionStatus `json:"vlanExtension,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:resource:scope=Namespaced

// SubnetStatus exposes the realized status of a Subnet retrieved live from NSX.
// It includes DHCP reserved IP ranges, unmanaged IP ranges, dynamic DHCP ranges,
// static IP pool ranges, gateway addresses, network addresses, and VLAN extension status.
// The SubnetStatus name is the same as the Subnet CR name.
type SubnetStatus struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Network addresses (CIDRs) of the Subnet across all configured address families.
	// +listType=atomic
	NetworkAddresses []string `json:"networkAddresses,omitempty"`

	// Gateway addresses of the Subnet.
	// +listType=atomic
	GatewayAddresses []string `json:"gatewayAddresses,omitempty"`

	// DHCP server IP addresses for the Subnet.
	// +listType=atomic
	DHCPServerAddresses []string `json:"dhcpServerAddresses,omitempty"`

	// DHCP address ranges used for dynamic IP allocation.
	// +listType=atomic
	DHCPRanges []string `json:"dhcpRanges,omitempty"`

	// DHCP reserved IP ranges excluded from dynamic DHCP assignment.
	// +listType=atomic
	DHCPReservedRanges []string `json:"dhcpReservedRanges,omitempty"`

	// Unmanaged IP addresses or ranges in the subnet CIDR not managed by NSX IP pools or DHCP.
	// +listType=atomic
	UnmanagedIPs []string `json:"unmanagedIPs,omitempty"`

	// Static IP address ranges used for static IP allocation.
	// +listType=atomic
	StaticIPPoolRanges []string `json:"staticIPPoolRanges,omitempty"`

	// Detailed status per address family (IPv4, IPv6).
	// +listType=atomic
	SubnetStatuses []VPCSubnetStatusDetails `json:"subnetStatuses,omitempty"`

	// VLAN extension status for the Subnet.
	VLANExtension *VLANExtensionStatus `json:"vlanExtension,omitempty"`
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
