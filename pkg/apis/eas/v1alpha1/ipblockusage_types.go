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
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// Used IP ranges in an IPBlock.
	// +listType=atomic
	UsedIPRanges []string `json:"usedIPRanges,omitempty"`
	// Available IP ranges in an IPBlock.
	// +listType=atomic
	AvailableIPRanges []string `json:"availableIPRanges,omitempty"`
	// Used IPs count in an IPBlock.
	UsedIPsCount string `json:"usedIPsCount"`
	// Available IP count in an IPBlock.
	AvailableIPsCount string `json:"availableIPsCount"`
	// Total count of IPs which are present in this block.
	OverallIPsCount string `json:"overallIPsCount"`
	// CIDR usage details for each CIDR in an IPBlock.
	// +listType=atomic
	CIDRUsages []CIDRUsage `json:"cidrUsages,omitempty"`
	// Range usage details for each IP range in an IPBlock.
	// +listType=atomic
	RangeUsages []RangeUsage `json:"rangeUsages,omitempty"`
	// Visibility of IPBlock.
	// Must be External or Private.
	// +kubebuilder:validation:Enum=External;Private
	Visibility IPAddressVisibility `json:"visibility,omitempty"`
}

// Represents used and available IP statistics for CIDRs in an IPBlock.
type CIDRUsage struct {
	// One CIDR in an IPBlock CIDRList.
	CIDR         string `json:"cidr,omitempty"`
	UsageDetails `json:"usageDetails,omitempty"`
}

// Represents used and available IP statistics for IP ranges in an IPBlock.
type RangeUsage struct {
	// One range in an IPBlock range list.
	Range        string `json:"range,omitempty"`
	UsageDetails `json:"usageDetails,omitempty"`
}

type UsageDetails struct {
	// Represents IP ranges that are allocated from the CIDR or IP range in the org scope.
	// +listType=atomic
	UsedIPRanges []string `json:"usedIPRanges,omitempty"`
	// Represents the overall IP ranges allocated from the CIDR or IP range across all orgs.
	// +listType=atomic
	OverallUsedIPRanges []string `json:"overallUsedIPRanges,omitempty"`
	// Represents free IP ranges from the CIDR or IP range.
	// +listType=atomic
	AvailableIPRanges []string `json:"availableIPRanges,omitempty"`
	// Represents IP count that are allocated from the CIDR or IP range in the org scope.
	UsedIPsCount string `json:"usedIPsCount"`
	// Represents the overall IP count allocated from the CIDR or IP range across all orgs.
	OverallUsedIPsCount string `json:"overallUsedIPsCount"`
	// Represents free IP count in the CIDR or IP range.
	AvailableIPsCount string `json:"availableIPsCount"`
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
