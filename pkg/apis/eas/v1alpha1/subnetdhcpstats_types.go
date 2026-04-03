// Copyright (c) 2026 Broadcom. All Rights Reserved.
// Broadcom Confidential. The term "Broadcom" refers to Broadcom Inc.
// and/or its subsidiaries.

   package v1alpha1

   import (
	   metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
   )
   
   // DhcpIpPoolUsage represents DHCP IP pool usage statistics.
   type DhcpIpPoolUsage struct {
	   // Allocated number (reference only).
	   AllocatedNumber int64 `json:"allocatedNumber,omitempty"`
	   // Allocated percentage (reference only).
	   AllocatedPercentage int64 `json:"allocatedPercentage,omitempty"`
	   // Total number of IP addresses consumed by DHCP clients.
	   ConsumedNumber int64 `json:"consumedNumber,omitempty"`
	   // UUID of DHCP IP pool.
	   DhcpIpPoolId string `json:"dhcpIpPoolId,omitempty"`
	   // Pool size.
	   PoolSize int64 `json:"poolSize,omitempty"`
   }
   
   // SubnetDHCPServerConfigStatsSpec represents the DHCP server statistics for a subnet.
   type SubnetDHCPServerConfigStatsSpec struct {
	   // Total number of DHCP ACK packets.
	   Acks int64 `json:"acks,omitempty"`
	   // Total number of DHCP DECLINE packets.
	   Declines int64 `json:"declines,omitempty"`
	   // DHCP server UUID.
	   DhcpServerId string `json:"dhcpServerId,omitempty"`
	   // Total number of DHCP DISCOVER packets.
	   Discovers int64 `json:"discovers,omitempty"`
	   // Total number of DHCP errors.
	   Errors int64 `json:"errors,omitempty"`
	   // Total number of DHCP INFORM packets.
	   Informs int64 `json:"informs,omitempty"`
	   // DHCP IP pool usage statistics.
	   IpPoolStats []DhcpIpPoolUsage `json:"ipPoolStats,omitempty"`
	   // Total number of DHCP NACK packets.
	   Nacks int64 `json:"nacks,omitempty"`
	   // Total number of DHCP OFFER packets.
	   Offers int64 `json:"offers,omitempty"`
	   // Total number of DHCP RELEASE packets.
	   Releases int64 `json:"releases,omitempty"`
   }
   
   // +genclient
   //+kubebuilder:object:root=true
   //+kubebuilder:storageversion
   
   // SubnetDHCPServerConfigStats is the Schema for the subnet DHCP server config stats API.
   // +kubebuilder:printcolumn:name="DHCPServerID",type=string,JSONPath=`.spec.dhcpServerId`,description="DHCP server UUID"
   type SubnetDHCPServerConfigStats struct {
	   metav1.TypeMeta   `json:",inline"`
	   metav1.ObjectMeta `json:"metadata,omitempty"`
   
	   Spec SubnetDHCPServerConfigStatsSpec `json:"spec"`
   }
   
   //+kubebuilder:object:root=true
   
   // SubnetDHCPServerConfigStatsList contains a list of SubnetDHCPServerConfigStats.
   type SubnetDHCPServerConfigStatsList struct {
	   metav1.TypeMeta `json:",inline"`
	   metav1.ListMeta `json:"metadata,omitempty"`
	   Items           []SubnetDHCPServerConfigStats `json:"items"`
   }
   
   func init() {
	   SchemeBuilder.Register(&SubnetDHCPServerConfigStats{}, &SubnetDHCPServerConfigStatsList{})
   }