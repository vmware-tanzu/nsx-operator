/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:nonNamespaced
//+kubebuilder:object:root=true
//+kubebuilder:resource:scope="Cluster",path=ipblocksinfos

// IPBlocksInfo is the Schema for the ipblocksinfo API
type IPBlocksInfo struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// ExternalIPCIDRs is a list of IPv4 CIDR strings. Each CIDR is a contiguous IP address
	// space represented by network address and prefix length. The visibility of the
	// IPBlocks is External. IPv6 CIDRs are listed in externalIPv6CIDRs.
	ExternalIPCIDRs []string `json:"externalIPCIDRs,omitempty"`
	// ExternalIPv6CIDRs is a list of IPv6 CIDR strings for external IP blocks (same visibility as externalIPCIDRs).
	ExternalIPv6CIDRs []string `json:"externalIPv6CIDRs,omitempty"`
	// PrivateTGWIPCIDRs is a list of IPv4 CIDR strings. Each CIDR is a contiguous IP address
	// space represented by network address and prefix length. The visibility of the
	// IPBlocks is Private Transit Gateway. Only IPBlocks in default project will be included.
	// IPv6 CIDRs are listed in privateTGWIPv6CIDRs.
	PrivateTGWIPCIDRs []string `json:"privateTGWIPCIDRs,omitempty"`
	// PrivateTGWIPv6CIDRs is a list of IPv6 CIDR strings for private transit gateway IP blocks.
	PrivateTGWIPv6CIDRs []string `json:"privateTGWIPv6CIDRs,omitempty"`
	// ExternalIPRanges is an array of contiguous IPv4 address space represented by start and end IPs.
	// The visibility of the IPBlocks is External.
	ExternalIPRanges []IPPoolRange `json:"externalIPRanges,omitempty"`
	// ExternalIPv6Ranges is an array of contiguous IPv6 address space for external IP blocks.
	ExternalIPv6Ranges []IPPoolRange `json:"externalIPv6Ranges,omitempty"`
	// PrivateTGWIPRanges is an array of contiguous IPv4 address space represented by start and end IPs.
	// The visibility of the IPBlocks is Private Transit Gateway.
	PrivateTGWIPRanges []IPPoolRange `json:"privateTGWIPRanges,omitempty"`
	// PrivateTGWIPv6Ranges is an array of contiguous IPv6 address space for private transit gateway IP blocks.
	PrivateTGWIPv6Ranges []IPPoolRange `json:"privateTGWIPv6Ranges,omitempty"`
}

//+kubebuilder:object:root=true

// IPBlocksInfoList contains a list of IPBlocksInfo
type IPBlocksInfoList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IPBlocksInfo `json:"items"`
}

type IPPoolRange struct {
	// The start IP Address of the IP Range.
	Start string `json:"start"`
	// The end IP Address of the IP Range.
	End string `json:"end"`
}

func init() {
	SchemeBuilder.Register(&IPBlocksInfo{}, &IPBlocksInfoList{})
}
