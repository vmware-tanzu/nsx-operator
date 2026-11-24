/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package inspection

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	protocol "github.com/vmware/vsphere-automation-sdk-go/runtime/protocol/client"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra/realized_state"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/vpcs"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/vpcs/subnets"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

type fakeConnector struct{ protocol.Connector }
type fakeTGW struct {
	projects.TransitGatewaysClient
	items []model.TransitGateway
}

func (f fakeTGW) List(_, _ string, _ *string, _, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.TransitGatewayListResult, error) {
	return model.TransitGatewayListResult{Results: f.items}, nil
}

type fakeVPCs struct {
	projects.VpcsClient
	items []model.Vpc
}

func (f fakeVPCs) List(_, _ string, _ *string, _, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.VpcListResult, error) {
	return model.VpcListResult{Results: f.items}, nil
}

type fakeProfiles struct {
	projects.VpcConnectivityProfilesClient
	items []model.VpcConnectivityProfile
}

func (f fakeProfiles) List(_, _ string, _ *string, _, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.VpcConnectivityProfileListResult, error) {
	return model.VpcConnectivityProfileListResult{Results: f.items}, nil
}

type fakeAttachments struct {
	vpcs.AttachmentsClient
	items []model.VpcAttachment
}

func (f fakeAttachments) List(_, _, _ string, _ *string, _, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.VpcAttachmentListResult, error) {
	return model.VpcAttachmentListResult{Results: f.items}, nil
}

type fakeSubnets struct {
	vpcs.SubnetsClient
	items []model.VpcSubnet
}

func (f fakeSubnets) List(_, _, _ string, _ *string, _, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.VpcSubnetListResult, error) {
	return model.VpcSubnetListResult{Results: f.items}, nil
}

type fakeRealized struct {
	realized_state.RealizedEntitiesClient
}

func (fakeRealized) List(_ string, _ *string) (model.GenericPolicyRealizedResourceListResult, error) {
	state := model.GenericPolicyRealizedResource_STATE_REALIZED
	return model.GenericPolicyRealizedResourceListResult{Results: []model.GenericPolicyRealizedResource{{State: &state}}}, nil
}

type fakePortSettings struct {
	subnets.PortSettingsClient
	items []model.VpcSubnetPortSetting
}

func (f fakePortSettings) List(_, _, _, _ string, _ *string, _, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.VpcSubnetPortSettingListResult, error) {
	return model.VpcSubnetPortSettingListResult{Results: f.items}, nil
}

type fakeDVC struct{}

func (fakeDVC) Get(id string) (model.DistributedVlanConnection, error) {
	return model.DistributedVlanConnection{Id: &id}, nil
}

func TestNSXTargetInventoryListsZoneBasedTopology(t *testing.T) {
	str := func(s string) *string { return &s }
	span := model.ZoneBasedSpan{Type_: model.ZoneBasedSpan__TYPE_IDENTIFIER, ZoneExternalIds: []string{"vc:zone"}}
	value, errs := span.GetDataValue__()
	require.Empty(t, errs)
	c := &nsx.Client{
		RestConnector:                 fakeConnector{},
		TransitGatewayClient:          fakeTGW{items: []model.TransitGateway{{Id: str("tgw"), Path: str("/tgw"), Span: value.(*data.StructValue)}}},
		VPCClient:                     fakeVPCs{items: []model.Vpc{{Id: str("vpc"), Path: str("/vpc")}}},
		VPCConnectivityProfilesClient: fakeProfiles{items: []model.VpcConnectivityProfile{{Path: str("/profile"), TransitGatewayPath: str("/tgw")}}},
		VpcAttachmentClient:           fakeAttachments{items: []model.VpcAttachment{{Path: str("/attachment"), VpcConnectivityProfile: str("/profile")}}},
		SubnetsClient:                 fakeSubnets{}, PortSettingsClient: fakePortSettings{}, RealizedEntitiesClient: fakeRealized{},
	}
	inventory := NSXTargetInventory{Client: c, OrgID: "default", ProjectID: "project", VCenterUUID: "vc", Topology: topologyStub{}}
	result, err := inventory.ListTopology(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, "tgw", result[0].TGWID)
	require.Equal(t, "vpc", result[0].VPCID)
	require.Equal(t, []string{"vc:zone"}, result[0].ZoneIDs)
	require.True(t, result[0].VPCReady)
	require.True(t, result[0].Attached)
	// A zone span alone does not prove which default VDS NSX selected.
	require.Empty(t, result[0].VDSUUIDs)

	tag, err := DVPGTag("vc", "dvportgroup-1")
	require.NoError(t, err)
	c.SubnetsClient = fakeSubnets{items: []model.VpcSubnet{{Id: str("subnet"), VlanConnection: str("/infra/distributed-vlan-connections/dvc")}}}
	c.PortSettingsClient = fakePortSettings{items: []model.VpcSubnetPortSetting{{Path: str("/port-setting"), Tags: []model.Tag{tag}}}}
	inventory.DVCs = fakeDVC{}
	result, err = inventory.ListTopology(context.Background())
	require.NoError(t, err)
	require.Len(t, result[0].PortSettings, 1)
	require.Equal(t, "dvportgroup-1", result[0].PortSettings[0].PortGroupID)
	require.Equal(t, "subnet", result[0].PortSettings[0].SubnetID)
	require.Equal(t, "dvc", result[0].PortSettings[0].DVCID)

	c.SubnetsClient = fakeSubnets{items: []model.VpcSubnet{{Id: str("subnet")}}}
	_, err = inventory.ListTopology(context.Background())
	require.ErrorContains(t, err, "has no DVC")
	c.SubnetsClient = fakeSubnets{items: []model.VpcSubnet{{Id: str("subnet"), VlanConnection: str("/infra/distributed-vlan-connections/dvc")}}}

	c.VpcAttachmentClient = fakeAttachments{}
	_, err = inventory.ListTopology(context.Background())
	require.ErrorContains(t, err, "tagged PortSettings but no attachment")
}

func TestNSXPagesRejectsPartialInventory(t *testing.T) {
	calls := 0
	_, err := nsxPages(context.Background(), func(_ *string) ([]string, *string, error) {
		calls++
		cursor := "again"
		return []string{"item"}, &cursor, nil
	})
	require.ErrorContains(t, err, "repeated cursor")
	require.Equal(t, 2, calls)
}
