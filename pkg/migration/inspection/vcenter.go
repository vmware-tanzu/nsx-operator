/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package inspection

import (
	"context"
	"fmt"

	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// PropertyReader is implemented by govmomi's property.Collector. The caller owns
// vCenter authentication/session lifecycle; inspection only retrieves properties.
type PropertyReader interface {
	RetrieveOne(context.Context, types.ManagedObjectReference, []string, any) error
}

// BulkPropertyReader retrieves the same properties for many objects in one
// vCenter request. Test readers can keep using PropertyReader and its fallback.
type BulkPropertyReader interface {
	Retrieve(context.Context, []types.ManagedObjectReference, []string, any) error
}

// ZoneResolver maps Supervisor-enabled VDS clusters to their zone external IDs.
// Other VDS clusters are omitted.
type ZoneResolver interface {
	ResolveZones(context.Context, []string) (map[string]string, error)
}

// MissingZonesError identifies VDS clusters that require a vCenter zone.
type MissingZonesError struct {
	SwitchUUID string
	ClusterIDs []string
}

func (e *MissingZonesError) Error() string {
	return fmt.Sprintf("create a vCenter zone for VDS %s cluster(s) %v", e.SwitchUUID, e.ClusterIDs)
}

type VCenterTopology struct {
	VCenterUUID string
	Properties  PropertyReader
	Zones       ZoneResolver
}

func (topologyReader VCenterTopology) ResolvePortGroup(ctx context.Context, dvpgID string) (Topology, error) {
	dvpgTopology := Topology{VCenterUUID: topologyReader.VCenterUUID, PortGroupID: dvpgID}
	if topologyReader.Properties == nil || topologyReader.VCenterUUID == "" || dvpgID == "" {
		return dvpgTopology, fmt.Errorf("vCenter UUID, property reader and port group ID are required")
	}
	var distributedPortGroup mo.DistributedVirtualPortgroup
	if err := topologyReader.Properties.RetrieveOne(ctx, types.ManagedObjectReference{Type: "DistributedVirtualPortgroup", Value: dvpgID}, []string{"name", "config.name", "config.distributedVirtualSwitch"}, &distributedPortGroup); err != nil {
		return dvpgTopology, err
	}
	dvpgTopology.PortGroupName = distributedPortGroup.Name
	if dvpgTopology.PortGroupName == "" {
		dvpgTopology.PortGroupName = distributedPortGroup.Config.Name
	}
	if distributedPortGroup.Config.DistributedVirtualSwitch == nil {
		return dvpgTopology, invalid("port group %s has no distributed switch", dvpgID)
	}
	distributedSwitchRef := *distributedPortGroup.Config.DistributedVirtualSwitch
	dvpgTopology.SwitchID = distributedSwitchRef.Value
	var distributedSwitch mo.DistributedVirtualSwitch
	if err := topologyReader.Properties.RetrieveOne(ctx, distributedSwitchRef, []string{"uuid", "summary.hostMember"}, &distributedSwitch); err != nil {
		return dvpgTopology, err
	}
	dvpgTopology.SwitchUUID = distributedSwitch.Uuid
	if distributedSwitch.Uuid == "" {
		return dvpgTopology, invalid("switch %s has incomplete configuration", distributedSwitchRef.Value)
	}
	hostRefs := distributedSwitch.Summary.HostMember
	for _, hostRef := range hostRefs {
		if hostRef.Type != "HostSystem" || hostRef.Value == "" {
			return dvpgTopology, invalid("switch %s has a host member without an identity", distributedSwitchRef.Value)
		}
	}
	hostsByID := make(map[string]mo.HostSystem, len(hostRefs))
	bulkReader, bulkSupported := topologyReader.Properties.(BulkPropertyReader)
	if bulkSupported && len(hostRefs) > 0 {
		hosts := make([]mo.HostSystem, 0, len(hostRefs))
		if err := bulkReader.Retrieve(ctx, hostRefs, []string{"parent"}, &hosts); err != nil {
			return dvpgTopology, err
		}
		for _, host := range hosts {
			hostsByID[host.Self.Value] = host
		}
	}
	clusterIDs := map[string]struct{}{}
	for _, hostRef := range hostRefs {
		hostSystem, found := hostsByID[hostRef.Value]
		if !found {
			if bulkSupported {
				return dvpgTopology, invalid("switch host %s was not returned by vCenter", hostRef.Value)
			}
			if err := topologyReader.Properties.RetrieveOne(ctx, hostRef, []string{"parent"}, &hostSystem); err != nil {
				return dvpgTopology, err
			}
		}
		if hostSystem.Parent == nil || hostSystem.Parent.Type != "ClusterComputeResource" {
			return dvpgTopology, invalid("switch host %s is not attached to a cluster", hostRef.Value)
		}
		clusterIDs[hostSystem.Parent.Value] = struct{}{}
	}
	if len(clusterIDs) == 0 {
		return dvpgTopology, invalid("switch %s has no attached clusters", distributedSwitchRef.Value)
	}
	dvpgTopology.ClusterIDs = sortedKeys(clusterIDs)
	if topologyReader.Zones == nil {
		return dvpgTopology, fmt.Errorf("zone resolver is required")
	}
	zonesByCluster, err := topologyReader.Zones.ResolveZones(ctx, dvpgTopology.ClusterIDs)
	if err != nil {
		return dvpgTopology, err
	}
	zoneIDs := make(map[string]struct{})
	for _, zoneID := range zonesByCluster {
		if zoneID == "" {
			return dvpgTopology, fmt.Errorf("Supervisor zone identity is empty")
		}
		zoneIDs[zoneID] = struct{}{}
	}
	if len(zoneIDs) == 0 {
		return dvpgTopology, invalid("switch %s has no clusters in the Supervisor zone span", dvpgTopology.SwitchUUID)
	}
	dvpgTopology.ZoneIDs = sortedKeys(zoneIDs)
	return dvpgTopology, nil
}
