/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SubnetSetSpec defines the desired state of SubnetSet.
type SubnetSetSpec struct {
	// Size of Subnet based upon estimated workload count.
	// Defaults to 64.
	// +kubebuilder:default:=64
	// +kubebuilder:validation:Maximum:=65536
	// +kubebuilder:validation:Minimum:=16
	IPv4SubnetSize int `json:"ipv4SubnetSize,omitempty"`
	// Access mode of Subnet, accessible only from within VPC or from outside VPC.
	// Defaults to private.
	// +kubebuilder:default:=private
	// +kubebuilder:validation:Enum=private;public
	AccessMode AccessMode `json:"accessMode,omitempty"`
	// Subnet advanced configuration.
	AdvancedConfig AdvancedConfig `json:"advancedConfig,omitempty"`
	// DHCPConfig DHCP configuration.
	DHCPConfig DHCPConfig `json:"DHCPConfig,omitempty"`
}

// SubnetInfo defines the observed state of a single Subnet of a SubnetSet.
type SubnetInfo struct {
	NSXResourcePath string   `json:"nsxResourcePath"`
	IPAddresses     []string `json:"ipAddresses"`
}

// SubnetSetStatus defines the observed state of SubnetSet.
type SubnetSetStatus struct {
	Conditions []Condition  `json:"conditions"`
	Subnets    []SubnetInfo `json:"subnets"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// SubnetSet is the Schema for the subnetsets API.
type SubnetSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SubnetSetSpec   `json:"spec,omitempty"`
	Status SubnetSetStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// SubnetSetList contains a list of SubnetSet.
type SubnetSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SubnetSet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SubnetSet{}, &SubnetSetList{})
}
