/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PoolUsage represents the IP usage statistics of an IP pool.
type PoolUsage struct {
	// Total number of IPs in the pool.
	TotalIps int64 `json:"totalIps,omitempty"`
	// Number of available IPs.
	AvailableIps int64 `json:"availableIps,omitempty"`
	// Number of allocated IP allocations.
	AllocatedIpAllocations int64 `json:"allocatedIpAllocations,omitempty"`
	// Number of requested IP allocations.
	RequestedIpAllocations int64 `json:"requestedIpAllocations,omitempty"`
}

// SubnetIPPoolSpec represents an IP pool associated with a subnet.
type SubnetIPPoolSpec struct {
	// Display name of the IP pool.
	DisplayName string `json:"displayName,omitempty"`
	// ID of the IP pool.
	ID string `json:"id,omitempty"`
	// Path of the IP pool.
	Path string `json:"path,omitempty"`
	// IP pool usage statistics.
	PoolUsage *PoolUsage `json:"poolUsage,omitempty"`
	// IP pool subnets (CIDR ranges).
	PoolSubnets []SubnetIPPoolSubnet `json:"poolSubnets,omitempty"`
}

// SubnetIPPoolSubnet represents a subnet within an IP pool.
type SubnetIPPoolSubnet struct {
	// CIDR of the subnet.
	Cidr string `json:"cidr,omitempty"`
	// Gateway IP.
	GatewayIp string `json:"gatewayIp,omitempty"`
	// IP ranges in the subnet.
	AllocationRanges []IpPoolRange `json:"allocationRanges,omitempty"`
}

// +genclient
//+kubebuilder:object:root=true
//+kubebuilder:storageversion

// SubnetIPPools is the Schema for the subnet IP pools API.
// +kubebuilder:printcolumn:name="DisplayName",type=string,JSONPath=`.spec.displayName`,description="Display name of the IP pool"
// +kubebuilder:printcolumn:name="TotalIPs",type=integer,JSONPath=`.spec.poolUsage.totalIps`,description="Total IPs in pool"
// +kubebuilder:printcolumn:name="AvailableIPs",type=integer,JSONPath=`.spec.poolUsage.availableIps`,description="Available IPs in pool"
type SubnetIPPools struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec SubnetIPPoolSpec `json:"spec"`
}

//+kubebuilder:object:root=true

// SubnetIPPoolsList contains a list of SubnetIPPools.
type SubnetIPPoolsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SubnetIPPools `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SubnetIPPools{}, &SubnetIPPoolsList{})
}
