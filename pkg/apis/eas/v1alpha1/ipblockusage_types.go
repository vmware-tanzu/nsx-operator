/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CidrUsageDetails represents usage details for a CIDR of IpAddressBlock.
type CidrUsageDetails struct {
	// CIDR notation.
	Cidr string `json:"cidr,omitempty"`
	// Used IP count in the user's scope.
	UsedIpsCount string `json:"usedIpsCount,omitempty"`
	// Available IP count.
	AvailableIpsCount string `json:"availableIpsCount,omitempty"`
	// Available IP ranges.
	AvailableIpRanges []string `json:"availableIpRanges,omitempty"`
	// Used IP ranges in the user's scope.
	UsedIpRanges []string `json:"usedIpRanges,omitempty"`
	// Overall used IP count (not limited to user's scope).
	OverallUsedIpsCount string `json:"overallUsedIpsCount,omitempty"`
}

// RangeUsageDetails represents usage details for an IP range of IpAddressBlock.
type RangeUsageDetails struct {
	// The IP range from RangeList in IpAddressBlock.
	Start string `json:"start,omitempty"`
	// Used IP count in the user's scope.
	UsedIpsCount string `json:"usedIpsCount,omitempty"`
	// Available IP count.
	AvailableIpsCount string `json:"availableIpsCount,omitempty"`
	// Available IP ranges.
	AvailableIpRanges []string `json:"availableIpRanges,omitempty"`
	// Used IP ranges in the user's scope.
	UsedIpRanges []string `json:"usedIpRanges,omitempty"`
	// Overall used IP count (not limited to user's scope).
	OverallUsedIpsCount string `json:"overallUsedIpsCount,omitempty"`
}

// IPBlockUsageSpec represents the usage of an IP Address Block.
type IPBlockUsageSpec struct {
	// Address type (IPV4 or IPV6).
	AddressType string `json:"addressType,omitempty"`
	// Available IP ranges.
	AvailableIpRanges []string `json:"availableIpRanges,omitempty"`
	// Total count of available IPs.
	AvailableIpsCount string `json:"availableIpsCount,omitempty"`
	// Usage details for each CIDR.
	CidrUsage []CidrUsageDetails `json:"cidrUsage,omitempty"`
	// Description of the IP block.
	Description string `json:"description,omitempty"`
	// Display name of the IP block.
	DisplayName string `json:"displayName,omitempty"`
	// Policy path of IpAddressBlock.
	IntentPath string `json:"intentPath,omitempty"`
	// Total count of IPs in this block.
	OverallIpsCount string `json:"overallIpsCount,omitempty"`
	// Usage details for each IP range.
	RangeUsage []RangeUsageDetails `json:"rangeUsage,omitempty"`
	// Used IP ranges.
	UsedIpRanges []string `json:"usedIpRanges,omitempty"`
	// Total count of used IPs.
	UsedIpsCount string `json:"usedIpsCount,omitempty"`
	// Visibility (PRIVATE or EXTERNAL).
	Visibility string `json:"visibility,omitempty"`
}

// +genclient
//+kubebuilder:object:root=true
//+kubebuilder:storageversion

// IPBlockUsage is the Schema for the IP block usage API.
// +kubebuilder:printcolumn:name="Visibility",type=string,JSONPath=`.spec.visibility`,description="IP block visibility"
// +kubebuilder:printcolumn:name="UsedIPs",type=string,JSONPath=`.spec.usedIpsCount`,description="Used IP count"
// +kubebuilder:printcolumn:name="AvailableIPs",type=string,JSONPath=`.spec.availableIpsCount`,description="Available IP count"
type IPBlockUsage struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec IPBlockUsageSpec `json:"spec"`
}

//+kubebuilder:object:root=true

// IPBlockUsageList contains a list of IPBlockUsage.
type IPBlockUsageList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IPBlockUsage `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IPBlockUsage{}, &IPBlockUsageList{})
}
