// Copyright (c) 2026 Broadcom. All Rights Reserved.
// Broadcom Confidential. The term "Broadcom" refers to Broadcom Inc.
// and/or its subsidiaries.

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Statistics contains the traffic statistics for a VPC service endpoint or VPC endpoint.
type Statistics struct {
	DroppedPackets int64 `json:"droppedPackets,omitempty"`
	TotalBytes     int64 `json:"totalBytes,omitempty"`
	TotalPackets   int64 `json:"totalPackets,omitempty"`
}

// VPCEndpointStatistics represents the statistics of a VPC Endpoint.
// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type VPCEndpointStatistics struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Rx Statistics `json:"rx,omitempty"`
	Tx Statistics `json:"tx,omitempty"`
}

// +kubebuilder:object:root=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// VPCEndpointStatisticsList contains a list of VPCEndpointStatistics.
type VPCEndpointStatisticsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VPCEndpointStatistics `json:"items"`
}

func init() {
	SchemeBuilder.Register(&VPCEndpointStatistics{}, &VPCEndpointStatisticsList{})
}
