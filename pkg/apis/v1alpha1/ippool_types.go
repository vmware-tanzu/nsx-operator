/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// IPPool is the Schema for the ippools API
type IPPool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   IPPoolSpec   `json:"spec"`
	Status IPPoolStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// IPPoolList contains a list of IPPool
type IPPoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IPPool `json:"items"`
}

// IPPoolSpec defines the desired state of IPPool.
type IPPoolSpec struct {
	// Type defines the type of this IPPool, public or private.
	// +kubebuilder:validation:Enum=public;private
	// +kubebuilder:default=public
	Type string `json:"type"`
	// Subnets defines set of subnets need to be allocated.
	// +optional
	Subnets []SubnetRequest `json:"subnets"`
}

// IPPoolStatus defines the observed state of IPPool.
type IPPoolStatus struct {
	// Subnets defines subnets allocation result.
	Subnets []SubnetResult `json:"subnets"`
	// Conditions defines current state of the IPPool.
	Conditions []IPPoolCondition `json:"conditions"`
}

// SubnetRequest defines the subnet allocation request.
type SubnetRequest struct {
	// PrefixLength defines prefix length for this subnet.
	// +optional
	PrefixLength int `json:"prefixLength,omitempty"`

	// IPFamily defines the IP family type for this subnet, could be IPv4 or IPv6.
	// This is optional, the default is IPv4.
	// +kubebuilder:validation:Enum=IPv4;IPv6
	// +kubebuilder:default=IPv4
	IPFamily string `json:"ipFamily,omitempty"`

	// Name defines the name of this subnet.
	Name string `json:"name"`
}

// SubnetResult defines the subnet allocation result.
type SubnetResult struct {
	// CIDR defines the allocated CIDR.
	CIDR string `json:"cidr"`

	// Name defines the name of this subnet.
	Name string `json:"name"`
}

// IPPoolConditionType describes the IPPool condition type.
type IPPoolConditionType string

// IPPoolCondition defines the condition for the IPPool.
type IPPoolCondition struct {
	// IPPoolConditionType defines the type of condition.
	Type IPPoolConditionType `json:"type"`
	// Status shows the status of condition, one of True, False, Unknown.
	Status corev1.ConditionStatus `json:"status"`
	// Reason shows a brief reason of condition
	Reason string `json:"reason,omitempty"`
	// Message shows a human-readable message about the condition
	Message string `json:"message,omitempty"`
}

func init() {
	SchemeBuilder.Register(&IPPool{}, &IPPoolList{})
}
