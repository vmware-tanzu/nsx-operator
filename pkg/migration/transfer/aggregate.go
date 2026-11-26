/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Package transfer compares authorized NRT observations with a protected inventory.
package transfer

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

type OperationType string

const (
	SubnetPortCutover       OperationType = "SubnetPortCutover"
	SubnetPortRevert        OperationType = "SubnetPortRevert"
	IPAddressAllocationMove OperationType = "IPAddressAllocationMove"
)

// Operation identifies an expected transfer. Source and Target refer respectively
// to reservation/port, port/reservation, or source/target allocation.
type Operation struct {
	Type           OperationType
	Source, Target api.NamespacedName
	IPs            []string
}

type key struct {
	operationType  OperationType
	source, target api.NamespacedName
}

func (o Operation) key() key { return key{o.Type, o.Source, o.Target} }

// Observation must be restricted by the caller to children whose parent, object
// identities and lifecycle are established by the Net Operator contract. A move
// has no IP in NRT status; VerifiedMoveIPs must come from authoritative allocation
// and identity evidence, including after source bookkeeping is consumed.
type Observation struct {
	Child           *api.NetworkResourceTransition
	VerifiedMoveIPs map[api.IPAddressAllocationMoveSpec][]string
}

type Result struct {
	Ready    bool
	Pending  []string
	Failures []string
}

// Aggregate never creates requests or establishes identity. An empty inventory
// requires the caller to establish explicitly that discovery is complete.
func Aggregate(inventoryComplete bool, expected []Operation, children []Observation) Result {
	var result Result
	if !inventoryComplete {
		result.Pending = append(result.Pending, "protected inventory is not complete")
		return result
	}
	want := make(map[key][]string, len(expected))
	sourceSeen, targetSeen := map[string]bool{}, map[string]bool{}
	for _, op := range expected {
		k := op.key()
		if (op.Type != SubnetPortCutover && op.Type != SubnetPortRevert && op.Type != IPAddressAllocationMove) || !validRef(op.Source) || !validRef(op.Target) {
			result.Failures = append(result.Failures, fmt.Sprintf("invalid expected operation %v", k))
			continue
		}
		if op.Type == IPAddressAllocationMove && op.Source == op.Target {
			result.Failures = append(result.Failures, fmt.Sprintf("invalid expected operation %v: source and target are identical", k))
			continue
		}
		ips, err := normalizeIPs(op.IPs)
		if err != nil {
			result.Failures = append(result.Failures, fmt.Sprintf("invalid expected addresses for %v: %v", k, err))
			continue
		}
		src := fmt.Sprintf("%s/%s/%s", op.Type, op.Source.Namespace, op.Source.Name)
		dst := fmt.Sprintf("%s/%s/%s", op.Type, op.Target.Namespace, op.Target.Name)
		if _, ok := want[k]; ok || sourceSeen[src] || targetSeen[dst] {
			result.Failures = append(result.Failures, fmt.Sprintf("duplicate expected operation or reference %v", k))
			continue
		}
		want[k] = ips
		sourceSeen[src] = true
		targetSeen[dst] = true
	}
	if len(result.Failures) > 0 {
		sort.Strings(result.Failures)
		return result
	}
	seen := map[key]string{}
	childNames := map[string]bool{}
	childUIDs := map[string]bool{}
	for _, obs := range children {
		c := obs.Child
		if c == nil || c.Name == "" || c.UID == "" {
			result.Failures = append(result.Failures, "child NRT missing name or UID")
			continue
		}
		if childNames[c.Name] {
			result.Failures = append(result.Failures, fmt.Sprintf("duplicate child NRT %s", c.Name))
			continue
		}
		childNames[c.Name] = true
		if childUIDs[string(c.UID)] {
			result.Failures = append(result.Failures, fmt.Sprintf("duplicate child UID %s", c.UID))
			continue
		}
		childUIDs[string(c.UID)] = true
		if len(c.Spec.SubnetPortCutovers) > 100 || len(c.Spec.SubnetPortReverts) > 100 || len(c.Spec.IPAddressAllocationMoves) > 100 || (len(c.Spec.SubnetPortCutovers) > 0 && len(c.Spec.SubnetPortReverts) > 0) {
			result.Failures = append(result.Failures, fmt.Sprintf("child %s violates request splitting contract", c.Name))
			continue
		}
		if len(c.Spec.SubnetPortCutovers)+len(c.Spec.SubnetPortReverts)+len(c.Spec.IPAddressAllocationMoves) == 0 {
			result.Failures = append(result.Failures, fmt.Sprintf("child %s has no operations", c.Name))
			continue
		}
		fresh := c.Generation > 0 && c.Status.ObservedGeneration == c.Generation
		operationFailed := false
		if fresh {
			checkStatusReferences(&result, c)
		}
		for _, op := range c.Spec.SubnetPortCutovers {
			k := key{SubnetPortCutover, op.SubnetIPReservation, op.SubnetPort}
			var phases []item
			for _, s := range c.Status.SubnetPortCutovers {
				if s.SubnetIPReservation == op.SubnetIPReservation && s.SubnetPort == op.SubnetPort {
					phases = append(phases, item{s.Phase, s.Message, s.IPAddresses})
					operationFailed = operationFailed || s.Phase == api.TransitionPhaseFailed
				}
			}
			record(&result, want, seen, c.Name, k, fresh, phases, nil)
		}
		for _, op := range c.Spec.SubnetPortReverts {
			k := key{SubnetPortRevert, op.SubnetPort, op.SubnetIPReservation}
			var phases []item
			for _, s := range c.Status.SubnetPortReverts {
				if s.SubnetIPReservation != nil && *s.SubnetIPReservation == op.SubnetIPReservation && s.SubnetPort == op.SubnetPort {
					phases = append(phases, item{s.Phase, s.Message, s.ReservedIPs})
					operationFailed = operationFailed || s.Phase == api.TransitionPhaseFailed
				}
			}
			if expectedIPs, ok := want[k]; ok {
				requestIPs, err := normalizeIPs(op.ReservedIPs)
				if err != nil || !equal(requestIPs, expectedIPs) {
					result.Failures = append(result.Failures, fmt.Sprintf("child %s operation %v has incorrect requested addresses", c.Name, k))
				}
			}
			record(&result, want, seen, c.Name, k, fresh, phases, nil)
		}
		for _, op := range c.Spec.IPAddressAllocationMoves {
			k := key{IPAddressAllocationMove, op.Source, op.Target}
			var phases []item
			for _, s := range c.Status.IPAddressAllocationMoves {
				if s.Source == op.Source && s.Target == op.Target {
					phases = append(phases, item{s.Phase, s.Message, nil})
					operationFailed = operationFailed || s.Phase == api.TransitionPhaseFailed
				}
			}
			record(&result, want, seen, c.Name, k, fresh, phases, obs.VerifiedMoveIPs[op])
		}
		if fresh && c.Status.Phase == api.TransitionPhaseFailed && !operationFailed {
			message := fmt.Sprintf("child %s failed", c.Name)
			for _, condition := range c.Status.Conditions {
				if condition.Type == "Ready" && (condition.Reason != "" || condition.Message != "") {
					message += ": " + strings.TrimSpace(condition.Reason+" "+condition.Message)
					break
				}
			}
			result.Failures = append(result.Failures, message)
		}
	}
	for k := range want {
		if _, ok := seen[k]; !ok {
			result.Pending = append(result.Pending, fmt.Sprintf("missing operation %v", k))
		}
	}
	sort.Strings(result.Pending)
	sort.Strings(result.Failures)
	result.Ready = len(result.Pending) == 0 && len(result.Failures) == 0
	return result
}

func checkStatusReferences(r *Result, c *api.NetworkResourceTransition) {
	for _, s := range c.Status.SubnetPortCutovers {
		found := false
		for _, op := range c.Spec.SubnetPortCutovers {
			found = found || (s.SubnetIPReservation == op.SubnetIPReservation && s.SubnetPort == op.SubnetPort)
		}
		if !found {
			r.Failures = append(r.Failures, fmt.Sprintf("child %s has unexpected cutover status for %v", c.Name, s.SubnetPort))
		}
	}
	for _, s := range c.Status.SubnetPortReverts {
		found := false
		for _, op := range c.Spec.SubnetPortReverts {
			found = found || (s.SubnetIPReservation != nil && *s.SubnetIPReservation == op.SubnetIPReservation && s.SubnetPort == op.SubnetPort)
		}
		if !found {
			r.Failures = append(r.Failures, fmt.Sprintf("child %s has unexpected revert status for %v", c.Name, s.SubnetPort))
		}
	}
	for _, s := range c.Status.IPAddressAllocationMoves {
		found := false
		for _, op := range c.Spec.IPAddressAllocationMoves {
			found = found || (s.Source == op.Source && s.Target == op.Target)
		}
		if !found {
			r.Failures = append(r.Failures, fmt.Sprintf("child %s has unexpected allocation move status for %v", c.Name, s.Target))
		}
	}
}

type item struct {
	phase   api.TransitionPhase
	message string
	ips     []string
}

func record(r *Result, want map[key][]string, seen map[key]string, child string, k key, fresh bool, statuses []item, moveIPs []string) {
	label := fmt.Sprintf("child %s operation %v", child, k)
	expected, ok := want[k]
	if !ok {
		r.Failures = append(r.Failures, label+" is not in inventory")
		return
	}
	if prior, ok := seen[k]; ok {
		r.Failures = append(r.Failures, fmt.Sprintf("%s duplicates child %s", label, prior))
		return
	}
	seen[k] = child
	if !fresh {
		r.Pending = append(r.Pending, label+" has stale status")
		return
	}
	if len(statuses) == 0 {
		r.Pending = append(r.Pending, label+" has no status")
		return
	}
	if len(statuses) != 1 {
		r.Failures = append(r.Failures, label+" has duplicate statuses")
		return
	}
	s := statuses[0]
	switch s.phase {
	case api.TransitionPhaseFailed:
		r.Failures = append(r.Failures, label+" failed: "+s.message)
		return
	case api.TransitionPhaseInProgress, "":
		r.Pending = append(r.Pending, label+" is in progress")
		return
	case api.TransitionPhaseSucceeded:
	default:
		r.Failures = append(r.Failures, label+" has invalid phase "+string(s.phase))
		return
	}
	ips := s.ips
	if k.operationType == IPAddressAllocationMove {
		ips = moveIPs
	}
	if len(ips) == 0 {
		r.Pending = append(r.Pending, label+" awaits verified addresses")
		return
	}
	actual, err := normalizeIPs(ips)
	if err != nil || !equal(actual, expected) {
		r.Failures = append(r.Failures, label+" has incorrect addresses")
	}
}
func validRef(ref api.NamespacedName) bool { return ref.Namespace != "" && ref.Name != "" }
func normalizeIPs(ips []string) ([]string, error) {
	if len(ips) == 0 {
		return nil, fmt.Errorf("no addresses")
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range ips {
		ip, err := netip.ParseAddr(s)
		if err != nil || ip.Zone() != "" {
			return nil, fmt.Errorf("invalid IP %q", s)
		}
		v := ip.Unmap().String()
		if seen[v] {
			return nil, fmt.Errorf("duplicate IP %q", s)
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out, nil
}
func equal(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }
