package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// VPC describe virtual private cloud.
type VPC struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   VPCSpec   `json:"spec"`
	Status VPCStatus `json:"status,omitempty"`
}

// VPCSpec defines the VPC configuration.
type VPCSpec struct {
	// NetworkConfig is the name of NsxNetworkConfiguration instance
    NetworkConfig string `json:"networkConfig"`
	// SubnetCIDRs is a list of private subnet CIDR
	// This is optional, VPC will use subnetCIDRs from NsxNetworkConfiguration instance if this is not set
	SubnetCIDRs []string `json:"subnetCIDRs"`
}

// VPCStatus defines the realized state of VPC.
type VPCStatus struct {
	// Gateway is the top tier router of this VPC
	Gateway string `json:"gateway"`
	// DefaultSNATIP is the default SNAT IP of this VPC
	DefaultSNATIP string `json:"defaultSNATIP"`
    // SubnetCIDRUsage is the usage of subnet CIDR if specific SubnetCIDRs is set
	SubnetCIDRUsage []CIDRUsage `json:"subnetCIDRUsage"`
	// Conditions defines current state of the VPC
	Conditions []VPCCondition `json:"conditions"`
}

// CIDRUsage defines the usage of CIDR
type CIDRUsage struct {
	Allocated int `json:"allocated"`
	Total     int `json:"total"`
}

// VPCConditionType describes the VPC condition type.
type VPCConditionType string

const (
	// VPCConditionTypeReady means VPC is healthy.
	VPCConditionTypeReady VPCConditionType = "Ready"
)

// VPCCondition defines the condition for the VPC.
type VPCCondition struct {
	// VPCConditionType defines the type of condition.
	Type VPCConditionType `json:"type"`
	// Status shows the status of condition, one of True, False, Unknown.
	Status corev1.ConditionStatus `json:"status"`
	// Reason shows a brief reason of condition.
	Reason string `json:"reason,omitempty"`
	// Message shows a human readable message about the condition.
	Message string `json:"message,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// VPCList is a list of VPC.
type VPCList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VPC `json:"items"`
}
