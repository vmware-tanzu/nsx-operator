// Copyright (c) 2026 Broadcom. All Rights Reserved.
// Broadcom Confidential. The term "Broadcom" refers to Broadcom Inc.
// and/or its subsidiaries.

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DHCPLeasePerIP represents the IPv4 DHCP lease information for a client.
type DHCPLeasePerIP struct {
	// IP address of the DHCP client.
	IPAddress string `json:"ipAddress,omitempty"`
	// MAC address of the DHCP client.
	MACAddress string `json:"macAddress,omitempty"`
	// Start time of the lease.
	StartTime string `json:"startTime,omitempty"`
	// Expire time of the lease.
	ExpireTime string `json:"expireTime,omitempty"`
	// Lease time of the IP address, in seconds.
	LeaseTime string `json:"leaseTime,omitempty"`
	// Subnet of client network.
	Subnet string `json:"subnet,omitempty"`
}

// DHCPV6Lease represents the IPv6 DHCP lease information for a client.
type DHCPV6Lease struct {
	// DHCP Unique Identifier (DUID) of the client.
	DUID string `json:"duid,omitempty"`
	// MAC address of the DHCP client.
	MACAddress string `json:"macAddress,omitempty"`
	// IPv6 addresses assigned to the client.
	// +listType=atomic
	IPAddresses []string `json:"ipAddresses,omitempty"`
	// Identity Association (IA) type (e.g., IA_NA, IA_TA, IA_PD).
	IAType string `json:"iaType,omitempty"`
	// Identifier for an Identity Association.
	IAID int64 `json:"iaid,omitempty"`
	// Start time of the lease.
	StartTime string `json:"startTime,omitempty"`
	// Expire time of the lease.
	ExpireTime string `json:"expireTime,omitempty"`
	// Lease time of the IP address, in seconds.
	LeaseTime string `json:"leaseTime,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:resource:scope=Namespaced

// SubnetDHCPServerLease exposes the active DHCP leases for a Subnet's DHCP server.
// The SubnetDHCPServerLease name should be the same as the Subnet CR name.
type SubnetDHCPServerLease struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// DHCP server UUID in NSX.
	DHCPServerID string `json:"dhcpServerId,omitempty"`
	// Timestamp of the lease info.
	Timestamp int64 `json:"timestamp,omitempty"`
	// IPv4 DHCP lease information list.
	// +listType=atomic
	Leases []DHCPLeasePerIP `json:"leases,omitempty"`
	// IPv6 DHCP lease information list.
	// +listType=atomic
	IPv6Leases []DHCPV6Lease `json:"ipv6Leases,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// SubnetDHCPServerLeaseList contains a list of SubnetDHCPServerLease.
type SubnetDHCPServerLeaseList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SubnetDHCPServerLease `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SubnetDHCPServerLease{}, &SubnetDHCPServerLeaseList{})
}
