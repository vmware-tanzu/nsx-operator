/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package fabric

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

func TestIPBlockExclusionsCreationAndReuse(t *testing.T) {
	req := FabricPreparationRequest{ProjectPath: "/orgs/o/projects/p", Identity: "test", Zones: []string{"zone"}, VPCPurpose: VPCPurposeLoadBalancer,
		IPBlockID: "vip", IPBlockCIDR: "192.0.2.0/24", IPBlockAllowedIPs: []IPRange{{Start: "192.0.2.0", End: "192.0.2.255"}}, IPBlockSubnetExclusive: true,
		IPBlockExcludedIPs: []IPRange{{Start: "192.0.2.10", End: "192.0.2.20"}}, SNAT: &SNATPreparationRequest{IPAddress: "192.0.2.15"}}
	require.NoError(t, validate(req))
	blockClient := &chainBlock{objs: map[string]model.IpAddressBlock{}}
	service := IPBlockService{Client: blockClient, Store: NewResourceStore[model.IpAddressBlock]()}
	want := service.expected(req, req.ProjectPath)
	require.Equal(t, []IPRange{{Start: "192.0.2.10", End: "192.0.2.20"}}, want.ExcludedIPs)
	require.NoError(t, service.Create(context.Background(), want, nil))
	created := blockClient.objs["vip"]
	require.Equal(t, []string{"192.0.2.0/24"}, created.Cidrs)
	require.True(t, *created.SubnetExclusive)
	require.Equal(t, "192.0.2.10", *created.ExcludedIps[0].Start)
	require.Equal(t, "192.0.2.20", *created.ExcludedIps[0].End)
	actual, found, err := service.Get(context.Background(), want)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, compatible(want, actual))
	actual.ExcludedIPs = nil
	require.False(t, compatible(want, actual))
	actual = want
	actual.SubnetExclusive = false
	require.False(t, compatible(want, actual))

	req.IPBlockExcludedIPs = nil
	want = service.expected(req, req.ProjectPath)
	require.Equal(t, []IPRange{{Start: "192.0.2.15", End: "192.0.2.15"}}, want.ExcludedIPs)
	req.SNAT.IPAddress = "198.51.100.1"
	require.Empty(t, service.expected(req, req.ProjectPath).ExcludedIPs)
}

func TestIPBlockInvalidExclusionsFailBeforeWrite(t *testing.T) {
	base := FabricPreparationRequest{ProjectPath: "/orgs/o/projects/p", Identity: "test", Zones: []string{"zone"}, IPBlockID: "block", IPBlockCIDR: "192.0.2.0/24", IPBlockAllowedIPs: []IPRange{{Start: "192.0.2.0", End: "192.0.2.255"}}}
	for _, r := range []IPRange{{"192.0.2.20", "192.0.2.10"}, {"192.0.2.1", "198.51.100.1"}, {"invalid", "192.0.2.1"}, {"::1", "::2"}} {
		req := base
		req.IPBlockExcludedIPs = []IPRange{r}
		require.Error(t, validate(req))
	}
	base.IPBlockAllowedIPs = nil
	require.ErrorContains(t, validate(base), "complete source address ranges")
}

func TestFabricRejectsUnsupportedSourceInputs(t *testing.T) {
	base := FabricPreparationRequest{ProjectPath: "/orgs/o/projects/p", Identity: "test", Zones: []string{"zone"}}
	for _, vds := range []string{"", "  "} {
		req := base
		req.VDS = []string{vds}
		require.ErrorContains(t, validate(req), "VDS identity")
	}
	req := base
	req.VDS = []string{"dvs-1"}
	require.NoError(t, validate(req))
	req.IPBlockID = "block"
	req.IPBlockCIDR = "2001:db8::/64"
	req.IPBlockAllowedIPs = []IPRange{{Start: "2001:db8::1", End: "2001:db8::2"}}
	require.ErrorContains(t, validate(req), "invalid IP block CIDR")
}

func TestIPBlockExclusionsFromSourcePools(t *testing.T) {
	req := FabricPreparationRequest{IPBlockCIDR: "192.0.2.0/24", IPBlockAllowedIPs: []IPRange{
		{Start: "192.0.2.100", End: "192.0.2.150"},
		{Start: "192.0.2.10", End: "192.0.2.50"},
		{Start: "192.0.2.40", End: "192.0.2.110"},
	}, IPBlockExcludedIPs: []IPRange{{Start: "192.0.2.25", End: "192.0.2.25"}}}
	ranges, err := requestIPBlockExclusions(req)
	require.NoError(t, err)
	require.Equal(t, []IPRange{{"192.0.2.0", "192.0.2.9"}, {"192.0.2.25", "192.0.2.25"}, {"192.0.2.151", "192.0.2.255"}}, ranges)
	req.IPBlockAllowedIPs = []IPRange{{Start: "192.0.2.0", End: "192.0.2.255"}}
	req.IPBlockExcludedIPs = nil
	ranges, err = requestIPBlockExclusions(req)
	require.NoError(t, err)
	require.Empty(t, ranges)
	req.IPBlockAllowedIPs = []IPRange{{Start: "192.0.2.1", End: "192.0.3.1"}}
	_, err = requestIPBlockExclusions(req)
	require.Error(t, err)
}

func TestPrepareRejectsExistingIPBlockWithDifferentExclusions(t *testing.T) {
	blocks := &chainBlock{objs: map[string]model.IpAddressBlock{}}
	s := chainService(&chainTGW{}, blocks, &chainProfile{}, &chainVPC{}, &chainAttachment{})
	req := FabricPreparationRequest{ProjectPath: "/orgs/o/projects/p", Identity: "test", Zones: []string{"zone"},
		IPBlockID: "block", IPBlockCIDR: "192.0.2.0/24", IPBlockAllowedIPs: []IPRange{{"192.0.2.10", "192.0.2.20"}}}
	var result Result
	var err error
	for i := 0; i < 8; i++ {
		result, err = s.Prepare(context.Background(), req)
		require.NoError(t, err)
		if result.Ready {
			break
		}
	}
	require.True(t, result.Ready)
	block := blocks.objs["block"]
	block.ExcludedIps = nil
	blocks.objs["block"] = block
	_, err = s.Prepare(context.Background(), req)
	var conflict *ConflictError
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, IPBlock, conflict.Kind)
}

func TestResourceScopeRejectsMalformedPaths(t *testing.T) {
	for _, tc := range []struct {
		kind Kind
		path string
	}{
		{TGW, "/orgs/o/projects/p/transit-gateways/id"},
		{IPBlock, "/orgs/o/projects/p/infra/ip-blocks/id"},
		{Profile, "/orgs/o/projects/p/vpc-connectivity-profiles/id"},
		{VPC, "/orgs/o/projects/p/vpcs/id"},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			o, p, err := scope(Resource{Kind: tc.kind, ID: "id", Path: tc.path}, tc.kind)
			require.NoError(t, err)
			require.Equal(t, "o", o)
			require.Equal(t, "p", p)
			for _, path := range []string{tc.path + "/extra", tc.path + "/", "/wrong/o/projects/p/vpcs/id", "/orgs/../projects/p/vpcs/id", "/orgs/o/projects/p/vpcs/other"} {
				_, _, err = scope(Resource{Kind: tc.kind, ID: "id", Path: path}, tc.kind)
				require.Error(t, err, path)
			}
			_, _, err = scope(Resource{Kind: Attachment, ID: "id", Path: tc.path}, tc.kind)
			require.Error(t, err)
		})
	}
}

func TestIPBlockCreateRejectsMismatchedPathBeforePatch(t *testing.T) {
	blocks := &chainBlock{objs: map[string]model.IpAddressBlock{}}
	service := IPBlockService{Client: blocks, Store: NewResourceStore[model.IpAddressBlock]()}
	r := Resource{Kind: IPBlock, ID: "id", Path: "/orgs/o/projects/p/infra/ip-blocks/other", CIDR: "192.0.2.0/24"}
	require.Error(t, service.Create(context.Background(), r, nil))
	_, _, err := service.Get(context.Background(), r)
	require.Error(t, err)
	require.Zero(t, blocks.writes)
}
