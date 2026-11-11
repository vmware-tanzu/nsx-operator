/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package inspection

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"reflect"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	migrationstatus "github.com/vmware-tanzu/nsx-operator/pkg/migration/status"
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
	PortGroupID string
	SwitchID    string
	SwitchUUID  string
	ClusterIDs  []string
}

// Source is authoritative inventory evidence. DHCP is never inferred from an
// empty IP pool. Unsupported/unknown assignment modes must fail discovery.
// Topologies contains one entry per backing DVPG. Each entry keeps that DVPG's
// VDS identity and cluster span together; a VDN may span several VDSes.
type Source struct {
	VDNName       string
	Topologies    []Topology
	DHCP          bool
	Mask, Gateway string
	Ranges        []api.IPAddressRange
}

type Inventory interface {
	NamespaceVDNs(context.Context, []string) ([]string, error)
	Resolve(context.Context, string) (Source, error)
}

type Network struct {
	Source     Source
	Addressing Addressing
}

type Result struct {
	Networks       []Network
	RequiredInputs []api.RequiredInput
	Problems       []string
}

func (result Result) Ready() bool {
	return len(result.RequiredInputs) == 0 && len(result.Problems) == 0 && len(result.Networks) > 0
}

type Service struct{ Inventory Inventory }

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
	result := Result{}
	if service.Inventory == nil {
		return result, fmt.Errorf("inventory is required")
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
	networksByBacking := map[string]Network{}
	for _, vdnName := range orderedVDNs {
		sourceVDN, err := service.Inventory.Resolve(ctx, vdnName)
		if err != nil {
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
		dhcpInput, supplied := inputsByVDN[vdnName]
		subnetMask, gatewayAddress, allocationRanges := sourceVDN.Mask, sourceVDN.Gateway, sourceVDN.Ranges
		if sourceVDN.DHCP {
			missingInputs, inputProblems := checkInput(vdnName, dhcpInput)
			result.RequiredInputs = append(result.RequiredInputs, missingInputs...)
			result.Problems = append(result.Problems, inputProblems...)
			if len(missingInputs) > 0 || len(inputProblems) > 0 {
				continue
			}
			subnetMask, gatewayAddress, allocationRanges = dhcpInput.SubnetMask, dhcpInput.GatewayAddress, []api.IPAddressRange{*dhcpInput.DHCPRange}
		} else if supplied {
			result.Problems = append(result.Problems, fmt.Sprintf("VDN %s is static; user DHCP inputs are not applicable", vdnName))
			continue
		}
		addressing, err := ValidateAddressing(subnetMask, gatewayAddress, allocationRanges)
		if err != nil {
			result.Problems = append(result.Problems, fmt.Sprintf("VDN %s: %v", vdnName, err))
			continue
		}
		network := Network{Source: sourceVDN, Addressing: addressing}
		for _, topology := range sourceVDN.Topologies {
			backingID := topology.SwitchUUID + "/" + topology.PortGroupID
			if previousNetwork, exists := networksByBacking[backingID]; exists && (previousNetwork.Source.DHCP != sourceVDN.DHCP || !reflect.DeepEqual(previousNetwork.Addressing, addressing)) {
				result.Problems = append(result.Problems, fmt.Sprintf("VDNs %s and %s have conflicting inputs for backing %s", previousNetwork.Source.VDNName, vdnName, backingID))
			}
			networksByBacking[backingID] = network
		}
		result.Networks = append(result.Networks, network)
	}
	sort.Strings(result.Problems)
	return result, nil
}

func validTopologies(topologies []Topology) bool {
	if len(topologies) == 0 {
		return false
	}
	seenBackingIDs := make(map[string]struct{}, len(topologies))
	for _, topology := range topologies {
		if topology.PortGroupID == "" || topology.SwitchID == "" || topology.SwitchUUID == "" || len(topology.ClusterIDs) == 0 {
			return false
		}
		backingID := topology.SwitchUUID + "/" + topology.PortGroupID
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
		missingInputs = append(missingInputs, api.RequiredInput{FieldName: fmt.Sprintf("%s%x", inputPrefix, sha256.Sum256([]byte(vdnName))), Description: fmt.Sprintf("Supply %s for DHCP-backed VDN %s in the transition's subnet inputs", strings.Join(missingFields, ", "), vdnName)})
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
	phase, reason, message, conditionStatus := api.PhaseEvaluating, "NetworkInspectionComplete", "Source networks and addressing validated", metav1.ConditionTrue
	if !result.Ready() {
		phase, reason, message, conditionStatus = api.PhaseNeedsInput, "MissingNetworkInputs", "Supply the required DHCP network inputs", metav1.ConditionFalse
	}
	if len(result.Problems) > 0 || (len(result.Networks) == 0 && len(result.RequiredInputs) == 0) {
		phase, reason, message, conditionStatus = api.PhaseEvaluationFailed, "InvalidNetworkConfiguration", strings.Join(result.Problems, "; "), metav1.ConditionFalse
		if message == "" {
			message = "Source network inventory is empty"
		}
	}
	if len(message) > 4096 {
		message = message[:4093] + "..."
	}
	if err := migrationstatus.Observe(nextStatus, generation, phase, metav1.Condition{Type: ConditionNetworkInputsValidated, Status: conditionStatus, Reason: reason, Message: message}); err != nil {
		return err
	}
	retainedInputs := make([]api.RequiredInput, 0)
	for _, requiredInput := range nextStatus.RequiredInputs {
		if !strings.HasPrefix(requiredInput.FieldName, inputPrefix) &&
			!strings.HasPrefix(requiredInput.FieldName, "namespaceDHCPNetworks/") &&
			!strings.HasPrefix(requiredInput.FieldName, "controlPlaneAndServiceDHCPNetworks/") {
			retainedInputs = append(retainedInputs, requiredInput)
		}
	}
	retainedInputs = append(retainedInputs, result.RequiredInputs...)
	if len(retainedInputs) > 1000 {
		return fmt.Errorf("network diagnostics exceed requiredInputs limit of 1000")
	}
	nextStatus.RequiredInputs = retainedInputs
	if conditionStatus == metav1.ConditionTrue && len(retainedInputs) > 0 {
		nextStatus.Phase = api.PhaseNeedsInput
	}
	*target = *nextStatus
	return nil
}
