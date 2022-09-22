/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AccessMode string

// SubnetSpec defines the desired state of Subnet.
type SubnetSpec struct {
	// Size of Subnet based upon estimated workload count.
	// Defaults to 64.
	// +kubebuilder:default:=64
	IPv4SubnetSize int `json:"ipv4SubnetSize,omitempty"`
	// Access mode of Subnet, accessible only from within VPC or from outside VPC.
	// Defaults to private.
	// +kubebuilder:default:=private
	// +kubebuilder:validation:Enum=private;public
	AccessMode AccessMode `json:"accessMode,omitempty"`
	// Subnet CIDRS.
	// +kubebuilder:validation:MinItems=0
	// +kubebuilder:validation:MaxItems=2
	IPAddresses []string `json:"ipAddresses,omitempty"`
}

// SubnetStatus defines the observed state of Subnet.
type SubnetStatus struct {
	NSXResourcePath string      `json:"nsxResourcePath"`
	IPAddresses     []string    `json:"ipAddresses"`
	Conditions      []Condition `json:"conditions"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Subnet is the Schema for the subnets API.
type Subnet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SubnetSpec   `json:"spec,omitempty"`
	Status SubnetStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// SubnetList contains a list of Subnet.
type SubnetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Subnet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Subnet{}, &SubnetList{})
}
