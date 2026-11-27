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

type chainTGW struct {
	projects.TransitGatewaysClient
	obj    *model.TransitGateway
	writes int
	lost   bool
}

func (c *chainTGW) Get(_, _, id string) (model.TransitGateway, error) {
	if c.obj == nil || value(c.obj.Id) != id {
		return model.TransitGateway{}, vapierrors.NotFound{}
	}
	return *c.obj, nil
}
func (c *chainTGW) Patch(o, p, id string, x model.TransitGateway) error {
	c.writes++
	x.ParentPath = ptr.To("/orgs/" + o + "/projects/" + p)
	x.Path = ptr.To(*x.ParentPath + "/transit-gateways/" + id)
	c.obj = &x
	if c.lost {
		c.lost = false
		return errors.New("response lost")
	}
	return nil
}

type chainBlock struct {
	infra.IpBlocksClient
	objs   map[string]model.IpAddressBlock
	writes int
}

func (c *chainBlock) Get(_, _, id string, _ *bool) (model.IpAddressBlock, error) {
	x, ok := c.objs[id]
	if !ok {
		return x, vapierrors.NotFound{}
	}
	return x, nil
}
func (c *chainBlock) Patch(o, p, id string, x model.IpAddressBlock) error {
	c.writes++
	x.ParentPath = ptr.To("/orgs/" + o + "/projects/" + p + "/infra")
	x.Path = ptr.To(*x.ParentPath + "/ip-blocks/" + id)
	c.objs[id] = x
	return nil
}

type chainProfile struct {
	projects.VpcConnectivityProfilesClient
	obj    *model.VpcConnectivityProfile
	writes int
}

func (c *chainProfile) Get(_, _, _ string) (model.VpcConnectivityProfile, error) {
	if c.obj == nil {
		return model.VpcConnectivityProfile{}, vapierrors.NotFound{}
	}
	return *c.obj, nil
}
func (c *chainProfile) Patch(o, p, id string, x model.VpcConnectivityProfile) error {
	c.writes++
	x.ParentPath = ptr.To("/orgs/" + o + "/projects/" + p)
	x.Path = ptr.To(*x.ParentPath + "/vpc-connectivity-profiles/" + id)
	c.obj = &x
	return nil
}

type chainVPC struct {
	projects.VpcsClient
	obj    *model.Vpc
	writes int
}

func (c *chainVPC) Get(_, _, _ string) (model.Vpc, error) {
	if c.obj == nil {
		return model.Vpc{}, vapierrors.NotFound{}
	}
	return *c.obj, nil
}
func (c *chainVPC) Patch(o, p, id string, x model.Vpc) error {
	c.writes++
	x.ParentPath = ptr.To("/orgs/" + o + "/projects/" + p)
	x.Path = ptr.To(*x.ParentPath + "/vpcs/" + id)
	c.obj = &x
	return nil
}

type chainAttachment struct {
	vpcs.AttachmentsClient
	obj    *model.VpcAttachment
	writes int
}

func (c *chainAttachment) Get(_, _, _, _ string) (model.VpcAttachment, error) {
	if c.obj == nil {
		return model.VpcAttachment{}, vapierrors.NotFound{}
	}
	return *c.obj, nil
}
func (c *chainAttachment) Patch(o, p, v, id string, x model.VpcAttachment) error {
	c.writes++
	x.ParentPath = ptr.To("/orgs/" + o + "/projects/" + p + "/vpcs/" + v)
	x.Path = ptr.To(*x.ParentPath + "/attachments/" + id)
	c.obj = &x
	return nil
}
func chainService(tgw *chainTGW, blocks *chainBlock, profiles *chainProfile, vpc *chainVPC, attach *chainAttachment) *FabricService {
	return &FabricService{TransitGateways: TransitGatewayService{Client: tgw, Realizer: &snatReady{ready: true}, Store: NewResourceStore[model.TransitGateway]()}, IPBlocks: IPBlockService{Client: blocks, Realizer: &snatReady{ready: true}, Store: NewResourceStore[model.IpAddressBlock]()}, Profiles: ConnectivityProfileService{Client: profiles, Realizer: &snatReady{ready: true}, Store: NewResourceStore[model.VpcConnectivityProfile]()}, VPCs: VPCService{Client: vpc, Realizer: &snatReady{ready: true}, Store: NewResourceStore[model.Vpc]()}, Attachments: AttachmentService{Client: attach, Realizer: &snatReady{ready: true}, Store: NewResourceStore[model.VpcAttachment]()}, cluster: "cluster", initialized: true}
}
func TestFabricSNATRecoveryAndLiveChanges(t *testing.T) {
	ctx := context.Background()
	tgw := &chainTGW{lost: true}
	blocks := &chainBlock{objs: map[string]model.IpAddressBlock{}}
	profiles := &chainProfile{}
	vpc := &chainVPC{}
	attach := &chainAttachment{}
	s := chainService(tgw, blocks, profiles, vpc, attach)
	req := FabricPreparationRequest{ProjectPath: "/orgs/o/projects/p", Identity: "span", VPCPurpose: VPCPurposeLoadBalancer, Zones: []string{"zone"}, IPBlockID: "ordinary", IPBlockCIDR: "192.0.2.0/24", IPBlockAllowedIPs: []IPRange{{Start: "192.0.2.0", End: "192.0.2.255"}}}
	_, err := s.Prepare(ctx, req)
	require.ErrorContains(t, err, "response lost")
	var result Result
	for i := 0; i < 6; i++ {
		result, err = s.Prepare(ctx, req)
		require.NoError(t, err)
		if result.Ready {
			break
		}
	}
	require.True(t, result.Ready)
	require.Equal(t, []int{1, 1, 1, 1, 1}, []int{tgw.writes, blocks.writes, profiles.writes, vpc.writes, attach.writes})
	snatReq := SNATPreparationRequest{Identity: "transition", VPCPath: result.VPC.Path, ProfilePath: result.Profile.Path, IPAddress: "198.51.100.5", LBSize: "SMALL"}
	snat, err := NewSNATPreparationService(SNATPreparationDependencies{Cluster: "cluster", IPBlocks: blocks, Profiles: profiles, Allocations: &snatAllocations{}, LBServices: &snatLBs{}, Realizer: &snatReady{ready: true}})
	require.NoError(t, err)
	var sr SNATPreparationResult
	for i := 0; i < 5; i++ {
		sr, err = snat.Prepare(ctx, snatReq)
		require.NoError(t, err)
		if sr.Ready {
			break
		}
	}
	require.True(t, sr.Ready)
	s = chainService(tgw, blocks, profiles, vpc, attach)
	req.SNAT = &snatReq
	result, err = s.Prepare(ctx, req)
	require.NoError(t, err)
	require.True(t, result.Ready)
	require.Equal(t, 1, attach.writes)
	plan, _, err := buildSNATPlan(snatReq, "cluster", SNATPreparationResult{})
	require.NoError(t, err)
	block := blocks.objs[plan.blockID]
	block.Tags = nil
	blocks.objs[plan.blockID] = block
	_, err = s.Prepare(ctx, req)
	var conflict *ConflictError
	require.ErrorAs(t, err, &conflict)
	block.Tags = plan.tags
	blocks.objs[plan.blockID] = block
	profiles.obj.TransitGatewayPath = ptr.To("/wrong")
	_, err = s.Prepare(ctx, req)
	require.ErrorAs(t, err, &conflict)
	profiles.obj.TransitGatewayPath = ptr.To(result.TGW.Path)
	profiles.obj.ExternalIpBlocks = append(profiles.obj.ExternalIpBlocks, "/unrelated")
	_, err = s.Prepare(ctx, req)
	require.ErrorAs(t, err, &conflict)
}

func TestProfileGetRefreshesCachedObjectAndDeletion(t *testing.T) {
	path := "/orgs/o/projects/p/vpc-connectivity-profiles/profile"
	cached := model.VpcConnectivityProfile{Id: ptr.To("profile"), Path: &path, TransitGatewayPath: ptr.To("old")}
	store := NewResourceStore[model.VpcConnectivityProfile]()
	require.NoError(t, store.Put(path, &cached))
	live := cached
	live.TransitGatewayPath = ptr.To("new")
	client := &chainProfile{obj: &live}
	svc := ConnectivityProfileService{Client: client, Store: store}
	resource := Resource{Path: path, ID: "profile"}
	got, found, err := svc.Get(context.Background(), resource)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "new", got.Gateway)
	client.obj = nil
	_, found, err = svc.Get(context.Background(), resource)
	require.NoError(t, err)
	require.False(t, found)
	_, found = store.Get(path)
	require.False(t, found)
}

type recordingRealizer struct {
	ready bool
	err   error
	paths []string
}

func (r *recordingRealizer) Ready(_ context.Context, path string) (bool, error) {
	r.paths = append(r.paths, path)
	return r.ready, r.err
}

func TestResourceServicesIndependentlyCheckRealization(t *testing.T) {
	ctx := context.Background()
	tgw := &chainTGW{}
	blocks := &chainBlock{objs: map[string]model.IpAddressBlock{}}
	profiles := &chainProfile{}
	vpc := &chainVPC{}
	attach := &chainAttachment{}
	svc := chainService(tgw, blocks, profiles, vpc, attach)
	req := FabricPreparationRequest{ProjectPath: "/orgs/o/projects/p", Identity: "span", Zones: []string{"zone"}, IPBlockID: "ordinary", IPBlockCIDR: "192.0.2.0/24", IPBlockAllowedIPs: []IPRange{{Start: "192.0.2.0", End: "192.0.2.255"}}}
	var result Result
	for i := 0; i < 6; i++ {
		var err error
		result, err = svc.Prepare(ctx, req)
		require.NoError(t, err)
		if result.Ready {
			break
		}
	}
	require.True(t, result.Ready)
	checker := &recordingRealizer{}
	svc.TransitGateways.Realizer = checker
	svc.IPBlocks.Realizer = checker
	svc.Profiles.Realizer = checker
	svc.VPCs.Realizer = checker
	svc.Attachments.Realizer = checker
	for _, tc := range []struct {
		name     string
		resource Resource
		access   ResourceAccess
	}{{"tgw", result.TGW, svc.TransitGateways}, {"ipblock", result.IPBlock, svc.IPBlocks}, {"profile", result.Profile, svc.Profiles}, {"vpc", result.VPC, svc.VPCs}, {"attachment", result.Attachment, svc.Attachments}} {
		t.Run(tc.name, func(t *testing.T) {
			checker.paths = nil
			checker.ready = false
			checker.err = nil
			_, found, ready, err := tc.access.GetRealized(ctx, tc.resource)
			require.NoError(t, err)
			require.True(t, found)
			require.False(t, ready)
			require.Equal(t, []string{tc.resource.Path}, checker.paths)
			checker.ready = true
			_, found, ready, err = tc.access.GetRealized(ctx, tc.resource)
			require.NoError(t, err)
			require.True(t, found)
			require.True(t, ready)
			checker.err = errors.New("realization failure")
			_, found, ready, err = tc.access.GetRealized(ctx, tc.resource)
			require.True(t, found)
			require.False(t, ready)
			require.ErrorContains(t, err, tc.resource.Path)
		})
	}
}

func TestGetRealizedDistinguishesAbsentAndMissingRealizer(t *testing.T) {
	path := "/orgs/o/projects/p/vpc-connectivity-profiles/profile"
	resource := Resource{Kind: Profile, ID: "profile", Path: path}
	client := &chainProfile{}
	svc := ConnectivityProfileService{Client: client, Store: NewResourceStore[model.VpcConnectivityProfile]()}
	_, found, ready, err := svc.GetRealized(context.Background(), resource)
	require.NoError(t, err)
	require.False(t, found)
	require.False(t, ready)
	client.obj = &model.VpcConnectivityProfile{Id: ptr.To("profile"), Path: &path}
	_, found, ready, err = svc.GetRealized(context.Background(), resource)
	require.True(t, found)
	require.False(t, ready)
	require.ErrorContains(t, err, "realization service is required")
}

func TestRecoverReusedNonDefaultResourcesAfterSNAT(t *testing.T) {
	ctx := context.Background()
	tgw := &chainTGW{}
	blocks := &chainBlock{objs: map[string]model.IpAddressBlock{}}
	profiles := &chainProfile{}
	vpc := &chainVPC{}
	attach := &chainAttachment{}
	original := chainService(tgw, blocks, profiles, vpc, attach)
	req := FabricPreparationRequest{ProjectPath: "/orgs/o/projects/p", Identity: "original", VPCPurpose: VPCPurposeLoadBalancer, Zones: []string{"zone"}, TGWID: "existing-tgw", VPCID: "existing-vpc"}
	var before Result
	for i := 0; i < 5; i++ {
		var err error
		before, err = original.Prepare(ctx, req)
		require.NoError(t, err)
		if before.Ready {
			break
		}
	}
	require.True(t, before.Ready)
	snatReq := SNATPreparationRequest{Identity: "transition", VPCPath: before.VPC.Path, ProfilePath: before.Profile.Path, IPAddress: "198.51.100.5", LBSize: "SMALL"}
	snat, err := NewSNATPreparationService(SNATPreparationDependencies{Cluster: "cluster", IPBlocks: blocks, Profiles: profiles, Allocations: &snatAllocations{}, LBServices: &snatLBs{}, Realizer: &snatReady{ready: true}})
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		var state SNATPreparationResult
		state, err = snat.Prepare(ctx, snatReq)
		require.NoError(t, err)
		if state.Ready {
			break
		}
		if i == 4 {
			t.Fatal("SNAT did not become ready")
		}
	}
	// Populate the fresh service as a completed discovery would; its original
	// identity differs from that used to create the adopted resources.
	restarted := chainService(tgw, blocks, profiles, vpc, attach)
	require.NoError(t, restarted.TransitGateways.Store.Put(*tgw.obj.Path, tgw.obj))
	require.NoError(t, restarted.Profiles.Store.Put(*profiles.obj.Path, profiles.obj))
	require.NoError(t, restarted.VPCs.Store.Put(*vpc.obj.Path, vpc.obj))
	require.NoError(t, restarted.Attachments.Store.Put(*attach.obj.Path, attach.obj))
	retry := FabricPreparationRequest{ProjectPath: req.ProjectPath, Identity: "new-transition", VPCPurpose: VPCPurposeLoadBalancer, Zones: req.Zones, SNAT: &snatReq}
	after, err := restarted.Prepare(ctx, retry)
	require.NoError(t, err)
	require.True(t, after.Ready)
	require.Equal(t, before.TGW.Path, after.TGW.Path)
	require.Equal(t, before.Profile.Path, after.Profile.Path)
	require.Equal(t, before.VPC.Path, after.VPC.Path)
	require.Equal(t, before.Attachment.Path, after.Attachment.Path)
	require.Equal(t, []int{1, 1, 1, 1}, []int{tgw.writes, profiles.writes - 1, vpc.writes, attach.writes})
}

func TestPrepareResourceChecksConflictBeforeRealization(t *testing.T) {
	project := "/orgs/o/projects/p"
	path := project + "/vpc-connectivity-profiles/profile"
	expected := Resource{Kind: Profile, ID: "profile", Path: path, Parent: project, Gateway: project + "/transit-gateways/expected"}
	client := &chainProfile{obj: &model.VpcConnectivityProfile{Id: ptr.To(expected.ID), Path: &path, ParentPath: &project, TransitGatewayPath: ptr.To(project + "/transit-gateways/other")}}
	checker := &recordingRealizer{err: errors.New("realization error")}
	svc := &FabricService{Profiles: ConnectivityProfileService{Client: client, Store: NewResourceStore[model.VpcConnectivityProfile](), Realizer: checker}}
	for _, tc := range []struct {
		name           string
		realizationErr error
	}{{"realization error", errors.New("realization error")}, {"pending", nil}} {
		t.Run(tc.name, func(t *testing.T) {
			checker.paths = nil
			checker.ready = false
			checker.err = tc.realizationErr
			ready, err := svc.prepareResource(context.Background(), expected, "")
			var conflict *ConflictError
			require.ErrorAs(t, err, &conflict)
			require.False(t, ready)
			require.Empty(t, checker.paths)
		})
	}
	client.obj.TransitGatewayPath = ptr.To(expected.Gateway)
	checker.paths = nil
	checker.err = errors.New("realization error")
	_, err := svc.prepareResource(context.Background(), expected, "")
	require.ErrorContains(t, err, "realization error")
	require.Equal(t, []string{path}, checker.paths)
	checker.err = nil
	checker.ready = false
	ready, err := svc.prepareResource(context.Background(), expected, "")
	require.NoError(t, err)
	require.False(t, ready)
	checker.ready = true
	ready, err = svc.prepareResource(context.Background(), expected, "")
	require.NoError(t, err)
	require.True(t, ready)
}

func TestPrepareResourceRejectsVPCWithWrongOwnerOrPurpose(t *testing.T) {
	project := "/orgs/o/projects/p"
	path := project + "/vpcs/vpc"
	clusterTag := model.Tag{Scope: ptr.To(common.TagScopeCluster), Tag: ptr.To("cluster")}
	primaryPurposeTag := model.Tag{Scope: ptr.To(migrationVPCPurposeTagScope), Tag: ptr.To(string(VPCPurposePrimary))}
	tests := []struct {
		name string
		tags []model.Tag
	}{
		{
			name: "cluster request rejects tenant-owned VPC",
			tags: []model.Tag{clusterTag, primaryPurposeTag, {Scope: ptr.To(common.TagScopeNamespaceUID), Tag: ptr.To("namespace-a")}},
		},
		{
			name: "rejects VPC without purpose tag",
			tags: []model.Tag{clusterTag},
		},
		{
			name: "rejects VPC with a different purpose",
			tags: []model.Tag{clusterTag, {Scope: ptr.To(migrationVPCPurposeTagScope), Tag: ptr.To(string(VPCPurposeLoadBalancer))}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &chainVPC{obj: &model.Vpc{Id: ptr.To("vpc"), Path: &path, ParentPath: &project, Tags: tc.tags}}
			checker := &recordingRealizer{}
			service := &FabricService{VPCs: VPCService{Client: client, Store: NewResourceStore[model.Vpc](), Realizer: checker}}
			expected := Resource{
				Kind: VPC, ID: "vpc", Path: path, Parent: project,
				Tags: []model.Tag{clusterTag, primaryPurposeTag},
			}

			ready, err := service.prepareResource(context.Background(), expected, "")
			var conflict *ConflictError
			require.ErrorAs(t, err, &conflict)
			require.False(t, ready)
			require.Empty(t, checker.paths)
		})
	}
}
