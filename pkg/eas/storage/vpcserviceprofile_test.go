/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package storage

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	vapierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type fakeVPCServiceProfilesClient struct {
	projects.VpcServiceProfilesClient
	get      func(orgID, projectID, name string) (model.VpcServiceProfile, error)
	getCalls []string
}

func (f *fakeVPCServiceProfilesClient) Get(orgID, projectID, name string) (model.VpcServiceProfile, error) {
	f.getCalls = append(f.getCalls, orgID+"/"+projectID+"/"+name)
	return f.get(orgID, projectID, name)
}

type fakeServiceProfileVPCClient struct {
	projects.VpcsClient
	get      func(orgID, projectID, vpcID string) (model.Vpc, error)
	getCalls []string
}

func (f *fakeServiceProfileVPCClient) Get(orgID, projectID, vpcID string) (model.Vpc, error) {
	f.getCalls = append(f.getCalls, orgID+"/"+projectID+"/"+vpcID)
	return f.get(orgID, projectID, vpcID)
}

func TestConvertVpcServiceProfile(t *testing.T) {
	profile := &model.VpcServiceProfile{
		Id:                 strPtr("profile-id"),
		DisplayName:        strPtr("display name differs from ID"),
		Description:        strPtr("dual-stack service profile"),
		IsDefault:          ptr.To(true),
		ServiceSubnetCidrs: []string{"10.10.0.0/28", "2001:db8:10::/64"},
		DhcpConfig: &model.VpcProfileDhcpConfig{
			DhcpRelayConfig: &model.VpcDhcpRelayConfig{ServerAddresses: []string{"192.0.2.10"}},
			DhcpServerConfig: &model.VpcDhcpServerConfig{
				AdvancedConfig:  &model.VpcDhcpAdvancedConfig{IsDistributedDhcp: ptr.To(false)},
				DnsClientConfig: &model.DnsClientConfig{DnsServerIps: []string{"192.0.2.53"}},
				LeaseTime:       int64Ptr(3600),
				NtpServers:      []string{"192.0.2.123"},
			},
		},
		Dhcpv6Config: &model.VpcProfileDhcpV6Config{
			Dhcpv6RelayConfig: &model.VpcDhcpRelayConfig{ServerAddresses: []string{"2001:db8::10"}},
			Dhcpv6ServerConfig: &model.VpcDhcpv6ServerConfig{
				AdvancedConfig:  &model.VpcDhcpAdvancedConfig{IsDistributedDhcp: ptr.To(true)},
				DnsClientConfig: &model.DnsClientConfig{DnsServerIps: []string{"2001:db8::53"}},
				LeaseTime:       int64Ptr(7200),
				PreferredTime:   int64Ptr(5400),
				NtpServers:      []string{"ntp.example.com"},
				SntpServers:     []string{"2001:db8::123"},
			},
		},
		DnsConfig: &model.VpcProfileDnsConfig{
			Ipv4ResolverIp: strPtr("192.0.2.54"),
			Ipv6ResolverIp: strPtr("2001:db8::54"),
		},
	}
	want := &easv1alpha1.VPCServiceProfile{
		TypeMeta:           metav1.TypeMeta{APIVersion: easv1alpha1.GroupVersion.String(), Kind: "VPCServiceProfile"},
		ObjectMeta:         metav1.ObjectMeta{Name: "profile-id", Namespace: "ns1"},
		IsDefault:          true,
		Description:        "dual-stack service profile",
		ServiceSubnetCIDRs: []string{"10.10.0.0/28", "2001:db8:10::/64"},
		DHCPConfig: easv1alpha1.VPCProfileDHCPConfig{
			DHCPRelayConfig: &easv1alpha1.VPCDHCPRelayConfig{ServerAddresses: []string{"192.0.2.10"}},
			DHCPServerConfig: &easv1alpha1.VPCDHCPServerConfig{
				AdvancedConfig:  &easv1alpha1.VPCDHCPAdvancedConfig{IsDistributedDHCP: ptr.To(false)},
				DNSClientConfig: &easv1alpha1.DNSClientConfig{DNSServerIPs: []string{"192.0.2.53"}},
				LeaseTime:       3600,
				NTPServers:      []string{"192.0.2.123"},
			},
		},
		DHCPv6Config: &easv1alpha1.VPCProfileDHCPv6Config{
			DHCPv6RelayConfig: &easv1alpha1.VPCDHCPRelayConfig{ServerAddresses: []string{"2001:db8::10"}},
			DHCPv6ServerConfig: &easv1alpha1.VPCDHCPv6ServerConfig{
				AdvancedConfig:  &easv1alpha1.VPCDHCPAdvancedConfig{IsDistributedDHCP: ptr.To(true)},
				DNSClientConfig: &easv1alpha1.DNSClientConfig{DNSServerIPs: []string{"2001:db8::53"}},
				LeaseTime:       int64Ptr(7200),
				PreferredTime:   int64Ptr(5400),
				NTPServers:      []string{"ntp.example.com"},
				SNTPServers:     []string{"2001:db8::123"},
			},
		},
		DNSConfig: &easv1alpha1.VPCProfileDNSConfig{IPv4ResolverIP: "192.0.2.54", IPv6ResolverIP: "2001:db8::54"},
	}
	assert.Equal(t, want, ConvertVpcServiceProfile(profile, "profile-id", "ns1"))
}

func TestConvertVpcServiceProfile_OptionalFields(t *testing.T) {
	for _, profile := range []*model.VpcServiceProfile{nil, {}} {
		result := ConvertVpcServiceProfile(profile, "profile", "ns1")
		assert.Equal(t, "profile", result.Name)
		assert.False(t, result.IsDefault)
		assert.Nil(t, result.DHCPConfig.DHCPRelayConfig)
		assert.Nil(t, result.DHCPConfig.DHCPServerConfig)
		assert.Nil(t, result.DHCPv6Config)
		assert.Nil(t, result.DNSConfig)
		assert.Nil(t, result.ServiceSubnetCIDRs)
	}
	result := ConvertVpcServiceProfile(&model.VpcServiceProfile{
		IsDefault:    ptr.To(false),
		DhcpConfig:   &model.VpcProfileDhcpConfig{DhcpServerConfig: &model.VpcDhcpServerConfig{}},
		Dhcpv6Config: &model.VpcProfileDhcpV6Config{Dhcpv6ServerConfig: &model.VpcDhcpv6ServerConfig{}},
		DnsConfig:    &model.VpcProfileDnsConfig{},
	}, "profile", "ns1")
	assert.False(t, result.IsDefault)
	require.NotNil(t, result.DHCPConfig.DHCPServerConfig)
	assert.Nil(t, result.DHCPConfig.DHCPServerConfig.AdvancedConfig)
	assert.Nil(t, result.DHCPConfig.DHCPServerConfig.DNSClientConfig)
	assert.Zero(t, result.DHCPConfig.DHCPServerConfig.LeaseTime)
	require.NotNil(t, result.DHCPv6Config)
	assert.Nil(t, result.DHCPv6Config.DHCPv6RelayConfig)
	require.NotNil(t, result.DHCPv6Config.DHCPv6ServerConfig)
	assert.Nil(t, result.DHCPv6Config.DHCPv6ServerConfig.LeaseTime)
	assert.Nil(t, result.DHCPv6Config.DHCPv6ServerConfig.PreferredTime)
	assert.Equal(t, &easv1alpha1.VPCProfileDNSConfig{}, result.DNSConfig)
	result = ConvertVpcServiceProfile(&model.VpcServiceProfile{
		DhcpConfig:   &model.VpcProfileDhcpConfig{},
		Dhcpv6Config: &model.VpcProfileDhcpV6Config{},
	}, "profile", "ns1")
	assert.Nil(t, result.DHCPConfig.DHCPServerConfig)
	assert.Nil(t, result.DHCPv6Config.DHCPv6ServerConfig)
}

func TestVPCServiceProfileStorage_NoVPC(t *testing.T) {
	for _, provider := range []eas.VPCInfoProvider{
		emptyVPCProvider{},
		multiVPCProvider{entries: []eas.VPCEntry{
			{Info: common.VPCResourceInfo{OrgID: "org", VPCID: "vpc"}},
			{Info: common.VPCResourceInfo{ProjectID: "project", VPCID: "vpc"}},
			{Info: common.VPCResourceInfo{OrgID: "org", ProjectID: "project"}},
		}},
	} {
		store := NewVPCServiceProfileStorage(&nsx.Client{}, provider)
		item, err := store.Get(context.Background(), "ns1", "vpc")
		assert.True(t, k8serrors.IsNotFound(err))
		assert.Nil(t, item)
		list, err := store.List(context.Background(), "ns1")
		require.NoError(t, err)
		require.NotNil(t, list)
		assert.Equal(t, "VPCServiceProfileList", list.Kind)
		assert.NotNil(t, list.Items)
		assert.Empty(t, list.Items)
	}
}

func TestVPCServiceProfileStorage_Get_ByVPCID(t *testing.T) {
	backendErr := fmt.Errorf("NSX unavailable")
	for _, tc := range []struct {
		name                string
		vpcErr, profileErr  error
		notFound, forbidden bool
	}{
		{name: "success"},
		{name: "VPC missing", vpcErr: vapierrors.NotFound{}, notFound: true},
		{name: "VPC forbidden", vpcErr: vapierrors.Unauthorized{}, forbidden: true},
		{name: "VPC backend failure", vpcErr: backendErr},
		{name: "profile missing", profileErr: vapierrors.NotFound{}, notFound: true},
		{name: "wrapped missing profile", profileErr: fmt.Errorf("lookup: %w", vapierrors.NotFound{}), notFound: true},
		{name: "profile forbidden", profileErr: vapierrors.Unauthorized{}, forbidden: true},
		{name: "profile backend failure", profileErr: backendErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vpcs := &fakeServiceProfileVPCClient{get: func(orgID, projectID, vpcID string) (model.Vpc, error) {
				assert.Equal(t, "vpc-org", orgID)
				assert.Equal(t, "vpc-project", projectID)
				assert.Equal(t, "vpc-id", vpcID)
				return model.Vpc{VpcServiceProfile: strPtr("/orgs/profile-org/projects/profile-project/vpc-service-profiles/profile-id")}, tc.vpcErr
			}}
			profiles := &fakeVPCServiceProfilesClient{get: func(orgID, projectID, profileID string) (model.VpcServiceProfile, error) {
				assert.Equal(t, "profile-org", orgID)
				assert.Equal(t, "profile-project", projectID)
				assert.Equal(t, "profile-id", profileID, "the profile ID must come from the VPC reference")
				return model.VpcServiceProfile{Id: strPtr(profileID), DisplayName: strPtr("profile display name"), IsDefault: ptr.To(true)}, tc.profileErr
			}}
			store := NewVPCServiceProfileStorage(&nsx.Client{VPCClient: vpcs, VpcServiceProfileClient: profiles}, singleVPCProvider{
				info: common.VPCResourceInfo{OrgID: "vpc-org", ProjectID: "vpc-project", VPCID: "vpc-id"},
			})
			result, err := store.Get(context.Background(), "ns1", "vpc-id")
			assert.Equal(t, []string{"vpc-org/vpc-project/vpc-id"}, vpcs.getCalls)
			if tc.vpcErr != nil {
				assert.Empty(t, profiles.getCalls)
			} else {
				assert.Equal(t, []string{"profile-org/profile-project/profile-id"}, profiles.getCalls)
			}
			if tc.vpcErr != nil || tc.profileErr != nil {
				require.Error(t, err)
				assert.Nil(t, result)
				assert.Equal(t, tc.notFound, k8serrors.IsNotFound(err))
				assert.Equal(t, tc.forbidden, k8serrors.IsForbidden(err))
				if !tc.notFound && !tc.forbidden {
					assert.ErrorIs(t, err, backendErr)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "vpc-id", result.Name)
			assert.Equal(t, "ns1", result.Namespace)
			assert.True(t, result.IsDefault)
		})
	}
}

func TestVPCServiceProfileStorage_Get_RejectsUnassociatedVPC(t *testing.T) {
	vpcs := &fakeServiceProfileVPCClient{}
	profiles := &fakeVPCServiceProfilesClient{}
	provider := multiVPCProvider{entries: []eas.VPCEntry{{
		DisplayName: "display-name",
		Info:        common.VPCResourceInfo{OrgID: "org", ProjectID: "project", VPCID: "vpc-id"},
	}}}
	store := NewVPCServiceProfileStorage(&nsx.Client{VPCClient: vpcs, VpcServiceProfileClient: profiles}, provider)
	for _, name := range []string{"other-vpc", "profile-id", "display-name"} {
		result, err := store.Get(context.Background(), "ns1", name)
		assert.True(t, k8serrors.IsNotFound(err))
		assert.Nil(t, result)
	}
	assert.Empty(t, vpcs.getCalls)
	assert.Empty(t, profiles.getCalls)
}

func TestVPCServiceProfileStorage_Get_InvalidReference(t *testing.T) {
	for _, tc := range []struct {
		name     string
		path     *string
		notFound bool
	}{
		{"unset", nil, true},
		{"empty", strPtr(""), true},
		{"unsupported", strPtr("/infra/vpc-service-profiles/profile"), false},
		{"incomplete", strPtr("/orgs/org/projects/project/vpc-service-profiles"), false},
		{"missing organization", strPtr("/orgs//projects/project/vpc-service-profiles/profile"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vpcs := &fakeServiceProfileVPCClient{get: func(_, _, _ string) (model.Vpc, error) {
				return model.Vpc{VpcServiceProfile: tc.path}, nil
			}}
			profiles := &fakeVPCServiceProfilesClient{}
			store := NewVPCServiceProfileStorage(&nsx.Client{VPCClient: vpcs, VpcServiceProfileClient: profiles}, singleVPCProvider{
				info: common.VPCResourceInfo{OrgID: "org", ProjectID: "project", VPCID: "vpc"},
			})
			result, err := store.Get(context.Background(), "ns1", "vpc")
			require.Error(t, err)
			assert.Equal(t, tc.notFound, k8serrors.IsNotFound(err))
			if !tc.notFound {
				assert.Contains(t, err.Error(), "invalid VPC service profile path")
			}
			assert.Nil(t, result)
			assert.Empty(t, profiles.getCalls)
		})
	}
}

func TestVPCServiceProfileStorage_List_ByVPCID(t *testing.T) {
	provider := multiVPCProvider{entries: []eas.VPCEntry{
		{Info: common.VPCResourceInfo{OrgID: "org1", ProjectID: "project", VPCID: "vpc1"}},
		{Info: common.VPCResourceInfo{OrgID: "org1", ProjectID: "project", VPCID: "vpc1"}},
		{Info: common.VPCResourceInfo{OrgID: "org1", ProjectID: "project", VPCID: "vpc2"}},
		{Info: common.VPCResourceInfo{OrgID: "org2", ProjectID: "project", VPCID: "vpc3"}},
		{Info: common.VPCResourceInfo{OrgID: "org1", ProjectID: "project", VPCID: "deleted"}},
		{Info: common.VPCResourceInfo{OrgID: "org1", ProjectID: "project", VPCID: "unbound"}},
		{Info: common.VPCResourceInfo{OrgID: "org1", ProjectID: "project", VPCID: "missing-profile"}},
		{Info: common.VPCResourceInfo{OrgID: "org1"}},
	}}
	vpcs := &fakeServiceProfileVPCClient{get: func(orgID, projectID, vpcID string) (model.Vpc, error) {
		if vpcID == "deleted" {
			return model.Vpc{}, vapierrors.NotFound{}
		}
		if vpcID == "unbound" {
			return model.Vpc{}, nil
		}
		profileID := "shared-profile"
		if vpcID == "missing-profile" {
			profileID = "deleted-profile"
		}
		return model.Vpc{VpcServiceProfile: strPtr("/orgs/" + orgID + "/projects/" + projectID + "/vpc-service-profiles/" + profileID)}, nil
	}}
	profiles := &fakeVPCServiceProfilesClient{get: func(orgID, _, profileID string) (model.VpcServiceProfile, error) {
		if profileID == "deleted-profile" {
			return model.VpcServiceProfile{}, vapierrors.NotFound{}
		}
		return model.VpcServiceProfile{Id: strPtr(profileID), Description: strPtr(orgID + " profile"), IsDefault: ptr.To(true)}, nil
	}}
	store := NewVPCServiceProfileStorage(&nsx.Client{VPCClient: vpcs, VpcServiceProfileClient: profiles}, provider)
	result, err := store.List(context.Background(), "ns1")
	require.NoError(t, err)
	require.Len(t, result.Items, 3)
	assert.Len(t, vpcs.getCalls, 6, "duplicate VPC entries must only be queried once")
	for i, item := range result.Items {
		assert.Equal(t, fmt.Sprintf("vpc%d", i+1), item.Name)
		assert.Equal(t, "ns1", item.Namespace)
		byName, err := store.Get(context.Background(), "ns1", item.Name)
		require.NoError(t, err)
		assert.Equal(t, item, *byName, "a listed name must be usable in Get")
	}
	assert.Equal(t, "org1 profile", result.Items[0].Description)
	assert.Equal(t, "org1 profile", result.Items[1].Description, "two VPCs sharing a profile must both be listed")
	assert.Equal(t, "org2 profile", result.Items[2].Description)
}

func TestVPCServiceProfileStorage_List_Errors(t *testing.T) {
	for _, stage := range []string{"VPC", "profile"} {
		for _, backendErr := range []error{fmt.Errorf("NSX unavailable"), vapierrors.Unauthorized{}} {
			t.Run(stage+"/"+fmt.Sprintf("%T", backendErr), func(t *testing.T) {
				vpcs := &fakeServiceProfileVPCClient{get: func(_, _, _ string) (model.Vpc, error) {
					if stage == "VPC" {
						return model.Vpc{}, backendErr
					}
					return model.Vpc{VpcServiceProfile: strPtr("/orgs/org/projects/project/vpc-service-profiles/profile")}, nil
				}}
				profiles := &fakeVPCServiceProfilesClient{get: func(_, _, _ string) (model.VpcServiceProfile, error) {
					return model.VpcServiceProfile{}, backendErr
				}}
				store := NewVPCServiceProfileStorage(&nsx.Client{VPCClient: vpcs, VpcServiceProfileClient: profiles}, singleVPCProvider{
					info: common.VPCResourceInfo{OrgID: "org", ProjectID: "project", VPCID: "vpc"},
				})
				result, err := store.List(context.Background(), "ns1")
				require.Error(t, err)
				assert.Nil(t, result)
				if _, forbidden := backendErr.(vapierrors.Unauthorized); forbidden {
					assert.True(t, k8serrors.IsForbidden(err))
				} else {
					assert.ErrorIs(t, err, backendErr)
				}
			})
		}
	}
}

func TestVPCServiceProfileStorage_CancelledContext(t *testing.T) {
	vpcs := &fakeServiceProfileVPCClient{}
	profiles := &fakeVPCServiceProfilesClient{}
	store := NewVPCServiceProfileStorage(&nsx.Client{VPCClient: vpcs, VpcServiceProfileClient: profiles}, singleVPCProvider{
		info: common.VPCResourceInfo{OrgID: "org", ProjectID: "project", VPCID: "vpc"},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := store.Get(ctx, "ns1", "vpc")
	assert.ErrorIs(t, err, context.Canceled)
	_, err = store.List(ctx, "ns1")
	assert.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, vpcs.getCalls)
	assert.Empty(t, profiles.getCalls)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	vpcs.get = func(_, _, _ string) (model.Vpc, error) {
		cancel()
		return model.Vpc{VpcServiceProfile: strPtr("/orgs/org/projects/project/vpc-service-profiles/profile")}, nil
	}
	_, err = store.Get(ctx, "ns1", "vpc")
	assert.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, profiles.getCalls, "cancellation after the VPC read must prevent the profile read")
}
