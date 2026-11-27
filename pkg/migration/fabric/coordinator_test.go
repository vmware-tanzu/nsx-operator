/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package fabric

import (
	"context"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/utils/ptr"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/inspection"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type coordinatorCheckpointStoreStub struct {
	items map[CheckpointKey]PreparationCheckpoint
	saves int
}

func (s *coordinatorCheckpointStoreStub) Load(_ context.Context, key CheckpointKey) (PreparationCheckpoint, string, bool, error) {
	checkpoint, found := s.items[key]
	return checkpoint, "revision", found, nil
}

func (s *coordinatorCheckpointStoreStub) Save(_ context.Context, checkpoint PreparationCheckpoint, _ string) (string, error) {
	if s.items == nil {
		s.items = make(map[CheckpointKey]PreparationCheckpoint)
	}
	s.items[checkpoint.Key] = checkpoint
	s.saves++
	return "next-revision", nil
}

type coordinatorResolverStub struct {
	identities FrozenIdentities
	requests   []FabricPreparationRequest
}

func (s *coordinatorResolverStub) ResolveIdentities(_ context.Context, request FabricPreparationRequest) (FrozenIdentities, error) {
	s.requests = append(s.requests, request)
	return s.identities, nil
}

type coordinatorPreparerStub struct{ requests []FabricPreparationRequest }

func (s *coordinatorPreparerStub) Prepare(_ context.Context, request FabricPreparationRequest) (Result, error) {
	s.requests = append(s.requests, request)
	return Result{Ready: true, Identities: *request.Frozen}, nil
}

func (s *coordinatorPreparerStub) PrepareIPBlock(context.Context, FabricPreparationRequest) (bool, error) {
	return true, nil
}

type coordinatorInspectorStub struct {
	result inspection.Result
	calls  int
}

func (s *coordinatorInspectorStub) InspectNamespaces(context.Context, []string, []api.DHCPNetworkConfiguration) (inspection.Result, error) {
	s.calls++
	return s.result, nil
}

func TestInspectAndPrepareNamespacesUsesInspectorBeforeFabric(t *testing.T) {
	inspector := &coordinatorInspectorStub{result: inspection.Result{Networks: []inspection.Network{
		coordinatorNetwork("vdn-a", "192.0.2.0/24", "192.0.2.10", "192.0.2.30", ""),
	}}}
	resolver := &coordinatorResolverStub{identities: FrozenIdentities{TGWID: "tgw", ProfileID: "profile", VPCID: "vpc"}}
	preparer := &coordinatorPreparerStub{}
	coordinator := PreparationCoordinator{
		Inspector: inspector, Resolver: resolver, Preparer: preparer, Checkpoints: &coordinatorCheckpointStoreStub{},
	}
	options := PreparationOptions{TransitionUID: "transition-uid", ActionGeneration: 4, ProjectPath: "/orgs/o/projects/p/"}
	result, err := coordinator.InspectAndPrepareNamespaces(context.Background(), []string{"tenant"}, nil, options)
	require.NoError(t, err)
	require.True(t, result.Ready)
	require.Equal(t, 1, inspector.calls)
	require.Len(t, resolver.requests, 1)
	require.Len(t, preparer.requests, 1)
	require.Equal(t, "/orgs/o/projects/p", preparer.requests[0].ProjectPath)
	require.Equal(t, VPCPurposeWorkload, preparer.requests[0].VPCPurpose)
}

func TestPrepareInspectedUsesMergedTargetIdentityFromGroup(t *testing.T) {
	first := coordinatorNetwork("vdn-a", "192.0.2.0/24", "192.0.2.10", "192.0.2.30", "")
	second := coordinatorNetwork("vdn-b", "198.51.100.0/24", "198.51.100.10", "198.51.100.30", "")
	first.Target.TGWName = ""
	first.Target.VPCName = ""
	first.Target.TGWID = ""
	first.Target.VPCID = ""
	second.Target.TGWID = ""
	second.Target.VPCID = ""
	second.Target.TGWName = "custom-tgw"
	second.Target.VPCName = "custom-vpc"
	resolver := &coordinatorResolverStub{identities: FrozenIdentities{TGWID: "custom-tgw", ProfileID: "profile", VPCID: "custom-vpc"}}
	coordinator := PreparationCoordinator{
		Resolver: resolver, Preparer: &coordinatorPreparerStub{},
		Checkpoints: &coordinatorCheckpointStoreStub{},
	}
	_, err := coordinator.PrepareInspected(context.Background(), inspection.Result{Networks: []inspection.Network{first, second}},
		PreparationOptions{TransitionUID: "transition-uid", ActionGeneration: 4, ProjectPath: "/orgs/o/projects/p"})
	require.NoError(t, err)
	require.Len(t, resolver.requests, 1)
	require.Equal(t, "custom-tgw", resolver.requests[0].TGWID)
	require.Equal(t, "custom-vpc", resolver.requests[0].VPCID)
}

func TestProfileBlockExpansionPreservesLiveMetadata(t *testing.T) {
	project := "/orgs/o/projects/p"
	path := project + "/vpc-connectivity-profiles/profile"
	gateway := project + "/transit-gateways/tgw"
	blockA := project + "/infra/ip-blocks/block-a"
	blockB := project + "/infra/ip-blocks/block-b"
	clusterTag := model.Tag{Scope: ptr.To(common.TagScopeCluster), Tag: ptr.To("cluster")}
	customTag := model.Tag{Scope: ptr.To("custom-scope"), Tag: ptr.To("admin-tag")}
	client := &chainProfile{obj: &model.VpcConnectivityProfile{
		Id: ptr.To("profile"), Path: &path, ParentPath: &project, DisplayName: ptr.To("Administrator Profile"),
		Description: ptr.To("preserve this description"), TransitGatewayPath: &gateway,
		ExternalIpBlocks: []string{blockA}, Tags: []model.Tag{clusterTag, customTag},
	}}
	service := &FabricService{Profiles: ConnectivityProfileService{Client: client, Store: NewResourceStore[model.VpcConnectivityProfile]()}}
	expected := Resource{Kind: Profile, ID: "profile", Path: path, Parent: project, Gateway: gateway, Blocks: []string{blockA, blockB}, Tags: []model.Tag{clusterTag}}
	ready, err := service.prepareResource(context.Background(), expected, "")
	require.NoError(t, err)
	require.False(t, ready, "the profile update must be re-read on a later reconcile")
	require.Equal(t, []string{blockA, blockB}, client.obj.ExternalIpBlocks)
	require.Equal(t, "Administrator Profile", *client.obj.DisplayName)
	require.Equal(t, "preserve this description", *client.obj.Description)
	require.Equal(t, []model.Tag{clusterTag, customTag}, client.obj.Tags)
}

func TestCoordinatorUsesCompleteProfileBlocksForIdentityAndIncrementalPrepare(t *testing.T) {
	ctx := context.Background()
	service := chainService(&chainTGW{}, &chainBlock{objs: map[string]model.IpAddressBlock{}}, &chainProfile{}, &chainVPC{}, &chainAttachment{})
	// An empty profile must not be selected for a request whose shared topology
	// requires both network blocks.
	emptyProfilePath := "/orgs/o/projects/p/vpc-connectivity-profiles/empty-profile"
	emptyProfile := model.VpcConnectivityProfile{
		Id: ptr.To("empty-profile"), ParentPath: ptr.To("/orgs/o/projects/p"), Path: &emptyProfilePath,
		TransitGatewayPath: ptr.To("/orgs/o/projects/p/transit-gateways/tgw"),
		Tags:               []model.Tag{{Scope: common.String(common.TagScopeCluster), Tag: common.String("cluster")}},
	}
	require.NoError(t, service.Profiles.Store.Put(emptyProfilePath, &emptyProfile))

	checkpoints := &coordinatorCheckpointStoreStub{}
	coordinator := PreparationCoordinator{Resolver: service, Preparer: service, Checkpoints: checkpoints}
	inspected := inspection.Result{Networks: []inspection.Network{
		coordinatorNetwork("vdn-a", "192.0.2.0/24", "192.0.2.10", "192.0.2.30", ""),
		coordinatorNetwork("vdn-b", "198.51.100.0/24", "198.51.100.10", "198.51.100.30", "vdn-a"),
	}}
	options := PreparationOptions{TransitionUID: "transition-uid", ActionGeneration: 4, ProjectPath: "/orgs/o/projects/p"}

	var result InspectedPreparation
	var err error
	for attempt := 0; attempt < 30; attempt++ {
		result, err = coordinator.PrepareInspected(ctx, inspected, options)
		require.NoError(t, err, "coordinator attempt %d", attempt)
		if result.Ready {
			break
		}
	}
	require.True(t, result.Ready, "both VDN blocks and shared fabric should converge")
	require.Len(t, result.Networks, 2)
	require.Equal(t, 1, checkpoints.saves)
	require.Len(t, service.Profiles.Client.(*chainProfile).obj.ExternalIpBlocks, 2)
	require.ElementsMatch(t, []string{
		"/orgs/o/projects/p/infra/ip-blocks/" + networkBlockID(inspected.Networks[0], options),
		"/orgs/o/projects/p/infra/ip-blocks/" + networkBlockID(inspected.Networks[1], options),
	}, service.Profiles.Client.(*chainProfile).obj.ExternalIpBlocks)
	require.NotEqual(t, "empty-profile", result.Networks[0].Request.Frozen.ProfileID)
	require.Equal(t, result.Networks[0].Request.Frozen, result.Networks[1].Request.Frozen)
	require.ElementsMatch(t, result.Networks[0].Request.ProfileIPBlockIDs, result.Networks[1].Request.ProfileIPBlockIDs)
	require.Len(t, result.Networks[0].Request.ProfileIPBlockIDs, 2)
}

func TestCoordinatorReservationExclusionsClipToNetworkCIDR(t *testing.T) {
	exclusions, err := applicableExclusions("192.0.2.0/24", []IPRange{
		{Start: "192.0.1.250", End: "192.0.2.10"},
		{Start: "203.0.113.1", End: "203.0.113.8"},
	}, "192.0.2.20")
	require.NoError(t, err)
	require.Equal(t, []IPRange{
		{Start: "192.0.2.0", End: "192.0.2.10"},
		{Start: "192.0.2.20", End: "192.0.2.20"},
	}, exclusions)

	otherNetwork, err := applicableExclusions("198.51.100.0/24", []IPRange{{Start: "192.0.2.5", End: "192.0.2.12"}}, "192.0.2.20")
	require.NoError(t, err)
	require.Empty(t, otherNetwork)
}

func TestCoordinatorRestoresCheckpointedIdentitiesOnRetry(t *testing.T) {
	ctx := context.Background()
	identities := FrozenIdentities{TGWID: "tgw", ProfileID: "profile", VPCID: "vpc"}
	resolver := &coordinatorResolverStub{identities: identities}
	preparer := &coordinatorPreparerStub{}
	checkpoints := &coordinatorCheckpointStoreStub{}
	coordinator := PreparationCoordinator{Resolver: resolver, Preparer: preparer, Checkpoints: checkpoints}
	inspected := inspection.Result{Networks: []inspection.Network{coordinatorNetwork("vdn-a", "192.0.2.0/24", "192.0.2.10", "192.0.2.30", "")}}
	options := PreparationOptions{TransitionUID: "transition-uid", ActionGeneration: 4, ProjectPath: "/orgs/o/projects/p"}

	_, err := coordinator.PrepareInspected(ctx, inspected, options)
	require.NoError(t, err)
	_, err = coordinator.PrepareInspected(ctx, inspected, options)
	require.NoError(t, err)
	require.Len(t, resolver.requests, 1)
	require.Equal(t, identities, *preparer.requests[0].Frozen)
	require.Equal(t, identities, *preparer.requests[1].Frozen)
	require.Equal(t, resolver.requests[0].ProfileIPBlockIDs, preparer.requests[0].ProfileIPBlockIDs)
}

func coordinatorNetwork(vdn, cidr, start, end, sharedWith string) inspection.Network {
	prefix := netip.MustParsePrefix(cidr)
	return inspection.Network{
		Source: inspection.Source{VDNName: vdn, Topologies: []inspection.Topology{{
			VCenterUUID: "vc", PortGroupID: "dvportgroup-" + vdn, SwitchUUID: "switch", ZoneIDs: []string{"zone-a"},
		}}},
		Addressing: inspection.Addressing{CIDR: prefix, Ranges: []api.IPAddressRange{{Start: start, End: end}}},
		Target: inspection.TargetDecision{
			TGWID: "tgw", VPCID: "vpc", VDSUUIDs: []string{"switch"}, SharedWithVDN: sharedWith,
		},
	}
}
