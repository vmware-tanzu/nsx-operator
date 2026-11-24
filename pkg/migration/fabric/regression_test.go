/*
Copyright © 2026 Broadcom, Inc. All Rights Reserved.

	SPDX-License-Identifier: Apache-2.0
*/
package fabric

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/utils/ptr"
)

func TestPendingRetryKeepsSelectedGatewayIdentity(t *testing.T) {
	tgw := &chainTGW{}
	s := chainService(tgw, &chainBlock{objs: map[string]model.IpAddressBlock{}}, &chainProfile{}, &chainVPC{}, &chainAttachment{})
	req := FabricPreparationRequest{ProjectPath: "/orgs/o/projects/p", Identity: "transition", Zones: []string{"zone"}}
	first, err := s.Prepare(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, first.Pending)
	// Discovery changes before the prior operation is completed or checkpointed.
	other := *tgw.obj
	other.Id = ptr.To("other")
	other.Path = ptr.To(req.ProjectPath + "/transit-gateways/other")
	require.NoError(t, s.TransitGateways.Store.Put(*other.Path, &other))
	require.NoError(t, s.TransitGateways.Store.Put(*tgw.obj.Path, tgw.obj))
	defaultTGW := name("", "tgw", req.Identity)
	defaultVPC := name("", "vpc", req.Identity)
	defaultProfile := name("", "profile", req.Identity+"\x00"+defaultTGW+"\x00"+defaultVPC)
	chosen, _, _, err := s.reusableIDs(req, req.ProjectPath, defaultTGW, defaultProfile, defaultVPC, "")
	require.True(t, err != nil || chosen == first.TGW.ID, "retry selected %q after starting %q", chosen, first.TGW.ID)
}

func TestPendingFirstPrepareWithAcceptedSNATBeforeBlockExists(t *testing.T) {
	s := chainService(&chainTGW{}, &chainBlock{objs: map[string]model.IpAddressBlock{}}, &chainProfile{}, &chainVPC{}, &chainAttachment{})
	req := FabricPreparationRequest{ProjectPath: "/orgs/o/projects/p", Identity: "transition", VPCPurpose: VPCPurposeLoadBalancer, Zones: []string{"zone"}, SNAT: &SNATPreparationRequest{Identity: "transition", VPCPath: "/orgs/o/projects/p/vpcs/v", ProfilePath: "/orgs/o/projects/p/vpc-connectivity-profiles/p", IPAddress: "192.0.2.5", LBSize: "SMALL"}}
	result, err := s.Prepare(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.Ready)
}

type pagedRealizationStub struct {
	result model.GenericPolicyRealizedResourceListResult
}

func (p pagedRealizationStub) List(string, *string) (model.GenericPolicyRealizedResourceListResult, error) {
	return p.result, nil
}
func TestPendingIncompleteRealizationCannotReportReady(t *testing.T) {
	r := NSXRealizer{Client: pagedRealizationStub{model.GenericPolicyRealizedResourceListResult{Cursor: ptr.To("next"), Results: []model.GenericPolicyRealizedResource{{State: ptr.To(model.GenericPolicyRealizedResource_STATE_REALIZED)}}}}}
	ready, err := r.Ready(context.Background(), "/resource")
	require.True(t, err != nil || !ready, "incomplete realization scan reported ready")
}

func TestFrozenIdentitySurvivesLostResponseAndChangedInventory(t *testing.T) {
	ctx := context.Background()
	gateway := &chainTGW{lost: true}
	blocks := &chainBlock{objs: map[string]model.IpAddressBlock{}}
	profiles := &chainProfile{}
	vpc := &chainVPC{}
	attachments := &chainAttachment{}
	svc := chainService(gateway, blocks, profiles, vpc, attachments)
	req := FabricPreparationRequest{ProjectPath: "/orgs/o/projects/p", Identity: "transition", Zones: []string{"zone"}}
	ids, err := svc.ResolveIdentities(ctx, req)
	require.NoError(t, err)
	require.Equal(t, 0, gateway.writes)
	req.Frozen = &ids
	result, err := svc.Prepare(ctx, req)
	require.ErrorContains(t, err, "response lost")
	require.Equal(t, ids, result.Identities)
	restarted := chainService(gateway, blocks, profiles, vpc, attachments)
	other := *gateway.obj
	other.Id = ptr.To("other")
	other.Path = ptr.To(req.ProjectPath + "/transit-gateways/other")
	require.NoError(t, restarted.TransitGateways.Store.Put(*other.Path, &other))
	for i := 0; i < 5; i++ {
		result, err = restarted.Prepare(ctx, req)
		require.NoError(t, err)
		require.Equal(t, ids, result.Identities)
		if result.Ready {
			break
		}
	}
	require.True(t, result.Ready)
	require.Equal(t, 1, gateway.writes)
}

func TestFrozenIdentityRejectsMismatchedRequests(t *testing.T) {
	s := chainService(&chainTGW{}, &chainBlock{objs: map[string]model.IpAddressBlock{}}, &chainProfile{}, &chainVPC{}, &chainAttachment{})
	req := FabricPreparationRequest{ProjectPath: "/orgs/o/projects/p", Identity: "transition", Zones: []string{"zone"}, TGWID: "other", Frozen: &FrozenIdentities{TGWID: "frozen", ProfileID: "profile", VPCID: "vpc"}}
	_, err := s.Prepare(context.Background(), req)
	require.ErrorContains(t, err, "conflicts with frozen identities")
}

func TestRealizerCompleteAndIncompleteResults(t *testing.T) {
	realized := model.GenericPolicyRealizedResource{State: ptr.To(model.GenericPolicyRealizedResource_STATE_REALIZED)}
	for _, tc := range []struct {
		name      string
		cursor    *string
		wantReady bool
		wantError bool
	}{{"no cursor", nil, true, false}, {"empty cursor", ptr.To(""), true, false}, {"continuation", ptr.To("next"), false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			r := NSXRealizer{Client: pagedRealizationStub{model.GenericPolicyRealizedResourceListResult{Cursor: tc.cursor, Results: []model.GenericPolicyRealizedResource{realized}}}}
			ready, err := r.Ready(context.Background(), "/resource")
			require.Equal(t, tc.wantReady, ready)
			if tc.wantError {
				require.ErrorContains(t, err, "incomplete realization results")
			} else {
				require.NoError(t, err)
			}
		})
	}
}
