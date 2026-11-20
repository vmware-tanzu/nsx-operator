/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package inspection

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

type inventoryStub struct {
	sources map[string]DiscoveredSource
	calls   int
	err     error
}

func (inventory *inventoryStub) NamespaceVDNs(context.Context, []string) ([]string, error) {
	return []string{"a", "a"}, inventory.err
}
func (inventory *inventoryStub) Resolve(_ context.Context, vdnName string) (DiscoveredSource, error) {
	inventory.calls++
	return inventory.sources[vdnName], inventory.err
}
func sourceForVDN(vdnName string) DiscoveredSource {
	return DiscoveredSource{Source: Source{VDNName: vdnName, DHCP: true, Topologies: []Topology{{VCenterUUID: "vc-uuid", PortGroupID: "dvportgroup-1", SwitchID: "dvs-1", SwitchUUID: "uuid", ClusterIDs: []string{"domain-c1"}, ZoneIDs: []string{"zone-a"}}}}}
}

func TestTopologySameBackingUsesVCenterAndDVPGIdentity(t *testing.T) {
	topology := Topology{VCenterUUID: "vc-uuid", PortGroupID: "dvportgroup-1", SwitchUUID: "dvs-1"}
	require.True(t, topology.SameBacking(Topology{VCenterUUID: "vc-uuid", PortGroupID: "dvportgroup-1", SwitchUUID: "dvs-2"}))
	require.False(t, topology.SameBacking(Topology{VCenterUUID: "other-vc", PortGroupID: "dvportgroup-1", SwitchUUID: "dvs-1"}))
	require.False(t, topology.SameBacking(Topology{VCenterUUID: "vc-uuid", PortGroupID: "dvportgroup-2", SwitchUUID: "dvs-1"}))
	require.False(t, topology.SameBacking(Topology{PortGroupID: "dvportgroup-1"}))
}

func TestSelectLBSNATSource(t *testing.T) {
	flb := FLBNetworks{VCenterUUID: "vc-1", FrontendNetwork: "dvportgroup-vip", BackendNetwork: "dvportgroup-backend", VIPRanges: []VIPRange{{Address: "172.16.0.200", Count: 54}}}

	t.Run("backend is VIP network", func(t *testing.T) {
		flb.BackendNetwork = flb.FrontendNetwork
		decision, err := SelectLBSNATSource(flb, nil, []string{"192.0.2.25"})
		require.NoError(t, err)
		require.Equal(t, AllocateFromBackendNetwork, decision.Selection)
		require.Equal(t, flb.BackendNetwork, decision.AllocationSource)
	})
	t.Run("backend is a workload network", func(t *testing.T) {
		flb.BackendNetwork = "workload-network-name"
		workloads := []Topology{{VCenterUUID: "vc-1", PortGroupID: "dvportgroup-1", PortGroupName: flb.BackendNetwork}}
		decision, err := SelectLBSNATSource(flb, workloads, nil)
		require.NoError(t, err)
		require.Equal(t, AllocateFromBackendNetwork, decision.Selection)
		require.Equal(t, flb.BackendNetwork, decision.AllocationSource)
		decision, err = SelectLBSNATSource(flb, []Topology{{VCenterUUID: "different-vc", PortGroupID: flb.BackendNetwork}}, nil)
		require.NoError(t, err)
		require.Equal(t, AllocateFromVIPRange, decision.Selection)
	})
	t.Run("separate backend accepts user address", func(t *testing.T) {
		decision, err := SelectLBSNATSource(flb, nil, []string{"192.0.2.25"})
		require.NoError(t, err)
		require.Equal(t, UseConfiguredLBSNATIP, decision.Selection)
		require.Equal(t, "192.0.2.25", decision.IPAddress)
	})
	t.Run("separate backend without user address falls back to VIP range", func(t *testing.T) {
		decision, err := SelectLBSNATSource(flb, nil, nil)
		require.NoError(t, err)
		require.Equal(t, AllocateFromVIPRange, decision.Selection)
		require.Equal(t, flb.FrontendNetwork, decision.AllocationSource)
		require.Equal(t, flb.VIPRanges, decision.VIPRanges)
		require.NotEmpty(t, decision.Warning)
	})
}

func validDHCPInput(vdnName string) api.DHCPNetworkConfiguration {
	return api.DHCPNetworkConfiguration{VDNName: vdnName, SubnetMask: "255.255.255.0", GatewayAddress: "192.168.10.1", DHCPRange: &api.IPAddressRange{Start: "192.168.10.100", End: "192.168.10.200"}}
}

func TestInspectionAndDiagnostics(t *testing.T) {
	ctx := context.Background()
	inventory := &inventoryStub{sources: map[string]DiscoveredSource{"a": sourceForVDN("a")}}
	inspectionService := Service{Inventory: inventory}
	result, err := inspectionService.InspectNamespaces(ctx, []string{"ns1", "ns2"}, nil)
	require.NoError(t, err)
	require.Equal(t, 1, inventory.calls)
	require.False(t, result.Ready())
	require.Len(t, result.RequiredInputs, 1)
	require.Len(t, result.SourceTopologies, 1)
	require.Contains(t, result.RequiredInputs[0].Description, "dhcpRange.start")
	require.Contains(t, result.RequiredInputs[0].Description, "VDN a")
	status := api.TransitionStatus{RequiredInputs: []api.RequiredInput{{FieldName: "other", Description: "another service"}}, LastAction: api.ActionEvaluate, ActionGeneration: 1, ActionState: api.ActionStateRunning}
	require.NoError(t, result.Apply(&status, 1))
	require.Equal(t, api.PhaseNeedsInput, status.Phase)
	require.Len(t, status.RequiredInputs, 2)
	result, err = inspectionService.Inspect(ctx, []string{"a"}, []api.DHCPNetworkConfiguration{validDHCPInput("a")})
	require.NoError(t, err)
	require.True(t, result.Ready())
	require.NoError(t, result.Apply(&status, 2))
	require.Len(t, status.RequiredInputs, 1)
	require.Equal(t, "other", status.RequiredInputs[0].FieldName)
	require.Equal(t, api.PhaseNeedsInput, status.Phase)
	require.Equal(t, api.ActionStateRunning, status.ActionState)
	require.Equal(t, int64(1), status.ActionGeneration)
	require.Equal(t, metav1.ConditionTrue, status.Conditions[0].Status)
	status.RequiredInputs = nil
	require.NoError(t, result.Apply(&status, 2))
	require.Equal(t, api.PhaseEvaluating, status.Phase)
	statusBeforeStaleApply := status.DeepCopy()
	require.Error(t, result.Apply(&status, 1))
	require.Equal(t, *statusBeforeStaleApply, status)
}

func TestMissingClusterZoneRequiresExternalAction(t *testing.T) {
	inventory := &inventoryStub{err: &MissingZonesError{SwitchUUID: "vds-1", ClusterIDs: []string{"domain-c2"}}}
	service := Service{Inventory: inventory}
	result, err := service.Inspect(context.Background(), []string{"vdn-1"}, nil)
	require.NoError(t, err)
	status := api.TransitionStatus{}
	require.NoError(t, result.Apply(&status, 1))
	require.Equal(t, api.PhaseNeedsInput, status.Phase)
	require.Empty(t, status.RequiredInputs)
	require.Contains(t, status.Conditions[0].Message, "create a vCenter zone")
	require.Contains(t, status.Conditions[0].Message, "domain-c2")

	inventory.err = nil
	inventory.sources = map[string]DiscoveredSource{"vdn-1": sourceForVDN("vdn-1")}
	result, err = service.Inspect(context.Background(), []string{"vdn-1"}, []api.DHCPNetworkConfiguration{validDHCPInput("vdn-1")})
	require.NoError(t, err)
	require.NoError(t, result.Apply(&status, 2))
	require.Equal(t, api.PhaseEvaluating, status.Phase)
}

func TestInvalidInputsAreNotNeedsInput(t *testing.T) {
	inspectionService := Service{Inventory: &inventoryStub{sources: map[string]DiscoveredSource{"a": sourceForVDN("a")}}}
	for _, dhcpInputs := range [][]api.DHCPNetworkConfiguration{
		{validDHCPInput("unknown")}, {validDHCPInput("a"), validDHCPInput("a")},
		{{VDNName: "a", GatewayAddress: "2001:db8::1"}},
		{{VDNName: "a", SubnetMask: "255.255.255.0", DHCPRange: &api.IPAddressRange{Start: "192.168.10.100", End: "192.168.11.100"}}},
	} {
		result, err := inspectionService.Inspect(context.Background(), []string{"a"}, dhcpInputs)
		require.NoError(t, err)
		require.NotEmpty(t, result.Problems)
		status := api.TransitionStatus{}
		require.NoError(t, result.Apply(&status, 1))
		require.Equal(t, api.PhaseEvaluationFailed, status.Phase)
	}
}

func TestStaticAndSharedBacking(t *testing.T) {
	firstVDNSource, secondVDNSource := sourceForVDN("a"), sourceForVDN("b")
	inventory := &inventoryStub{sources: map[string]DiscoveredSource{"a": firstVDNSource, "b": secondVDNSource}}
	inspectionService := Service{Inventory: inventory}
	secondVDNInput := validDHCPInput("b")
	secondVDNInput.DHCPRange.End = "192.168.10.201"
	result, err := inspectionService.Inspect(context.Background(), []string{"b", "a"}, []api.DHCPNetworkConfiguration{validDHCPInput("a"), secondVDNInput})
	require.NoError(t, err)
	require.False(t, result.Ready())
	require.Contains(t, strings.Join(result.Problems, ""), "conflicting")
	firstVDNSource.DHCP = false
	firstVDNSource.Mask = "255.255.255.0"
	firstVDNSource.Gateway = "192.168.10.1"
	firstVDNSource.Ranges = []api.IPAddressRange{{Start: "192.168.10.10", End: "192.168.10.90"}}
	inventory.sources["a"] = firstVDNSource
	result, err = inspectionService.Inspect(context.Background(), []string{"a"}, nil)
	require.NoError(t, err)
	require.True(t, result.Ready())
	require.Empty(t, result.RequiredInputs)
	require.Equal(t, "192.168.10.1", result.Networks[0].Addressing.Gateway.String())
	require.Equal(t, firstVDNSource.Ranges, result.Networks[0].Addressing.Ranges)
	require.Equal(t, "a", result.Networks[0].Source.VDNName)
	result, err = inspectionService.Inspect(context.Background(), []string{"a"}, []api.DHCPNetworkConfiguration{validDHCPInput("a")})
	require.NoError(t, err)
	require.NotEmpty(t, result.Problems)
	inventory.err = errors.New("unavailable")
	_, err = inspectionService.Inspect(context.Background(), []string{"a"}, nil)
	require.Error(t, err)
	inventory.err = invalid("unsupported source")
	result, err = inspectionService.Inspect(context.Background(), []string{"a"}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, result.Problems)
}

func TestStaticVDNsSharingDVPG(t *testing.T) {
	staticSource := func(name, start, end string) DiscoveredSource {
		source := sourceForVDN(name)
		source.DHCP = false
		source.Mask = "255.255.255.0"
		source.Gateway = "192.168.1.1"
		source.Ranges = []api.IPAddressRange{{Start: start, End: end}}
		return source
	}
	inventory := &inventoryStub{sources: map[string]DiscoveredSource{
		"a": staticSource("a", "192.168.1.10", "192.168.1.29"),
		"b": staticSource("b", "192.168.1.50", "192.168.1.69"),
	}}
	service := Service{Inventory: inventory}
	result, err := service.Inspect(context.Background(), []string{"a", "b"}, nil)
	require.NoError(t, err)
	require.True(t, result.Ready())
	require.Len(t, result.Networks, 2)

	// The third VDN overlaps the first, even though it does not overlap the second.
	inventory.sources["c"] = staticSource("c", "192.168.1.20", "192.168.1.30")
	result, err = service.Inspect(context.Background(), []string{"a", "b", "c"}, nil)
	require.NoError(t, err)
	require.False(t, result.Ready())
	require.Contains(t, strings.Join(result.Problems, " "), "VDNs a and c have conflicting inputs")

	delete(inventory.sources, "c")
	second := inventory.sources["b"]
	second.Gateway = "192.168.1.2"
	inventory.sources["b"] = second
	result, err = service.Inspect(context.Background(), []string{"a", "b"}, nil)
	require.NoError(t, err)
	require.False(t, result.Ready())
	require.Contains(t, strings.Join(result.Problems, " "), "conflicting inputs")
}

func TestVDNWithMultipleDVPGsAcrossVDSes(t *testing.T) {
	vdnSource := sourceForVDN("vdn-multi-dvpg")
	vdnSource.Topologies = append(vdnSource.Topologies, Topology{
		VCenterUUID: "vc-uuid",
		PortGroupID: "dvportgroup-2",
		SwitchID:    "dvs-2",
		SwitchUUID:  "uuid-2",
		ClusterIDs:  []string{"domain-c2", "domain-c3"},
		ZoneIDs:     []string{"zone-b"},
	})
	inventory := &inventoryStub{sources: map[string]DiscoveredSource{vdnSource.VDNName: vdnSource}}
	result, err := (Service{Inventory: inventory}).Inspect(context.Background(), []string{vdnSource.VDNName}, []api.DHCPNetworkConfiguration{validDHCPInput(vdnSource.VDNName)})
	require.NoError(t, err)
	require.True(t, result.Ready())
	require.Len(t, result.Networks, 1)
	topologies := result.Networks[0].Source.Topologies
	require.Len(t, topologies, 2)
	require.Equal(t, "dvportgroup-1", topologies[0].PortGroupID)
	require.Equal(t, "uuid", topologies[0].SwitchUUID)
	require.Equal(t, "dvportgroup-2", topologies[1].PortGroupID)
	require.Equal(t, "uuid-2", topologies[1].SwitchUUID)
	require.Equal(t, []string{"domain-c2", "domain-c3"}, topologies[1].ClusterIDs)
}

type targetInventoryStub struct{ topologies []TargetTopology }

func (inventory targetInventoryStub) ListTopology(context.Context) ([]TargetTopology, error) {
	return inventory.topologies, nil
}

func TestEvaluateTopologyReuseAndOptionalNamingHints(t *testing.T) {
	ctx := context.Background()
	source := sourceForVDN("workload-vdn")
	inventory := &inventoryStub{sources: map[string]DiscoveredSource{source.VDNName: source}}
	service := Service{Inventory: inventory, TargetInventory: targetInventoryStub{}}
	missingInputResult, err := service.InspectWithNaming(ctx, []string{source.VDNName}, nil, nil)
	require.NoError(t, err)
	require.Len(t, missingInputResult.RequiredInputs, 1)
	require.Len(t, missingInputResult.OptionalInputs, 4)
	require.False(t, missingInputResult.Ready())
	result, err := service.InspectWithNaming(ctx, []string{source.VDNName}, []api.DHCPNetworkConfiguration{validDHCPInput(source.VDNName)}, nil)
	require.NoError(t, err)
	require.True(t, result.Ready())
	require.Len(t, result.OptionalInputs, 4)
	require.True(t, result.Networks[0].Target.CreateTGW)
	require.True(t, result.Networks[0].Target.CreateVPC)
	require.True(t, result.Networks[0].Target.Conversions[0].Create)
	require.Contains(t, result.OptionalInputs[0].FieldName, "[vdnName=workload-vdn].tgwName")
	resourceNames := api.VDNResourceNaming{VDNName: source.VDNName, TGWName: "new-tgw", VPCName: "new-vpc", DVCName: "new-dvc", SubnetName: "new-subnet"}
	result, err = service.InspectWithNaming(ctx, []string{source.VDNName}, []api.DHCPNetworkConfiguration{validDHCPInput(source.VDNName)}, []api.VDNResourceNaming{resourceNames})
	require.NoError(t, err)
	require.Empty(t, result.OptionalInputs)

	zoneSpan := []string{"zone-a"}
	candidate := TargetTopology{
		TGWID: "tgw-1", TGWName: "reused-tgw", ZoneIDs: zoneSpan, VDSUUIDs: []string{"uuid"},
		VPCID: "vpc-1", VPCName: "reused-vpc", VPCReady: true, Attached: true,
		PortSettings: []ConvertedPortSetting{{
			SubnetID: "subnet-1", SubnetName: "reused-subnet", DVCID: "dvc-1", DVCName: "reused-dvc",
			PortGroupID: "dvportgroup-1", SwitchUUID: "uuid", PortSettingPath: "/port-settings/1",
		}},
	}
	service.TargetInventory = targetInventoryStub{topologies: []TargetTopology{candidate}}
	result, err = service.InspectWithNaming(ctx, []string{source.VDNName}, []api.DHCPNetworkConfiguration{validDHCPInput(source.VDNName)}, nil)
	require.NoError(t, err)
	require.True(t, result.Ready())
	require.Empty(t, result.OptionalInputs)
	require.Equal(t, "subnet-1", result.Networks[0].Target.Conversions[0].SubnetID)
	resourceNames = api.VDNResourceNaming{VDNName: source.VDNName, TGWName: "tgw-1", VPCName: "vpc-1", DVCName: "dvc-1", SubnetName: "subnet-1"}
	result, err = service.InspectWithNaming(ctx, []string{source.VDNName}, []api.DHCPNetworkConfiguration{validDHCPInput(source.VDNName)}, []api.VDNResourceNaming{resourceNames})
	require.NoError(t, err)
	require.True(t, result.Ready())
	resourceNames.TGWName = "reused-tgw"
	result, err = service.InspectWithNaming(ctx, []string{source.VDNName}, []api.DHCPNetworkConfiguration{validDHCPInput(source.VDNName)}, []api.VDNResourceNaming{resourceNames})
	require.NoError(t, err)
	require.Contains(t, strings.Join(result.Problems, " "), "reusable TGW ID")
	candidate.VDSUUIDs = nil
	service.TargetInventory = targetInventoryStub{topologies: []TargetTopology{candidate}}
	result, err = service.InspectWithNaming(ctx, []string{source.VDNName}, []api.DHCPNetworkConfiguration{validDHCPInput(source.VDNName)}, nil)
	require.NoError(t, err)
	require.Contains(t, strings.Join(result.Problems, " "), "unverified or incompatible VDS attachments")
	candidate.VDSUUIDs = []string{"uuid"}

	service.TargetInventory = targetInventoryStub{topologies: []TargetTopology{candidate, candidate}}
	result, err = service.InspectWithNaming(ctx, []string{source.VDNName}, []api.DHCPNetworkConfiguration{validDHCPInput(source.VDNName)}, nil)
	require.NoError(t, err)
	require.Contains(t, strings.Join(result.Problems, " "), "multiple TGW/VPC pairs")
}

func TestSourceSpanIncludesAllZonesForEachVDS(t *testing.T) {
	first := sourceForVDN("vdn").Topologies[0]
	first.ZoneIDs = []string{"zone-b", "zone-a"}
	second := first
	second.PortGroupID, second.SwitchUUID = "dvportgroup-2", "uuid-2"
	second.ZoneIDs = []string{"zone-a"}
	zones, err := validateSourceTopology([]Topology{first, second})
	require.NoError(t, err)
	require.Equal(t, []string{"zone-a", "zone-b"}, zones)
}

func TestReuseTaggedPortSettingForAnotherVDN(t *testing.T) {
	source := sourceForVDN("new-vdn")
	target := TargetTopology{TGWID: "tgw", TGWName: "tgw", ZoneIDs: []string{"zone-a"}, VDSUUIDs: []string{"uuid"}, VPCID: "vpc", VPCName: "vpc", VPCReady: true, Attached: true, PortSettings: []ConvertedPortSetting{{
		PortGroupID: "dvportgroup-1", SwitchUUID: "uuid", PortSettingPath: "/subnets/subnet/port-settings/setting",
		SubnetID: "subnet", DVCID: "dvc",
	}}}
	service := Service{Inventory: &inventoryStub{sources: map[string]DiscoveredSource{source.VDNName: source}}, TargetInventory: targetInventoryStub{topologies: []TargetTopology{target}}}
	inspect := func() Result {
		result, err := service.InspectWithNaming(context.Background(), []string{source.VDNName}, []api.DHCPNetworkConfiguration{validDHCPInput(source.VDNName)}, nil)
		require.NoError(t, err)
		return result
	}
	result := inspect()
	require.True(t, result.Ready())
	conversion := result.Networks[0].Target.Conversions[0]
	require.False(t, conversion.Create)
	require.Equal(t, "subnet", conversion.SubnetID)
	require.Equal(t, "dvc", conversion.DVCID)
	require.NotEmpty(t, conversion.PortSettingPath)

	target.PortSettings = append(target.PortSettings, target.PortSettings[0])
	service.TargetInventory = targetInventoryStub{topologies: []TargetTopology{target}}
	require.Contains(t, strings.Join(inspect().Problems, " "), "multiple migration PortSettings")
	target.PortSettings = target.PortSettings[:1]
	otherTarget := target
	otherTarget.VPCID, otherTarget.TGWID = "other-vpc", "other-tgw"
	otherTarget.ZoneIDs = []string{"other-zone"}
	service.TargetInventory = targetInventoryStub{topologies: []TargetTopology{target, otherTarget}}
	require.Contains(t, strings.Join(inspect().Problems, " "), "different or incompatible VPC")

	target.PortSettings = nil
	service.TargetInventory = targetInventoryStub{topologies: []TargetTopology{target}}
	result = inspect()
	require.True(t, result.Ready())
	require.True(t, result.Networks[0].Target.Conversions[0].Create)

	target.PortSettings = []ConvertedPortSetting{{PortGroupID: "dvportgroup-1", SwitchUUID: "uuid"}}
	service.TargetInventory = targetInventoryStub{topologies: []TargetTopology{target}}
	result = inspect()
	require.Contains(t, strings.Join(result.Problems, " "), "incomplete subnet or DVC information")
}

func TestInspectionDecidesConversionForEachDVPG(t *testing.T) {
	source := sourceForVDN("vdn")
	second := source.Topologies[0]
	second.PortGroupID = "dvportgroup-2"
	source.Topologies = append(source.Topologies, second)
	target := TargetTopology{TGWID: "tgw", ZoneIDs: []string{"zone-a"}, VDSUUIDs: []string{"uuid"}, VPCID: "vpc", VPCReady: true, Attached: true, PortSettings: []ConvertedPortSetting{{
		PortGroupID: "dvportgroup-1", SwitchUUID: "uuid", PortSettingPath: "/port-settings/1", SubnetID: "subnet", DVCID: "dvc",
	}}}
	service := Service{Inventory: &inventoryStub{sources: map[string]DiscoveredSource{"vdn": source}}, TargetInventory: targetInventoryStub{topologies: []TargetTopology{target}}}
	result, err := service.InspectWithNaming(context.Background(), []string{"vdn"}, []api.DHCPNetworkConfiguration{validDHCPInput("vdn")}, nil)
	require.NoError(t, err)
	require.True(t, result.Ready())
	require.Len(t, result.Networks[0].Target.Conversions, 2)
	require.False(t, result.Networks[0].Target.Conversions[0].Create)
	require.True(t, result.Networks[0].Target.Conversions[1].Create)
}

func TestDVCNameCollisionAcrossVPCs(t *testing.T) {
	source := sourceForVDN("vdn")
	targets := []TargetTopology{
		{TGWID: "tgw-a", TGWName: "tgw-a", ZoneIDs: []string{"zone-a"}, VDSUUIDs: []string{"uuid"}, VPCID: "vpc-a", VPCName: "vpc-a", VPCReady: true, Attached: true},
		{TGWID: "tgw-b", TGWName: "tgw-b", ZoneIDs: []string{"zone-b"}, VPCID: "vpc-b", VPCName: "vpc-b", VPCReady: true, Attached: true, PortSettings: []ConvertedPortSetting{{DVCID: "shared-dvc", DVCName: "shared-dvc"}}},
	}
	service := Service{Inventory: &inventoryStub{sources: map[string]DiscoveredSource{source.VDNName: source}}, TargetInventory: targetInventoryStub{topologies: targets}}
	result, err := service.InspectWithNaming(context.Background(), []string{source.VDNName}, []api.DHCPNetworkConfiguration{validDHCPInput(source.VDNName)}, []api.VDNResourceNaming{{VDNName: source.VDNName, DVCName: "shared-dvc"}})
	require.NoError(t, err)
	require.Contains(t, strings.Join(result.Problems, " "), "dvcName")
}

func TestVDNsSharingSpanReuseSpecifiedTGWAndVPCNames(t *testing.T) {
	first, second := sourceForVDN("vdn-a"), sourceForVDN("vdn-b")
	inventory := &inventoryStub{sources: map[string]DiscoveredSource{first.VDNName: first, second.VDNName: second}}
	service := Service{Inventory: inventory, TargetInventory: targetInventoryStub{}}
	firstNames := api.VDNResourceNaming{VDNName: first.VDNName, TGWName: "shared-tgw", VPCName: "shared-vpc"}
	result, err := service.InspectWithNaming(context.Background(), []string{first.VDNName, second.VDNName}, []api.DHCPNetworkConfiguration{validDHCPInput(first.VDNName), validDHCPInput(second.VDNName)}, []api.VDNResourceNaming{firstNames})
	require.NoError(t, err)
	require.True(t, result.Ready())
	require.Len(t, result.Networks, 2)
	require.Equal(t, "shared-tgw", result.Networks[0].Target.TGWName)
	require.Equal(t, "shared-vpc", result.Networks[0].Target.VPCName)
	require.Equal(t, result.Networks[0].Target.TGWName, result.Networks[1].Target.TGWName)
	require.Equal(t, result.Networks[0].Target.VPCName, result.Networks[1].Target.VPCName)
	require.Len(t, result.OptionalInputs, 4)

	conflictingNames := api.VDNResourceNaming{VDNName: second.VDNName, TGWName: "other-tgw"}
	result, err = service.InspectWithNaming(context.Background(), []string{first.VDNName, second.VDNName}, []api.DHCPNetworkConfiguration{validDHCPInput(first.VDNName), validDHCPInput(second.VDNName)}, []api.VDNResourceNaming{firstNames, conflictingNames})
	require.NoError(t, err)
	require.Contains(t, strings.Join(result.Problems, " "), "conflicting TGW/VPC names")
	require.Empty(t, result.Networks)
}

func TestVDNsOnDifferentVDSesShareZoneSpanPlan(t *testing.T) {
	first, second := sourceForVDN("vdn-a"), sourceForVDN("vdn-b")
	second.Topologies[0].SwitchUUID = "uuid-2"
	second.Topologies[0].SwitchID = "dvs-2"
	second.Topologies[0].PortGroupID = "dvportgroup-2"
	service := Service{
		Inventory:       &inventoryStub{sources: map[string]DiscoveredSource{first.VDNName: first, second.VDNName: second}},
		TargetInventory: targetInventoryStub{},
	}
	inputs := []api.DHCPNetworkConfiguration{validDHCPInput(first.VDNName), validDHCPInput(second.VDNName)}
	inspect := func(names []api.VDNResourceNaming) Result {
		result, err := service.InspectWithNaming(context.Background(), []string{second.VDNName, first.VDNName}, inputs, names)
		require.NoError(t, err)
		return result
	}
	result := inspect(nil)
	require.True(t, result.Ready(), result.Problems)
	require.Len(t, result.Networks, 2)
	owner, shared := result.Networks[0].Target, result.Networks[1].Target
	require.True(t, owner.CreateTGW)
	require.True(t, owner.CreateVPC)
	require.False(t, shared.CreateTGW)
	require.False(t, shared.CreateVPC)
	require.Equal(t, "vdn-a", shared.SharedWithVDN)
	require.Equal(t, []string{"uuid", "uuid-2"}, owner.VDSUUIDs)
	require.Equal(t, owner.VDSUUIDs, shared.VDSUUIDs)
	require.True(t, owner.Conversions[0].Create)
	require.True(t, shared.Conversions[0].Create)

	conflicting := []api.VDNResourceNaming{{VDNName: "vdn-a", TGWName: "first"}, {VDNName: "vdn-b", TGWName: "second"}}
	require.Contains(t, strings.Join(inspect(conflicting).Problems, " "), "conflicting TGW/VPC names")

	candidate := TargetTopology{TGWID: "tgw", VPCID: "vpc", ZoneIDs: []string{"zone-a"}, VDSUUIDs: []string{"uuid", "uuid-2", "uuid-3"}, VPCReady: true, Attached: true}
	service.TargetInventory = targetInventoryStub{topologies: []TargetTopology{candidate}}
	result = inspect(nil)
	require.True(t, result.Ready(), result.Problems)
	for _, network := range result.Networks {
		require.Equal(t, "tgw", network.Target.TGWID)
		require.Equal(t, "vpc", network.Target.VPCID)
		require.False(t, network.Target.CreateTGW)
		require.Empty(t, network.Target.SharedWithVDN)
	}
	candidate.VDSUUIDs = []string{"uuid"}
	service.TargetInventory = targetInventoryStub{topologies: []TargetTopology{candidate}}
	require.Contains(t, strings.Join(inspect(nil).Problems, " "), "unverified or incompatible VDS attachments")
}
