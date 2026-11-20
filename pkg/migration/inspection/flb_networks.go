/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package inspection

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware/govmomi/vapi/rest"
)

const flbProvider = "VSPHERE_FOUNDATION"

// FLBNetworks contains the source networks used by the Supervisor Foundation
// Load Balancer. The API represents each backing as a vCenter network ID.
type FLBNetworks struct {
	VCenterUUID     string
	FrontendNetwork string
	BackendNetwork  string
	VIPRanges       []VIPRange
}

type VIPRange struct {
	Address string `json:"address"`
	Count   int    `json:"count"`
}

type LBSNATSelection string

const (
	AllocateFromBackendNetwork LBSNATSelection = "AllocateFromBackendNetwork"
	UseConfiguredLBSNATIP      LBSNATSelection = "UseConfiguredIP"
	AllocateFromVIPRange       LBSNATSelection = "AllocateFromVIPRange"
	ReuseAcceptedLBSNATIP      LBSNATSelection = "ReuseAcceptedAllocation"
)

// LBSNATDecision tells the Evaluate handler which allocation source to use.
// Inspection chooses the source; the handler reserves and records the address.
type LBSNATDecision struct {
	Selection        LBSNATSelection
	IPAddress        string
	AllocationRef    *api.NamespacedName
	AllocationSource string
	VIPRanges        []VIPRange
	Warning          string
}

// SelectLBSNATSource applies the Supervisor SNAT policy using live FLB
// networks and inspected workload backings.
func SelectLBSNATSource(flb FLBNetworks, workloadTopologies []Topology, configuredIPs []string) (LBSNATDecision, error) {
	if flb.FrontendNetwork == "" || flb.BackendNetwork == "" {
		return LBSNATDecision{}, fmt.Errorf("FLB frontend and backend networks are required")
	}
	if flb.BackendNetwork == flb.FrontendNetwork || networkIsWorkload(flb.VCenterUUID, flb.BackendNetwork, workloadTopologies) {
		return LBSNATDecision{Selection: AllocateFromBackendNetwork, AllocationSource: flb.BackendNetwork}, nil
	}
	if len(configuredIPs) > 1 {
		return LBSNATDecision{}, fmt.Errorf("at most one LB SNAT IP is supported")
	}
	if len(configuredIPs) == 1 {
		if _, err := ipv4(configuredIPs[0]); err != nil {
			return LBSNATDecision{}, fmt.Errorf("configured LB SNAT IP must be IPv4")
		}
		return LBSNATDecision{Selection: UseConfiguredLBSNATIP, IPAddress: configuredIPs[0]}, nil
	}
	if len(flb.VIPRanges) == 0 {
		return LBSNATDecision{}, fmt.Errorf("FLB has no VIP range for LB SNAT fallback")
	}
	warning := "FLB backend is separate from the VIP and workload networks; the LB SNAT IP will be allocated from the VIP range. Verify routing from the backend to the selected address."
	return LBSNATDecision{Selection: AllocateFromVIPRange, AllocationSource: flb.FrontendNetwork, VIPRanges: append([]VIPRange(nil), flb.VIPRanges...), Warning: warning}, nil
}

func networkIsWorkload(vCenterUUID, networkID string, workloadTopologies []Topology) bool {
	for _, topology := range workloadTopologies {
		if topology.VCenterUUID == vCenterUUID && (topology.PortGroupID == networkID || topology.PortGroupName == networkID) {
			return true
		}
	}
	return false
}

type FLBNetworkReader interface {
	LoadBalancerNetworks(context.Context) (FLBNetworks, error)
}

// VCenterFLBNetworkReader reads only fields needed for migration. Its response
// types intentionally omit edge credentials and other sensitive fields.
type VCenterFLBNetworkReader struct {
	VCenter      *rest.Client
	SupervisorID string
	VCenterUUID  string
}

type flbEdgeInterface struct {
	Personas []string `json:"personas"`
	Network  string   `json:"network"`
}

type flbFoundation struct {
	Interfaces []flbEdgeInterface `json:"interfaces"`
	VIPRanges  []VIPRange         `json:"vip_ranges"`
}

func (reader VCenterFLBNetworkReader) LoadBalancerNetworks(ctx context.Context) (FLBNetworks, error) {
	if reader.VCenter == nil || reader.SupervisorID == "" || reader.VCenterUUID == "" {
		return FLBNetworks{}, fmt.Errorf("vCenter session, UUID and Supervisor ID are required")
	}
	var response struct {
		Edges []struct {
			Provider   string         `json:"provider"`
			Foundation *flbFoundation `json:"foundation"`
		} `json:"edges"`
	}
	path := "/api/vcenter/namespace-management/supervisors/" + url.PathEscape(reader.SupervisorID) + "/networks/edges"
	if err := reader.VCenter.Do(ctx, reader.VCenter.Resource(path).Request(http.MethodGet), &response); err != nil {
		return FLBNetworks{}, fmt.Errorf("list Supervisor load-balancer edges: %w", err)
	}
	var foundation *flbFoundation
	for _, edge := range response.Edges {
		if edge.Provider != flbProvider {
			continue
		}
		if edge.Foundation == nil || foundation != nil {
			return FLBNetworks{}, fmt.Errorf("Supervisor must have exactly one complete vSphere Foundation load balancer")
		}
		foundation = edge.Foundation
	}
	if foundation == nil {
		return FLBNetworks{}, fmt.Errorf("Supervisor has no vSphere Foundation load balancer")
	}
	result := FLBNetworks{VCenterUUID: reader.VCenterUUID}
	for _, networkInterface := range foundation.Interfaces {
		if networkInterface.Network == "" {
			return FLBNetworks{}, fmt.Errorf("Foundation load-balancer interface has no network")
		}
		for _, persona := range networkInterface.Personas {
			switch persona {
			case "FRONTEND":
				if result.FrontendNetwork != "" && result.FrontendNetwork != networkInterface.Network {
					return FLBNetworks{}, fmt.Errorf("Foundation load balancer has multiple frontend networks")
				}
				result.FrontendNetwork = networkInterface.Network
			case "WORKLOAD", "BACKEND":
				if result.BackendNetwork != "" && result.BackendNetwork != networkInterface.Network {
					return FLBNetworks{}, fmt.Errorf("Foundation load balancer has multiple backend networks")
				}
				result.BackendNetwork = networkInterface.Network
			}
		}
	}
	if result.FrontendNetwork == "" || result.BackendNetwork == "" {
		return FLBNetworks{}, fmt.Errorf("Foundation load balancer is missing frontend or workload/backend network configuration")
	}
	result.VIPRanges = append([]VIPRange(nil), foundation.VIPRanges...)
	return result, nil
}
