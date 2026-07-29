// Copyright (c) 2026 Broadcom. All Rights Reserved.
// Broadcom Confidential. The term "Broadcom" refers to Broadcom Inc.
// and/or its subsidiaries.

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ServiceEndpointStatistics describes the statistics of a VPC Service Endpoint.
type ServiceEndpointStatistics struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	RX EndpointStatistics `json:"rx,omitempty"`
	TX EndpointStatistics `json:"tx,omitempty"`
}

// +kubebuilder:object:root=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ServiceEndpointStatisticsList contains a list of ServiceEndpointStatistics.
type ServiceEndpointStatisticsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceEndpointStatistics `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ServiceEndpointStatistics{}, &ServiceEndpointStatisticsList{})
}
