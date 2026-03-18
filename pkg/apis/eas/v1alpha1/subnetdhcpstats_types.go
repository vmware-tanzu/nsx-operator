// Copyright (c) 2026 Broadcom. All Rights Reserved.
// Broadcom Confidential. The term "Broadcom" refers to Broadcom Inc.
// and/or its subsidiaries.
   package v1alpha1

   import (
	   metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
   )
   
   // DHCPIPPoolUsage represents DHCP IP pool usage statistics.
   type DHCPIPPoolUsage struct {
	   // Allocated number (reference only).
	   AllocatedNumber int64 `json:"allocatedNumber,omitempty"`
	   // Allocated percentage (reference only).
	   AllocatedPercentage int64 `json:"allocatedPercentage,omitempty"`
	   // Total number of IP addresses consumed by DHCP clients.
	   ConsumedNumber int64 `json:"consumedNumber,omitempty"`
	   // Pool size.
	   PoolSize int64 `json:"poolSize,omitempty"`
   }
   
   // +genclient
   //+kubebuilder:object:root=true
   //+kubebuilder:storageversion
   
<<<<<<< HEAD
   // SubnetDHCPServerStats is the Schema for the Subnet DHCP server config stats API.
   // The SubnetDHCPServerStats name is the same as subnet CR name which is used as input parameter for query.
   type SubnetDHCPServerStats struct {
	   metav1.TypeMeta   `json:",inline"`
	   metav1.ObjectMeta `json:"metadata,omitempty"`
	   // DHCP IP pool usage statistics.
	   IPPoolStats []DHCPIPPoolUsage `json:"ipPoolStats,omitempty"`
=======
   // SubnetDHCPServerConfigStats is the Schema for the Subnet DHCP server config stats API.
   type SubnetDHCPServerConfigStats struct {
	   metav1.TypeMeta   `json:",inline"`
	   metav1.ObjectMeta `json:"metadata,omitempty"`

	   // Total number of DHCP ACK packets.
	   Acks int64 `json:"acks,omitempty"`
	   // Total number of DHCP DECLINE packets.
	   Declines int64 `json:"declines,omitempty"`
	   // Total number of DHCP DISCOVER packets.
	   Discovers int64 `json:"discovers,omitempty"`
	   // Total number of DHCP errors.
	   Errors int64 `json:"errors,omitempty"`
	   // Total number of DHCP INFORM packets.
	   Informs int64 `json:"informs,omitempty"`
	   // DHCP IP pool usage statistics.
	   IPPoolStats []DHCPIPPoolUsage `json:"ipPoolStats,omitempty"`
	   // Total number of DHCP NACK packets.
	   Nacks int64 `json:"nacks,omitempty"`
	   // Total number of DHCP OFFER packets.
	   Offers int64 `json:"offers,omitempty"`
	   // Total number of DHCP RELEASE packets.
	   Releases int64 `json:"releases,omitempty"`
>>>>>>> 4a28f7aa (Add API definition for EAS support)
   }
   
   //+kubebuilder:object:root=true
   
<<<<<<< HEAD
   // SubnetDHCPServerStatsList contains a list of SubnetDHCPServerStats.
   type SubnetDHCPServerStatsList struct {
	   metav1.TypeMeta `json:",inline"`
	   metav1.ListMeta `json:"metadata,omitempty"`
	   Items           []SubnetDHCPServerStats `json:"items"`
   }
   
   func init() {
	   SchemeBuilder.Register(&SubnetDHCPServerStats{}, &SubnetDHCPServerStatsList{})
=======
   // SubnetDHCPServerConfigStatsList contains a list of SubnetDHCPServerConfigStats.
   type SubnetDHCPServerConfigStatsList struct {
	   metav1.TypeMeta `json:",inline"`
	   metav1.ListMeta `json:"metadata,omitempty"`
	   Items           []SubnetDHCPServerConfigStats `json:"items"`
   }
   
   func init() {
	   SchemeBuilder.Register(&SubnetDHCPServerConfigStats{}, &SubnetDHCPServerConfigStatsList{})
>>>>>>> 4a28f7aa (Add API definition for EAS support)
   }