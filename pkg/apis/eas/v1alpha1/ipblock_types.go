// Copyright (c) 2026 Broadcom. All Rights Reserved.
// Broadcom Confidential. The term "Broadcom" refers to Broadcom Inc.
// and/or its subsidiaries.

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +genclient:nonNamespaced
// +kubebuilder:resource:scope=Namespace

type IPAddressVisibility string

const (
	External IPAddressVisibility = "External"
	Private  IPAddressVisibility = "Private"
	// A visibility type of IPBlock that's only used for IPAddressAllocation.
	PrivateTGW IPAddressVisibility = "PrivateTGW"
)

// A set of IPv4 or IPv6 addresses defined by a start and end address.
type IPPoolRange struct {
	// The start IP Address of the IP range. format: IP.
	Start string `json:"start" protobuf:"bytes,1,opt,name=start"`
	// The end IP Address of the IP range. format: IP.
	End string `json:"end" protobuf:"bytes,2,opt,name=end"`
}