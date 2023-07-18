/* Copyright � 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:storageversion

// VPC is the Schema for the VPC API
type VPC struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VPCSpec   `json:"spec,omitempty"`
	Status VPCStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// VPCList contains a list of VPC
type VPCList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VPC `json:"items"`
}

// VPCSpec defines VPC configuration
type VPCSpec struct {
}

// VPCStatus defines the observed state of VPC
type VPCStatus struct {
	Conditions []Condition `json:"conditions"`
	// NSX VPC Policy API resource path.
	NSXResourcePath string `json:"nsxResourcePath"`
	// Default SNAT IP for private Subnets
	DefaultSNATIP string            `json:"defaultSNATIP"`
	CIDRsUsage    VPCCIDRsUsageInfo `json:"cidrsUsage"`
}

type VPCCIDRsUsageInfo struct {
	PublicCIDRsUsage  CIDRsUsageInfo `json:"publicCIDRsUsage"`
	PrivateCIDRsUsage CIDRsUsageInfo `json:"privateCIDRsUsage"`
}

type CIDRsUsageInfo struct {
	// Allocated IPs in the IP Block (including IPs used by other VPCs).
	Allocated int `json:"allocated"`
	// Used IP for the VPC in the IP Block (IPs used only in current VPC).
	Used int `json:"used"`
	// Total IP number in the IP Block.
	Total int `json:"total"`
}

func init() {
	SchemeBuilder.Register(&VPC{}, &VPCList{})
}
