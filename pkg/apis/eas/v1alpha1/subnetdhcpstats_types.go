// Copyright (c) 2026 Broadcom. All Rights Reserved.
// Broadcom Confidential. The term "Broadcom" refers to Broadcom Inc.
// and/or its subsidiaries.
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DHCPIPPoolUsage represents DHCP IP pool usage statistics.
type DHCPIPPoolUsage struct {
	// PoolSize is the total size of the IP pool.
	PoolSize int64 `json:"poolSize,omitempty" protobuf:"varint,2,opt,name=poolSize"`
	// AllocatedNumber is the number of allocated IPs. For reference only; value may not be exact.
	AllocatedNumber int64 `json:"allocatedNumber,omitempty" protobuf:"varint,3,opt,name=allocatedNumber"`
	// AllocatedPercentage is the percentage of allocated IPs. For reference only; value may not be exact.
	AllocatedPercentage int64 `json:"allocatedPercentage,omitempty" protobuf:"varint,4,opt,name=allocatedPercentage"`
	// ConsumedNumber is the total number of IPs consumed by DHCP clients.
	ConsumedNumber int64 `json:"consumedNumber,omitempty" protobuf:"varint,5,opt,name=consumedNumber"`
}

// DHCPIPv6IPPoolUsage represents DHCP IPv6 pool usage statistics.
// Counters are decimal strings because IPv6 pools can exceed the int64 range.
type DHCPIPv6IPPoolUsage struct {
	// Number of allocated IP addresses, as a decimal string.
	AllocatedNumber string `json:"allocatedNumber"`
	// Allocated percentage, including fractional values.
	AllocatedPercentage float64 `json:"allocatedPercentage"`
	// Number of IP addresses consumed by DHCP clients, as a decimal string.
	ConsumedNumber string `json:"consumedNumber"`
	// Pool size, as a decimal string.
	PoolSize string `json:"poolSize"`
}

// +genclient
//+kubebuilder:object:root=true
//+kubebuilder:storageversion

// SubnetDHCPServerStats exposes the Subnet's DHCP server statistics.
// The SubnetDHCPServerStats name should be the same as the Subnet CR name.
type SubnetDHCPServerStats struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// DHCP IP pool usage statistics.
	// +listType=atomic
	IPPoolStats []DHCPIPPoolUsage `json:"ipPoolStats,omitempty"`
	// DHCP IPv6 pool usage statistics.
	// +listType=atomic
	IPv6PoolStats []DHCPIPv6IPPoolUsage `json:"ipv6PoolStats,omitempty"`
}

//+kubebuilder:object:root=true

// SubnetDHCPServerStatsList contains a list of SubnetDHCPServerStats.
type SubnetDHCPServerStatsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SubnetDHCPServerStats `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SubnetDHCPServerStats{}, &SubnetDHCPServerStatsList{})
}
