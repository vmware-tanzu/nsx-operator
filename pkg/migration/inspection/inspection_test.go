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
	sources map[string]Source
	calls   int
	err     error
}

func (inventory *inventoryStub) NamespaceVDNs(context.Context, []string) ([]string, error) {
	return []string{"a", "a"}, inventory.err
}
func (inventory *inventoryStub) Resolve(_ context.Context, vdnName string) (Source, error) {
	inventory.calls++
	return inventory.sources[vdnName], inventory.err
}
func sourceForVDN(vdnName string) Source {
	return Source{VDNName: vdnName, DHCP: true, Topologies: []Topology{{PortGroupID: "dvportgroup-1", SwitchID: "dvs-1", SwitchUUID: "uuid", ClusterIDs: []string{"domain-c1"}}}}
}
func validDHCPInput(vdnName string) api.DHCPNetworkConfiguration {
	return api.DHCPNetworkConfiguration{VDNName: vdnName, SubnetMask: "255.255.255.0", GatewayAddress: "192.168.10.1", DHCPRange: &api.IPAddressRange{Start: "192.168.10.100", End: "192.168.10.200"}}
}

func TestInspectionAndDiagnostics(t *testing.T) {
	ctx := context.Background()
	inventory := &inventoryStub{sources: map[string]Source{"a": sourceForVDN("a")}}
	inspectionService := Service{Inventory: inventory}
	result, err := inspectionService.InspectNamespaces(ctx, []string{"ns1", "ns2"}, nil)
	require.NoError(t, err)
	require.Equal(t, 1, inventory.calls)
	require.False(t, result.Ready())
	require.Len(t, result.RequiredInputs, 1)
	require.Contains(t, result.RequiredInputs[0].Description, "dhcpRange.start")
	require.Contains(t, result.RequiredInputs[0].Description, "VDN a")
	status := api.TransitionStatus{RequiredInputs: []api.RequiredInput{{Key: "other", Description: "another service"}}, LastAction: api.ActionEvaluate, ActionGeneration: 1, ActionState: api.ActionStateRunning}
	require.NoError(t, result.Apply(&status, 1))
	require.Equal(t, api.PhaseNeedsInput, status.Phase)
	require.Len(t, status.RequiredInputs, 2)
	result, err = inspectionService.Inspect(ctx, []string{"a"}, []api.DHCPNetworkConfiguration{validDHCPInput("a")})
	require.NoError(t, err)
	require.True(t, result.Ready())
	require.NoError(t, result.Apply(&status, 2))
	require.Len(t, status.RequiredInputs, 1)
	require.Equal(t, "other", status.RequiredInputs[0].Key)
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

func TestInvalidInputsAreNotNeedsInput(t *testing.T) {
	inspectionService := Service{Inventory: &inventoryStub{sources: map[string]Source{"a": sourceForVDN("a")}}}
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
	inventory := &inventoryStub{sources: map[string]Source{"a": firstVDNSource, "b": secondVDNSource}}
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

func TestVDNWithMultipleDVPGsAcrossVDSes(t *testing.T) {
	vdnSource := sourceForVDN("vdn-multi-dvpg")
	vdnSource.Topologies = append(vdnSource.Topologies, Topology{
		PortGroupID: "dvportgroup-2",
		SwitchID:    "dvs-2",
		SwitchUUID:  "uuid-2",
		ClusterIDs:  []string{"domain-c2", "domain-c3"},
	})
	inventory := &inventoryStub{sources: map[string]Source{vdnSource.VDNName: vdnSource}}
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

func TestIncompleteOrDuplicateVDNTopologiesAreRejected(t *testing.T) {
	validVDNSource := sourceForVDN("vdn-multi-dvpg")
	incompleteTopologies := append(append([]Topology(nil), validVDNSource.Topologies...), Topology{PortGroupID: "dvportgroup-2", SwitchID: "dvs-2"})
	duplicateTopologies := []Topology{validVDNSource.Topologies[0], validVDNSource.Topologies[0]}
	for _, testCase := range []struct {
		name       string
		topologies []Topology
	}{
		{name: "incomplete backing", topologies: incompleteTopologies},
		{name: "duplicate backing", topologies: duplicateTopologies},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			vdnSource := validVDNSource
			vdnSource.Topologies = testCase.topologies
			inventory := &inventoryStub{sources: map[string]Source{vdnSource.VDNName: vdnSource}}
			result, err := (Service{Inventory: inventory}).Inspect(context.Background(), []string{vdnSource.VDNName}, []api.DHCPNetworkConfiguration{validDHCPInput(vdnSource.VDNName)})
			require.NoError(t, err)
			require.NotEmpty(t, result.Problems)
		})
	}
}
