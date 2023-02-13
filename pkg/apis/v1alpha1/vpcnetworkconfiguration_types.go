/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// +kubebuilder:object:generate=true
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	AccessModePublic  string = "public"
	AccessModePrivate string = "private"
)

// VPCNetworkConfigurationSpec defines the desired state of VPCNetworkConfiguration.
// There is a default VPCNetworkConfiguration that applies to Namespaces
// do not have a VPCNetworkConfiguration assigned. When a field is not set
// in a Namespace's VPCNetworkConfiguration, the Namespace will use the value
// in the default VPCNetworkConfiguration.
type VPCNetworkConfigurationSpec struct {
	// PolicyPath of Tier0 or Tier0 VRF gateway.
	DefaultGatewayPath string `json:"defaultGatewayPath,omitempty"`
	// Edge cluster path on which the networking elements will be created.
	EdgeClusterPath string `json:"edgeClusterPath,omitempty"`
	// NSX-T Project the Namespace associated with.
	NSXTProject string `json:"nsxtProject,omitempty"`
	// NSX-T IPv4 Block paths used to allocate public Subnets.
	// +kubebuilder:validation:MinItems=0
	// +kubebuilder:validation:MaxItems=5
	PublicIPv4Blocks []string `json:"publicIPv4Blocks,omitempty"`
	// Private IPv4 CIDRs used to allocate private Subnets.
	// +kubebuilder:validation:MinItems=0
	// +kubebuilder:validation:MaxItems=5
	PrivateIPv4CIDRs []string `json:"privateIPv4CIDRs,omitempty"`
	// Default size of Subnet based upon estimated workload count.
	// Defaults to 26.
	// +kubebuilder:default=26
	DefaultIPv4SubnetSize int `json:"defaultIPv4SubnetSize,omitempty"`
	// DefaultSubnetAccessMode defines the access mode of the default SubnetSet for PodVM and VM.
	// Must be public or private.
	// +kubebuilder:validation:Enum=public;private
	DefaultSubnetAccessMode string `json:"defaultSubnetAccessMode,omitempty"`
}

// VPCNetworkConfigurationStatus defines the observed state of VPCNetworkConfiguration
type VPCNetworkConfigurationStatus struct {
	// Conditions describes current state of VPCNetworkConfiguration.
	Conditions []Condition `json:"conditions"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// VPCNetworkConfiguration is the Schema for the vpcnetworkconfigurations API.
// +kubebuilder:resource:scope="Cluster"
// +kubebuilder:printcolumn:name="NSXTProject",type=string,JSONPath=`.spec.NSXTProject`,description="NSXTProject the Namespace associated with"
// +kubebuilder:printcolumn:name="PublicIPv4Blocks",type=string,JSONPath=`.spec.PublicIPv4Blocks`,description="PublicIPv4Blocks assigned to the Namespace"
// +kubebuilder:printcolumn:name="PrivateIPv4CIDRs",type=string,JSONPath=`.spec.PrivateIPv4CIDRs`,description="PrivateIPv4CIDRs assigned to the Namespace"
type VPCNetworkConfiguration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VPCNetworkConfigurationSpec   `json:"spec,omitempty"`
	Status VPCNetworkConfigurationStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// VPCNetworkConfigurationList contains a list of VPCNetworkConfiguration.
type VPCNetworkConfigurationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VPCNetworkConfiguration `json:"items"`
}

func init() {
	SchemeBuilder.Register(&VPCNetworkConfiguration{}, &VPCNetworkConfigurationList{})
}
