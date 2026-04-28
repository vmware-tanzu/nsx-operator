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
   
   // SubnetIPPools is the Schema for the subnet IP pools API.
   // The SubnetIPPools name is the same as subnet CR name which is used as input parameter for query.
   type SubnetIPPools struct {
	   metav1.TypeMeta   `json:",inline"`
	   metav1.ObjectMeta `json:"metadata,omitempty"`
	   // Check if ip pool overlap with other IP pools
	   CheckOverlapWithExistingPools bool `json:"checkOverlapWithExistingPools,omitempty"`
	   // IP address type
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