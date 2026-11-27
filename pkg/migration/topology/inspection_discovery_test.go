/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package topology

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/inspection"
)

type testInventory struct {
	err    error
	source inspection.DiscoveredSource
}

func (i *testInventory) NamespaceVDNs(context.Context, []string) ([]string, error) {
	return []string{"vdn"}, i.err
}
func (i *testInventory) Resolve(context.Context, string) (inspection.DiscoveredSource, error) {
	return i.source, nil
}

type testTarget struct{ targets []inspection.TargetTopology }

func (t testTarget) ListTopology(context.Context) ([]inspection.TargetTopology, error) {
	return t.targets, nil
}

type testGraph struct{}

func (testGraph) DiscoverShared(context.Context, *api.NamespaceNetworkTransition, []inspection.Network) ([]ResourceIdentity, []ConsumerIdentity, error) {
	return nil, nil, nil
}

func TestInspectionDiscovererClaims(t *testing.T) {
	source := inspection.DiscoveredSource{Source: inspection.Source{VDNName: "vdn", Topologies: []inspection.Topology{{VCenterUUID: "vc", PortGroupID: "dvpg", SwitchID: "switch", SwitchUUID: "uuid", ClusterIDs: []string{"cluster"}, ZoneIDs: []string{"zone"}}}}, Mask: "255.255.255.0", Gateway: "192.168.1.1", Ranges: []api.IPAddressRange{{Start: "192.168.1.10", End: "192.168.1.20"}}}
	inventory := &testInventory{source: source}
	target := inspection.TargetTopology{TGWID: "tgw", VPCID: "vpc", VPCReady: true, Attached: true, ZoneIDs: []string{"zone"}, VDSUUIDs: []string{"uuid"}, PortSettings: []inspection.ConvertedPortSetting{{PortGroupID: "dvpg", SwitchUUID: "uuid", PortSettingPath: "/setting", SubnetID: "subnet", DVCID: "dvc"}}}
	batch := &api.NamespaceNetworkTransition{Spec: api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}, Namespaces: []string{"member"}}}
	d := InspectionDiscoverer{Service: inspection.Service{Inventory: inventory, TargetInventory: testTarget{}}, Graph: testGraph{}}
	snapshot, err := d.DiscoverBatch(context.Background(), batch)
	require.NoError(t, err)
	require.Equal(t, []ResourceIdentity{{Kind: ResourceDVPG, Key: "vc:dvpg", VDNName: "vdn"}}, snapshot.Resources)
	d.Service.TargetInventory = testTarget{targets: []inspection.TargetTopology{target}}
	snapshot, err = d.DiscoverBatch(context.Background(), batch)
	require.NoError(t, err)
	require.Empty(t, snapshot.Resources)
	batch.Spec.Action = api.ActionEvaluate
	inventory.source.DHCP = true
	d.Service.TargetInventory = nil
	d.Graph = nil
	snapshot, err = d.DiscoverBatch(context.Background(), batch)
	require.NoError(t, err)
	require.Empty(t, snapshot.Resources)
	require.True(t, snapshot.Complete)
	batch.Spec.Action = api.ActionRollback
	_, err = d.DiscoverBatch(context.Background(), batch)
	require.ErrorContains(t, err, "live shared resource and consumer discovery is required")
	d.Graph = testGraph{}
	inventory.err = errors.New("API unavailable")
	_, err = d.DiscoverBatch(context.Background(), batch)
	require.ErrorContains(t, err, "API unavailable")
	inventory.err = nil
	inventory.source.Topologies[0].PortGroupID = ""
	_, err = d.DiscoverBatch(context.Background(), batch)
	require.ErrorContains(t, err, "VDN vdn backing")
}
