
// Copyright (c) 2026 Broadcom. All Rights Reserved.
// Broadcom Confidential. The term "Broadcom" refers to Broadcom Inc.
// and/or its subsidiaries.

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// VPCIPAddressUsage is the usage information for IP addresses within a specific VPC. This information provides insights
// into the allocation and utilization of IP addresses by the VPC and its subnets.
type VPCIPAddressUsage struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Array of VPC IP address block.
	IPBlocks []VPCIPAddressBlock `json:"ipBlocks,omitempty"`
}

// VPC IP address block.
type VPCIPAddressBlock struct {
	IPBlockName string `json:"name,omitempty"`
	// CIDR address for IPBlock.
	// Deprecated: Use CIDRs instead.
	CIDR string `json:"cidr,omitempty"`
	// Total IP address space.
	Total int64 `json:"total,omitempty"`
	// Available IP address space.
	Available int64 `json:"available,omitempty"`
	// Percentage of used IP address space.
	PercentageUsed string `json:"percentageUsed,omitempty"`
	// Visibility of IP Block. Must be External or Private.
	// +kubebuilder:validation:Enum=External;Private
	Visibility IPAddressVisibility `json:"visibility,omitempty"`
	// AllocatedByVPC contains the CIDR, used IP range and subnet access mode etc.
	AllocatedByVPC AllocatedByVPC `json:"allocatedByVPC,omitempty"`
	// The list of CIDRs.
	CIDRs []string `json:"cidrs,omitempty"`
	// The list of IP address ranges in the form of start and end IPs.
	Ranges []IPPoolRange `json:"ranges,omitempty"`
	// The list of excluded IP address in the form of start and end IPs.
	ExcludedIPs []IPPoolRange `json:"excludedIPs,omitempty"`
}

type AccessMode string

const (
	PublicSubnet     AccessMode = "Public"
	PrivateTGWSubnet AccessMode = "PrivateTGW"
	PrivateSubnet    AccessMode = "Private"
)

type AllocatedByVPC struct {
	// Count of used IPs by VPC from the IP Block.
	Count int64 `json:"count,omitempty"`
	// IPAddresses contains CIDR and subnet or IP address allocation.
	IPAddresses []VPCIPAddress `json:"ipAddresses,omitempty"`
	// Access mode of the subnet allocated from the IP Block.
	// Must be Public, PrivateTGW or Private.
	// +kubebuilder:validation:Enum=Public;PrivateTGW;Private
	AccessMode AccessMode `json:"accessMode,omitempty"`
	// Percentage of used IP address resources by VPC.
	PercentageUsed string `json:"percentageUsed,omitempty"`
}

type VPCIPAddress struct {
	Address string `json:"address,omitempty"`
	// The name of the Subnet to which the IP address is allocated.
	// Only one of subnetName and ipAddressAllocationName will be set.
	SubnetName string `json:"subnetName,omitempty"`
	// The name of the IPAddressAllocation to which the IP address is allocated.
	// Only one of subnetName and ipAddressAllocationName will be set.
	IPAddressAllocationName string `json:"ipAddressAllocationName,omitempty"`
}
