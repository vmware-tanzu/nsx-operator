/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type StaticRouteStatusCondition string

const (
	StaticRouteReady    StaticRouteStatusCondition = "Ready"
	StaticRouteNotReady StaticRouteStatusCondition = "NotReady"
)

// StaticRouteCondition defines condition of StaticRoute.
type StaticRouteCondition struct {
	// Type defines condition type.
	Type StaticRouteStatusCondition `json:"type"`
	// Status defines status of condition type, True or False.
	Status corev1.ConditionStatus `json:"status"`
	// Reason shows a brief reason of condition.
	Reason string `json:"reason,omitempty"`
	// Message shows a human readable message about condition.
	Message string `json:"message,omitempty"`
}

// StaticRouteSpec defines static routes configuration on Tier-0/Tier-1 or on VPC
type StaticRouteSpec struct {
	// Specify network address in CIDR format
	Network string `json:"network"`
	// Specify next hop routes for network
	// +kubebuilder:validation:MinItems=1
	NextHop []StaticRouteNextHop `json:"next_hops"`
}

// StaticRouteNextHop defines next hop configuration for network
type StaticRouteNextHop struct {
	// Next hop gateway IP address
	IpAddress string `json:"ip-address"`
	// Cost associated with next hop route
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=255
	AdministrativeDistance int `json:"administrative_distance"`
}

// StaticRouteStatus defines the observed state of StaticRoute
type StaticRouteStatus struct {
	Conditions   []StaticRouteCondition `json:"conditions"`
	StaticRoutes []StaticRoute          `json:"staticroute"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// StaticRoute is the Schema for the staticroutes API
type StaticRoute struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   StaticRouteSpec   `json:"spec,omitempty"`
	Status StaticRouteStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// StaticRouteList contains a list of StaticRoute
type StaticRouteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []StaticRoute `json:"items"`
}

func init() {
	SchemeBuilder.Register(&StaticRoute{}, &StaticRouteList{})
}
