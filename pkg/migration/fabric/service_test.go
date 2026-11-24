/*
Copyright © 2026 Broadcom, Inc. All Rights Reserved.

	SPDX-License-Identifier: Apache-2.0
*/
package fabric

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	vapierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects"
	projectinfra "github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/infra"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/vpcs"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/search"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type ipBlocksClient struct{ projectinfra.IpBlocksClient }
type profilesClient struct {
	projects.VpcConnectivityProfilesClient
}
type vpcsClient struct{ projects.VpcsClient }
type attachmentsClient struct{ vpcs.AttachmentsClient }
type realizedClient struct{}

func (realizedClient) List(string, *string) (model.GenericPolicyRealizedResourceListResult, error) {
	return model.GenericPolicyRealizedResourceListResult{}, nil
}

type gatewayClient struct {
	projects.TransitGatewaysClient
	object    *model.TransitGateway
	writes    int
	failWrite bool
}

func (c *gatewayClient) Get(_, _, _ string) (model.TransitGateway, error) {
	if c.object == nil {
		return model.TransitGateway{}, vapierrors.NotFound{}
	}
	return *c.object, nil
}
func (c *gatewayClient) Patch(_, _, _ string, obj model.TransitGateway) error {
	c.writes++
	if c.failWrite {
		return errors.New("timeout")
	}
	c.object = &obj
	return nil
}
func TestPrepareUsesGatewayClient(t *testing.T) {
	gateway := &gatewayClient{}
	service := FabricService{TransitGateways: TransitGatewayService{Client: gateway, Store: NewResourceStore[model.TransitGateway]()}, IPBlocks: IPBlockService{Store: NewResourceStore[model.IpAddressBlock]()}, Profiles: ConnectivityProfileService{Store: NewResourceStore[model.VpcConnectivityProfile]()}, VPCs: VPCService{Store: NewResourceStore[model.Vpc]()}, Attachments: AttachmentService{Store: NewResourceStore[model.VpcAttachment]()}, cluster: "cluster", initialized: true}
	req := FabricPreparationRequest{ProjectPath: "/orgs/o/projects/p", Identity: "span", Zones: []string{"zone"}, VDS: []string{"vds"}, TGWID: "gateway"}
	result, err := service.Prepare(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.Ready)
	require.Equal(t, 1, gateway.writes)
	require.Equal(t, "gateway", value(gateway.object.Id))
	require.NotNil(t, gateway.object.Span)
	// Return NSX-generated identity to exercise read-back and compatibility.
	gateway.object.Path = &result.TGW.Path
	gateway.object.ParentPath = &result.TGW.Parent
	_, err = service.Prepare(context.Background(), req)
	require.ErrorContains(t, err, "fabric realization service is required")
	require.Equal(t, 1, gateway.writes)
}
func TestValidateAndCompatibility(t *testing.T) {
	for _, req := range []FabricPreparationRequest{{ProjectPath: "/orgs/o/projects/..", Identity: "x", Zones: []string{"z"}}, {ProjectPath: "/orgs/o/projects/p", Identity: "x", Zones: []string{"z"}, IPBlockID: "x", IPBlockCIDR: "192.0.2.1/24"}, {ProjectPath: "/orgs/o/projects/p", Identity: "x"}} {
		require.Error(t, validate(req))
	}
	a := Resource{Kind: TGW, Zones: []string{"z1", "z2"}, VDS: []string{"vds"}}
	b := a
	b.Zones = []string{"z2", "z1"}
	require.True(t, compatible(a, b))
	b.VDS = []string{"other"}
	require.False(t, compatible(a, b))
}

type searchClient struct {
	search.QueryClient
	calls []string
}

func (s *searchClient) List(query string, cursor *string, _ *string, _ *int64, _ *bool, _ *string) (model.SearchResponse, error) {
	s.calls = append(s.calls, query)
	if strings.Contains(query, "resource_type:TransitGateway") && cursor == nil {
		next := "next"
		return model.SearchResponse{Cursor: &next, Results: []*data.StructValue{data.NewStructValue("TransitGateway", map[string]data.DataValue{"path": data.NewStringValue("/orgs/o/projects/p/transit-gateways/one")})}}, nil
	}
	return model.SearchResponse{}, nil
}
func TestInitializeSearchAndTags(t *testing.T) {
	query := &searchClient{}
	gateway := &gatewayClient{}
	service, err := NewFabricService(FabricServiceClients{QueryClient: query, Cluster: "cluster:one", TransitGatewaysClient: gateway, IPBlocksClient: &ipBlocksClient{}, ProfilesClient: &profilesClient{}, VPCsClient: &vpcsClient{}, AttachmentsClient: &attachmentsClient{}, RealizedEntitiesClient: realizedClient{}})
	require.NoError(t, err)
	err = service.InitializeStore(context.Background())
	require.NoError(t, err)
	require.Len(t, query.calls, 6)
	require.Contains(t, query.calls[0], `tags.tag:cluster\:one`)
	storedTGW, found := service.TransitGateways.Store.Get("/orgs/o/projects/p/transit-gateways/one")
	require.True(t, found)
	require.Equal(t, "/orgs/o/projects/p/transit-gateways/one", value(storedTGW.Path))
	_, err = service.Prepare(context.Background(), FabricPreparationRequest{ProjectPath: "/orgs/o/projects/p", Identity: "span", Zones: []string{"zone"}, NamespaceUID: "namespace-uid"})
	require.NoError(t, err)
	require.Equal(t, []model.Tag{{Scope: common.String(common.TagScopeCluster), Tag: common.String("cluster:one")}}, gateway.object.Tags)
}

func TestUncertainCreateRetriesSameIdentity(t *testing.T) {
	gateway := &gatewayClient{failWrite: true}
	service := FabricService{TransitGateways: TransitGatewayService{Client: gateway, Store: NewResourceStore[model.TransitGateway]()}, IPBlocks: IPBlockService{Store: NewResourceStore[model.IpAddressBlock]()}, Profiles: ConnectivityProfileService{Store: NewResourceStore[model.VpcConnectivityProfile]()}, VPCs: VPCService{Store: NewResourceStore[model.Vpc]()}, Attachments: AttachmentService{Store: NewResourceStore[model.VpcAttachment]()}, cluster: "cluster", initialized: true}
	req := FabricPreparationRequest{ProjectPath: "/orgs/o/projects/p", Identity: "span", Zones: []string{"zone"}}
	_, err := service.Prepare(context.Background(), req)
	require.ErrorContains(t, err, "timeout")
	gateway.failWrite = false
	result, err := service.Prepare(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.Ready)
	require.Equal(t, 2, gateway.writes)
	require.Equal(t, name("", "tgw", req.Identity), value(gateway.object.Id))
}
