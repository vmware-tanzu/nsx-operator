package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// NSXNetworkConfiguration describe a NSX Network Configuration resource
// +k8s:openapi-gen=true
type NSXNetworkConfiguration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   NSXNetworkConfigurationSpec   `json:"spec"`
	Status NSXNetworkConfigurationStatus `json:"status,omitempty"`
}

// ModeType defines the Mode options supported
type ModeType string

const (
	// TypeNat defines the Mode as NAT
	TypeNat ModeType = "NAT"
	// TypeRouted defines the Mode as ROUTED
	TypeRouted ModeType = "ROUTED"
)

// SizeType defines load balancer size supported
type SizeType string

const (
	// LbSizeSmall defines the size as SMALL
	LbSizeSmall SizeType = "SMALL"
	// LbSizeMedium defines the size as MEDIUM
	LbSizeMedium SizeType = "MEDIUM"
	// LbSizeLarge defines the size as LARGE
	LbSizeLarge SizeType = "LARGE"
)

// NSXNetworkConfigurationSpec defines the desired state of NSXNetworkConfiguration
type NSXNetworkConfigurationSpec  struct {
    IngressCIDRs []string `json:"ingressCIDRs,omitempty"`
    EgressCIDRs  []string `json:"egressCIDRs,omitempty"`
    AppliedToNamespaces []string `json:"appliedToNamespaces,omitempty"`
    Mode ModeType `json:"mode,omitempty"`
    SubnetCIDRs []string `json:"subnetCIDRs,omitempty"`
    Tier0Gateway string `json:"tier0Gateway,omitempty"`
    LoadBalancerSize SizeType `json:"loadbalancerSize,omitempty"`
    SubnetPrefixLength int32 `json:"subnetPrefixLength,omitempty"`
}

// NSXNetworkConfigurationStatus defines the observed state of NSXNetworkConfiguration
type NSXNetworkConfigurationStatus struct {
	Conditions       []NSXNetworkConfigurationCondition     `json:"conditions"`
}

// NSXNetworkConfigurationConditionType describes the NSXNetworkConfiguration condition type
type NSXNetworkConfigurationConditionType string

const (
	// NSXNetworkConfigurationConditionTypeReady means NSXNetworkConfiguration is healthy
	NSXNetworkConfigurationConditionTypeReady NSXNetworkConfigurationConditionType = "ready"
	// NSXNetworkConfigurationConditionTypeFailure means
	// NSXNetworkConfiguration is not healthy
	NSXNetworkConfigurationConditionTypeFailure NSXNetworkConfigurationConditionType = "failure"
)

// NSXNetworkConfigurationCondition defines the condition for the NSXNetworkConfiguration
type NSXNetworkConfigurationCondition struct {
	Type    NSXNetworkConfigurationConditionType  `json:"type"`
	Status  corev1.ConditionStatus                `json:"status"`
	Reason  string                                `json:"reason,omitempty"`
	Message string                                `json:"message,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// NSXNetworkConfigurationList is a list of NSXNetworkConfiguration
type NSXNetworkConfigurationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NSXNetworkConfiguration `json:"items"`
}
