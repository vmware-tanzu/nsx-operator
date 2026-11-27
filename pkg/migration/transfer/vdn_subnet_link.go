/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package transfer

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	vdnSubnetLinkAPIVersion = "netoperator.vmware.com/v1alpha1"
	vdnSubnetLinkKind       = "VDNSubnetLink"
)

// LinkMapping is the expected mapping supplied by topology discovery. Paths
// must be verified against live topology separately; the link is a hint.
type LinkMapping struct{ VDN, Subnet, PortConfig string }

// LinkObservation reports the two conditions separately. IPReserved does not
// establish that the protected workload/VIP inventory is completely covered.
type LinkObservation struct {
	Ready, IPReserved bool
	Pending           []string
}

// ObserveVDNSubnetLink interprets the preliminary Net Operator example. It does
// not establish ownership, validate live NSX topology, or verify reservations.
// The production CRD and condition semantics must be confirmed before wiring it.
func ObserveVDNSubnetLink(link *unstructured.Unstructured, expected LinkMapping) (LinkObservation, error) {
	var out LinkObservation
	if expected.VDN == "" || expected.Subnet == "" || expected.PortConfig == "" {
		return out, fmt.Errorf("expected VDN, subnet and portConfig must be provided")
	}
	if link == nil {
		out.Pending = append(out.Pending, "VDNSubnetLink is missing")
		return out, nil
	}
	if link.GetAPIVersion() != vdnSubnetLinkAPIVersion || link.GetKind() != vdnSubnetLinkKind || link.GetNamespace() != "" || link.GetName() == "" {
		return out, fmt.Errorf("invalid VDNSubnetLink identity or API version")
	}
	if !link.GetDeletionTimestamp().IsZero() {
		out.Pending = append(out.Pending, "VDNSubnetLink is deleting")
		return out, nil
	}
	specComplete := true
	for _, mapping := range []struct{ field, want string }{{"vdn_network", expected.VDN}, {"subnet", expected.Subnet}, {"portConfig", expected.PortConfig}} {
		field, want := mapping.field, mapping.want
		actual, found, err := unstructured.NestedString(link.Object, "spec", field)
		if err != nil {
			return out, fmt.Errorf("VDNSubnetLink %s spec.%s: %w", link.GetName(), field, err)
		}
		if !found || actual == "" {
			specComplete = false
			out.Pending = append(out.Pending, fmt.Sprintf("VDNSubnetLink %s missing spec.%s", link.GetName(), field))
			continue
		}
		if actual != want {
			return out, fmt.Errorf("VDNSubnetLink %s spec.%s mismatch: got %q, expected %q", link.GetName(), field, actual, want)
		}
	}
	conditions, found, err := unstructured.NestedSlice(link.Object, "status", "conditions")
	if err != nil {
		return out, fmt.Errorf("VDNSubnetLink %s conditions: %w", link.GetName(), err)
	}
	if !found {
		out.Pending = append(out.Pending, "VDNSubnetLink has no conditions")
		return out, nil
	}
	seen := map[string]bool{}
	for _, entry := range conditions {
		c, ok := entry.(map[string]interface{})
		if !ok {
			return out, fmt.Errorf("VDNSubnetLink %s has invalid condition", link.GetName())
		}
		kind, ok := c["type"].(string)
		if !ok {
			return out, fmt.Errorf("VDNSubnetLink %s condition has invalid type", link.GetName())
		}
		if kind != "Ready" && kind != "IPReserved" {
			continue
		}
		if seen[kind] {
			return out, fmt.Errorf("VDNSubnetLink %s has duplicate %s conditions", link.GetName(), kind)
		}
		seen[kind] = true
		ready := false
		switch status := c["status"].(type) {
		case bool:
			ready = status
		case string:
			switch status {
			case "True":
				ready = true
			case "False", "Unknown":
			default:
				return out, fmt.Errorf("VDNSubnetLink %s condition %s has invalid status %q", link.GetName(), kind, status)
			}
		default:
			return out, fmt.Errorf("VDNSubnetLink %s condition %s has invalid status", link.GetName(), kind)
		}
		if generation, found, err := unstructured.NestedInt64(c, "observedGeneration"); err != nil || (found && generation <= 0) {
			return out, fmt.Errorf("VDNSubnetLink %s condition %s has invalid observedGeneration", link.GetName(), kind)
		} else if found && generation != link.GetGeneration() {
			ready = false
		}
		if kind == "Ready" {
			out.Ready = ready
		} else {
			out.IPReserved = ready
		}
	}
	if !out.Ready {
		out.Pending = append(out.Pending, "VDNSubnetLink Ready condition is not true")
	}
	if !specComplete {
		out.Ready = false
		out.IPReserved = false
	}
	return out, nil
}
