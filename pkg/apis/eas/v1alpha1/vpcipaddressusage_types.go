/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AllocatedByVpc represents resources allocated by VPC on an IP block.
type AllocatedByVpc struct {
	// Access mode of VPC subnet.
	AccessMode string `json:"accessMode,omitempty"`
	// Count of used resources by VPC on IP block.
	Count int64 `json:"count,omitempty"`
	// IP addresses allocated by VPC.
	IpAddresses []VpcIpAddress `json:"ipAddresses,omitempty"`
	// Percentage of used IP address resources by VPC.
	PercentageUsed string `json:"percentageUsed,omitempty"`
}

// VpcIpAddress represents a VPC IP address entry.
type VpcIpAddress struct {
	// IP address allocated.
	IpAddress string `json:"ipAddress,omitempty"`
	// Path of the object consuming this IP (subnet or IP allocation).
	Path string `json:"path,omitempty"`
}

// IpPoolRange represents an IP range with start and end addresses.
type IpPoolRange struct {
	// The start IP address of the range.
	Start string `json:"start"`
	// The end IP address of the range.
	End string `json:"end"`
}

// VpcIpAddressBlock represents IP block usage for a VPC.
type VpcIpAddressBlock struct {
	// Resources allocated by VPC.
	AllocatedByVpc *AllocatedByVpc `json:"allocatedByVpc,omitempty"`
	// Available IP address space.
	Available int64 `json:"available,omitempty"`
	// CIDR list for IP block.
	Cidrs []string `json:"cidrs,omitempty"`
	// Excluded IP address ranges.
	ExcludedIps []IpPoolRange `json:"excludedIps,omitempty"`
	// Path for IP block.
	Path string `json:"path,omitempty"`
	// Percentage of used IP address space.
	PercentageUsed string `json:"percentageUsed,omitempty"`
	// IP address ranges.
	Ranges []IpPoolRange `json:"ranges,omitempty"`
	// Total ip address space.
	Total int64 `json:"total,omitempty"`
	// Visibility of IP block.
	Visibility string `json:"visibility,omitempty"`
}

// VPCIPAddressUsageSpec represents the IP address usage of a VPC.
type VPCIPAddressUsageSpec struct {
	// VPC ip address blocks.
	IpBlocks []VpcIpAddressBlock `json:"ipBlocks,omitempty"`
}

// +genclient
//+kubebuilder:object:root=true
//+kubebuilder:storageversion

// VPCIPAddressUsage is the Schema for the VPC IP address usage API.
// +kubebuilder:printcolumn:name="VPC",type=string,JSONPath=`.metadata.name`,description="VPC name"
type VPCIPAddressUsage struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec VPCIPAddressUsageSpec `json:"spec"`
}

//+kubebuilder:object:root=true

// VPCIPAddressUsageList contains a list of VPCIPAddressUsage.
type VPCIPAddressUsageList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VPCIPAddressUsage `json:"items"`
}

func init() {
	SchemeBuilder.Register(&VPCIPAddressUsage{}, &VPCIPAddressUsageList{})
}
