
// Copyright (c) 2026 Broadcom. All Rights Reserved.
// Broadcom Confidential. The term "Broadcom" refers to Broadcom Inc.
// and/or its subsidiaries.
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// IPBlockUsage is the usage information of an IPBlock.
// It contains used IP ranges and available IP ranges statistics of an IPBlock.
type IPBlockUsage struct {
	metav1.TypeMeta	 `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Used IP ranges in an IPBlock.
	UsedIPRanges []string `json:"usedIpRanges,omitempty"`
	// Available IP ranges in an IPBlock.
	AvailableIPRanges []string `json:"availableIpRanges,omitempty"`
	// Used IPs count in an IPBlock.
	UsedIPsCount string `json:"usedIpsCount,omitempty"`
	// Available IP count in an IPBlock.
	AvailableIPsCount string `json:"availableIpsCount,omitempty"`
	// Total count of IPs which are present in this block.
	OverallIPsCount string `json:"overallIpsCount,omitempty"`
	// CIDR usage details for each CIDR in an IPBlock.
	CIDRUsages []CIDRUsage `json:"cidrUsage,omitempty"`
	// Range usage details for each IP Range in an IPBlock.
	RangeUsages []RangeUsage `json:"rangeUsage,omitempty"`
	// Visibility of IPBlock.
	// Must be External or Private.
	// +kubebuilder:validation:Enum=External;Private
	Visibility IPAddressVisibility `json:"visibility,omitempty"`
}

// Represents used and available IP statistics for CIDRs in an IPBlock.
type CIDRUsage struct {
	// One CIDR in an IPBlock CIDRList.
	CIDR string `json:"cidr,omitempty"`
	UsageDetails  `json:"usage,omitempty"`
}

// Represents used and available IP statistics for IP Ranges in an IPBlock.
type RangeUsage struct {
	// One range in an IPBlock RangeList.
	Range string `json:"range,omitempty"`
	UsageDetails `json:"usage,omitempty"`
}

type UsageDetails struct {
	// Represents IP ranges that are allocated from the CIDR or IP Range in the tenant scope.
	UsedIPRanges []string `json:"usedIpRanges,omitempty"`
	// Represent the overall IP ranges allocated from the CIDR or IP Range across all tenants.
	OverallUsedIPRanges []string `json:"overallUsedIpsCount,omitempty"`
	// Represents free IP ranges from the CIDR or IP Range.
	AvailableIPRanges []string `json:"availableIpRanges,omitempty"`
	// Represents IP count that are allocated from the CIDR or IP Range in the tenant scope.
	UsedIPsCount string `json:"usedIpsCount,omitempty"`
	// Represent the overall IP count allocated from the CIDR or IP Range across all tenants.
	OverallUsedIPsCount string `json:"overallUsedIpsCount,omitempty"`
	// Represents free IP count in the CIDR or IP Range.
	AvailableIPsCount string `json:"availableIpsCount,omitempty"`
}