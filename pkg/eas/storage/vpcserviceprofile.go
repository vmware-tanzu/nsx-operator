/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package storage

import (
	"context"
	"fmt"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

const vpcServiceProfileResource = "vpcserviceprofiles"

// VPCServiceProfileStorage reads the service profiles referenced by the namespace's VPCs.
type VPCServiceProfileStorage struct {
	nsxClient   *nsx.Client
	vpcProvider eas.VPCInfoProvider
}

func NewVPCServiceProfileStorage(nsxClient *nsx.Client, provider eas.VPCInfoProvider) *VPCServiceProfileStorage {
	return &VPCServiceProfileStorage{nsxClient: nsxClient, vpcProvider: provider}
}

// Get retrieves the service profile attached to the VPC identified by vpcName.
// vpcName and the returned metadata.name are the VPC ID, not the profile ID.
func (s *VPCServiceProfileStorage) Get(ctx context.Context, namespace, vpcName string) (*easv1alpha1.VPCServiceProfile, error) {
	for _, entry := range s.vpcProvider.ListVPCInfo(namespace) {
		info := entry.Info
		if !vpcMatchesByName(entry, vpcName) || info.OrgID == "" || info.ProjectID == "" || info.VPCID == "" {
			continue
		}
		return s.getForVPC(ctx, namespace, info)
	}
	return nil, vpcServiceProfileNotFound(vpcName)
}

func (s *VPCServiceProfileStorage) getForVPC(ctx context.Context, namespace string, info common.VPCResourceInfo) (*easv1alpha1.VPCServiceProfile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	vpc, err := s.nsxClient.VPCClient.Get(info.OrgID, info.ProjectID, info.VPCID)
	if err != nil {
		return nil, HandleEASError(err, vpcServiceProfileResource, info.VPCID, fmt.Errorf("failed to get VPC %s: %w", info.VPCID, err))
	}
	profilePath := DerefString(vpc.VpcServiceProfile)
	if profilePath == "" {
		return nil, vpcServiceProfileNotFound(info.VPCID)
	}
	// Follow /orgs/{orgID}/projects/{projectID}/vpc-service-profiles/{profileID}.
	parts := splitPolicyPath(profilePath)
	if len(parts) != 6 || parts[0] != "orgs" || parts[1] == "" || parts[2] != "projects" || parts[3] == "" || parts[4] != "vpc-service-profiles" || parts[5] == "" {
		return nil, fmt.Errorf("invalid VPC service profile path %q on VPC %s", profilePath, info.VPCID)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	profile, err := s.nsxClient.VpcServiceProfileClient.Get(parts[1], parts[3], parts[5])
	if err != nil {
		return nil, HandleEASError(err, vpcServiceProfileResource, info.VPCID, fmt.Errorf("failed to get service profile %s for VPC %s: %w", profilePath, info.VPCID, err))
	}
	return ConvertVpcServiceProfile(&profile, info.VPCID, namespace), nil
}

func vpcServiceProfileNotFound(vpcName string) error {
	return k8serrors.NewNotFound(schema.GroupResource{Group: easv1alpha1.GroupVersion.Group, Resource: vpcServiceProfileResource}, vpcName)
}

// List returns the profile attached to each VPC in the namespace, named by VPC ID.
func (s *VPCServiceProfileStorage) List(ctx context.Context, namespace string) (*easv1alpha1.VPCServiceProfileList, error) {
	list := &easv1alpha1.VPCServiceProfileList{
		TypeMeta: metav1.TypeMeta{APIVersion: easv1alpha1.GroupVersion.String(), Kind: "VPCServiceProfileList"},
		Items:    make([]easv1alpha1.VPCServiceProfile, 0),
	}
	seenVPCs := make(map[string]struct{})
	for _, entry := range s.vpcProvider.ListVPCInfo(namespace) {
		info := entry.Info
		if info.OrgID == "" || info.ProjectID == "" || info.VPCID == "" {
			continue
		}
		if _, exists := seenVPCs[info.VPCID]; exists {
			continue
		}
		seenVPCs[info.VPCID] = struct{}{}
		profile, err := s.getForVPC(ctx, namespace, info)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				continue
			}
			return nil, err
		}
		list.Items = append(list.Items, *profile)
	}
	return list, nil
}

// ConvertVpcServiceProfile maps NSX profile data to the EAS response, preserving optional fields.
func ConvertVpcServiceProfile(profile *model.VpcServiceProfile, name, namespace string) *easv1alpha1.VPCServiceProfile {
	result := &easv1alpha1.VPCServiceProfile{
		TypeMeta:   metav1.TypeMeta{APIVersion: easv1alpha1.GroupVersion.String(), Kind: "VPCServiceProfile"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
	if profile == nil {
		return result
	}
	result.IsDefault = profile.IsDefault != nil && *profile.IsDefault
	result.Description = DerefString(profile.Description)
	result.ServiceSubnetCIDRs = profile.ServiceSubnetCidrs
	if config := profile.DhcpConfig; config != nil {
		result.DHCPConfig = easv1alpha1.VPCProfileDHCPConfig{
			DHCPRelayConfig:  convertVPCDHCPRelayConfig(config.DhcpRelayConfig),
			DHCPServerConfig: convertVPCDHCPServerConfig(config.DhcpServerConfig),
		}
	}
	if config := profile.Dhcpv6Config; config != nil {
		result.DHCPv6Config = &easv1alpha1.VPCProfileDHCPv6Config{
			DHCPv6RelayConfig:  convertVPCDHCPRelayConfig(config.Dhcpv6RelayConfig),
			DHCPv6ServerConfig: convertVPCDHCPv6ServerConfig(config.Dhcpv6ServerConfig),
		}
	}
	if config := profile.DnsConfig; config != nil {
		result.DNSConfig = &easv1alpha1.VPCProfileDNSConfig{
			IPv4ResolverIP: DerefString(config.Ipv4ResolverIp),
			IPv6ResolverIP: DerefString(config.Ipv6ResolverIp),
		}
	}
	return result
}

func convertVPCDHCPRelayConfig(config *model.VpcDhcpRelayConfig) *easv1alpha1.VPCDHCPRelayConfig {
	if config == nil {
		return nil
	}
	return &easv1alpha1.VPCDHCPRelayConfig{ServerAddresses: config.ServerAddresses}
}

func convertVPCDHCPAdvancedConfig(config *model.VpcDhcpAdvancedConfig) *easv1alpha1.VPCDHCPAdvancedConfig {
	if config == nil {
		return nil
	}
	return &easv1alpha1.VPCDHCPAdvancedConfig{IsDistributedDHCP: config.IsDistributedDhcp}
}

func convertVPCDNSClientConfig(config *model.DnsClientConfig) *easv1alpha1.DNSClientConfig {
	if config == nil {
		return nil
	}
	return &easv1alpha1.DNSClientConfig{DNSServerIPs: config.DnsServerIps}
}

func convertVPCDHCPServerConfig(config *model.VpcDhcpServerConfig) *easv1alpha1.VPCDHCPServerConfig {
	if config == nil {
		return nil
	}
	return &easv1alpha1.VPCDHCPServerConfig{
		AdvancedConfig:  convertVPCDHCPAdvancedConfig(config.AdvancedConfig),
		DNSClientConfig: convertVPCDNSClientConfig(config.DnsClientConfig),
		LeaseTime:       DerefInt64(config.LeaseTime),
		NTPServers:      config.NtpServers,
	}
}

func convertVPCDHCPv6ServerConfig(config *model.VpcDhcpv6ServerConfig) *easv1alpha1.VPCDHCPv6ServerConfig {
	if config == nil {
		return nil
	}
	return &easv1alpha1.VPCDHCPv6ServerConfig{
		AdvancedConfig:  convertVPCDHCPAdvancedConfig(config.AdvancedConfig),
		DNSClientConfig: convertVPCDNSClientConfig(config.DnsClientConfig),
		LeaseTime:       config.LeaseTime,
		PreferredTime:   config.PreferredTime,
		NTPServers:      config.NtpServers,
		SNTPServers:     config.SntpServers,
	}
}
