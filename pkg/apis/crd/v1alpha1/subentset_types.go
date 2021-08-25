package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SubnetSet describe a set of subnet.
type SubnetSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   SubnetSetSpec   `json:"spec"`
	Status SubnetSetStatus `json:"status,omitempty"`
}

// SubnetSetSpec defines the configuration of SubnetSet.
type SubnetSetSpec struct {
	// VPC defines where the SubnetSet belongs to
	VPC string `json:"vpc"`
	// SubnetCIDRs is a list of CIDR for this SubnetSet
    SubnetCIDRs []string `json:"subnetCIDRs"`
	// PrefixLength is the size of CIDR allocated for subnet
	PrefixLength int `json:"prefixLength"`
	// EnableDHCP is a flag to enable DHCP for subnet
	EnableDHCP bool `json:"enableDHCP"`
	// Private is a flag to identify subnet is private or public
    Private bool `json:"private"`
}

// SubnetSetStatus defines the realized state of SubnetSet.
type SubnetSetStatus struct {
	// Subnets is the set of realized subnets.
	Subnets []Subnet `json:"subnets"`
	// SubnetCIDRUsage is the usage of subnet CIDRs
	SubnetCIDRUsage CIDRUsage `json:"subnetCIDRUsage"`
	// Conditions defines current state of the SubnetSet.
	Conditions []SubnetSetCondition `json:"conditions"`
}

// CIDRUsage defines the usage of CIDR
type CIDRUsage struct {
	Allocated int `json:"allocated"`
	Total     int `json:"total"`
}

// SubnetSetConditionType describes the SubnetSet condition type.
type SubnetSetConditionType string

const (
	// SubnetSetConditionTypeReady means SubnetSet is healthy.
	SubnetSetConditionTypeReady SubnetSetConditionType = "Ready"
)

// SubnetSetCondition defines the condition for the SubnetSet.
type SubnetSetCondition struct {
	// SubnetSetConditionType defines the type of condition.
	Type SubnetSetConditionType `json:"type"`
	// Status shows the status of condition, one of True, False, Unknown.
	Status corev1.ConditionStatus `json:"status"`
	// Reason shows a brief reason of condition.
	Reason string `json:"reason,omitempty"`
	// Message shows a human readable message about the condition.
	Message string `json:"message,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SubnetSetList is a list of SubnetSet.
type SubnetSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SubnetSet `json:"items"`
}
