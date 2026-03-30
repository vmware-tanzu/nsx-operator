/* Copyright © 2024 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type NetworkStackType string

const (
	FullStackVPC  NetworkStackType = "FullStackVPC"
	VLANBackedVPC NetworkStackType = "VLANBackedVPC"
)

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:storageversion

// NetworkInfo is used to report the network information for a namespace.
// +kubebuilder:resource:path=networkinfos
type NetworkInfo struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	VPCs       []VPCState       `json:"vpcs"`
	DNSDomains []DNSDomainState `json:"dnsDomains"`
}

// +kubebuilder:object:root=true

// NetworkInfoList contains a list of NetworkInfo.
type NetworkInfoList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NetworkInfo `json:"items"`
}

// VPCState defines information for VPC.
type VPCState struct {
	// VPC name.
	Name string `json:"name"`
	// Default SNAT IP for Private Subnets.
	DefaultSNATIP string `json:"defaultSNATIP"`
	// LoadBalancerIPAddresses (AVI SE Subnet CIDR or NSX LB SNAT IPs).
	LoadBalancerIPAddresses string `json:"loadBalancerIPAddresses,omitempty"`
	// Private CIDRs used for the VPC.
	PrivateIPs []string `json:"privateIPs,omitempty"`
	// NetworkStack indicates the networking stack for the VPC.
	// Valid values: FullStackVPC, VLANBackedVPC
	// +kubebuilder:validation:Enum=FullStackVPC;VLANBackedVPC
	NetworkStack NetworkStackType `json:"networkStack,omitempty"`
}

// DNSDomainState defines the information for a DNS Domain.
type DNSDomainState struct {
	// Domain is the DNS domain name configured in the zone.
	Domain string `json:"domain"`

	//Type describes the access type of the DNS domain.
	// +kubebuilder:validation:Enum=Private;Public
	Type string `json:"type"`
}

func init() {
	SchemeBuilder.Register(&NetworkInfo{}, &NetworkInfoList{})
}
