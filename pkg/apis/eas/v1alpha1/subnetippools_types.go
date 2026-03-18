/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */
   package v1alpha1

   import (
	   metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
   )
   
   // PoolUsage represents the IP usage statistics of an IP pool.
   type PoolUsage struct {
	   // Total number of IPs in the pool.
	   TotalIPs int64 `json:"totalIPs,omitempty"`
	   // Number of available IPs.
	   AvailableIPs int64 `json:"availableIPs,omitempty"`
	   // Number of allocated IP allocations.
	   AllocatedIPAllocations int64 `json:"allocatedIPAllocations,omitempty"`
	   // Number of requested IP allocations.
	   RequestedIPAllocations int64 `json:"requestedIPAllocations,omitempty"`
   }
   
   // +genclient
   //+kubebuilder:object:root=true
   //+kubebuilder:storageversion
   
   // SubnetIPPools describes IP pools of a Subnet.
   // The SubnetIPPools name is the same as subnet CR name.
   type SubnetIPPools struct {
	   metav1.TypeMeta   `json:",inline"`
	   metav1.ObjectMeta `json:"metadata,omitempty"`
	   // Type of IP address.
	   // Must be IPv4 or IPv6.
	   // +kubebuilder:validation:Enum=IPv4;IPv6
	   IPAddressType string `json:"ipAddressType,omitempty"`
	   // IP pool usage statistics.
	   PoolUsage *PoolUsage `json:"poolUsage,omitempty"`
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