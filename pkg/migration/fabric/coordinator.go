/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package fabric

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/inspection"
)

// NamespaceInspector is implemented by inspection.Service. Inspection is kept
// separate from fabric preparation so callers can inspect once and pass the
// validated result to PrepareInspected.
type NamespaceInspector interface {
	InspectNamespaces(context.Context, []string, []api.DHCPNetworkConfiguration) (inspection.Result, error)
}

// IdentityResolver prepares identities without writing NSX resources.
type IdentityResolver interface {
	ResolveIdentities(context.Context, FabricPreparationRequest) (FrozenIdentities, error)
}

// Preparer performs the level-driven NSX resource preparation.
type Preparer interface {
	Prepare(context.Context, FabricPreparationRequest) (Result, error)
	PrepareIPBlock(context.Context, FabricPreparationRequest) (bool, error)
}

// CoordinatorCheckpointStore persists the selected TGW, profile and VPC IDs
// before the first fabric write. PreparationCheckpointStore satisfies this
// interface. A transition-status-backed implementation is still needed to
// connect this contract to the production reconciler.
type CoordinatorCheckpointStore interface {
	Load(context.Context, CheckpointKey) (PreparationCheckpoint, string, bool, error)
	Save(context.Context, PreparationCheckpoint, string) (string, error)
}

// PreparationCoordinator builds fabric requests from validated inspection
// output. It does not perform namespace inspection or DVPG conversion itself.
// TODO(WBS 1.4.3 integration): submit one checkpointed conversion per DVPG,
// reuse inspected PortSettings, and verify the resulting DVC, VLAN extension
// Subnet and PortSetting before the owning Execute handler reports success.
// TODO(WBS 1.4.3 integration): prepare target LB SNAT resources after the
// target VPC/profile paths are known, using the accepted reservation.
type PreparationCoordinator struct {
	Inspector   NamespaceInspector
	Resolver    IdentityResolver
	Preparer    Preparer
	Checkpoints CoordinatorCheckpointStore
}

// PreparationOptions contains transition identity and reservations already
// accepted by the owning transition. VIPRanges are inclusive; AcceptedSNATIP
// is excluded from every inspected block whose CIDR contains that address.
type PreparationOptions struct {
	TransitionUID    string
	ActionGeneration int64
	ProjectPath      string
	NamespaceUID     string
	VPCPurpose       VPCPurpose
	VIPRanges        []IPRange
	AcceptedSNATIP   string
	SubnetExclusive  bool
}

// InspectedPreparation reports both the authoritative inspection and fabric
// progress. A non-ready inspection returns without making any fabric calls.
type InspectedPreparation struct {
	Inspection inspection.Result
	Networks   []NetworkPreparation
	Ready      bool
}

// NetworkPreparation records one source VDN's request and observed result.
type NetworkPreparation struct {
	VDNName string
	Request FabricPreparationRequest
	Result  Result
}

// InspectAndPrepareNamespaces calls the existing namespace inspection
// interface first, checks Ready, then delegates only validated networks to the
// fabric coordinator.
func (coordinator PreparationCoordinator) InspectAndPrepareNamespaces(
	ctx context.Context,
	namespaces []string,
	dhcpInputs []api.DHCPNetworkConfiguration,
	options PreparationOptions,
) (InspectedPreparation, error) {
	if coordinator.Inspector == nil {
		return InspectedPreparation{}, fmt.Errorf("namespace inspector is required")
	}
	inspected, err := coordinator.Inspector.InspectNamespaces(ctx, namespaces, dhcpInputs)
	if err != nil {
		return InspectedPreparation{}, err
	}
	return coordinator.PrepareInspected(ctx, inspected, options)
}

// PrepareInspected prepares one IP block per inspected network while reusing
// the checkpointed TGW/profile/VPC identities for networks that share target
// topology. Callers must not construct network facts from source inventory.
func (coordinator PreparationCoordinator) PrepareInspected(ctx context.Context, inspected inspection.Result, options PreparationOptions) (InspectedPreparation, error) {
	result := InspectedPreparation{Inspection: inspected}
	if !inspected.Ready() && !checkpointedTargetRetry(inspected) {
		return result, nil
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if coordinator.Resolver == nil || coordinator.Preparer == nil || coordinator.Checkpoints == nil {
		return result, fmt.Errorf("fabric resolver, preparer and checkpoint store are required")
	}
	if options.TransitionUID == "" || options.ActionGeneration < 1 || options.ProjectPath == "" {
		return result, fmt.Errorf("transition UID, action generation and NSX project path are required")
	}
	options.ProjectPath = strings.TrimSuffix(options.ProjectPath, "/")
	if options.VPCPurpose == "" {
		options.VPCPurpose = VPCPurposeWorkload
	}

	networks := append([]inspection.Network(nil), inspected.Networks...)
	sort.Slice(networks, func(i, j int) bool { return networks[i].Source.VDNName < networks[j].Source.VDNName })
	groups := make(map[string][]inspection.Network)
	for _, network := range networks {
		if network.Source.VDNName == "" || !network.Addressing.CIDR.IsValid() {
			return result, fmt.Errorf("inspection returned a network without a VDN identity or validated CIDR")
		}
		key, err := targetGroupKey(network)
		if err != nil {
			return result, err
		}
		groups[key] = append(groups[key], network)
	}
	groupKeys := make([]string, 0, len(groups))
	for key := range groups {
		groupKeys = append(groupKeys, key)
	}
	sort.Strings(groupKeys)

	type groupPlan struct {
		networks []inspection.Network
		identity FrozenIdentities
		blockIDs []string
	}
	plans := make([]groupPlan, 0, len(groupKeys))
	// Resolve and persist every validated group's identity before the first NSX
	// write. This prevents an earlier group write from changing discovery before
	// a later group's checkpoint exists.
	for _, groupKey := range groupKeys {
		groupNetworks := groups[groupKey]
		first := groupNetworks[0]
		tgwID, vpcID, err := validateGroupTargetIdentity(groupNetworks)
		if err != nil {
			return result, err
		}
		groupIdentity := stableIdentity("target-plan", options.TransitionUID, fmt.Sprint(options.ActionGeneration), stableGroupPlanID(groupNetworks))
		planDigest := inspectedGroupDigest(groupNetworks)
		groupBlockIDs := make([]string, 0, len(groupNetworks))
		for _, network := range groupNetworks {
			groupBlockIDs = append(groupBlockIDs, networkBlockID(network, options))
		}
		groupRequest := FabricPreparationRequest{
			ProjectPath:       options.ProjectPath,
			Identity:          groupIdentity,
			NamespaceUID:      options.NamespaceUID,
			VPCPurpose:        options.VPCPurpose,
			Zones:             sourceZones(first),
			VDS:               sortedUnique(first.Target.VDSUUIDs),
			TGWID:             tgwID,
			VPCID:             vpcID,
			ProfileIPBlockIDs: groupBlockIDs,
		}
		checkpointKey := CheckpointKey{TransitionUID: options.TransitionUID, ActionGeneration: options.ActionGeneration, Identity: groupIdentity}
		checkpoint, revision, found, err := coordinator.Checkpoints.Load(ctx, checkpointKey)
		if err != nil {
			return result, fmt.Errorf("load fabric identity checkpoint for topology %s: %w", groupIdentity, err)
		}
		var identities FrozenIdentities
		if found {
			if checkpoint.Key != checkpointKey || checkpoint.PlanDigest != planDigest || checkpoint.Identities == nil || checkpoint.Identities.TGWID == "" || checkpoint.Identities.ProfileID == "" || checkpoint.Identities.VPCID == "" {
				return result, fmt.Errorf("fabric identity checkpoint for topology %s is incomplete or inconsistent", groupIdentity)
			}
			identities = *checkpoint.Identities
		} else {
			for _, network := range groupNetworks {
				if network.Target.Pending {
					return result, nil
				}
			}
			identities, err = coordinator.Resolver.ResolveIdentities(ctx, groupRequest)
			if err != nil {
				return result, fmt.Errorf("resolve fabric identities for topology %s: %w", groupIdentity, err)
			}
			if identities.TGWID == "" || identities.ProfileID == "" || identities.VPCID == "" {
				return result, fmt.Errorf("fabric resolver returned incomplete identities for topology %s", groupIdentity)
			}
			checkpoint = PreparationCheckpoint{Key: checkpointKey, PlanDigest: planDigest, Identities: &identities}
			if _, err := coordinator.Checkpoints.Save(ctx, checkpoint, revision); err != nil {
				return result, fmt.Errorf("persist fabric identity checkpoint for topology %s: %w", groupIdentity, err)
			}
		}
		plans = append(plans, groupPlan{networks: groupNetworks, identity: identities, blockIDs: groupBlockIDs})
	}

	for _, plan := range plans {
		groupNetworks := plan.networks
		identities := plan.identity
		groupBlockIDs := plan.blockIDs
		requests := make([]FabricPreparationRequest, 0, len(groupNetworks))
		for _, network := range groupNetworks {
			request, err := buildNetworkRequest(network, options, identities, groupBlockIDs)
			if err != nil {
				return result, err
			}
			requests = append(requests, request)
			blockReady, err := coordinator.Preparer.PrepareIPBlock(ctx, request)
			if err != nil {
				return result, fmt.Errorf("prepare IP block for VDN %s: %w", network.Source.VDNName, err)
			}
			if !blockReady {
				result.Networks = append(result.Networks, NetworkPreparation{
					VDNName: network.Source.VDNName,
					Request: request,
					Result:  Result{State: PreparationPending, Pending: &ResourceRef{Kind: IPBlock, Path: options.ProjectPath + "/infra/ip-blocks/" + request.IPBlockID}},
				})
				return result, nil
			}
		}

		for i, network := range groupNetworks {
			request := requests[i]
			prepared, err := coordinator.Preparer.Prepare(ctx, request)
			if err != nil {
				return result, fmt.Errorf("prepare fabric for VDN %s: %w", network.Source.VDNName, err)
			}
			result.Networks = append(result.Networks, NetworkPreparation{VDNName: network.Source.VDNName, Request: request, Result: prepared})
			if !prepared.Ready {
				result.Ready = false
				return result, nil
			}
		}
	}
	result.Ready = len(result.Networks) == len(networks)
	return result, nil
}

func buildNetworkRequest(network inspection.Network, options PreparationOptions, identities FrozenIdentities, profileIPBlockIDs []string) (FabricPreparationRequest, error) {
	networkIdentity := stableIdentity("network", options.TransitionUID, fmt.Sprint(options.ActionGeneration), network.Source.VDNName)
	cidr := network.Addressing.CIDR.String()
	allowed := make([]IPRange, 0, len(network.Addressing.Ranges))
	for _, sourceRange := range network.Addressing.Ranges {
		allowed = append(allowed, IPRange{Start: sourceRange.Start, End: sourceRange.End})
	}
	exclusions, err := applicableExclusions(cidr, options.VIPRanges, options.AcceptedSNATIP)
	if err != nil {
		return FabricPreparationRequest{}, fmt.Errorf("build exclusions for VDN %s: %w", network.Source.VDNName, err)
	}
	return FabricPreparationRequest{
		ProjectPath:  options.ProjectPath,
		Identity:     networkIdentity,
		NamespaceUID: options.NamespaceUID,
		VPCPurpose:   options.VPCPurpose,
		Frozen:       &identities,
		Zones:        sourceZones(network),
		VDS:          sortedUnique(network.Target.VDSUUIDs),
		// Checkpointed identities are authoritative on retries. Discovery can
		// expose a different partial candidate after our previous NSX write.
		TGWID:                  identities.TGWID,
		VPCID:                  identities.VPCID,
		IPBlockID:              networkBlockID(network, options),
		ProfileIPBlockIDs:      append([]string(nil), profileIPBlockIDs...),
		IPBlockCIDR:            cidr,
		IPBlockAllowedIPs:      allowed,
		IPBlockExcludedIPs:     exclusions,
		IPBlockSubnetExclusive: options.SubnetExclusive,
	}, nil
}

func validateGroupTargetIdentity(networks []inspection.Network) (string, string, error) {
	var tgwID, vpcID string
	for _, network := range networks {
		candidateTGW := targetID(network.Target.TGWID, network.Target.TGWName)
		candidateVPC := targetID(network.Target.VPCID, network.Target.VPCName)
		if candidateTGW != "" && tgwID != "" && candidateTGW != tgwID {
			return "", "", fmt.Errorf("VDNs sharing a target span have conflicting TGW identities %q and %q", tgwID, candidateTGW)
		}
		if candidateVPC != "" && vpcID != "" && candidateVPC != vpcID {
			return "", "", fmt.Errorf("VDNs sharing a target span have conflicting VPC identities %q and %q", vpcID, candidateVPC)
		}
		if tgwID == "" {
			tgwID = candidateTGW
		}
		if vpcID == "" {
			vpcID = candidateVPC
		}
	}
	return tgwID, vpcID, nil
}

func networkBlockID(network inspection.Network, options PreparationOptions) string {
	networkIdentity := stableIdentity("network", options.TransitionUID, fmt.Sprint(options.ActionGeneration), network.Source.VDNName)
	return stableResourceID("block", networkIdentity)
}

func targetGroupKey(network inspection.Network) (string, error) {
	zones := sourceZones(network)
	if len(zones) == 0 {
		return "", fmt.Errorf("VDN %s has no inspected target zones", network.Source.VDNName)
	}
	// The source zone/VDS span is stable across reconciles. Target IDs and
	// SharedWithVDN can change after this transition's own writes, so they must
	// not participate in the durable plan identity or grouping key.
	return strings.Join([]string{
		strings.Join(zones, ","),
		strings.Join(sortedUnique(network.Target.VDSUUIDs), ","),
	}, "\x00"), nil
}

func checkpointedTargetRetry(inspected inspection.Result) bool {
	if len(inspected.RequiredInputs) != 0 || len(inspected.Problems) != 0 || len(inspected.MissingZones) != 0 || len(inspected.Networks) == 0 {
		return false
	}
	pending := false
	for _, network := range inspected.Networks {
		if !network.Addressing.CIDR.IsValid() {
			return false
		}
		pending = pending || network.Target.Pending
	}
	return pending
}

func stableGroupPlanID(networks []inspection.Network) string {
	ids := make([]string, 0, len(networks))
	for _, network := range networks {
		ids = append(ids, network.Source.VDNName)
	}
	sort.Strings(ids)
	return strings.Join(ids, "\x00")
}

func inspectedGroupDigest(networks []inspection.Network) string {
	ordered := append([]inspection.Network(nil), networks...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Source.VDNName < ordered[j].Source.VDNName })
	var facts []string
	for _, network := range ordered {
		facts = append(facts, network.Source.VDNName, fmt.Sprint(network.Source.DHCP), network.Addressing.CIDR.String(), network.Addressing.Gateway.String())
		allocations := append([]api.IPAddressRange(nil), network.Addressing.Ranges...)
		sort.Slice(allocations, func(i, j int) bool {
			if allocations[i].Start == allocations[j].Start {
				return allocations[i].End < allocations[j].End
			}
			return allocations[i].Start < allocations[j].Start
		})
		for _, allocation := range allocations {
			facts = append(facts, allocation.Start, allocation.End)
		}
		facts = append(facts, "--topologies--")
		topologies := append([]inspection.Topology(nil), network.Source.Topologies...)
		sort.Slice(topologies, func(i, j int) bool {
			left := topologies[i].VCenterUUID + "\x00" + topologies[i].PortGroupID
			right := topologies[j].VCenterUUID + "\x00" + topologies[j].PortGroupID
			return left < right
		})
		for _, topology := range topologies {
			facts = append(facts, topology.VCenterUUID, topology.PortGroupID, topology.SwitchUUID)
			facts = append(facts, sortedUnique(topology.ZoneIDs)...)
		}
		facts = append(facts, "--end-network--")
	}
	hash := sha256.Sum256([]byte(strings.Join(facts, "\x00")))
	return hex.EncodeToString(hash[:])
}

func sourceZones(network inspection.Network) []string {
	var zones []string
	for _, topology := range network.Source.Topologies {
		zones = append(zones, topology.ZoneIDs...)
	}
	return sortedUnique(zones)
}

func sortedUnique(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func targetID(id, name string) string {
	if id != "" {
		return id
	}
	return name
}

func stableIdentity(parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "migration-" + hex.EncodeToString(hash[:12])
}

func stableResourceID(kind, identity string) string {
	hash := sha256.Sum256([]byte(identity))
	return "migration-" + kind + "-" + hex.EncodeToString(hash[:12])
}

func applicableExclusions(cidr string, vipRanges []IPRange, snatIP string) ([]IPRange, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil || prefix != prefix.Masked() || !prefix.Addr().Is4() {
		return nil, fmt.Errorf("invalid inspected IPv4 CIDR %q", cidr)
	}
	last := prefixLastAddress(prefix)
	var result []IPRange
	for _, reserved := range vipRanges {
		start, startErr := netip.ParseAddr(reserved.Start)
		end, endErr := netip.ParseAddr(reserved.End)
		if startErr != nil || endErr != nil || !start.Is4() || !end.Is4() || start.Compare(end) > 0 {
			return nil, fmt.Errorf("invalid reserved VIP range %q-%q", reserved.Start, reserved.End)
		}
		if start.Compare(last) > 0 || end.Compare(prefix.Addr()) < 0 {
			continue
		}
		if start.Compare(prefix.Addr()) < 0 {
			start = prefix.Addr()
		}
		if end.Compare(last) > 0 {
			end = last
		}
		result = append(result, IPRange{Start: start.String(), End: end.String()})
	}
	if snatIP != "" {
		address, parseErr := netip.ParseAddr(snatIP)
		if parseErr != nil || !address.Is4() {
			return nil, fmt.Errorf("accepted SNAT address %q is not IPv4", snatIP)
		}
		if prefix.Contains(address) {
			result = append(result, IPRange{Start: address.String(), End: address.String()})
		}
	}
	return normalizeExclusions(cidr, result)
}
