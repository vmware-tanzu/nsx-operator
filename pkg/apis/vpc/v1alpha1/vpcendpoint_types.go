/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// VPCEndpointSpec defines the desired state of VPCEndpoint
type VPCEndpointSpec struct {
	// ServiceEndpointName is VPC service endpoint name being consumed.
	// +kubebuilder:validation:Required
	ServiceEndpointName string `json:"serviceEndpointName"`

	// IPAllocationName defines the IPAddressAllocation CR name that supplies the IP of VPC endpoint.
	// +kubebuilder:validation:Required
	IPAllocationName string `json:"ipAllocationName"`
}

// VPCEndpointStatus defines the observed state of VPCEndpoint
type VPCEndpointStatus struct {
	// Conditions describe the current state of the VPCEndpoint.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=vpcep,categories=nsx

// VPCEndpoint is the Schema for the vpcendpoints API
type VPCEndpoint struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VPCEndpointSpec   `json:"spec,omitempty"`
	Status VPCEndpointStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// VPCEndpointList contains a list of VPCEndpoint
type VPCEndpointList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VPCEndpoint `json:"items"`
}

func init() {
	SchemeBuilder.Register(&VPCEndpoint{}, &VPCEndpointList{})
}
