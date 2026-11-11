/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package inspection

import (
	"context"
	"fmt"
	"sort"

	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// PropertyReader is implemented by govmomi's property.Collector. The caller owns
// vCenter authentication/session lifecycle; inspection only retrieves properties.
type PropertyReader interface {
	RetrieveOne(context.Context, types.ManagedObjectReference, []string, any) error
}

type VCenterTopology struct{ Properties PropertyReader }

func (topologyReader VCenterTopology) ResolvePortGroup(ctx context.Context, dvpgID string) (Topology, error) {
	dvpgTopology := Topology{PortGroupID: dvpgID}
	if topologyReader.Properties == nil || dvpgID == "" {
		return dvpgTopology, fmt.Errorf("vCenter property reader and port group ID are required")
	}
	var distributedPortGroup mo.DistributedVirtualPortgroup
	if err := topologyReader.Properties.RetrieveOne(ctx, types.ManagedObjectReference{Type: "DistributedVirtualPortgroup", Value: dvpgID}, []string{"config.distributedVirtualSwitch"}, &distributedPortGroup); err != nil {
		return dvpgTopology, err
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
	clusterIDs := map[string]struct{}{}
	for _, hostRef := range distributedSwitch.Summary.HostMember {
		if hostRef.Type != "HostSystem" || hostRef.Value == "" {
			return dvpgTopology, invalid("switch %s has a host member without an identity", distributedSwitchRef.Value)
		}
		var hostSystem mo.HostSystem
		if err := topologyReader.Properties.RetrieveOne(ctx, hostRef, []string{"parent"}, &hostSystem); err != nil {
			return dvpgTopology, err
		}
		if hostSystem.Parent == nil || hostSystem.Parent.Type != "ClusterComputeResource" {
			return dvpgTopology, invalid("switch host %s is not attached to a cluster", hostRef.Value)
		}
		clusterIDs[hostSystem.Parent.Value] = struct{}{}
	}
	if len(clusterIDs) == 0 {
		return dvpgTopology, invalid("switch %s has no attached clusters", distributedSwitchRef.Value)
	}
	for clusterID := range clusterIDs {
		dvpgTopology.ClusterIDs = append(dvpgTopology.ClusterIDs, clusterID)
	}
	sort.Strings(dvpgTopology.ClusterIDs)
	return dvpgTopology, nil
}
