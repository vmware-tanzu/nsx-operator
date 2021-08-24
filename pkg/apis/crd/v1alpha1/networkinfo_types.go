package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ClusterNetworkInfo describe cluster network information
// +genclient:nonNamespaced
type ClusterNetworkInfo struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Topology NetworkTopology   `json:"topology"`
	Usage    NetworkUsage      `json:"usage"`
	Status   NetworkInfoStatus `json:"status,omitempty"`
}

// NamespaceNetworkInfo describe namespace network information
type NamespaceNetworkInfo struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Topology NetworkTopology   `json:"topology"`
	Usage    NetworkUsage      `json:"usage"`
	Status   NetworkInfoStatus `json:"status,omitempty"`
}

// NetworkTopology defines the network topology
type NetworkTopology struct{
    Gateway string        `json:"gateway"`
	IngressCIDRs []string `json:"ingressCIDRs"`
	EgressCIDRs []string  `json:"egressCIDRs"`
	SubnetCIDRs []string  `json:"subnetCIDRs"`
}

// NetworkUsage defines the usage of network
type NetworkUsage struct {
	IngressCIDRUsage CIDRUsage `json:"ingressCIDRUsage"`
	EgressCIDRUsage  CIDRUsage `json:"egressCIDRUsage"`
	SubnetCIDRUsage  CIDRUsage `json:"subnetCIDRUsage"`
}

// CIDRUsage defines the usage of CIDR
type CIDRUsage struct {
	Allocated int `json:"allocated"`
	Total     int `json:"total"`
}

// NetworkInfoStatus defines the state of NetworkInfo
type NetworkInfoStatus struct {
	Conditions    []NetworkInfoCondition `json:"conditions"`
}

// NetworkInfoConditionType describes the NetworkInfo condition type
type NetworkInfoConditionType string

const (
	// NetworkInfoConditionTypeReady means NetworkInfo is healthy
	NetworkInfoConditionTypeReady NetworkInfoConditionType = "ready"
	// NetworkInfoConditionTypeFailure means NetworkInfo is not healthy
	NetworkInfoConditionTypeFailure NetworkInfoConditionType = "failure"
)

// NetworkInfoCondition defines the condition for the NetworkInfo
type NetworkInfoCondition struct {
	Type    NetworkInfoConditionType `json:"type"`
	Status  corev1.ConditionStatus   `json:"status"`
	Reason  string                   `json:"reason,omitempty"`
	Message string                   `json:"message,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ClusterNetworkInfoList is a list of ClusterNetworkInfo
type ClusterNetworkInfoList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterNetworkInfo `json:"items"`
}

// NamespaceNetworkInfoList is a list of NamespaceNetworkInfo
type NamespaceNetworkInfoList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NamespaceNetworkInfo `json:"items"`
}
