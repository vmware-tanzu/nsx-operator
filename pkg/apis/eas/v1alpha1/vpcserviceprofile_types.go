// Copyright (c) 2026 Broadcom. All Rights Reserved.
// Broadcom Confidential. The term "Broadcom" refers to Broadcom Inc.
// and/or its subsidiaries.

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +genclient
// +kubebuilder:resource:scope=Namespaced

type VPCServiceProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Flag to indicate that the VPC Service Profile is a default profile.
	// If true, then this VPC Service Profile is the default system created profile.
	IsDefault bool `json:"isDefault"`
	// Common DHCP config for VPC Subnet.
	DHCPConfig VPCProfileDHCPConfig `json:"dhcpConfig,omitempty"`
	// An array of service subnet CIDRs. CIDRs should be in ip/prefix_length format.
	ServiceSubnetCIDRs []string `json:"serviceSubnetCIDRs,omitempty"`
	// DNS configuration
	DNSConfig *VPCProfileDNSConfig `json:"dnsConfig,omitempty"`
	// Common DHCPv6 config for VPC Subnets.
	// +optional
	DHCPv6Config *VPCProfileDHCPv6Config `json:"dhcpv6Config,omitempty"`
	// The description of the VPCServiceProfile.
	// +optional
	Description string `json:"description,omitempty"`
}

type VPCProfileDHCPv6Config struct {
	// DHCPv6 relay configuration
	// +optional
	DHCPv6RelayConfig *VPCDHCPRelayConfig `json:"dhcpv6RelayConfig,omitempty"`
	// DHCPv6 server configuration
	// +optional
	DHCPv6ServerConfig *VPCDHCPv6ServerConfig `json:"dhcpv6ServerConfig,omitempty"`
}

type VPCDHCPv6ServerConfig struct {
	// VPC DHCPv6 advanced configuration
	// +optional
	AdvancedConfig *VPCDHCPAdvancedConfig `json:"advancedConfig,omitempty"`
	// DNS client configuration includes the DNS servers that DHCPv6 clients use to resolve
	// domain names into IPv6 addresses.
	// +optional
	DNSClientConfig *DNSClientConfig `json:"dnsClientConfig,omitempty"`
	// DHCPv6 lease time in seconds. Value range is [60, 4294967295], default value is 86400.
	// +kubebuilder:default=86400
	// +kubebuilder:validation:Minimum=60
	// +kubebuilder:validation:Maximum=4294967295
	// +optional
	LeaseTime *int64 `json:"leaseTime,omitempty"`
	// Preferred time in seconds. Value range is [48, 4294967295].
	// +kubebuilder:validation:Minimum=48
	// +kubebuilder:validation:Maximum=4294967295
	// +optional
	PreferredTime *int64 `json:"preferredTime,omitempty"`
	// NTP servers as FQDN or IPv6 address. Supports unicast, multicast, and FQDN formats.
	// +optional
	// +kubebuilder:validation:MaxItems=2
	NTPServers []string `json:"ntpServers,omitempty"`
	// SNTP server IPv6 addresses for VPC Subnets.
	// +optional
	// +kubebuilder:validation:MaxItems=2
	SNTPServers []string `json:"sntpServers,omitempty"`
}

type VPCProfileDHCPConfig struct {
	// DHCP relay configuration
	// +optional
	DHCPRelayConfig *VPCDHCPRelayConfig `json:"dhcpRelayConfig,omitempty"`
	// DHCP server configuration
	// +optional
	DHCPServerConfig *VPCDHCPServerConfig `json:"dhcpServerConfig,omitempty"`
}

type VPCDHCPServerConfig struct {
	// DHCP Advanced configuration
	// +optional
	AdvancedConfig *VPCDHCPAdvancedConfig `json:"advancedConfig,omitempty"`
	// DNS client configuration
	DNSClientConfig *DNSClientConfig `json:"dnsClientConfig,omitempty"`
	// DHCP lease time in seconds. Value range is [60, 4294967295], default value is 86400.
	LeaseTime int64 `json:"leaseTime"`
	// Hostname/IP list of NTP servers
	NTPServers []string `json:"ntpServers,omitempty"`
}

type VPCDHCPRelayConfig struct {
	// DHCP server IP addresses for DHCP relay configuration.
	// Both IPv4 and IPv6 addresses are supported.
	ServerAddresses []string `json:"serverAddresses,omitempty"`
}

type VPCDHCPAdvancedConfig struct {
	// Flag indicating the DHCP mode.
	// - When set to true (default), the DHCP server dynamically assigns an IP address per VM network interface.
	// - When set to false, the DHCP server operates in traditional mode, allocating IPs based on the VM's MAC address.
	//   This mode requires the VPC to be attached to a TransitGateway connected to a Provider Gateway.
	IsDistributedDHCP *bool `json:"isDistributedDHCP,omitempty"`
}

type DNSClientConfig struct {
	// IPs of the DNS servers which need to be configured on the workload VMs
	DNSServerIPs []string `json:"dnsServerIPs,omitempty"`
}

type VPCProfileDNSConfig struct {
	// IPv4 address of the DNS resolver that VPC workloads use for
	// DNS resolution. Optional, but at least one of IPv4ResolverIP or IPv6ResolverIP
	// must be provided.
	IPv4ResolverIP string `json:"ipv4ResolverIP,omitempty"`
	// IPv6 address of the DNS resolver that VPC workloads use for
	// DNS resolution. Optional, but at least one of IPv4ResolverIP or IPv6ResolverIP
	// must be provided.
	IPv6ResolverIP string `json:"ipv6ResolverIP,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type VPCServiceProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []VPCServiceProfile `json:"items"`
}

func init() {
	SchemeBuilder.Register(&VPCServiceProfile{}, &VPCServiceProfileList{})
}
