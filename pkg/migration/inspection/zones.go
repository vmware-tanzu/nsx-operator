/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package inspection

import (
	"context"
	"fmt"
	"net/http"

	"github.com/vmware/govmomi/vapi/rest"
	mpmodel "github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
)

// NSXZonesClient is implemented by the NSX fabric ZonesClient.
type NSXZonesClient interface {
	List(cmLocalID, clusterExternalID, cursor, includedFields, originID *string, pageSize *int64, sortAscending *bool, sortBy *string) (mpmodel.ZoneListResult, error)
}

// VCenterZoneResolver resolves only Supervisor clusters in the VDS span.
// ComputeManagerID is the NSX compute manager that registered this vCenter.
type VCenterZoneResolver struct {
	VCenter          *rest.Client
	NSXZones         NSXZonesClient
	ComputeManagerID string
	SupervisorID     string
}

func (resolver VCenterZoneResolver) ResolveZones(ctx context.Context, clusterIDs []string) (map[string]string, error) {
	if resolver.VCenter == nil || resolver.NSXZones == nil || resolver.ComputeManagerID == "" || resolver.SupervisorID == "" {
		return nil, fmt.Errorf("vCenter session, NSX zones client, compute manager and Supervisor IDs are required")
	}
	wanted := make(map[string]bool, len(clusterIDs))
	for _, clusterID := range clusterIDs {
		if clusterID == "" {
			return nil, fmt.Errorf("empty cluster identity")
		}
		wanted[clusterID] = true
	}
	var supervisorZones []struct {
		Zone     string   `json:"zone"`
		Clusters []string `json:"clusters"`
	}
	resource := resolver.VCenter.Resource("/api/vcenter/namespace-management/supervisors").WithSubpath(resolver.SupervisorID).WithSubpath("topology")
	if err := resolver.VCenter.Do(ctx, resource.Request(http.MethodGet), &supervisorZones); err != nil {
		return nil, fmt.Errorf("read Supervisor topology: %w", err)
	}
	clustersByZone := make(map[string][]string)
	for _, zone := range supervisorZones {
		if zone.Zone == "" {
			return nil, fmt.Errorf("Supervisor topology contains a zone without an identity")
		}
		for _, clusterID := range zone.Clusters {
			if wanted[clusterID] {
				clustersByZone[zone.Zone] = append(clustersByZone[zone.Zone], clusterID)
			}
		}
	}
	if len(clustersByZone) == 0 {
		return map[string]string{}, nil
	}
	externalIDs, err := resolver.nsxZoneIDs(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for localID, relevant := range clustersByZone {
		externalID := externalIDs[localID]
		if externalID == "" {
			return nil, fmt.Errorf("vCenter zone %s is not yet available in NSX", localID)
		}
		for _, clusterID := range relevant {
			if existing := result[clusterID]; existing != "" && existing != externalID {
				return nil, fmt.Errorf("cluster %s belongs to multiple zones", clusterID)
			}
			result[clusterID] = externalID
		}
	}
	return result, nil
}

func (resolver VCenterZoneResolver) nsxZoneIDs(ctx context.Context) (map[string]string, error) {
	var cursor *string
	seen := make(map[string]bool)
	result := make(map[string]string)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, err := resolver.NSXZones.List(nil, nil, cursor, nil, &resolver.ComputeManagerID, nil, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("list NSX zones: %w", err)
		}
		for _, zone := range page.Results {
			if zone.CmLocalId == nil || *zone.CmLocalId == "" || zone.OriginId == nil || *zone.OriginId != resolver.ComputeManagerID || zone.ExternalId == nil || *zone.ExternalId == "" {
				return nil, fmt.Errorf("NSX returned an invalid zone for compute manager %s", resolver.ComputeManagerID)
			}
			if _, exists := result[*zone.CmLocalId]; exists {
				return nil, fmt.Errorf("multiple NSX zones match vCenter zone %s", *zone.CmLocalId)
			}
			result[*zone.CmLocalId] = *zone.ExternalId
		}
		if page.Cursor == nil || *page.Cursor == "" {
			break
		}
		if seen[*page.Cursor] {
			return nil, fmt.Errorf("NSX zone listing repeated cursor")
		}
		seen[*page.Cursor] = true
		cursor = page.Cursor
	}
	return result, nil
}
