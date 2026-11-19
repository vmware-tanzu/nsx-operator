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
)

type dhcpDiscoveryFunc func(context.Context, Request) ([]string, error)

func (f dhcpDiscoveryFunc) DiscoverDHCPNetworks(ctx context.Context, req Request) ([]string, error) {
	return f(ctx, req)
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
			require.ErrorContains(t, err, "source network discovery")
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
				require.ErrorIs(t, err, tc.err)
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
