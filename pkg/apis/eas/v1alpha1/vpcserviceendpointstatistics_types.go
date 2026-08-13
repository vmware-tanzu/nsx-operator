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

// VPCServiceEndpointStatistics describes the statistics of a VPC Service Endpoint.
type VPCServiceEndpointStatistics struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Rx Statistics `json:"rx,omitempty"`
	Tx Statistics `json:"tx,omitempty"`
}

// +kubebuilder:object:root=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// VPCServiceEndpointStatisticsList contains a list of VPCServiceEndpointStatistics.
type VPCServiceEndpointStatisticsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VPCServiceEndpointStatistics `json:"items"`
}

func init() {
	SchemeBuilder.Register(&VPCServiceEndpointStatistics{}, &VPCServiceEndpointStatisticsList{})
}
