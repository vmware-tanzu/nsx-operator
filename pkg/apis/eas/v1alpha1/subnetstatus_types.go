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

	// DHCP IP address ranges. The field is populated only for single-resource retrieval.
	// +optional
	DHCPRanges []string `json:"dhcpRanges,omitempty" protobuf:"bytes,2,rep,name=dhcpRanges"`
	// DHCP reserved IP ranges that are reserved and excluded from being assigned by the DHCP server to clients.
	// The field is populated only for single-resource retrieval.
	// +optional
	DHCPReservedRanges []string `json:"dhcpReservedRanges,omitempty" protobuf:"bytes,3,rep,name=dhcpReservedRanges"`
	// Gateway address.
	// +optional
	GatewayAddresses []string `json:"gatewayAddresses,omitempty" protobuf:"bytes,4,rep,name=gatewayAddresses"`
	// IP address type that will be allocated for VPC Subnets. The field is populated only for single-resource retrieval.
	// +kubebuilder:validation:Enum=IPv4;IPv6
	// +optional
	IPAddressTypes []IPAddressType `json:"ipAddressTypes,omitempty" protobuf:"bytes,5,rep,name=ipAddressTypes"`
	// IP address ranges for static IP allocation. The field is populated only for single-resource retrieval.
	// +optional
	StaticIPPoolRanges []string `json:"staticIPPoolRanges,omitempty" protobuf:"bytes,6,rep,name=staticIPPoolRanges"`
	// UnmanagedIPs specifies IP addresses or IP ranges in the Subnet CIDR that are unmanaged (not allocated by any IP pool,
	// DHCP range, DHCP reserved ranges, or system-reserved addresses like gateway, network, DHCP server, or broadcast).
	// The field is populated only for single-resource retrieval.
	// +optional
	UnmanagedIPs []string `json:"unmanagedIPs,omitempty" protobuf:"bytes,7,rep,name=unmanagedIPs"`
	// VLAN extension configured for VPC Subnet. The field is populated only for single-resource retrieval.
	// +optional
	VLANExtension *VLANExtension `json:"vlanExtension,omitempty" protobuf:"bytes,8,opt,name=vlanExtension"`
}

type VLANExtension struct {
	// The VLAN ID used for extending to the VPC Subnet.
	VLANID int32 `json:"vlanID" protobuf:"varint,1,opt,name=vlanID"`
	// Indicates whether the VPC gateway connection is enabled for this Subnet.
	VPCGatewayConnectionEnable bool `json:"vpcGatewayConnectionEnable" protobuf:"varint,2,opt,name=vpcGatewayConnectionEnable"`
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
