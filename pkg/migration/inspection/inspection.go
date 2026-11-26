/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package inspection

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"reflect"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	migrationstatus "github.com/vmware-tanzu/nsx-operator/pkg/migration/status"
	migrationtopology "github.com/vmware-tanzu/nsx-operator/pkg/migration/topology"
)

const ConditionNetworkInputsValidated = "NetworkInputsValidated"
const inputPrefix = "networkInspection/"

// ConfigurationError identifies incompatible inventory, as distinct from a
// transient API failure. It becomes EvaluationFailed, never NeedsInput.
type ConfigurationError struct{ Message string }

func (configurationError *ConfigurationError) Error() string { return configurationError.Message }
func invalid(format string, args ...any) error {
	return &ConfigurationError{Message: fmt.Sprintf(format, args...)}
}

type Topology struct {
	// VCenterUUID and PortGroupID form the source network identity used by
	// migration tags. Compare these fields for LB SNAT network relationships;
	// VLAN IDs and CIDRs alone do not prove two networks share a backing.
	VCenterUUID   string
	PortGroupID   string
	PortGroupName string
	SwitchID      string
	SwitchUUID    string
	ClusterIDs    []string
	ZoneIDs       []string
}

// SameBacking reports whether two DVPG records identify the same source
// network. VLAN IDs and CIDRs are not sufficient to establish shared backing.
func (topology Topology) SameBacking(other Topology) bool {
	identity, err := DVPGIdentity(topology.VCenterUUID, topology.PortGroupID)
	if err != nil {
		return false
	}
	otherIdentity, err := DVPGIdentity(other.VCenterUUID, other.PortGroupID)
	return err == nil && identity == otherIdentity
}

// Source is authoritative inventory evidence. DHCP is never inferred from an
// empty IP pool. Unsupported/unknown assignment modes must fail discovery.
// Topologies contains one entry per backing DVPG. Each entry keeps that DVPG's
// VDS identity and cluster span together; a VDN may span several VDSes.
type Source struct {
	VDNName    string
	Topologies []Topology
	DHCP       bool
}

// DiscoveredSource includes unvalidated static addressing used during inspection.
// Handlers receive Source and validated Addressing separately in Network.
type DiscoveredSource struct {
	Source
	Mask, Gateway string
	Ranges        []api.IPAddressRange
}

type Inventory interface {
	NamespaceVDNs(context.Context, []string) ([]string, error)
	Resolve(context.Context, string) (DiscoveredSource, error)
}

type Network struct {
	Source     Source
	Addressing Addressing
	Target     TargetDecision
}

// inspectedSource keeps the zone span with its source VDN.
type inspectedSource struct {
	vdn     DiscoveredSource
	zoneIDs []string
}

type Result struct {
	Networks []Network
	// SourceTopologies remain available even when DHCP inputs are missing, so
	// Evaluate handlers can still compare FLB backings with workload networks.
	SourceTopologies []Topology
	RequiredInputs   []api.RequiredInput
	OptionalInputs   []api.OptionalInput
	Problems         []string
	MissingZones     []string
	Warnings         []string
	LBSNAT           *LBSNATDecision
}

func (result Result) Ready() bool {
	return len(result.RequiredInputs) == 0 && len(result.Problems) == 0 && len(result.MissingZones) == 0 && len(result.Networks) > 0
}

type Service struct {
	Inventory           Inventory
	TargetInventory     TargetInventory
	FLBNetworkInventory FLBNetworkReader
	StatusClient        client.Client
}

// InspectNamespaces derives VDN membership from namespaced Network resources,
// not the administrator's input list. Shared VDNs are inspected once.
func (service Service) InspectNamespaces(ctx context.Context, namespaces []string, dhcpInputs []api.DHCPNetworkConfiguration) (Result, error) {
	if service.Inventory == nil {
		return Result{}, fmt.Errorf("inventory is required")
	}
	vdns, err := service.Inventory.NamespaceVDNs(ctx, namespaces)
	if err != nil {
		return Result{}, err
	}
	return service.Inspect(ctx, vdns, dhcpInputs)
}

// Inspect accepts the authoritative source VDN set. For control plane this must
// come from WCP's CPVM/service network configuration, never networkConfiguration. API or
// transport errors are returned for retry and are not classified as missing input.
func (service Service) Inspect(ctx context.Context, vdns []string, dhcpInputs []api.DHCPNetworkConfiguration) (Result, error) {
	return service.InspectWithNaming(ctx, vdns, dhcpInputs, nil)
}

// ValidateResourceNaming checks naming selectors and shared-span TGW/VPC
// names against the complete live VDN inventory provided by namespace
// Evaluate. It deliberately performs no target resource discovery or writes.
func (service Service) ValidateResourceNaming(sources []migrationtopology.SourceNetwork, names []api.VDNResourceNaming) error {
	var problems []string
	orderedVDNs := make([]string, 0, len(sources))
	sourcesByVDN := make(map[string]inspectedSource, len(sources))
	for _, source := range sources {
		if source.VDNName == "" {
			problems = append(problems, "source network inventory contains an empty VDN name")
			continue
		}
		if _, exists := sourcesByVDN[source.VDNName]; exists {
			problems = append(problems, fmt.Sprintf("source network inventory contains duplicate VDN %q", source.VDNName))
			continue
		}
		zoneIDs := append([]string(nil), source.ZoneIDs...)
		sort.Strings(zoneIDs)
		uniqueZoneIDs := make([]string, 0, len(zoneIDs))
		for _, zoneID := range zoneIDs {
			if zoneID == "" {
				problems = append(problems, fmt.Sprintf("source VDN %q has an empty Transport Zone ID", source.VDNName))
				continue
			}
			if len(uniqueZoneIDs) == 0 || uniqueZoneIDs[len(uniqueZoneIDs)-1] != zoneID {
				uniqueZoneIDs = append(uniqueZoneIDs, zoneID)
			}
		}
		orderedVDNs = append(orderedVDNs, source.VDNName)
		sourcesByVDN[source.VDNName] = inspectedSource{zoneIDs: uniqueZoneIDs}
	}
	sort.Strings(orderedVDNs)

	knownVDNs := make(map[string]struct{}, len(sourcesByVDN))
	for vdnName := range sourcesByVDN {
		knownVDNs[vdnName] = struct{}{}
	}
	nameByVDN, namingProblems := indexResourceNaming(knownVDNs, names)
	problems = append(problems, namingProblems...)
	needsZoneSpans := false
	for _, name := range names {
		if _, found := nameByVDN[name.VDNName]; found && (name.TGWName != "" || name.VPCName != "") {
			needsZoneSpans = true
		}
	}
	if needsZoneSpans {
		for _, vdnName := range orderedVDNs {
			if len(sourcesByVDN[vdnName].zoneIDs) == 0 {
				problems = append(problems, fmt.Sprintf("source VDN %q has no Transport Zone span for TGW/VPC naming validation", vdnName))
			}
		}
	} else {
		// Empty spans must not accidentally group unrelated VDNs when the
		// discoverer has no span data and no TGW/VPC override needs checking.
		for vdnName, source := range sourcesByVDN {
			if len(source.zoneIDs) == 0 {
				delete(sourcesByVDN, vdnName)
			}
		}
	}
	_, _, spanProblems := mergeSpanNames(orderedVDNs, sourcesByVDN, nameByVDN)
	problems = append(problems, spanProblems...)
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return &ConfigurationError{Message: strings.Join(problems, "; ")}
}

func indexResourceNaming(knownVDNs map[string]struct{}, names []api.VDNResourceNaming) (map[string]api.VDNResourceNaming, []string) {
	nameByVDN := make(map[string]api.VDNResourceNaming, len(names))
	var problems []string
	for _, name := range names {
		if _, found := knownVDNs[name.VDNName]; !found {
			problems = append(problems, fmt.Sprintf("resource naming references unknown VDN %q", name.VDNName))
			continue
		}
		if _, found := nameByVDN[name.VDNName]; found {
			problems = append(problems, fmt.Sprintf("duplicate resource naming for VDN %q", name.VDNName))
			continue
		}
		nameByVDN[name.VDNName] = name
	}
	return nameByVDN, problems
}

// InspectWithNaming validates source data, resolves reusable target topology,
// and reports optional name fields only for target resources that need creation.
func (service Service) InspectWithNaming(ctx context.Context, vdns []string, dhcpInputs []api.DHCPNetworkConfiguration, names []api.VDNResourceNaming) (Result, error) {
	result := Result{}
	if service.Inventory == nil {
		return result, fmt.Errorf("inventory is required")
	}
	topologyCache := &topologyReaderCache{values: make(map[string]Topology)}
	switch inventory := service.Inventory.(type) {
	case KubernetesInventory:
		inventory.Topology = topologyCache.readerOrNil(inventory.Topology)
		service.Inventory = inventory
	case *KubernetesInventory:
		copy := *inventory
		copy.Topology = topologyCache.readerOrNil(copy.Topology)
		service.Inventory = copy
	}
	switch inventory := service.TargetInventory.(type) {
	case NSXTargetInventory:
		inventory.Topology = topologyCache.readerOrNil(inventory.Topology)
		service.TargetInventory = inventory
	case *NSXTargetInventory:
		copy := *inventory
		copy.Topology = topologyCache.readerOrNil(copy.Topology)
		service.TargetInventory = copy
	}
	uniqueVDNs := map[string]struct{}{}
	for _, vdnName := range vdns {
		if vdnName == "" {
			return result, fmt.Errorf("empty source VDN identity")
		}
		uniqueVDNs[vdnName] = struct{}{}
	}
	if len(uniqueVDNs) == 0 {
		return result, fmt.Errorf("source network inventory is empty")
	}
	orderedVDNs := make([]string, 0, len(uniqueVDNs))
	for vdnName := range uniqueVDNs {
		orderedVDNs = append(orderedVDNs, vdnName)
	}
	sort.Strings(orderedVDNs)
	inputsByVDN := map[string]api.DHCPNetworkConfiguration{}
	for _, dhcpInput := range dhcpInputs {
		if _, found := uniqueVDNs[dhcpInput.VDNName]; !found {
			result.Problems = append(result.Problems, fmt.Sprintf("unknown input VDN %q", dhcpInput.VDNName))
			continue
		}
		if _, found := inputsByVDN[dhcpInput.VDNName]; found {
			result.Problems = append(result.Problems, fmt.Sprintf("duplicate input VDN %q", dhcpInput.VDNName))
			continue
		}
		inputsByVDN[dhcpInput.VDNName] = dhcpInput
	}
	var targetTopologies []TargetTopology
	if service.TargetInventory != nil {
		var err error
		targetTopologies, err = service.TargetInventory.ListTopology(ctx)
		if err != nil {
			return Result{}, fmt.Errorf("list existing NSX migration topology: %w", err)
		}
	}
	nameByVDN, namingProblems := indexResourceNaming(uniqueVDNs, names)
	result.Problems = append(result.Problems, namingProblems...)
	sourcesByVDN := make(map[string]inspectedSource, len(orderedVDNs))
	for _, vdnName := range orderedVDNs {
		sourceVDN, err := service.Inventory.Resolve(ctx, vdnName)
		if err != nil {
			var missingZones *MissingZonesError
			if errors.As(err, &missingZones) {
				result.MissingZones = append(result.MissingZones, fmt.Sprintf("VDN %s: %s", vdnName, missingZones.Error()))
				continue
			}
			var configuration *ConfigurationError
			if errors.As(err, &configuration) {
				result.Problems = append(result.Problems, fmt.Sprintf("VDN %s: %v", vdnName, err))
				continue
			}
			return Result{}, fmt.Errorf("resolve VDN %s: %w", vdnName, err)
		}
		if sourceVDN.VDNName != vdnName || !validTopologies(sourceVDN.Topologies) {
			result.Problems = append(result.Problems, fmt.Sprintf("VDN %s has incomplete backing topology", vdnName))
			continue
		}
		sort.Slice(sourceVDN.Topologies, func(leftIndex, rightIndex int) bool {
			if sourceVDN.Topologies[leftIndex].SwitchUUID != sourceVDN.Topologies[rightIndex].SwitchUUID {
				return sourceVDN.Topologies[leftIndex].SwitchUUID < sourceVDN.Topologies[rightIndex].SwitchUUID
			}
			return sourceVDN.Topologies[leftIndex].PortGroupID < sourceVDN.Topologies[rightIndex].PortGroupID
		})
		zoneIDs, topologyErr := validateSourceTopology(sourceVDN.Topologies)
		if topologyErr != nil {
			result.Problems = append(result.Problems, fmt.Sprintf("VDN %s: %v", vdnName, topologyErr))
			continue
		}
		sourcesByVDN[vdnName] = inspectedSource{vdn: sourceVDN, zoneIDs: zoneIDs}
		result.SourceTopologies = append(result.SourceTopologies, sourceVDN.Topologies...)
	}
	effectiveNames, invalidSpans, spanProblems := mergeSpanNames(orderedVDNs, sourcesByVDN, nameByVDN)
	result.Problems = append(result.Problems, spanProblems...)
	// One zone span has one TGW/VPC plan, including the VDSes of all its VDNs.
	vdsBySpan := make(map[string]map[string]struct{})
	for _, source := range sourcesByVDN {
		span := strings.Join(source.zoneIDs, "\x00")
		if vdsBySpan[span] == nil {
			vdsBySpan[span] = make(map[string]struct{})
		}
		for _, vds := range switchUUIDs(source.vdn.Topologies) {
			vdsBySpan[span][vds] = struct{}{}
		}
	}
	planOwnerBySpan := make(map[string]string)
	networksByBacking := map[string][]Network{}
	for _, vdnName := range orderedVDNs {
		inspected, found := sourcesByVDN[vdnName]
		if !found || invalidSpans[vdnName] {
			continue
		}
		sourceVDN := inspected.vdn
		dhcpInput, supplied := inputsByVDN[vdnName]
		subnetMask, gatewayAddress, allocationRanges := sourceVDN.Mask, sourceVDN.Gateway, sourceVDN.Ranges
		var addressing Addressing
		addressingReady := false
		canValidateAddressing := true
		if sourceVDN.DHCP {
			missingInputs, inputProblems := checkInput(vdnName, dhcpInput)
			result.RequiredInputs = append(result.RequiredInputs, missingInputs...)
			result.Problems = append(result.Problems, inputProblems...)
			if len(missingInputs) == 0 && len(inputProblems) == 0 {
				subnetMask, gatewayAddress, allocationRanges = dhcpInput.SubnetMask, dhcpInput.GatewayAddress, []api.IPAddressRange{*dhcpInput.DHCPRange}
			} else {
				canValidateAddressing = false
			}
		} else if supplied {
			result.Problems = append(result.Problems, fmt.Sprintf("VDN %s is static; user DHCP inputs are not applicable", vdnName))
			continue
		}
		if canValidateAddressing {
			var err error
			addressing, err = ValidateAddressing(subnetMask, gatewayAddress, allocationRanges)
			if err != nil {
				result.Problems = append(result.Problems, fmt.Sprintf("VDN %s: %v", vdnName, err))
			} else {
				addressingReady = true
			}
		}
		var targetDecision TargetDecision
		if service.TargetInventory != nil {
			name := effectiveNames[vdnName]
			span := strings.Join(inspected.zoneIDs, "\x00")
			decision, optionalInputs, targetErr := resolveTargetTopology(vdnName, inspected.zoneIDs, sortedKeys(vdsBySpan[span]), sourceVDN.Topologies, name, targetTopologies)
			if targetErr != nil {
				result.Problems = append(result.Problems, fmt.Sprintf("VDN %s: %v", vdnName, targetErr))
				continue
			}
			if decision.CreateTGW {
				if owner := planOwnerBySpan[span]; owner != "" {
					decision.CreateTGW, decision.CreateVPC = false, false
					decision.SharedWithVDN = owner
				} else {
					planOwnerBySpan[span] = vdnName
				}
			}
			targetDecision = decision
			result.OptionalInputs = append(result.OptionalInputs, optionalInputs...)
		}
		if !addressingReady {
			continue
		}
		network := Network{Source: sourceVDN.Source, Addressing: addressing, Target: targetDecision}
		for _, topology := range sourceVDN.Topologies {
			backingID := dvpgIdentity(topology.VCenterUUID, topology.PortGroupID)
			for _, previousNetwork := range networksByBacking[backingID] {
				if !compatibleSharedAddressing(previousNetwork, network) {
					result.Problems = append(result.Problems, fmt.Sprintf("VDNs %s and %s have conflicting inputs for backing %s", previousNetwork.Source.VDNName, vdnName, backingID))
				}
			}
			networksByBacking[backingID] = append(networksByBacking[backingID], network)
		}
		result.Networks = append(result.Networks, network)
	}
	sort.Strings(result.Problems)
	return result, nil
}

func compatibleSharedAddressing(first, second Network) bool {
	if first.Source.DHCP != second.Source.DHCP || first.Addressing.CIDR != second.Addressing.CIDR || first.Addressing.Gateway != second.Addressing.Gateway {
		return false
	}
	if first.Source.DHCP {
		return reflect.DeepEqual(first.Addressing.Ranges, second.Addressing.Ranges)
	}
	for _, firstRange := range first.Addressing.Ranges {
		for _, secondRange := range second.Addressing.Ranges {
			// ValidateAddressing has already parsed and ordered these endpoints.
			firstStart := netip.MustParseAddr(firstRange.Start)
			firstEnd := netip.MustParseAddr(firstRange.End)
			secondStart := netip.MustParseAddr(secondRange.Start)
			secondEnd := netip.MustParseAddr(secondRange.End)
			if firstStart.Compare(secondEnd) <= 0 && secondStart.Compare(firstEnd) <= 0 {
				return false
			}
		}
	}
	return true
}

func validTopologies(topologies []Topology) bool {
	if len(topologies) == 0 {
		return false
	}
	seenBackingIDs := make(map[string]struct{}, len(topologies))
	for _, topology := range topologies {
		if topology.SwitchID == "" || topology.SwitchUUID == "" || len(topology.ClusterIDs) == 0 {
			return false
		}
		backingID, err := DVPGIdentity(topology.VCenterUUID, topology.PortGroupID)
		if err != nil {
			return false
		}
		if _, exists := seenBackingIDs[backingID]; exists {
			return false
		}
		seenBackingIDs[backingID] = struct{}{}
	}
	return true
}

func checkInput(vdnName string, dhcpInput api.DHCPNetworkConfiguration) ([]api.RequiredInput, []string) {
	var missingInputs []api.RequiredInput
	var missingFields []string
	var inputProblems []string
	fields := []struct{ fieldName, fieldValue string }{{"subnetMask", dhcpInput.SubnetMask}, {"gatewayAddress", dhcpInput.GatewayAddress}}
	rangeStart, rangeEnd := "", ""
	if dhcpInput.DHCPRange != nil {
		rangeStart, rangeEnd = dhcpInput.DHCPRange.Start, dhcpInput.DHCPRange.End
	}
	fields = append(fields, struct{ fieldName, fieldValue string }{"dhcpRange.start", rangeStart}, struct{ fieldName, fieldValue string }{"dhcpRange.end", rangeEnd})
	for _, field := range fields {
		if field.fieldValue == "" {
			missingFields = append(missingFields, field.fieldName)
			continue
		}
		valid := true
		if field.fieldName == "subnetMask" {
			maskAddress, err := netip.ParseAddr(field.fieldValue)
			valid = err == nil && maskAddress.Is4()
			if valid {
				maskBytes := maskAddress.As4()
				_, addressBitLength := net.IPMask(maskBytes[:]).Size()
				valid = addressBitLength == 32
			}
		} else {
			_, err := ipv4(field.fieldValue)
			valid = err == nil
		}
		if !valid {
			inputProblems = append(inputProblems, fmt.Sprintf("VDN %s: invalid %s %q", vdnName, field.fieldName, field.fieldValue))
		}
	}
	if parsedRangeStart, err := ipv4(rangeStart); err == nil {
		if parsedRangeEnd, err := ipv4(rangeEnd); err == nil && parsedRangeStart.Compare(parsedRangeEnd) > 0 {
			inputProblems = append(inputProblems, fmt.Sprintf("VDN %s: DHCP range start exceeds end", vdnName))
		}
	}
	if len(missingFields) > 0 {
		missingInputs = append(missingInputs, api.RequiredInput{FieldName: inputPrefix + vdnName, Description: fmt.Sprintf("Supply %s for DHCP-backed VDN %s in the transition's subnet inputs", strings.Join(missingFields, ", "), vdnName)})
	}
	// Check relationships among available values even if another field is absent.
	if maskAddress, err := netip.ParseAddr(dhcpInput.SubnetMask); err == nil && maskAddress.Is4() {
		maskBytes := maskAddress.As4()
		prefixLength, addressBitLength := net.IPMask(maskBytes[:]).Size()
		if parsedRangeStart, err := ipv4(rangeStart); err == nil && addressBitLength == 32 {
			sourceCIDR := netip.PrefixFrom(parsedRangeStart, prefixLength).Masked()
			for _, address := range []string{rangeEnd, dhcpInput.GatewayAddress} {
				if parsedAddress, err := ipv4(address); err == nil && !sourceCIDR.Contains(parsedAddress) {
					inputProblems = append(inputProblems, fmt.Sprintf("VDN %s: address %s is outside %s", vdnName, address, sourceCIDR))
				}
			}
		}
	}
	return missingInputs, inputProblems
}

// Apply reports this service's observation only. The caller persists it with the
// status package's optimistic writer against the inspected CR snapshot. It does
// not record action completion or erase other services' requiredInputs.
func (result Result) Apply(target *api.TransitionStatus, generation int64) error {
	if target == nil {
		return fmt.Errorf("status is required")
	}
	nextStatus := target.DeepCopy()
	phase, reason, message, conditionStatus := api.PhaseEvaluating, "NetworkInspectionComplete", "Source addressing and reusable target topology validated", metav1.ConditionTrue
	if !result.Ready() {
		phase, reason, message, conditionStatus = api.PhaseNeedsInput, "MissingNetworkInputs", "Supply the required DHCP network inputs", metav1.ConditionFalse
	}
	if len(result.MissingZones) > 0 {
		message = strings.Join(result.MissingZones, "; ")
		if len(result.RequiredInputs) > 0 {
			message += "; supply the required DHCP network inputs"
		}
	}
	if len(result.Problems) > 0 || (len(result.Networks) == 0 && len(result.RequiredInputs) == 0 && len(result.MissingZones) == 0) {
		phase, reason, message, conditionStatus = api.PhaseEvaluationFailed, "InvalidNetworkConfiguration", strings.Join(result.Problems, "; "), metav1.ConditionFalse
		if message == "" {
			message = "Source network inventory is empty"
		}
	}
	if len(result.Warnings) > 0 {
		if conditionStatus == metav1.ConditionTrue {
			reason = "NetworkInspectionCompleteWithWarnings"
		}
		message += "; " + strings.Join(result.Warnings, "; ")
	}
	if len(message) > 4096 {
		message = message[:4093] + "..."
	}
	if err := migrationstatus.Observe(nextStatus, generation, phase, metav1.Condition{Type: ConditionNetworkInputsValidated, Status: conditionStatus, Reason: reason, Message: message}); err != nil {
		return err
	}
	clearInspectionInputs(nextStatus)
	nextStatus.RequiredInputs = append(nextStatus.RequiredInputs, result.RequiredInputs...)
	if len(nextStatus.RequiredInputs) > 1000 {
		return fmt.Errorf("network diagnostics exceed requiredInputs limit of 1000")
	}
	nextStatus.OptionalInputs = append(nextStatus.OptionalInputs, result.OptionalInputs...)
	if len(nextStatus.OptionalInputs) > 1000 {
		return fmt.Errorf("network diagnostics exceed optionalInputs limit of 1000")
	}
	if conditionStatus == metav1.ConditionTrue && len(nextStatus.RequiredInputs) > 0 {
		nextStatus.Phase = api.PhaseNeedsInput
	}
	*target = *nextStatus
	return nil
}
