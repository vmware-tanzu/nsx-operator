/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package fabric

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/utils/ptr"

	"github.com/vmware-tanzu/nsx-operator/pkg/migration/nsxworkflow"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestAttachmentScopeRejectsMalformedPaths(t *testing.T) {
	for _, path := range []string{"/orgs/o/projects/p/vpcs", "/orgs/o/projects/p/wrong/v/attachments/default", "/orgs/o/projects/p/vpcs/v/attachments/other", "/orgs/o/projects/p/vpcs//attachments/default"} {
		t.Run(path, func(t *testing.T) {
			_, _, _, err := attachmentScope(Resource{Path: path, ID: "default"})
			require.Error(t, err)
		})
	}
	o, p, v, err := attachmentScope(Resource{Path: "/orgs/o/projects/p/vpcs/v/attachments/default", ID: "default"})
	require.NoError(t, err)
	require.Equal(t, []string{"o", "p", "v"}, []string{o, p, v})
}

func TestIPBlockCIDRSupportsCurrentAndLegacyFields(t *testing.T) {
	for _, block := range []model.IpAddressBlock{
		{Cidrs: []string{"192.0.2.0/24"}},
		{Cidr: ptr.To("192.0.2.0/24")},
	} {
		cidr, err := ipBlockCIDR(block)
		require.NoError(t, err)
		require.Equal(t, "192.0.2.0/24", cidr)
	}
	_, err := ipBlockCIDR(model.IpAddressBlock{Cidrs: []string{"192.0.2.0/24", "198.51.100.0/24"}})
	require.Error(t, err)
}

func TestIPBlockResourceReportsVisibilityMismatch(t *testing.T) {
	path := "/orgs/o/projects/p/infra/ip-blocks/block"
	block := model.IpAddressBlock{
		Id: ptr.To("block"), Path: &path, Cidr: ptr.To("192.0.2.0/24"),
		Visibility: ptr.To(model.IpAddressBlock_VISIBILITY_PRIVATE),
	}
	_, found, err := ipBlockResource(block)
	require.True(t, found)
	require.EqualError(t, err, "IP block /orgs/o/projects/p/infra/ip-blocks/block has visibility \"PRIVATE\", expected \"EXTERNAL\"")
}

func TestReuseCandidatesStayInProjectAndIgnoreDeleting(t *testing.T) {
	project := "/orgs/o/projects/p"
	tags := []model.Tag{{Scope: ptr.To(common.TagScopeCluster), Tag: ptr.To("cluster")}}
	spanValue, errs := common.NewConverter().ConvertToVapi(model.ZoneBasedSpan{Type_: model.ZoneBasedSpan__TYPE_IDENTIFIER, ZoneExternalIds: []string{"zone"}}, model.ZoneBasedSpanBindingType())
	require.Empty(t, errs)
	store := NewResourceStore[model.TransitGateway]()
	for _, tc := range []struct {
		id, project string
		deleted     bool
	}{{"other", "/orgs/o/projects/q", false}, {"deleting", project, true}, {"valid", project, false}} {
		path := tc.project + "/transit-gateways/" + tc.id
		require.NoError(t, store.Put(path, &model.TransitGateway{Id: ptr.To(tc.id), Path: ptr.To(path), ParentPath: ptr.To(tc.project), Tags: tags, Span: spanValue.(*data.StructValue), MarkedForDelete: ptr.To(tc.deleted)}))
	}
	id, err := (TransitGatewayService{Store: store}).reusableID(FabricPreparationRequest{ProjectPath: project, Zones: []string{"zone"}}, "cluster", "fallback")
	require.NoError(t, err)
	require.Equal(t, "valid", id)
	profiles := NewResourceStore[model.VpcConnectivityProfile]()
	for _, tc := range []struct {
		id, project string
		deleted     bool
	}{{"other", "/orgs/o/projects/q", false}, {"deleting", project, true}, {"valid", project, false}} {
		path := tc.project + "/vpc-connectivity-profiles/" + tc.id
		require.NoError(t, profiles.Put(path, &model.VpcConnectivityProfile{Id: ptr.To(tc.id), Path: ptr.To(path), ParentPath: ptr.To(tc.project), Tags: tags, TransitGatewayPath: ptr.To(project + "/transit-gateways/valid"), MarkedForDelete: ptr.To(tc.deleted)}))
	}
	id, err = (ConnectivityProfileService{Store: profiles}).reusableID("cluster", project, "valid", project+"/transit-gateways/valid", "", "fallback", "")
	require.NoError(t, err)
	require.Equal(t, "valid", id)
}

func TestVPCReuseIsScopedToPurpose(t *testing.T) {
	project := "/orgs/o/projects/p"
	profilePath := project + "/vpc-connectivity-profiles/shared"
	primaryID := name("", "vpc", "primary")
	primaryPath := project + "/vpcs/" + primaryID
	tags := []model.Tag{
		{Scope: ptr.To(common.TagScopeCluster), Tag: ptr.To("cluster")},
		{Scope: ptr.To(migrationVPCPurposeTagScope), Tag: ptr.To(string(VPCPurposePrimary))},
	}
	vpcs := NewResourceStore[model.Vpc]()
	require.NoError(t, vpcs.Put(primaryPath, &model.Vpc{Id: ptr.To(primaryID), Path: &primaryPath, ParentPath: &project, Tags: tags}))
	attachments := NewResourceStore[model.VpcAttachment]()
	attachmentPath := primaryPath + "/attachments/default"
	require.NoError(t, attachments.Put(attachmentPath, &model.VpcAttachment{
		Id: ptr.To("default"), Path: &attachmentPath, ParentPath: &primaryPath,
		VpcConnectivityProfile: &profilePath,
	}))

	service := VPCService{Store: vpcs}
	attachmentService := AttachmentService{Store: attachments}
	cluster := "cluster"
	loadBalancerRequest := FabricPreparationRequest{Identity: "load-balancer-vdn", VPCPurpose: VPCPurposeLoadBalancer}
	loadBalancerID, err := service.reusableID(attachmentService, loadBalancerRequest, cluster, project, "shared", profilePath, name("", "vpc", loadBalancerRequest.Identity))
	require.NoError(t, err)
	require.Equal(t, name("", "vpc", loadBalancerRequest.Identity), loadBalancerID)

	primaryRequest := FabricPreparationRequest{Identity: "another-primary-vdn", VPCPurpose: VPCPurposePrimary}
	primaryReuseID, err := service.reusableID(attachmentService, primaryRequest, cluster, project, "shared", profilePath, name("", "vpc", primaryRequest.Identity))
	require.NoError(t, err)
	require.Equal(t, primaryID, primaryReuseID)
}

func TestOwnedBySeparatesClusterAndNamespaceResources(t *testing.T) {
	clusterTags := []model.Tag{{Scope: ptr.To(common.TagScopeCluster), Tag: ptr.To("cluster")}}
	tenantTags := append(append([]model.Tag(nil), clusterTags...), model.Tag{Scope: ptr.To(common.TagScopeNamespaceUID), Tag: ptr.To("namespace-a")})
	emptyNamespaceTag := append(append([]model.Tag(nil), clusterTags...), model.Tag{Scope: ptr.To(common.TagScopeNamespaceUID), Tag: ptr.To("")})

	require.True(t, ownedBy("cluster", "", clusterTags))
	require.False(t, ownedBy("cluster", "", tenantTags))
	require.False(t, ownedBy("cluster", "", emptyNamespaceTag))
	require.True(t, ownedBy("cluster", "namespace-a", tenantTags))
	require.False(t, ownedBy("cluster", "namespace-b", tenantTags))
}

func TestReusableIDsRejectsDotSNATProfileIDs(t *testing.T) {
	project := "/orgs/o/projects/p"
	service := &FabricService{
		cluster:         "cluster",
		TransitGateways: TransitGatewayService{Store: NewResourceStore[model.TransitGateway]()},
		Profiles:        ConnectivityProfileService{Store: NewResourceStore[model.VpcConnectivityProfile]()},
		VPCs:            VPCService{Store: NewResourceStore[model.Vpc]()},
		Attachments:     AttachmentService{Store: NewResourceStore[model.VpcAttachment]()},
	}
	for _, id := range []string{".", ".."} {
		t.Run(id, func(t *testing.T) {
			req := FabricPreparationRequest{
				ProjectPath: project, Identity: "transition", Zones: []string{"zone"},
				VPCPurpose: VPCPurposeLoadBalancer,
				SNAT: &SNATPreparationRequest{
					ProfilePath: project + "/vpc-connectivity-profiles/" + id,
					VPCPath:     project + "/vpcs/vpc",
				},
			}
			_, _, _, err := service.reusableIDs(req, project, "tgw", "profile", "vpc", "")
			require.ErrorContains(t, err, "invalid frozen SNAT profile path")
		})
	}
}

type workflowReaderStub struct{ workflow model.DvpgToSubnetWorkflow }

type countingWorkflowReader struct{ calls int }

func (w *countingWorkflowReader) GetStatus(context.Context, string) (model.DvpgToSubnetWorkflow, error) {
	w.calls++
	return model.DvpgToSubnetWorkflow{}, nil
}

func TestTrackWorkflowRejectsInvalidIDsBeforeRead(t *testing.T) {
	reader := &countingWorkflowReader{}
	for _, id := range []string{"..", ".", "bad/id", "bad%2fid", " "} {
		_, err := TrackWorkflow(context.Background(), reader, &snatReady{ready: true}, id, "/orgs/o/projects/p/vpcs/v")
		require.ErrorContains(t, err, "invalid workflow ID")
	}
	require.Zero(t, reader.calls)
	_, err := TrackWorkflow(context.Background(), reader, &snatReady{ready: true}, "workflow-a", "/orgs/o/projects/p/vpcs/v")
	require.Error(t, err) // Empty response is not a valid scoped workflow.
	require.Equal(t, 1, reader.calls)
}

func (w workflowReaderStub) GetStatus(context.Context, string) (model.DvpgToSubnetWorkflow, error) {
	return w.workflow, nil
}

func TestTrackWorkflowValidatesPathsAndProgress(t *testing.T) {
	vpc := "/orgs/o/projects/p/vpcs/v"
	w := model.DvpgToSubnetWorkflow{VpcPath: &vpc, DesiredState: ptr.To(nsxworkflow.DesiredStateConversion), WorkflowState: &model.DvpgToSubnetWorkflowState{Status: ptr.To("SUCCESS"), MigrationStatus: &model.DvpgToSubnetMigrationStatus{NsxObject: []model.DvpgToSubnetNsxObjectMigrationStatus{{ResourceType: ptr.To("VpcSubnet"), PolicyPath: ptr.To(vpc + "/subnets/a"), State: ptr.To("COMPLETED")}, {ResourceType: ptr.To("DistributedVlanConnection"), PolicyPath: ptr.To("/infra/distributed-vlan-connections/d"), State: ptr.To("COMPLETED")}}}}}
	tracked, err := TrackWorkflow(context.Background(), workflowReaderStub{w}, &snatReady{ready: true}, "workflow", vpc)
	require.NoError(t, err)
	require.True(t, tracked.Ready)
	require.Equal(t, []string{vpc + "/subnets/a"}, tracked.SubnetPaths)
	w.WorkflowState.MigrationStatus.NsxObject[0].PolicyPath = nil
	_, err = TrackWorkflow(context.Background(), workflowReaderStub{w}, &snatReady{ready: true}, "workflow", vpc)
	require.Error(t, err)
	w.WorkflowState.MigrationStatus.NsxObject[0].PolicyPath = ptr.To(vpc + "-other/subnets/a")
	_, err = TrackWorkflow(context.Background(), workflowReaderStub{w}, &snatReady{ready: true}, "workflow", vpc)
	require.Error(t, err)
}

func TestTrackWorkflowSuccessWithoutOutputResourcesFails(t *testing.T) {
	vpc := "/orgs/o/projects/p/vpcs/v"
	w := model.DvpgToSubnetWorkflow{
		VpcPath:      &vpc,
		DesiredState: ptr.To(nsxworkflow.DesiredStateConversion),
		WorkflowState: &model.DvpgToSubnetWorkflowState{
			Status:          ptr.To("SUCCESS"),
			MigrationStatus: &model.DvpgToSubnetMigrationStatus{},
		},
	}

	_, err := TrackWorkflow(context.Background(), workflowReaderStub{w}, &snatReady{ready: true}, "workflow", vpc)
	require.EqualError(t, err, `NSX workflow "workflow" succeeded without reporting output resources`)
}

func TestTrackWorkflowFailureWithoutMigrationStatusIsError(t *testing.T) {
	vpc := "/orgs/o/projects/p/vpcs/v"
	w := model.DvpgToSubnetWorkflow{
		VpcPath:      &vpc,
		DesiredState: ptr.To(nsxworkflow.DesiredStateConversion),
		WorkflowState: &model.DvpgToSubnetWorkflowState{
			Status: ptr.To("FAILED"),
		},
	}

	_, err := TrackWorkflow(context.Background(), workflowReaderStub{w}, &snatReady{ready: true}, "workflow", vpc)
	require.EqualError(t, err, `NSX workflow "workflow" failed`)
}

type workflowRealizer struct {
	states map[string]bool
	fail   string
	calls  []string
}

func (r *workflowRealizer) Ready(_ context.Context, path string) (bool, error) {
	r.calls = append(r.calls, path)
	if path == r.fail {
		return false, fmt.Errorf("backend error")
	}
	return r.states[path], nil
}
func TestTrackWorkflowRequiresIndependentRealization(t *testing.T) {
	vpc := "/orgs/o/projects/p/vpcs/v"
	subnet := vpc + "/subnets/a"
	dvc := "/infra/distributed-vlan-connections/d"
	w := model.DvpgToSubnetWorkflow{VpcPath: &vpc, DesiredState: ptr.To(nsxworkflow.DesiredStateConversion), WorkflowState: &model.DvpgToSubnetWorkflowState{Status: ptr.To("SUCCESS"), MigrationStatus: &model.DvpgToSubnetMigrationStatus{NsxObject: []model.DvpgToSubnetNsxObjectMigrationStatus{{ResourceType: ptr.To("VpcSubnet"), PolicyPath: &subnet, State: ptr.To("COMPLETED")}, {ResourceType: ptr.To("DistributedVlanConnection"), PolicyPath: &dvc, State: ptr.To("COMPLETED")}}}}}
	realizer := &workflowRealizer{states: map[string]bool{dvc: true, subnet: false}}
	got, err := TrackWorkflow(context.Background(), workflowReaderStub{w}, realizer, "workflow", vpc)
	require.NoError(t, err)
	require.False(t, got.Ready)
	require.ElementsMatch(t, []string{dvc, subnet}, realizer.calls)
	realizer.states[subnet] = true
	got, err = TrackWorkflow(context.Background(), workflowReaderStub{w}, realizer, "workflow", vpc)
	require.NoError(t, err)
	require.True(t, got.Ready)
	realizer.fail = dvc
	_, err = TrackWorkflow(context.Background(), workflowReaderStub{w}, realizer, "workflow", vpc)
	require.ErrorContains(t, err, dvc)
}

func TestProfileReuseAllowsOnlyAcceptedSNATBlock(t *testing.T) {
	project := "/orgs/o/projects/p"
	path := project + "/vpc-connectivity-profiles/reused"
	tgw := project + "/transit-gateways/t"
	ordinary := project + "/infra/ip-blocks/ordinary"
	snat := project + "/infra/ip-blocks/accepted-snat"
	store := NewResourceStore[model.VpcConnectivityProfile]()
	obj := &model.VpcConnectivityProfile{Id: ptr.To("reused"), Path: &path, ParentPath: &project, TransitGatewayPath: &tgw, ExternalIpBlocks: []string{ordinary, snat}, Tags: []model.Tag{{Scope: ptr.To(common.TagScopeCluster), Tag: ptr.To("cluster")}}}
	require.NoError(t, store.Put(path, obj))
	svc := ConnectivityProfileService{Store: store}
	got, err := svc.reusableID("cluster", project, "t", tgw, ordinary, "default", snat)
	require.NoError(t, err)
	require.Equal(t, "reused", got)
	got, err = svc.reusableID("cluster", project, "t", tgw, ordinary, "default", project+"/infra/ip-blocks/unrelated")
	require.NoError(t, err)
	require.Equal(t, "default", got)
}
