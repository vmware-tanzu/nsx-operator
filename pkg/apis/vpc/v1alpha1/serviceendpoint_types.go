/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ServiceEndpointSpec defines the desired state of ServiceEndpoint
type ServiceEndpointSpec struct {
	// ServiceEndpointIP is the IP address of the VPC service endpoint.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^([0-9]{1,3}\.){3}[0-9]{1,3}$`
	ServiceEndpointIP string `json:"serviceEndpointIP"`
}

// ServiceEndpointStatus defines the observed state of ServiceEndpoint
type ServiceEndpointStatus struct {
	// Conditions describe the current state of the ServiceEndpoint.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=sep,categories=nsx

// ServiceEndpoint is the Schema for the serviceendpoints API
type ServiceEndpoint struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ServiceEndpointSpec   `json:"spec,omitempty"`
	Status ServiceEndpointStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ServiceEndpointList contains a list of ServiceEndpoint
type ServiceEndpointList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceEndpoint `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ServiceEndpoint{}, &ServiceEndpointList{})
}
