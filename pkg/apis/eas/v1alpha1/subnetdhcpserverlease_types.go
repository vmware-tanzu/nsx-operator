// Copyright (c) 2026 Broadcom. All Rights Reserved.
// Broadcom Confidential. The term "Broadcom" refers to Broadcom Inc.
// and/or its subsidiaries.

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DHCPLease represents an IPv4 DHCP server lease.
type DHCPLease struct {
	// MACAddress is the MAC address of the client holding the lease.
	MACAddress string `json:"macAddress,omitempty" protobuf:"bytes,1,opt,name=macAddress"`
	// IPAddress is the leased IPv4 address.
	IPAddress string `json:"ipAddress,omitempty" protobuf:"bytes,2,opt,name=ipAddress"`
	// Subnet is the network address of the CIDR the lease was allocated from, e.g. "192.168.10.0".
	// +optional
	Subnet string `json:"subnet,omitempty" protobuf:"bytes,3,opt,name=subnet"`
	// LeaseTime is the lease duration, in seconds.
	// +optional
	LeaseTime string `json:"leaseTime,omitempty" protobuf:"bytes,4,opt,name=leaseTime"`
	// StartTime is the time at which the lease was issued.
	// +optional
	StartTime string `json:"startTime,omitempty" protobuf:"bytes,5,opt,name=startTime"`
	// ExpireTime is the time at which the lease expires.
	// +optional
	ExpireTime string `json:"expireTime,omitempty" protobuf:"bytes,6,opt,name=expireTime"`
}

// DHCPv6Lease represents an IPv6 DHCP server lease.
type DHCPv6Lease struct {
	// DUID is the DHCP unique identifier of the client holding the lease.
	DUID string `json:"duid,omitempty" protobuf:"bytes,1,opt,name=duid"`
	// IAType is the identity association type of the lease, e.g. IA_NA or IA_PD.
	// +optional
	IAType string `json:"iaType,omitempty" protobuf:"bytes,2,opt,name=iaType"`
	// IAID is the identity association identifier of the lease.
	// +optional
	IAID int64 `json:"iaid,omitempty" protobuf:"varint,3,opt,name=iaid"`
	// IPAddresses are the leased IPv6 addresses.
	// +optional
	IPAddresses []string `json:"ipAddresses,omitempty" protobuf:"bytes,4,rep,name=ipAddresses"`
	// LeaseTime is the lease duration, in seconds.
	// +optional
	LeaseTime string `json:"leaseTime,omitempty" protobuf:"bytes,5,opt,name=leaseTime"`
	// StartTime is the time at which the lease was issued.
	// +optional
	StartTime string `json:"startTime,omitempty" protobuf:"bytes,6,opt,name=startTime"`
	// ExpireTime is the time at which the lease expires.
	// +optional
	ExpireTime string `json:"expireTime,omitempty" protobuf:"bytes,7,opt,name=expireTime"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:resource:scope=Namespaced

// SubnetDHCPServerLeases is a sub-resource of Subnet that represents the DHCP server leases of the Subnet.
type SubnetDHCPServerLeases struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	// Leases contains the IPv4 DHCP server leases.
	// +optional
	Leases []DHCPLease `json:"leases,omitempty" protobuf:"bytes,3,rep,name=leases"`
	// IPv6Leases contains the IPv6 DHCP server leases.
	// +optional
	IPv6Leases []DHCPv6Lease `json:"ipv6Leases,omitempty" protobuf:"bytes,4,rep,name=ipv6Leases"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// SubnetDHCPServerLeaseList contains a list of SubnetDHCPServerLease.
type SubnetDHCPServerLeasesList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SubnetDHCPServerLeases `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SubnetDHCPServerLeases{}, &SubnetDHCPServerLeasesList{})
}
