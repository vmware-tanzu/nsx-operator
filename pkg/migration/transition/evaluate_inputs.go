/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package transition

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

// DHCPNetworkDiscovery returns the names of all DHCP-backed source VDNs in the
// requested stage. It must only inspect sources and must not allocate resources.
type DHCPNetworkDiscovery interface {
	DiscoverDHCPNetworks(context.Context, Request) ([]string, error)
}

// EvaluateInputs discovers required networks and validates supplied inputs before
// invoking the stage evaluator. The
// infrastructure evaluator also owns SNAT selection and reservation. It must
// return allocation failures with actionable context and preserve accepted
// allocation identities across retries. Evaluator may update the request's status;
// the engine persists that status, including diagnostics returned with errors.
// A nil Evaluator keeps Evaluate unsuccessful until the stage evaluator is wired.
type EvaluateInputs struct {
	Discovery DHCPNetworkDiscovery
	Preflight Handler
	Evaluator Handler
}

func (h EvaluateInputs) Reconcile(ctx context.Context, req Request) (Result, error) {
	var networks []api.DHCPNetworkConfiguration
	var field string
	switch obj := req.Object.(type) {
	case *api.ControlPlaneNetworkTransition:
		if req.Stage != Infrastructure {
			return Result{}, fmt.Errorf("control plane input supplied for stage %s", req.Stage)
		}
		field = "spec.networkConfiguration.controlPlaneAndServiceDHCPNetworks"
		if obj.Spec.NetworkConfiguration != nil {
			networks = obj.Spec.NetworkConfiguration.ControlPlaneAndServiceDHCPNetworks
			if err := validateLBSNATIPs(obj.Spec.NetworkConfiguration.LBSNATIPs); err != nil {
				return Result{}, fmt.Errorf("spec.networkConfiguration.lbSNATIPs: %w", err)
			}
		}
	case *api.NamespaceNetworkTransition:
		if req.Stage != Namespaces {
			return Result{}, fmt.Errorf("namespace input supplied for stage %s", req.Stage)
		}
		field = "spec.networkConfiguration.namespaceDHCPNetworks"
		if obj.Spec.NetworkConfiguration != nil {
			networks = obj.Spec.NetworkConfiguration.NamespaceDHCPNetworks
		}
	default:
		return Result{}, fmt.Errorf("unsupported transition object %T", req.Object)
	}
	req.Status.RequiredInputs = nil
	req.Status.OptionalInputs = nil
	if h.Preflight != nil {
		result, err := h.Preflight.Reconcile(ctx, req)
		if err != nil {
			return Result{}, err
		}
		if result.Outcome != OutcomeSucceeded {
			return result, nil
		}
	}
	configured := make(map[string]bool, len(networks))
	for _, network := range networks {
		if network.VDNName == "" || configured[network.VDNName] {
			return Result{}, fmt.Errorf("%s: each entry must have a unique vdnName", field)
		}
		configured[network.VDNName] = true
	}
	var discoveredVDNs []string
	if h.Discovery != nil {
		vdns, err := h.Discovery.DiscoverDHCPNetworks(ctx, req)
		if err != nil {
			return Result{}, fmt.Errorf("discovering DHCP networks: %w", err)
		}
		discovered := make(map[string]bool, len(vdns))
		for _, vdn := range vdns {
			if vdn == "" {
				return Result{}, fmt.Errorf("discovered DHCP network has no vdnName")
			}
			discovered[vdn] = true
		}
		for _, network := range networks {
			if !discovered[network.VDNName] {
				return Result{}, fmt.Errorf("%s[vdnName=%s]: VDN does not match a discovered DHCP network", field, network.VDNName)
			}
		}
		discoveredVDNs = vdns
	}
	var requiredInputs []api.RequiredInput
	for _, network := range networks {
		path := fmt.Sprintf("%s[vdnName=%s]", field, network.VDNName)
		missing := missingDHCPFields(network)
		if len(missing) != 0 {
			requiredInputs = append(requiredInputs, api.RequiredInput{
				FieldName: path, Description: "Supply " + strings.Join(missing, ", ") + " for DHCP network " + network.VDNName + ".",
			})
			continue
		}
		if err := validateDHCPNetwork(network); err != nil {
			return Result{}, fmt.Errorf("%s: %w", path, err)
		}
	}
	missingVDNs := make(map[string]bool, len(discoveredVDNs))
	for _, vdn := range discoveredVDNs {
		if configured[vdn] || missingVDNs[vdn] {
			continue
		}
		missingVDNs[vdn] = true
		requiredInputs = append(requiredInputs, api.RequiredInput{
			FieldName:   fmt.Sprintf("%s[vdnName=%s]", field, vdn),
			Description: "Supply dhcpRange.start, dhcpRange.end, subnetMask, gatewayAddress for DHCP network " + vdn + ".",
		})
	}
	if len(requiredInputs) > 1000 {
		return Result{}, fmt.Errorf("discovered more than 1000 DHCP networks requiring input")
	}
	req.Status.RequiredInputs = requiredInputs
	if len(req.Status.RequiredInputs) > 0 {
		return Result{Outcome: OutcomeNeedsInput}, nil
	}
	if h.Discovery == nil || h.Evaluator == nil {
		return Result{}, fmt.Errorf("input validation completed; source network discovery and remaining Evaluate checks are not implemented")
	}
	return h.Evaluator.Reconcile(ctx, req)
}

func validateLBSNATIPs(addresses []string) error {
	if len(addresses) > 1 {
		return fmt.Errorf("at most one IPv4 address is supported")
	}
	for _, address := range addresses {
		ip, err := netip.ParseAddr(address)
		if err != nil || !ip.Is4() {
			return fmt.Errorf("%q must be a plain IPv4 address", address)
		}
	}
	return nil
}

func missingDHCPFields(network api.DHCPNetworkConfiguration) []string {
	var missing []string
	if network.DHCPRange == nil || network.DHCPRange.Start == "" {
		missing = append(missing, "dhcpRange.start")
	}
	if network.DHCPRange == nil || network.DHCPRange.End == "" {
		missing = append(missing, "dhcpRange.end")
	}
	if network.SubnetMask == "" {
		missing = append(missing, "subnetMask")
	}
	if network.GatewayAddress == "" {
		missing = append(missing, "gatewayAddress")
	}
	return missing
}

func validateDHCPNetwork(network api.DHCPNetworkConfiguration) error {
	start, err := netip.ParseAddr(network.DHCPRange.Start)
	if err != nil || !start.Is4() {
		return fmt.Errorf("dhcpRange.start must be a plain IPv4 address")
	}
	end, err := netip.ParseAddr(network.DHCPRange.End)
	if err != nil || !end.Is4() {
		return fmt.Errorf("dhcpRange.end must be a plain IPv4 address")
	}
	mask := net.ParseIP(network.SubnetMask).To4()
	if mask == nil {
		return fmt.Errorf("subnetMask must be a contiguous IPv4 mask")
	}
	ones, bits := net.IPMask(mask).Size()
	if bits != 32 || ones == 0 {
		return fmt.Errorf("subnetMask must be a contiguous IPv4 mask with a prefix length between /1 and /32")
	}
	gateway, err := netip.ParseAddr(network.GatewayAddress)
	if err != nil || !gateway.Is4() {
		return fmt.Errorf("gatewayAddress must be a plain IPv4 address")
	}
	if start.Compare(end) > 0 {
		return fmt.Errorf("dhcpRange.start must not exceed dhcpRange.end")
	}
	subnet := netip.PrefixFrom(start, ones).Masked()
	if !subnet.Contains(end) || !subnet.Contains(gateway) {
		return fmt.Errorf("dhcpRange.end and gatewayAddress must belong to subnet %s derived from dhcpRange.start and subnetMask", subnet)
	}
	if ones < 31 {
		networkAddress := subnet.Addr()
		broadcastOctets := networkAddress.As4()
		for i := range broadcastOctets {
			broadcastOctets[i] |= ^mask[i]
		}
		broadcastAddress := netip.AddrFrom4(broadcastOctets)
		if gateway == networkAddress || gateway == broadcastAddress {
			return fmt.Errorf("gatewayAddress must not be the network or broadcast address of subnet %s", subnet)
		}
	}
	return nil
}
