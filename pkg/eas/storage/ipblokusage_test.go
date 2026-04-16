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
	"github.com/vmware-tanzu/nsx-operator/pkg/eas"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestConvertIpAddressBlockUsageList_Nil(t *testing.T) {
	assert.Nil(t, ConvertIpAddressBlockUsageList(nil, "", "ns"))
}

func TestConvertIpAddressBlockUsageList_NameFromIntentPath(t *testing.T) {
	intent := "/orgs/default/projects/myproject/ip-blocks/shared-block-1"
	nsx := &model.IpAddressBlockUsageList{
		Results: []model.IpAddressBlockUsage{
			{
				IntentPath: &intent,
				Visibility: strPtr("EXTERNAL"),
			},
		},
	}
	items := ConvertIpAddressBlockUsageList(nsx, "myproject", "ns1")
	require.Len(t, items, 1)
	assert.Equal(t, "shared-block-1", items[0].Name)
	assert.Equal(t, easv1alpha1.External, items[0].Visibility)
}

func TestConvertIpAddressBlockUsageList_InfraScoped(t *testing.T) {
	intent := "/infra/ip-blocks/global-block"
	nsx := &model.IpAddressBlockUsageList{
		Results: []model.IpAddressBlockUsage{
			{IntentPath: &intent, Visibility: strPtr("EXTERNAL")},
		},
	}
	items := ConvertIpAddressBlockUsageList(nsx, "", "ns1")
	require.Len(t, items, 1)
	assert.Equal(t, "global-block", items[0].Name)
	assert.Equal(t, easv1alpha1.External, items[0].Visibility)
}

func TestConvertIpAddressBlockUsageList_Fallback_WithProject(t *testing.T) {
	nsx := &model.IpAddressBlockUsageList{
		Results: []model.IpAddressBlockUsage{{}},
	}
	items := ConvertIpAddressBlockUsageList(nsx, "proj1", "ns1")
	require.Len(t, items, 1)
	assert.Equal(t, "ipblock-0", items[0].Name)
}

func TestConvertIpAddressBlockUsageList_Fallback_NoProject(t *testing.T) {
	nsx := &model.IpAddressBlockUsageList{
		Results: []model.IpAddressBlockUsage{{}},
	}
	items := ConvertIpAddressBlockUsageList(nsx, "", "ns1")
	require.Len(t, items, 1)
	assert.Equal(t, "ipblock-0", items[0].Name)
}

func TestConvertIpAddressBlockUsage_Nil(t *testing.T) {
	item := ConvertIpAddressBlockUsage(nil, "block1", "ns1")
	require.NotNil(t, item)
	assert.Equal(t, "block1", item.Name)
	assert.Equal(t, "ns1", item.Namespace)
	assert.Empty(t, item.CIDRUsages)
	assert.Empty(t, item.RangeUsages)
}

func TestConvertIpAddressBlockUsage_WithFullData(t *testing.T) {
	cidr, blockID := "10.0.0.0/8", "block-a"
	overallUsed := "10.0.0.1-10.0.0.5"
	rangeVal := "10.1.0.0-10.1.0.255"
	nsxUsage := &model.IpAddressBlockUsage{
		Visibility:        strPtr("PRIVATE"),
		UsedIpsCount:      strPtr("5"),
		AvailableIpsCount: strPtr("100"),
		OverallIpsCount:   strPtr("256"),
		UsedIpRanges:      []string{"10.0.0.1-10.0.0.5"},
		AvailableIpRanges: []string{"10.0.0.6-10.0.0.255"},
		CidrUsage: []model.CidrUsageDetails{
			{
				Cidr:                &cidr,
				UsedIpRanges:        []string{"10.0.0.1-10.0.0.3"},
				OverallUsedIpRanges: []string{overallUsed},
				AvailableIpRanges:   []string{"10.0.0.4-10.0.0.255"},
				UsedIpsCount:        strPtr("3"),
				OverallUsedIpsCount: strPtr("5"),
				AvailableIpsCount:   strPtr("100"),
			},
		},
		RangeUsage: []model.RangeUsageDetails{
			{
				Range_:              strPtr(rangeVal),
				UsedIpRanges:        []string{"10.1.0.1-10.1.0.10"},
				OverallUsedIpRanges: []string{"10.1.0.1-10.1.0.20"},
				AvailableIpRanges:   []string{"10.1.0.11-10.1.0.255"},
				UsedIpsCount:        strPtr("10"),
				OverallUsedIpsCount: strPtr("20"),
				AvailableIpsCount:   strPtr("245"),
			},
		},
		IntentPath: strPtr("/infra/ip-blocks/" + blockID),
	}
	item := ConvertIpAddressBlockUsage(nsxUsage, "block-a", "ns1")
	assert.Equal(t, "block-a", item.Name)
	assert.Equal(t, easv1alpha1.Private, item.Visibility)
	assert.Equal(t, "5", item.UsedIPsCount)
	assert.Equal(t, "100", item.AvailableIPsCount)
	assert.Equal(t, "256", item.OverallIPsCount)
	require.Len(t, item.CIDRUsages, 1)
	assert.Equal(t, "10.0.0.0/8", item.CIDRUsages[0].CIDR)
	assert.Equal(t, "3", item.CIDRUsages[0].UsedIPsCount)
	require.Len(t, item.RangeUsages, 1)
	assert.Equal(t, rangeVal, item.RangeUsages[0].Range)
	assert.Equal(t, "10", item.RangeUsages[0].UsedIPsCount)
}

func TestConvertIpAddressBlockUsage_NilCounts(t *testing.T) {
	// NSX returns nil for count strings — must appear as "0" in output (not omitted).
	nsx := &model.IpAddressBlockUsage{
		UsedIpsCount:      nil,
		AvailableIpsCount: nil,
		OverallIpsCount:   nil,
		CidrUsage: []model.CidrUsageDetails{
			{UsedIpsCount: nil, OverallUsedIpsCount: nil, AvailableIpsCount: nil},
		},
		RangeUsage: []model.RangeUsageDetails{
			{UsedIpsCount: nil, OverallUsedIpsCount: nil, AvailableIpsCount: nil},
		},
	}
	out := ConvertIpAddressBlockUsage(nsx, "ib1", "ns1")
	assert.Equal(t, "0", out.UsedIPsCount)
	assert.Equal(t, "0", out.AvailableIPsCount)
	assert.Equal(t, "0", out.OverallIPsCount)
	require.Len(t, out.CIDRUsages, 1)
	assert.Equal(t, "0", out.CIDRUsages[0].UsedIPsCount)
	assert.Equal(t, "0", out.CIDRUsages[0].OverallUsedIPsCount)
	assert.Equal(t, "0", out.CIDRUsages[0].AvailableIPsCount)
	require.Len(t, out.RangeUsages, 1)
	assert.Equal(t, "0", out.RangeUsages[0].UsedIPsCount)
	assert.Equal(t, "0", out.RangeUsages[0].OverallUsedIPsCount)
	assert.Equal(t, "0", out.RangeUsages[0].AvailableIPsCount)
}

func TestIpBlockUsageName(t *testing.T) {
	cases := []struct {
		name       string
		intentPath string
		projectID  string
		index      int
		want       string
	}{
		{"infra block", "/infra/ip-blocks/blk1", "", 0, "blk1"},
		{"project block with param", "/orgs/default/projects/proj1/infra/ip-blocks/blk1", "proj1", 0, "blk1"},
		{"project block param override path project", "/orgs/default/projects/path-proj/infra/ip-blocks/blk1", "override-proj", 0, "blk1"},
		{"fallback with project", "", "proj1", 2, "ipblock-2"},
		{"fallback no project", "", "", 3, "ipblock-3"},
		{"empty path", "/", "p1", 0, "ipblock-0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ipBlockUsageName(tc.intentPath, tc.projectID, tc.index)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDerefCount(t *testing.T) {
	assert.Equal(t, "0", derefCount(nil), "nil should return '0'")
	assert.Equal(t, "42", derefCount(strPtr("42")))
	assert.Equal(t, "0", derefCount(strPtr("0")))
}
func TestIPBlockUsageStorage_List_NoVPC(t *testing.T) {
	s := NewIPBlockUsageStorage(&nsx.Client{}, emptyVPCProvider{})
	list, err := s.List(context.Background(), "ns1")
	require.NoError(t, err)
	require.NotNil(t, list)
	assert.Empty(t, list.Items)
}
func TestIPBlockUsageStorage_Get_InvalidFormat(t *testing.T) {
	s := NewIPBlockUsageStorage(&nsx.Client{}, emptyVPCProvider{})
	_, err := s.Get(context.Background(), "ns1", ":")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}
func TestIPBlockUsageStorage_Get_Infra_OK(t *testing.T) {
	c := &nsx.Client{}
	c.InfraIPBlockUsageClient = &fakeInfraIPBlockUsageClient{}
	s := NewIPBlockUsageStorage(c, emptyVPCProvider{})
	result, err := s.Get(context.Background(), "ns1", ":block1")
	require.NoError(t, err)
	assert.Equal(t, ":block1", result.Name)
}
func TestIPBlockUsageStorage_Get_Project_OK(t *testing.T) {
	p := singleVPCProvider{info: common.VPCResourceInfo{OrgID: "o1", ProjectID: "p1", VPCID: "vpc1"}}
	c := &nsx.Client{}
	profilePath := "/orgs/default/projects/p1/vpc-connectivity-profiles/profile1"
	blockPath := "/orgs/default/projects/p1/infra/ip-blocks/block1"
	c.VPCClient = &fakeVpcsClient{}
	c.VpcAttachmentClient = &fakeVpcAttachmentClient{result: model.VpcAttachmentListResult{
		Results: []model.VpcAttachment{{VpcConnectivityProfile: &profilePath}},
	}}
	c.VPCConnectivityProfilesClient = &fakeVpcConnectivityProfilesClient{result: model.VpcConnectivityProfile{
		ExternalIpBlocks: []string{blockPath},
	}}
	c.ProjectIPBlockUsageClient = &fakeProjectIPBlockUsageClient{getResult: model.IpAddressBlockUsage{}}
	s := NewIPBlockUsageStorage(c, p)
	result, err := s.Get(context.Background(), "ns1", "block1")
	require.NoError(t, err)
	assert.Equal(t, "block1", result.Name)
}
func TestIPBlockUsageStorage_Get_Project_OK_FromConnectivityProfile(t *testing.T) {
	p := singleVPCProvider{info: common.VPCResourceInfo{OrgID: "o1", ProjectID: "p1", VPCID: "vpc1"}}
	c := &nsx.Client{}
	profilePath := "/orgs/default/projects/p1/vpc-connectivity-profiles/profile1"
	blockPath := "/orgs/default/projects/p1/infra/ip-blocks/block1"
	c.VPCClient = &fakeVpcsClient{}
	c.VpcAttachmentClient = &fakeVpcAttachmentClient{result: model.VpcAttachmentListResult{
		Results: []model.VpcAttachment{{VpcConnectivityProfile: &profilePath}},
	}}
	c.VPCConnectivityProfilesClient = &fakeVpcConnectivityProfilesClient{result: model.VpcConnectivityProfile{
		ExternalIpBlocks: []string{blockPath},
	}}
	c.ProjectIPBlockUsageClient = &fakeProjectIPBlockUsageClient{getResult: model.IpAddressBlockUsage{}}
	s := NewIPBlockUsageStorage(c, p)
	result, err := s.Get(context.Background(), "ns1", "block1")
	require.NoError(t, err)
	assert.Equal(t, "block1", result.Name)
}
func TestIPBlockUsageStorage_Get_Infra_Error(t *testing.T) {
	c := &nsx.Client{}
	c.InfraIPBlockUsageClient = &fakeInfraIPBlockUsageClient{err: fmt.Errorf("nsx error")}
	s := NewIPBlockUsageStorage(c, emptyVPCProvider{})
	_, err := s.Get(context.Background(), "ns1", ":block1")
	require.Error(t, err)
}
func TestIPBlockUsageStorage_Get_Project_Error(t *testing.T) {
	p := singleVPCProvider{info: common.VPCResourceInfo{OrgID: "o1", ProjectID: "p1", VPCID: "vpc1"}}
	c := &nsx.Client{}
	profilePath := "/orgs/default/projects/p1/vpc-connectivity-profiles/profile1"
	c.VPCClient = &fakeVpcsClient{}
	c.VpcAttachmentClient = &fakeVpcAttachmentClient{result: model.VpcAttachmentListResult{
		Results: []model.VpcAttachment{{VpcConnectivityProfile: &profilePath}},
	}}
	c.VPCConnectivityProfilesClient = &fakeVpcConnectivityProfilesClient{}
	c.ProjectIPBlockUsageClient = &fakeProjectIPBlockUsageClient{err: fmt.Errorf("nsx error")}
	s := NewIPBlockUsageStorage(c, p)
	_, err := s.Get(context.Background(), "ns1", "block1")
	require.Error(t, err)
}
func TestIPBlockUsageStorage_Get_Project_ErrorDoesNotAffectList(t *testing.T) {
	// Confirm Get error field is separate from List: a Get error should not bleed into List.
	p := singleVPCProvider{info: common.VPCResourceInfo{OrgID: "o1", ProjectID: "p1", VPCID: "vpc1"}}
	c := &nsx.Client{}
	c.ProjectIPBlockUsageClient = &fakeProjectIPBlockUsageClient{err: fmt.Errorf("nsx error")}
	s := NewIPBlockUsageStorage(c, p)
	_, err := s.List(context.Background(), "ns1")
	require.Error(t, err)
}
func TestIPBlockUsageStorage_List_OK(t *testing.T) {
	p := singleVPCProvider{info: common.VPCResourceInfo{OrgID: "o1", ProjectID: "p1", VPCID: "vpc1"}}
	c := &nsx.Client{}
	c.ProjectIPBlockUsageClient = &fakeProjectIPBlockUsageClient{}
	s := NewIPBlockUsageStorage(c, p)
	list, err := s.List(context.Background(), "ns1")
	require.NoError(t, err)
	require.NotNil(t, list)
}
func TestIPBlockUsageStorage_List_Error(t *testing.T) {
	p := singleVPCProvider{info: common.VPCResourceInfo{OrgID: "o1", ProjectID: "p1", VPCID: "vpc1"}}
	c := &nsx.Client{}
	c.ProjectIPBlockUsageClient = &fakeProjectIPBlockUsageClient{err: fmt.Errorf("nsx error")}
	s := NewIPBlockUsageStorage(c, p)
	_, err := s.List(context.Background(), "ns1")
	require.Error(t, err)
}
func TestIPBlockUsageStorage_List_WithResults(t *testing.T) {
	p := singleVPCProvider{info: common.VPCResourceInfo{OrgID: "o1", ProjectID: "p1", VPCID: "vpc1"}}
	blockID := "block-1"
	// Project-scoped path: metadata.name must be "block-1"
	intent := "/orgs/default/projects/p1/infra/ip-blocks/" + blockID
	c := &nsx.Client{}
	c.ProjectIPBlockUsageClient = &fakeProjectIPBlockUsageClient{
		listResult: model.IpAddressBlockUsageList{
			Results: []model.IpAddressBlockUsage{{IntentPath: &intent}},
		},
	}
	s := NewIPBlockUsageStorage(c, p)
	list, err := s.List(context.Background(), "ns1")
	require.NoError(t, err)
	require.Len(t, list.Items, 1)
	assert.Equal(t, blockID, list.Items[0].Name)
}
func TestIPBlockUsageStorage_List_DeduplicatesProjects(t *testing.T) {
	// Two VPCs in the same project should only produce one List call.
	vpc1 := eas.VPCEntry{DisplayName: "vpc1", Info: common.VPCResourceInfo{OrgID: "o1", ProjectID: "p1", VPCID: "vpc1"}}
	vpc2 := eas.VPCEntry{DisplayName: "vpc2", Info: common.VPCResourceInfo{OrgID: "o1", ProjectID: "p1", VPCID: "vpc2"}}
	p := multiVPCProvider{entries: []eas.VPCEntry{vpc1, vpc2}}
	blockID := "block-1"
	intent := "/orgs/default/projects/p1/infra/ip-blocks/" + blockID
	c := &nsx.Client{}
	c.ProjectIPBlockUsageClient = &fakeProjectIPBlockUsageClient{
		listResult: model.IpAddressBlockUsageList{
			Results: []model.IpAddressBlockUsage{{IntentPath: &intent}},
		},
	}
	s := NewIPBlockUsageStorage(c, p)
	list, err := s.List(context.Background(), "ns1")
	require.NoError(t, err)
	// Only one set of results because both VPCs share project p1.
	require.Len(t, list.Items, 1)
	assert.Equal(t, blockID, list.Items[0].Name)
}
