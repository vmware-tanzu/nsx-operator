/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package topology

import (
	"context"
	"errors"
	"fmt"
	"sort"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/inspection"
)

type retainedSourceInventory interface {
	RetainedNamespaceVDNs(context.Context, string) ([]string, error)
}

// SharedGraph discovers the remaining mutable resources and all live consumers.
// It must fail if any part of the graph cannot be read authoritatively.
// During Rollback networks carry verified source topology. The graph must use
// live target resources and ownership evidence to identify reversal claims.
type SharedGraph interface {
	DiscoverShared(context.Context, *api.NamespaceNetworkTransition, []inspection.Network) ([]ResourceIdentity, []ConsumerIdentity, error)
}

// InspectionDiscoverer adapts inspection to the live preflight contract.
type InspectionDiscoverer struct {
	Service inspection.Service
	Graph   SharedGraph
}

func (d InspectionDiscoverer) DiscoverBatch(ctx context.Context, batch *api.NamespaceNetworkTransition) (BatchSnapshot, error) {
	if batch == nil || len(batch.Spec.Namespaces) == 0 || d.Service.Inventory == nil {
		return BatchSnapshot{}, fmt.Errorf("namespace batch and inspection inventory are required")
	}
	if batch.Spec.Action != api.ActionEvaluate && d.Graph == nil {
		return BatchSnapshot{}, fmt.Errorf("live shared resource and consumer discovery is required for %s", batch.Spec.Action)
	}
	vdnSet := map[string]struct{}{}
	for _, member := range batch.Spec.Namespaces {
		vdns, err := d.Service.Inventory.NamespaceVDNs(ctx, []string{member})
		if err != nil {
			var configuration *inspection.ConfigurationError
			retained, canRecover := d.Service.Inventory.(retainedSourceInventory)
			if !canRecover || !hasSourceRecoveryCheckpoint(batch, member) || !errors.As(err, &configuration) {
				return BatchSnapshot{}, fmt.Errorf("namespace %s source backing unavailable: %w", member, err)
			}
			vdns, err = retained.RetainedNamespaceVDNs(ctx, member)
			if err != nil {
				return BatchSnapshot{}, fmt.Errorf("namespace %s source backing recovery: %w", member, err)
			}
		}
		if len(vdns) == 0 {
			return BatchSnapshot{}, fmt.Errorf("namespace %s has no verified source VDN", member)
		}
		for _, vdn := range vdns {
			if vdn == "" {
				return BatchSnapshot{}, fmt.Errorf("namespace %s has an empty source VDN", member)
			}
			vdnSet[vdn] = struct{}{}
		}
	}
	vdns := make([]string, 0, len(vdnSet))
	for vdn := range vdnSet {
		vdns = append(vdns, vdn)
	}
	sort.Strings(vdns)
	if batch.Spec.Action == api.ActionExecute {
		return d.discoverExecute(ctx, batch, vdns)
	}
	// Evaluate's source preflight must let the handler report missing inputs.
	// Rollback may have already removed a conversion and has no forward plan.
	networks := make([]inspection.Network, 0, len(vdns))
	for _, vdn := range vdns {
		source, err := d.Service.Inventory.Resolve(ctx, vdn)
		if err != nil {
			return BatchSnapshot{}, fmt.Errorf("resolve source VDN %s: %w", vdn, err)
		}
		if source.VDNName != vdn || len(source.Topologies) == 0 {
			return BatchSnapshot{}, fmt.Errorf("VDN %s has no verified source backing", vdn)
		}
		for _, backing := range source.Topologies {
			if _, err := inspection.DVPGIdentity(backing.VCenterUUID, backing.PortGroupID); err != nil {
				return BatchSnapshot{}, fmt.Errorf("VDN %s backing: %w", vdn, err)
			}
		}
		networks = append(networks, inspection.Network{Source: source.Source})
	}
	if batch.Spec.Action == api.ActionEvaluate {
		return BatchSnapshot{Complete: true}, nil
	}
	return d.discoverShared(ctx, batch, networks, nil)
}

func hasSourceRecoveryCheckpoint(batch *api.NamespaceNetworkTransition, member string) bool {
	if batch.Spec.Action != api.ActionExecute && batch.Spec.Action != api.ActionRollback {
		return false
	}
	status := batch.Status
	if status == nil || status.ActionGeneration < 1 || status.ActionGeneration > batch.Generation ||
		(status.LastAction != api.ActionExecute && status.LastAction != api.ActionRollback) {
		return false
	}
	if batch.Spec.Action == api.ActionExecute && (status.LastAction != api.ActionExecute || status.ActionGeneration != batch.Generation) {
		return false
	}
	for _, progress := range status.NamespaceStatuses {
		if progress.Name == member {
			switch progress.Phase {
			case api.PhaseExecuting, api.PhaseCompleted, api.PhaseFailed, api.PhaseRollingBack, api.PhaseRolledBack:
				return true
			}
		}
	}
	return false
}

func (d InspectionDiscoverer) discoverExecute(ctx context.Context, batch *api.NamespaceNetworkTransition, vdns []string) (BatchSnapshot, error) {
	if d.Service.TargetInventory == nil {
		return BatchSnapshot{}, fmt.Errorf("target inspection inventory is required for Execute")
	}
	var inputs []api.DHCPNetworkConfiguration
	var names []api.VDNResourceNaming
	if batch.Spec.NetworkConfiguration != nil {
		inputs = batch.Spec.NetworkConfiguration.NamespaceDHCPNetworks
		if batch.Spec.NetworkConfiguration.ResourceNaming != nil {
			names = batch.Spec.NetworkConfiguration.ResourceNaming.Networks
		}
	}
	result, err := d.Service.InspectWithNaming(ctx, vdns, inputs, names)
	if err != nil {
		return BatchSnapshot{}, err
	}
	if !result.Ready() {
		return BatchSnapshot{}, fmt.Errorf("source inspection incomplete: problems=%v missing zones=%v required inputs=%v", result.Problems, result.MissingZones, result.RequiredInputs)
	}
	var claims []ResourceIdentity
	for _, network := range result.Networks {
		for _, backing := range network.Source.Topologies {
			for _, conversion := range network.Target.Conversions {
				if !conversion.Create || conversion.PortGroupID != backing.PortGroupID || conversion.SwitchUUID != backing.SwitchUUID {
					continue
				}
				key, err := inspection.DVPGIdentity(backing.VCenterUUID, backing.PortGroupID)
				if err != nil {
					return BatchSnapshot{}, err
				}
				claims = append(claims, ResourceIdentity{Kind: ResourceDVPG, Key: key, VDNName: network.Source.VDNName})
			}
		}
	}
	return d.discoverShared(ctx, batch, result.Networks, claims)
}

func (d InspectionDiscoverer) discoverShared(ctx context.Context, batch *api.NamespaceNetworkTransition, networks []inspection.Network, claims []ResourceIdentity) (BatchSnapshot, error) {
	resources, consumers, err := d.Graph.DiscoverShared(ctx, batch, networks)
	if err != nil {
		return BatchSnapshot{}, fmt.Errorf("discover shared resources and consumers: %w", err)
	}
	snapshot := BatchSnapshot{Complete: true, Resources: append(claims, resources...), Consumers: consumers}
	if err := snapshot.Validate(); err != nil {
		return BatchSnapshot{}, err
	}
	return snapshot, nil
}
