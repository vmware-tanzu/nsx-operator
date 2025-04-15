/* Copyright © 2022-2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AccessMode string
type DHCPConfigMode string

const (
	AccessModePublic          string = "Public"
	AccessModePrivate         string = "Private"
	AccessModeProject         string = "PrivateTGW"
	DHCPConfigModeDeactivated string = "DHCPDeactivated"
	DHCPConfigModeServer      string = "DHCPServer"
	DHCPConfigModeRelay       string = "DHCPRelay"
)

// SubnetSpec defines the desired state of Subnet.
// +kubebuilder:validation:XValidation:rule="has(oldSelf.subnetDHCPConfig)==has(self.subnetDHCPConfig) || (has(oldSelf.subnetDHCPConfig) && !has(self.subnetDHCPConfig) && (!has(oldSelf.subnetDHCPConfig.mode) || oldSelf.subnetDHCPConfig.mode=='DHCPDeactivated')) || (!has(oldSelf.subnetDHCPConfig) && has(self.subnetDHCPConfig) && (!has(self.subnetDHCPConfig.mode) || self.subnetDHCPConfig.mode=='DHCPDeactivated'))", message="subnetDHCPConfig mode can only switch between DHCPServer and DHCPRelay"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.ipv4SubnetSize) || has(self.ipv4SubnetSize)", message="ipv4SubnetSize is required once set"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.accessMode) || has(self.accessMode)", message="accessMode is required once set"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.ipAddresses) || has(self.ipAddresses)", message="ipAddresses is required once set"
// +kubebuilder:validation:XValidation:rule="!(has(self.advancedConfig) && self.advancedConfig.staticIPAllocation.enable==true && (self.subnetDHCPConfig.mode=='DHCPServer' || self.subnetDHCPConfig.mode=='DHCPRely'))", message="Static IP pool allocation and subnet DHCP configuration cannot both be enabled simultaneously in subnet"
type SubnetSpec struct {
	// Size of Subnet based upon estimated workload count.
	// +kubebuilder:validation:Maximum:=65536
	// +kubebuilder:validation:Minimum:=16
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	IPv4SubnetSize int `json:"ipv4SubnetSize,omitempty"`
	// Access mode of Subnet, accessible only from within VPC or from outside VPC.
	// +kubebuilder:validation:Enum=Private;Public;PrivateTGW
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	AccessMode AccessMode `json:"accessMode,omitempty"`
	// Subnet CIDRS.
	// +kubebuilder:validation:MinItems=0
	// +kubebuilder:validation:MaxItems=2
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	IPAddresses []string `json:"ipAddresses,omitempty"`

	// DHCP mode of a Subnet can only switch between DHCPServer or DHCPRelay.
	// If subnetDHCPConfig is not set, the DHCP mode is DHCPDeactivated by default.
	// In order to enforce this rule, three XValidation rules are defined.
	// The rule on SubnetSpec prevents the condition that subnetDHCPConfig is not set in
	// old or current SubnetSpec while the current or old SubnetSpec specifies a Mode
	// other than DHCPDeactivated.
	// The rule on SubnetDHCPConfig prevents the condition that Mode is not set in old
	// or current SubnetDHCPConfig while the current or old one specifies a Mode other
	// than DHCPDeactivated.
	// The rule on SubnetDHCPConfig.Mode prevents the Mode changing between DHCPDeactivated
	// and DHCPServer or DHCPRelay.

	// DHCP configuration for Subnet.
	SubnetDHCPConfig SubnetDHCPConfig `json:"subnetDHCPConfig,omitempty"`

	// VPC Subnet advanced configuration.
	AdvancedConfig AdvancedConfig `json:"advancedConfig,omitempty"`
}

// SubnetStatus defines the observed state of Subnet.
type SubnetStatus struct {
	// Network address of the Subnet.
	NetworkAddresses []string `json:"networkAddresses,omitempty"`
	// Gateway address of the Subnet.
	GatewayAddresses []string `json:"gatewayAddresses,omitempty"`
	// DHCP server IP address.
	DHCPServerAddresses []string    `json:"DHCPServerAddresses,omitempty"`
	Conditions          []Condition `json:"conditions,omitempty"`
}

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion

// Subnet is the Schema for the subnets API.
// +kubebuilder:printcolumn:name="AccessMode",type=string,JSONPath=`.spec.accessMode`,description="Access mode of Subnet"
// +kubebuilder:printcolumn:name="IPv4SubnetSize",type=string,JSONPath=`.spec.ipv4SubnetSize`,description="Size of Subnet"
// +kubebuilder:printcolumn:name="NetworkAddresses",type=string,JSONPath=`.status.networkAddresses[*]`,description="CIDRs for the Subnet"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.spec) || has(self.spec)", message="spec is required once set"
type Subnet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SubnetSpec   `json:"spec,omitempty"`
	Status SubnetStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SubnetList contains a list of Subnet.
type SubnetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Subnet `json:"items"`
}

// SubnetDHCPConfig is DHCP configuration for Subnet.
// +kubebuilder:validation:XValidation:rule="has(oldSelf.mode)==has(self.mode) || (has(oldSelf.mode) && !has(self.mode)  && oldSelf.mode=='DHCPDeactivated') || (!has(oldSelf.mode) && has(self.mode) && self.mode=='DHCPDeactivated')", message="subnetDHCPConfig mode can only switch between DHCPServer and DHCPRelay"
type SubnetDHCPConfig struct {
	// DHCP Mode. DHCPDeactivated will be used if it is not defined.
	// It cannot switch from DHCPDeactivated to DHCPServer or DHCPRelay.
	// +kubebuilder:validation:Enum=DHCPServer;DHCPRelay;DHCPDeactivated
	// +kubebuilder:validation:XValidation:rule="oldSelf!='DHCPDeactivated' && self!='DHCPDeactivated' || oldSelf==self", message="subnetDHCPConfig mode can only switch between DHCPServer and DHCPRelay"
	Mode DHCPConfigMode `json:"mode,omitempty"`
}

// AdvancedConfig is Subnet advanced configuration.
type AdvancedConfig struct {
	// StaticIPAllocation configuration for subnet ports with VIF attachment.
	// If this field is not set, the static IP pool allocation will be automatically
	// enabled when the DHCP mode is DHCPDeactivated or not set and disabled when the DHCP
	// mode is DHCPServer or DHCPRelay.
	// The static IP pool allocation and DHCP mode cannot both be enabled simultaneously
	// in subnet.
	StaticIPAllocation StaticIPAllocation `json:"staticIPAllocation,omitempty"`
}

// Static IP allocation for VPC Subnet ports with VIF attachment.
type StaticIPAllocation struct {
	// Activate or Deactivate static ip allocation for VPC Subnet ports with VIF attachment.
	Enabled *bool `json:"enabled,omitempty"`
}

func init() {
	SchemeBuilder.Register(&Subnet{}, &SubnetList{})
}
