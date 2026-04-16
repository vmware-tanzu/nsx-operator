/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package storage

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestDerefString(t *testing.T) {
	assert.Equal(t, "", DerefString(nil))
	s := "x"
	assert.Equal(t, "x", DerefString(&s))
}

func TestDerefInt64(t *testing.T) {
	assert.Equal(t, int64(0), DerefInt64(nil))
	v := int64(42)
	assert.Equal(t, int64(42), DerefInt64(&v))
}

func TestConvertVpcIpAddressBlocks_Nil(t *testing.T) {
	out := ConvertVpcIpAddressBlocks(nil, "vpc1", "ns1")
	require.NotNil(t, out)
	assert.Equal(t, "vpc1", out.Name)
	assert.Equal(t, "ns1", out.Namespace)
	assert.Equal(t, easv1alpha1.GroupVersion.String(), out.APIVersion)
	assert.Empty(t, out.IPBlocks)
}

func TestConvertVpcIpAddressBlocks_ExternalBlock(t *testing.T) {
	path := "/orgs/o1/projects/p1/vpcs/vpcid"
	cidrs := []string{"10.1.0.0/16"}
	pct := "50"
	nsx := &model.VpcIpAddressBlocks{
		IpBlocks: []model.VpcIpAddressBlock{
			{
				Path:           &path,
				Cidrs:          cidrs,
				PercentageUsed: &pct,
				Visibility:     strPtr("EXTERNAL"),
			},
		},
	}
	out := ConvertVpcIpAddressBlocks(nsx, "vpc1", "ns1")
	require.Len(t, out.IPBlocks, 1)
	b := out.IPBlocks[0]
	assert.Equal(t, ":vpcid", b.IPBlockName)
	assert.Equal(t, easv1alpha1.External, b.Visibility)
}

func TestConvertVpcIpAddressBlocks_WithBlocks(t *testing.T) {
	path := "/orgs/o1/projects/p1/vpcs/vpcid"
	cidrs := []string{"10.1.0.0/16"}
	pct := "50"
	nsx := &model.VpcIpAddressBlocks{
		IpBlocks: []model.VpcIpAddressBlock{
			{
				Path:           &path,
				Cidrs:          cidrs,
				PercentageUsed: &pct,
				Visibility:     strPtr("PRIVATE"),
			},
		},
	}
	out := ConvertVpcIpAddressBlocks(nsx, "vpc1", "ns1")
	require.Len(t, out.IPBlocks, 1)
	b := out.IPBlocks[0]
	assert.Equal(t, "vpcid", b.IPBlockName)
	assert.Equal(t, []string{"10.1.0.0/16"}, b.CIDRs)
	assert.Equal(t, "50", b.PercentageUsed)
	assert.Equal(t, easv1alpha1.Private, b.Visibility)
}

func TestConvertVpcIpAddressBlocks_WithRangesAndAllocatedByVPC(t *testing.T) {
	start, end := "10.0.0.1", "10.0.0.10"
	pct := "80"
	am := "Public"
	count := int64(3)
	addrAlloc := "10.0.0.5"
	pathAlloc := "/orgs/o1/projects/p1/vpcs/v1/ip-address-allocations/alloc-1"
	addrSubnet := "10.0.0.6"
	pathSubnet := "/orgs/o1/projects/p1/vpcs/v1/subnets/sub-1"
	addrOther := "10.0.0.7"
	pathOther := "/some/other/path/leaf"

	nsx := &model.VpcIpAddressBlocks{
		IpBlocks: []model.VpcIpAddressBlock{
			{
				Cidrs:          []string{"10.0.0.0/8"},
				PercentageUsed: &pct,
				ExcludedIps:    []model.IpPoolRange{{Start: &start, End: &end}},
				Ranges:         []model.IpPoolRange{{Start: &start, End: &end}},
				AllocatedByVpc: &model.AllocatedByVpc{
					AccessMode: &am,
					Count:      &count,
					IpAddresses: []model.VpcIpAddress{
						{Address: &addrAlloc, Path: &pathAlloc},
						{Address: &addrSubnet, Path: &pathSubnet},
						{Address: &addrOther, Path: &pathOther},
					},
				},
			},
		},
	}
	out := ConvertVpcIpAddressBlocks(nsx, "vpc1", "ns1")
	require.Len(t, out.IPBlocks, 1)
	b := out.IPBlocks[0]
	require.Len(t, b.ExcludedIPs, 1)
	assert.Equal(t, "10.0.0.1", b.ExcludedIPs[0].Start)
	assert.Equal(t, "10.0.0.10", b.ExcludedIPs[0].End)
	require.Len(t, b.Ranges, 1)
	assert.Equal(t, easv1alpha1.PublicSubnet, b.AllocatedByVPC.AccessMode)
	assert.Equal(t, int64(3), b.AllocatedByVPC.Count)
	require.Len(t, b.AllocatedByVPC.IPAddresses, 3)
	assert.Equal(t, "alloc-1", b.AllocatedByVPC.IPAddresses[0].IPAddressAllocationName)
	assert.Equal(t, "sub-1", b.AllocatedByVPC.IPAddresses[1].SubnetName)
	assert.Equal(t, "leaf", b.AllocatedByVPC.IPAddresses[2].IPAddressAllocationName)
}

func TestConvertVpcIpAddressBlocks_ZeroIntegers(t *testing.T) {
	// NSX returns 0 for Total/Available/Count — must appear in output (not omitted).
	zero := int64(0)
	nsx := &model.VpcIpAddressBlocks{
		IpBlocks: []model.VpcIpAddressBlock{
			{
				Total:     &zero,
				Available: &zero,
				AllocatedByVpc: &model.AllocatedByVpc{
					Count: &zero,
				},
			},
		},
	}
	out := ConvertVpcIpAddressBlocks(nsx, "vpc1", "ns1")
	require.Len(t, out.IPBlocks, 1)
	assert.Equal(t, int64(0), out.IPBlocks[0].Total)
	assert.Equal(t, int64(0), out.IPBlocks[0].Available)
	assert.Equal(t, int64(0), out.IPBlocks[0].AllocatedByVPC.Count)
}

func TestToAccessMode(t *testing.T) {
	assert.Equal(t, easv1alpha1.AccessMode(""), toAccessMode(""))
	assert.Equal(t, easv1alpha1.PublicSubnet, toAccessMode("Public"))
	assert.Equal(t, easv1alpha1.PrivateSubnet, toAccessMode("Private"))
	assert.Equal(t, easv1alpha1.PrivateTGWSubnet, toAccessMode("PrivateTGW"))
	assert.Equal(t, easv1alpha1.PublicSubnet, toAccessMode("PUBLIC"))
	assert.Equal(t, easv1alpha1.PrivateTGWSubnet, toAccessMode("PRIVATE-TGW"))
	assert.Equal(t, easv1alpha1.AccessMode("unknown"), toAccessMode("unknown"))
}

func TestVpcIPAddressFromNSX_Paths(t *testing.T) {
	addr := "10.0.0.1"
	allocPath := "/orgs/o1/projects/p1/vpcs/v1/ip-address-allocations/my-alloc"
	subnetPath := "/orgs/o1/projects/p1/vpcs/v1/subnets/my-subnet"
	otherPath := "/some/path/leaf-name"

	out := vpcIPAddressFromNSX(model.VpcIpAddress{Address: &addr, Path: &allocPath})
	assert.Equal(t, "my-alloc", out.IPAddressAllocationName)
	assert.Empty(t, out.SubnetName)

	out = vpcIPAddressFromNSX(model.VpcIpAddress{Address: &addr, Path: &subnetPath})
	assert.Equal(t, "my-subnet", out.SubnetName)
	assert.Empty(t, out.IPAddressAllocationName)

	out = vpcIPAddressFromNSX(model.VpcIpAddress{Address: &addr, Path: &otherPath})
	assert.Equal(t, "leaf-name", out.IPAddressAllocationName)

	emptyPath := ""
	out = vpcIPAddressFromNSX(model.VpcIpAddress{Address: &addr, Path: &emptyPath})
	assert.Empty(t, out.IPAddressAllocationName)
	assert.Empty(t, out.SubnetName)
}
func TestVPCIPAddressUsageStorage_Get_NoVPC(t *testing.T) {
	s := NewVPCIPAddressUsageStorage(&nsx.Client{}, emptyVPCProvider{})
	_, err := s.Get(context.Background(), "ns1", "ignored")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no VPC found")
}
func TestVPCIPAddressUsageStorage_List_NoVPC(t *testing.T) {
	s := NewVPCIPAddressUsageStorage(&nsx.Client{}, emptyVPCProvider{})
	list, err := s.List(context.Background(), "ns1")
	require.NoError(t, err)
	require.NotNil(t, list)
	assert.Empty(t, list.Items)
}
func TestVPCIPAddressUsageStorage_Get_VPCNotFound(t *testing.T) {
	p := singleVPCProvider{info: common.VPCResourceInfo{VPCID: "vpc1"}}
	s := NewVPCIPAddressUsageStorage(&nsx.Client{}, p)
	_, err := s.Get(context.Background(), "ns1", "other-vpc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
func TestVPCIPAddressUsageStorage_Get_OK(t *testing.T) {
	p := singleVPCProvider{info: common.VPCResourceInfo{OrgID: "o1", ProjectID: "p1", VPCID: "vpc1"}}
	c := &nsx.Client{}
	c.IPAddressUsageClient = &fakeIPAddressUsageClient{}
	s := NewVPCIPAddressUsageStorage(c, p)
	result, err := s.Get(context.Background(), "ns1", "vpc1")
	require.NoError(t, err)
	assert.Equal(t, "vpc1", result.Name)
}
func TestVPCIPAddressUsageStorage_List_OK(t *testing.T) {
	p := singleVPCProvider{info: common.VPCResourceInfo{OrgID: "o1", ProjectID: "p1", VPCID: "vpc1"}}
	c := &nsx.Client{}
	c.IPAddressUsageClient = &fakeIPAddressUsageClient{}
	s := NewVPCIPAddressUsageStorage(c, p)
	list, err := s.List(context.Background(), "ns1")
	require.NoError(t, err)
	require.Len(t, list.Items, 1)
	assert.Equal(t, "vpc1", list.Items[0].Name)
}
func TestVPCIPAddressUsageStorage_List_Error(t *testing.T) {
	p := singleVPCProvider{info: common.VPCResourceInfo{OrgID: "o1", ProjectID: "p1", VPCID: "vpc1"}}
	c := &nsx.Client{}
	c.IPAddressUsageClient = &fakeIPAddressUsageClient{err: fmt.Errorf("nsx error")}
	s := NewVPCIPAddressUsageStorage(c, p)
	_, err := s.List(context.Background(), "ns1")
	require.Error(t, err)
}
func TestVPCIPAddressUsageStorage_Get_ClientError(t *testing.T) {
	p := singleVPCProvider{info: common.VPCResourceInfo{OrgID: "o1", ProjectID: "p1", VPCID: "vpc1"}}
	c := &nsx.Client{}
	c.IPAddressUsageClient = &fakeIPAddressUsageClient{err: fmt.Errorf("nsx unavailable")}
	s := NewVPCIPAddressUsageStorage(c, p)
	_, err := s.Get(context.Background(), "ns1", "vpc1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nsx unavailable")
}
