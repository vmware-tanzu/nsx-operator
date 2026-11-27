/*
Copyright © 2026 Broadcom, Inc. All Rights Reserved.

	SPDX-License-Identifier: Apache-2.0
*/
package fabric

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	vapierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/infra"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/vpcs"
	"k8s.io/utils/ptr"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type snatBlocks struct {
	infra.IpBlocksClient
	object *model.IpAddressBlock
	lost   bool
	writes int
}

func (c *snatBlocks) Get(_, _, _ string, _ *bool) (model.IpAddressBlock, error) {
	if c.object == nil {
		return model.IpAddressBlock{}, vapierrors.NotFound{}
	}
	return *c.object, nil
}
func (c *snatBlocks) Patch(o, p, id string, obj model.IpAddressBlock) error {
	c.writes++
	obj.Path = ptr.To("/orgs/" + o + "/projects/" + p + "/infra/ip-blocks/" + id)
	c.object = &obj
	if c.lost {
		c.lost = false
		return errors.New("response lost")
	}
	return nil
}

type snatProfiles struct {
	projects.VpcConnectivityProfilesClient
	object model.VpcConnectivityProfile
	writes int
}

func (c *snatProfiles) Get(_, _, _ string) (model.VpcConnectivityProfile, error) {
	return c.object, nil
}
func (c *snatProfiles) Patch(_, _, _ string, obj model.VpcConnectivityProfile) error {
	c.writes++
	c.object = obj
	return nil
}

type snatAllocations struct {
	vpcs.IpAddressAllocationsClient
	object *model.VpcIpAddressAllocation
	writes int
}

func (c *snatAllocations) Get(_, _, _, _ string) (model.VpcIpAddressAllocation, error) {
	if c.object == nil {
		return model.VpcIpAddressAllocation{}, vapierrors.NotFound{}
	}
	return *c.object, nil
}
func (c *snatAllocations) Patch(o, p, v, id string, obj model.VpcIpAddressAllocation) error {
	c.writes++
	obj.Path = ptr.To("/orgs/" + o + "/projects/" + p + "/vpcs/" + v + "/ip-address-allocations/" + id)
	c.object = &obj
	return nil
}

type snatLBs struct {
	vpcs.VpcLbsClient
	object *model.LBService
	writes int
}

func (c *snatLBs) Get(_, _, _, _ string) (model.LBService, error) {
	if c.object == nil {
		return model.LBService{}, vapierrors.NotFound{}
	}
	return *c.object, nil
}
func (c *snatLBs) Patch(o, p, v, id string, obj model.LBService, _ *string) error {
	c.writes++
	obj.Path = ptr.To("/orgs/" + o + "/projects/" + p + "/vpcs/" + v + "/vpc-lbs/" + id)
	c.object = &obj
	return nil
}

type snatReady struct{ ready bool }

func (r *snatReady) Ready(context.Context, string) (bool, error) { return r.ready, nil }

func TestSNATPreparationRetriesAndReusesResources(t *testing.T) {
	profilePath := "/orgs/o/projects/p/vpc-connectivity-profiles/profile"
	blocks := &snatBlocks{lost: true}
	profiles := &snatProfiles{object: model.VpcConnectivityProfile{Id: ptr.To("profile"), Path: &profilePath, Tags: []model.Tag{{Scope: ptr.To(common.TagScopeCluster), Tag: ptr.To("cluster")}}}}
	allocations := &snatAllocations{}
	lbs := &snatLBs{}
	realizer := &snatReady{ready: true}
	service, err := NewSNATPreparationService(SNATPreparationDependencies{Cluster: "cluster", IPBlocks: blocks, Profiles: profiles, Allocations: allocations, LBServices: lbs, Realizer: realizer})
	require.NoError(t, err)
	req := SNATPreparationRequest{Identity: "transition", VPCPath: "/orgs/o/projects/p/vpcs/v", ProfilePath: profilePath, IPAddress: "192.0.2.5", LBSize: "SMALL"}
	_, err = service.Prepare(context.Background(), req)
	require.ErrorContains(t, err, "response lost")
	require.Equal(t, []string{"192.0.2.5/32"}, blocks.object.Cidrs)
	realizer.ready = false
	result, err := service.Prepare(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, Kind("snat-ip-block"), result.Pending.Kind)
	require.Equal(t, 1, blocks.writes)
	realizer.ready = true
	for _, kind := range []Kind{"snat-profile", "snat-ip-allocation", "snat-lb-service"} {
		result, err = service.Prepare(context.Background(), req)
		require.NoError(t, err)
		require.Equal(t, kind, result.Pending.Kind)
	}
	result, err = service.Prepare(context.Background(), req)
	require.NoError(t, err)
	require.True(t, result.Ready)
	result, err = service.Prepare(context.Background(), req)
	require.NoError(t, err)
	require.True(t, result.Ready)
	require.Equal(t, []int{1, 1, 1, 1}, []int{blocks.writes, profiles.writes, allocations.writes, lbs.writes})
	allocations.object.AllocationIp = ptr.To("192.0.2.6")
	_, err = service.Prepare(context.Background(), req)
	var conflict *ConflictError
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, Kind("snat-ip-allocation"), conflict.Kind)
	allocations.object.AllocationIp = ptr.To(req.IPAddress)
	profiles.object.MarkedForDelete = ptr.To(true)
	_, err = service.Prepare(context.Background(), req)
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, Profile, conflict.Kind)
	require.Equal(t, 1, profiles.writes)
}

func TestSNATPreparationRejectsInvalidInputsBeforeNSXCalls(t *testing.T) {
	service, err := NewSNATPreparationService(SNATPreparationDependencies{Cluster: "cluster", IPBlocks: &snatBlocks{}, Profiles: &snatProfiles{}, Allocations: &snatAllocations{}, LBServices: &snatLBs{}, Realizer: &snatReady{ready: true}})
	require.NoError(t, err)
	valid := SNATPreparationRequest{Identity: "transition", VPCPath: "/orgs/o/projects/p/vpcs/v", ProfilePath: "/orgs/o/projects/p/vpc-connectivity-profiles/profile", IPAddress: "192.0.2.5", LBSize: model.LBService_SIZE_SMALL}
	for _, path := range []string{"foo/orgs/o/projects/p/vpc-connectivity-profiles/profile", "/orgs/o/projects/p/vpc-connectivity-profiles/..", "/orgs/o/projects/p/vpc-connectivity-profiles/.", "/orgs/o/projects/p/vpc-connectivity-profiles/id/extra", "/orgs/o/projects/p/vpc-connectivity-profiles/"} {
		req := valid
		req.ProfilePath = path
		_, err = service.Prepare(context.Background(), req)
		require.ErrorContains(t, err, "invalid target connectivity profile path", path)
	}
	invalid := valid
	invalid.LBSize = "invalid"
	_, err = service.Prepare(context.Background(), invalid)
	require.ErrorContains(t, err, "unsupported LB size")
	for _, size := range []string{model.LBService_SIZE_SMALL, model.LBService_SIZE_MEDIUM, model.LBService_SIZE_LARGE, model.LBService_SIZE_XLARGE, model.LBService_SIZE_DLB} {
		req := valid
		req.LBSize = size
		_, _, err = buildSNATPlan(req, "cluster", SNATPreparationResult{})
		require.NoError(t, err, size)
	}
}
