/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ModeType defines the routing mode supported.
type ModeType string

const (
	// TypeNat defines the Mode as NAT.
	TypeNat ModeType = "NAT"
	// TypeRouted defines the Mode as ROUTED.
	TypeRouted ModeType = "ROUTED"
)

// SizeType defines load balancer size supported.
type SizeType string

const (
	// LbSizeSmall defines the size as SMALL.
	LbSizeSmall SizeType = "SMALL"
	// LbSizeMedium defines the size as MEDIUM.
	LbSizeMedium SizeType = "MEDIUM"
	// LbSizeLarge defines the size as LARGE.
	LbSizeLarge SizeType = "LARGE"
)

// NSXNetworkConfigurationSpec defines the desired state of NSXNetworkConfiguration.
type NSXNetworkConfigurationSpec struct {
	// IngressCIDRs defines CIDRs used for ingress traffic.
	IngressCIDRs []string `json:"ingressCIDRs,omitempty"`
	// EgressCIDRs defines CIDRs used for egress SNAT.
	EgressCIDRs []string `json:"egressCIDRs,omitempty"`
	// AppliedToNamespaces lists the namespaces where this network configuration applies.
	AppliedToNamespaces []string `json:"appliedToNamespaces,omitempty"`
	// Mode specifies the network mode: NAT or ROUTED.
	Mode ModeType `json:"mode,omitempty"`
	// SubnetCIDRs defines the CIDRs allocated for subnets.
	SubnetCIDRs []string `json:"subnetCIDRs,omitempty"`
	// Tier0Gateway specifies the path or name of the Tier0 gateway.
	Tier0Gateway string `json:"tier0Gateway,omitempty"`
	// LoadBalancerSize specifies the default load balancer size.
	LoadBalancerSize SizeType `json:"loadbalancerSize,omitempty"`
	// SubnetPrefixLength specifies the prefix length for namespace subnets.
	SubnetPrefixLength int32 `json:"subnetPrefixLength,omitempty"`
	// SubnetQuota specifies quota for subnets.
	SubnetQuota *int32 `json:"subnetQuota,omitempty"`
	// SNATIPQuota specifies quota for SNAT IPs.
	SNATIPQuota *int32 `json:"snatIPQuota,omitempty"`
	// LBVIPQuota specifies quota for LoadBalancer VIPs.
	LBVIPQuota *int32 `json:"lbVIPQuota,omitempty"`
	// L7RuleQuota specifies quota for L7 rules.
	L7RuleQuota *int32 `json:"l7ruleQuota,omitempty"`
}

// NSXNetworkConfigurationConditionType describes the condition type.
type NSXNetworkConfigurationConditionType string

const (
	// NSXNetworkConfigurationConditionTypeReady means NSXNetworkConfiguration is healthy.
	NSXNetworkConfigurationConditionTypeReady NSXNetworkConfigurationConditionType = "ready"
	// NSXNetworkConfigurationConditionTypeFailure means NSXNetworkConfiguration is unhealthy.
	NSXNetworkConfigurationConditionTypeFailure NSXNetworkConfigurationConditionType = "failure"
)

// NSXNetworkConfigurationCondition defines the condition for the NSXNetworkConfiguration.
type NSXNetworkConfigurationCondition struct {
	Type    NSXNetworkConfigurationConditionType `json:"type"`
	Status  corev1.ConditionStatus               `json:"status"`
	Reason  string                               `json:"reason,omitempty"`
	Message string                               `json:"message,omitempty"`
}

// NSXNetworkConfigurationStatus defines the observed state of NSXNetworkConfiguration.
type NSXNetworkConfigurationStatus struct {
	Conditions []NSXNetworkConfigurationCondition `json:"conditions,omitempty"`
}

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=nsxnetworkconfig
// +kubebuilder:storageversion

// NSXNetworkConfiguration is the Schema for the nsxnetworkconfigurations API.
type NSXNetworkConfiguration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NSXNetworkConfigurationSpec   `json:"spec,omitempty"`
	Status NSXNetworkConfigurationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NSXNetworkConfigurationList contains a list of NSXNetworkConfiguration.
type NSXNetworkConfigurationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NSXNetworkConfiguration `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NSXNetworkConfiguration{}, &NSXNetworkConfigurationList{})
}
