/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package transition

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/topology"
)

type dhcpDiscoveryFunc func(context.Context, Request) ([]string, error)

func (f dhcpDiscoveryFunc) DiscoverDHCPNetworks(ctx context.Context, req Request) ([]string, error) {
	return f(ctx, req)
}

func TestNamespaceEvaluateValidatesExistingAddressesAgainstVDNSubnet(t *testing.T) {
	config := api.DHCPNetworkConfiguration{VDNName: "vdn-a", DHCPRange: &api.IPAddressRange{Start: "192.0.2.10", End: "192.0.2.20"}, SubnetMask: "255.255.255.0", GatewayAddress: "192.0.2.1"}
	obj := &api.NamespaceNetworkTransition{Spec: api.NamespaceNetworkTransitionSpec{Namespaces: []string{"tenant"}, NetworkConfiguration: &api.NamespaceNetworkConfiguration{NamespaceDHCPNetworks: []api.DHCPNetworkConfiguration{config}}}}
	base := topology.SourceNetwork{VDNName: "vdn-a", VDSRef: "dvs-1", DVPGRefs: []string{"dvpg-1"}, DHCPKnown: true, DHCPBacked: true, Namespaces: []string{"tenant"}, WorkloadAddressInventoryComplete: true}
	for _, tc := range []struct {
		name      string
		address   string
		complete  bool
		wantError string
	}{
		{"outside subnet", "198.51.100.10/24", true, "outside DHCP subnet 192.0.2.0/24"},
		{"inside subnet outside pool", "192.0.2.100/24", true, ""},
		{"inside pool", "192.0.2.15", true, ""},
		{"unavailable inventory", "", false, "inventory for VDN \"vdn-a\" is incomplete"},
		{"malformed address", "bad-address", true, "invalid address"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := base
			source.WorkloadAddressInventoryComplete = tc.complete
			if tc.address != "" {
				source.WorkloadAddresses = []topology.WorkloadAddress{{Namespace: "tenant", Kind: "VirtualMachine", Name: "vm", Interface: "eth0", Address: tc.address}}
			}
			calls := 0
			h := EvaluateInputs{Discovery: NamespaceTopologyDHCPDiscovery{}, Evaluator: handlerFunc(func(context.Context, Request) (Result, error) { calls++; return Result{Outcome: OutcomeSucceeded}, nil })}
			result, err := h.Reconcile(context.Background(), Request{Stage: Namespaces, Object: obj, Status: &api.TransitionStatus{}, NamespaceTopology: namespaceTopology(source)})
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				require.Zero(t, calls)
			} else {
				require.NoError(t, err)
				require.Equal(t, OutcomeSucceeded, result.Outcome)
				require.Equal(t, 1, calls)
			}
		})
	}
}

func TestNamespaceEvaluateUsesEachWorkloadVDN(t *testing.T) {
	inputs := []api.DHCPNetworkConfiguration{
		{VDNName: "vdn-a", DHCPRange: &api.IPAddressRange{Start: "192.0.2.10", End: "192.0.2.20"}, SubnetMask: "255.255.255.0", GatewayAddress: "192.0.2.1"},
		{VDNName: "vdn-b", DHCPRange: &api.IPAddressRange{Start: "198.51.100.10", End: "198.51.100.20"}, SubnetMask: "255.255.255.0", GatewayAddress: "198.51.100.1"},
	}
	obj := &api.NamespaceNetworkTransition{Spec: api.NamespaceNetworkTransitionSpec{Namespaces: []string{"tenant"}, NetworkConfiguration: &api.NamespaceNetworkConfiguration{NamespaceDHCPNetworks: inputs}}}
	source := func(vdn, ip string) topology.SourceNetwork {
		return topology.SourceNetwork{VDNName: vdn, VDSRef: "dvs-1", DVPGRefs: []string{"dvpg-1"}, DHCPKnown: true, DHCPBacked: true, Namespaces: []string{"tenant"}, WorkloadAddressInventoryComplete: true, WorkloadAddresses: []topology.WorkloadAddress{{Namespace: "tenant", Kind: "VirtualMachine", Name: "vm", Interface: vdn, Address: ip}}}
	}
	a, b := source("vdn-a", "192.0.2.100/24"), source("vdn-b", "198.51.100.100/24")
	calls := 0
	h := EvaluateInputs{Discovery: NamespaceTopologyDHCPDiscovery{}, Evaluator: handlerFunc(func(context.Context, Request) (Result, error) { calls++; return Result{Outcome: OutcomeSucceeded}, nil })}
	req := Request{Stage: Namespaces, Object: obj, Status: &api.TransitionStatus{}, NamespaceTopology: namespaceTopology(a, b)}
	_, err := h.Reconcile(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	a.WorkloadAddresses[0].Address = "198.51.100.100/24"
	req.NamespaceTopology = namespaceTopology(a, b)
	_, err = h.Reconcile(context.Background(), req)
	require.ErrorContains(t, err, `VDN "vdn-a" is outside DHCP subnet 192.0.2.0/24`)
	require.Equal(t, 1, calls)
}

func TestEvaluateInputs(t *testing.T) {
	for _, stage := range []Stage{Infrastructure, Namespaces} {
		t.Run(string(stage), func(t *testing.T) {
			status := &api.TransitionStatus{}
			network := api.DHCPNetworkConfiguration{VDNName: "source-vdn"}
			req := Request{Stage: stage, Action: api.ActionEvaluate, Generation: 1, Status: status}
			if stage == Infrastructure {
				req.Object = &api.ControlPlaneNetworkTransition{Spec: api.ControlPlaneNetworkTransitionSpec{NetworkConfiguration: &api.ControlPlaneNetworkConfiguration{ControlPlaneAndServiceDHCPNetworks: []api.DHCPNetworkConfiguration{network}}}}
			} else {
				req.Object = &api.NamespaceNetworkTransition{Spec: api.NamespaceNetworkTransitionSpec{NetworkConfiguration: &api.NamespaceNetworkConfiguration{NamespaceDHCPNetworks: []api.DHCPNetworkConfiguration{network}}}}
			}
			e := &Engine{Enabled: true, Handlers: map[Key]Handler{{Stage: stage, Action: api.ActionEvaluate}: EvaluateInputs{}}}
			_, err := e.Reconcile(context.Background(), req)
			require.NoError(t, err)
			result, err := e.Reconcile(context.Background(), req)
			require.NoError(t, err)
			require.Equal(t, OutcomeNeedsInput, result.Outcome)
			require.Equal(t, "NeedsInput", status.Phase)
			require.Len(t, status.RequiredInputs, 1)
			require.Contains(t, status.RequiredInputs[0].FieldName, "[vdnName=source-vdn]")
			require.Contains(t, status.RequiredInputs[0].Description, "dhcpRange.start, dhcpRange.end, subnetMask, gatewayAddress")
			network.DHCPRange = &api.IPAddressRange{Start: "192.0.2.10", End: "192.0.2.20"}
			network.SubnetMask = "255.255.255.0"
			network.GatewayAddress = "192.0.2.1"
			switch obj := req.Object.(type) {
			case *api.ControlPlaneNetworkTransition:
				obj.Spec.NetworkConfiguration.ControlPlaneAndServiceDHCPNetworks[0] = network
			case *api.NamespaceNetworkTransition:
				obj.Spec.NetworkConfiguration.NamespaceDHCPNetworks[0] = network
			}
			req.Generation++
			_, err = e.Reconcile(context.Background(), req)
			require.NoError(t, err)
			_, err = e.Reconcile(context.Background(), req)
			require.NoError(t, err)
			require.Empty(t, status.RequiredInputs)
			require.Equal(t, "EvaluationFailed", status.Phase)
			require.Equal(t, metav1.ConditionFalse, meta.FindStatusCondition(status.Conditions, "Ready").Status)
		})
	}
}

func TestEvaluateInputsDelegatesToStageHandler(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outcome Outcome
		err     error
		phase   string
	}{
		{"success", OutcomeSucceeded, nil, "Evaluated"},
		{"discovered missing VDN", OutcomeNeedsInput, nil, "NeedsInput"},
		{"SNAT allocation failure", "", errors.New("SNAT allocation for network backend-a failed: pool exhausted"), "EvaluationFailed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status := &api.TransitionStatus{}
			obj := &api.ControlPlaneNetworkTransition{}
			req := Request{Stage: Infrastructure, Action: api.ActionEvaluate, Generation: 1, Object: obj, Status: status}
			calls := 0
			next := handlerFunc(func(_ context.Context, received Request) (Result, error) {
				calls++
				require.Same(t, obj, received.Object)
				if tc.outcome == OutcomeNeedsInput {
					received.Status.RequiredInputs = []api.RequiredInput{{FieldName: "spec.networkConfiguration.controlPlaneAndServiceDHCPNetworks[vdnName=discovered-vdn]", Description: "Supply DHCP settings for discovered-vdn"}}
				}
				return Result{Outcome: tc.outcome}, tc.err
			})
			e := &Engine{Enabled: true, Handlers: map[Key]Handler{{Stage: Infrastructure, Action: api.ActionEvaluate}: EvaluateInputs{Discovery: dhcpDiscoveryFunc(func(context.Context, Request) ([]string, error) { return nil, nil }), Evaluator: next}}}
			_, err := e.Reconcile(context.Background(), req)
			require.NoError(t, err)
			_, err = e.Reconcile(context.Background(), req)
			if tc.err != nil {
				require.NoError(t, err)
				require.Contains(t, meta.FindStatusCondition(status.Conditions, "Ready").Message, "pool exhausted")
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, 1, calls)
			require.Equal(t, tc.phase, status.Phase)
			if tc.outcome == OutcomeNeedsInput {
				require.Len(t, status.RequiredInputs, 1)
				require.Contains(t, status.RequiredInputs[0].FieldName, "discovered-vdn")
			}
		})
	}
}

func TestEvaluateInputsCollectsMissingFieldsAndDiscoveredNetworks(t *testing.T) {
	obj := &api.NamespaceNetworkTransition{Spec: api.NamespaceNetworkTransitionSpec{NetworkConfiguration: &api.NamespaceNetworkConfiguration{NamespaceDHCPNetworks: []api.DHCPNetworkConfiguration{{VDNName: "incomplete"}}}}}
	called := false
	h := EvaluateInputs{Discovery: dhcpDiscoveryFunc(func(context.Context, Request) ([]string, error) {
		return []string{"incomplete", "missing", "missing"}, nil
	}), Evaluator: handlerFunc(func(context.Context, Request) (Result, error) {
		called = true
		return Result{Outcome: OutcomeSucceeded}, nil
	})}
	status := &api.TransitionStatus{}
	result, err := h.Reconcile(context.Background(), Request{Stage: Namespaces, Object: obj, Status: status})
	require.NoError(t, err)
	require.Equal(t, OutcomeNeedsInput, result.Outcome)
	require.False(t, called)
	require.Len(t, status.RequiredInputs, 2)
	require.Contains(t, status.RequiredInputs[0].FieldName, "incomplete")
	require.Contains(t, status.RequiredInputs[1].FieldName, "missing")
}

func TestEvaluateInputsRejectsUnknownVDNWithoutRequiredInputs(t *testing.T) {
	valid := api.DHCPNetworkConfiguration{
		VDNName:        "unknown-vdn",
		DHCPRange:      &api.IPAddressRange{Start: "192.0.2.10", End: "192.0.2.20"},
		SubnetMask:     "255.255.255.0",
		GatewayAddress: "192.0.2.1",
	}
	for _, tc := range []struct {
		name    string
		network api.DHCPNetworkConfiguration
	}{
		{name: "complete fields", network: valid},
		{name: "missing fields", network: api.DHCPNetworkConfiguration{VDNName: "unknown-vdn"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := &api.NamespaceNetworkTransition{Spec: api.NamespaceNetworkTransitionSpec{
				NetworkConfiguration: &api.NamespaceNetworkConfiguration{
					NamespaceDHCPNetworks: []api.DHCPNetworkConfiguration{tc.network},
				},
			}}
			status := &api.TransitionStatus{}
			handler := EvaluateInputs{Discovery: dhcpDiscoveryFunc(func(context.Context, Request) ([]string, error) {
				return []string{"discovered-vdn"}, nil
			})}

			_, err := handler.Reconcile(context.Background(), Request{
				Stage: Namespaces, Object: obj, Status: status,
			})
			require.ErrorContains(t, err, "does not match a discovered DHCP network")
			require.Empty(t, status.RequiredInputs)
		})
	}
}

func TestEvaluateInputDiscoveryFailure(t *testing.T) {
	status := &api.TransitionStatus{}
	h := EvaluateInputs{Discovery: dhcpDiscoveryFunc(func(context.Context, Request) ([]string, error) {
		return nil, errors.New("source inventory unavailable")
	})}
	obj := &api.NamespaceNetworkTransition{Spec: api.NamespaceNetworkTransitionSpec{NetworkConfiguration: &api.NamespaceNetworkConfiguration{NamespaceDHCPNetworks: []api.DHCPNetworkConfiguration{{VDNName: "incomplete"}}}}}
	_, err := h.Reconcile(context.Background(), Request{Stage: Namespaces, Object: obj, Status: status})
	require.ErrorContains(t, err, "source inventory unavailable")
	require.Empty(t, status.RequiredInputs)
}

func TestDHCPInputValidation(t *testing.T) {
	base := api.DHCPNetworkConfiguration{VDNName: "vdn", DHCPRange: &api.IPAddressRange{Start: "192.0.2.10", End: "192.0.2.20"}, SubnetMask: "255.255.255.0", GatewayAddress: "192.0.2.1"}
	for _, tc := range []struct {
		name   string
		change func(*api.DHCPNetworkConfiguration)
		want   string
	}{
		{"valid", func(*api.DHCPNetworkConfiguration) {}, ""},
		{"reversed range", func(n *api.DHCPNetworkConfiguration) { n.DHCPRange.End = "192.0.2.5" }, "must not exceed"},
		{"different subnet", func(n *api.DHCPNetworkConfiguration) { n.DHCPRange.End = "192.0.3.5" }, "must belong to subnet"},
		{"gateway outside subnet", func(n *api.DHCPNetworkConfiguration) { n.GatewayAddress = "192.0.3.1" }, "must belong to subnet"},
		{"noncontiguous mask", func(n *api.DHCPNetworkConfiguration) { n.SubnetMask = "255.0.255.0" }, "contiguous IPv4 mask"},
		{"zero mask", func(n *api.DHCPNetworkConfiguration) { n.SubnetMask = "0.0.0.0" }, "prefix length between /1 and /32"},
		{"network address gateway", func(n *api.DHCPNetworkConfiguration) { n.GatewayAddress = "192.0.2.0" }, "network or broadcast address"},
		{"broadcast address gateway", func(n *api.DHCPNetworkConfiguration) { n.GatewayAddress = "192.0.2.255" }, "network or broadcast address"},
		{"valid /31 gateway", func(n *api.DHCPNetworkConfiguration) {
			n.DHCPRange.Start, n.DHCPRange.End = "192.0.2.0", "192.0.2.1"
			n.SubnetMask, n.GatewayAddress = "255.255.255.254", "192.0.2.0"
		}, ""},
		{"ipv6 start", func(n *api.DHCPNetworkConfiguration) { n.DHCPRange.Start = "2001:db8::1" }, "plain IPv4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			network := *base.DeepCopy()
			tc.change(&network)
			err := validateDHCPNetwork(network)
			if tc.want == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.want)
			}
		})
	}
}

func TestEvaluateSNATInput(t *testing.T) {
	for _, tc := range []struct {
		name      string
		addresses []string
		want      string
	}{
		{"IPv4", []string{"192.0.2.10"}, "source network discovery"},
		{"IPv6", []string{"2001:db8::1"}, "must be a plain IPv4"},
		{"CIDR", []string{"192.0.2.10/32"}, "must be a plain IPv4"},
		{"multiple", []string{"192.0.2.10", "192.0.2.11"}, "at most one"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := &api.ControlPlaneNetworkTransition{Spec: api.ControlPlaneNetworkTransitionSpec{NetworkConfiguration: &api.ControlPlaneNetworkConfiguration{LBSNATIPs: tc.addresses}}}
			_, err := (EvaluateInputs{}).Reconcile(context.Background(), Request{Stage: Infrastructure, Object: obj, Status: &api.TransitionStatus{}})
			require.ErrorContains(t, err, tc.want)
		})
	}
}
