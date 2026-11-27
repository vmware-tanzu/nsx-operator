/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package inspection

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/netip"
	"sort"

	"sigs.k8s.io/controller-runtime/pkg/client"

	netop "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

var netAPIVersion = netop.SchemeGroupVersion.String()

type TopologyReader interface {
	ResolvePortGroup(context.Context, string) (Topology, error)
}

// topologyReaderCache is scoped to one inspection so shared DVPGs reuse their
// vCenter and zone lookups without keeping inventory stale across inspections.
type topologyReaderCache struct {
	values map[string]Topology
}

type cachedTopologyReader struct {
	cache  *topologyReaderCache
	reader TopologyReader
}

func (cache *topologyReaderCache) readerOrNil(reader TopologyReader) TopologyReader {
	if reader == nil {
		return nil
	}
	return cachedTopologyReader{cache: cache, reader: reader}
}

func (reader cachedTopologyReader) ResolvePortGroup(ctx context.Context, dvpgID string) (Topology, error) {
	if err := ctx.Err(); err != nil {
		return Topology{}, err
	}
	if topology, found := reader.cache.values[dvpgID]; found {
		return topology, nil
	}
	topology, err := reader.reader.ResolvePortGroup(ctx, dvpgID)
	if err != nil {
		return Topology{}, err
	}
	reader.cache.values[dvpgID] = topology
	return topology, nil
}

// +kubebuilder:rbac:groups=netoperator.vmware.com,resources=networks;vspheredistributednetworks;ippools,verbs=get;list

// KubernetesInventory reads the source Net Operator v1alpha1 contract. A direct
// API reader is recommended so Evaluate/Plan rechecks do not reuse stale caches.
// Register netop.AddToScheme on the reader's scheme before use.
type KubernetesInventory struct {
	Reader   client.Reader
	Topology TopologyReader
}

func (inventory KubernetesInventory) NamespaceVDNs(ctx context.Context, namespaces []string) ([]string, error) {
	if inventory.Reader == nil || len(namespaces) == 0 {
		return nil, fmt.Errorf("reader and namespace batch are required")
	}
	uniqueVDNs := map[string]struct{}{}
	for _, namespaceName := range namespaces {
		vdns, err := inventory.namespaceVDNs(ctx, namespaceName, false)
		if err != nil {
			return nil, err
		}
		for _, vdn := range vdns {
			uniqueVDNs[vdn] = struct{}{}
		}
	}
	return sortedKeys(uniqueVDNs), nil
}

// RetainedNamespaceVDNs reads legacy VDS Networks for a member whose migration
// checkpoint has been verified by the caller. Net Operator keeps legacy VDS
// resources functional while publishing VPC Networks during cutover.
func (inventory KubernetesInventory) RetainedNamespaceVDNs(ctx context.Context, namespace string) ([]string, error) {
	if inventory.Reader == nil || namespace == "" {
		return nil, fmt.Errorf("reader and namespace are required")
	}
	settings := &netop.NetworkSettings{}
	if err := inventory.Reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: "default"}, settings); err != nil {
		return nil, fmt.Errorf("read NetworkSettings/default: %w", err)
	}
	source := settings.Provider == netop.NetworkProviderVSphereDistributed && settings.LegacyProvider == ""
	migrated := settings.Provider == netop.NetworkProviderVPC && settings.LegacyProvider == netop.NetworkProviderVSphereDistributed
	if !source && !migrated {
		return nil, invalid("namespace %s has no effective or legacy VDS provider", namespace)
	}
	return inventory.namespaceVDNs(ctx, namespace, true)
}

func (inventory KubernetesInventory) namespaceVDNs(ctx context.Context, namespace string, retained bool) ([]string, error) {
	if namespace == "" {
		return nil, fmt.Errorf("namespace must not be empty")
	}
	var networks netop.NetworkList
	if err := inventory.Reader.List(ctx, &networks, client.InNamespace(namespace)); err != nil {
		return nil, err
	}
	vdns := map[string]struct{}{}
	for _, network := range networks.Items {
		if retained && network.Spec.Type == netop.NetworkTypeNSXTVPC {
			continue
		}
		ref := network.Spec.ProviderRef
		if network.Spec.Type != netop.NetworkTypeVDS || ref.APIGroup != netop.GroupName || ref.Kind != "VSphereDistributedNetwork" || ref.Name == "" || ref.Namespace != "" || (ref.APIVersion != "" && ref.APIVersion != netAPIVersion && ref.APIVersion != "v1alpha1") {
			return nil, invalid("Network %s/%s has unsupported source provider", namespace, network.Name)
		}
		if !network.DeletionTimestamp.IsZero() {
			return nil, invalid("source Network %s/%s is deleting", namespace, network.Name)
		}
		vdns[ref.Name] = struct{}{}
	}
	if len(vdns) == 0 {
		return nil, invalid("namespace %s has no source Networks", namespace)
	}
	return sortedKeys(vdns), nil
}

func (inventory KubernetesInventory) Resolve(ctx context.Context, vdnName string) (DiscoveredSource, error) {
	resolvedSource := DiscoveredSource{Source: Source{VDNName: vdnName}}
	if inventory.Reader == nil {
		return resolvedSource, fmt.Errorf("Kubernetes reader is required")
	}
	vdnResource := &netop.VSphereDistributedNetwork{}
	if err := inventory.Reader.Get(ctx, client.ObjectKey{Name: vdnName}, vdnResource); err != nil {
		return resolvedSource, err
	}
	vdnSpec := vdnResource.Spec
	portConfig := vdnResource.Status.DefaultPortConfig
	if portConfig == nil || portConfig.Vlan == nil {
		return resolvedSource, fmt.Errorf("VDN %s VLAN status has not been observed yet", vdnName)
	}
	vlan := portConfig.Vlan
	if vlan.Type != netop.VLANTypeStandard {
		return resolvedSource, invalid("VDN %s has unsupported VLAN type %q", vdnName, vlan.Type)
	}
	if vdnSpec.PortGroupID == "" || inventory.Topology == nil {
		return resolvedSource, fmt.Errorf("port group and topology reader are required")
	}
	dvpgTopology, err := inventory.Topology.ResolvePortGroup(ctx, vdnSpec.PortGroupID)
	if err != nil {
		return resolvedSource, err
	}
	resolvedSource.Topologies = []Topology{dvpgTopology}
	switch vdnSpec.IPAssignmentMode {
	case netop.IPAssignmentModeDHCP:
		resolvedSource.DHCP = true
	case "", netop.IPAssignmentModeStaticPool: // Documented Net Operator default is staticpool.
		resolvedSource.Mask, resolvedSource.Gateway = vdnSpec.SubnetMask, vdnSpec.Gateway
		for _, poolRef := range vdnSpec.IPPools {
			if poolRef.Name == "" || (poolRef.APIVersion != "" && poolRef.APIVersion != "v1alpha1" && poolRef.APIVersion != netAPIVersion) {
				return resolvedSource, invalid("invalid IPPool reference")
			}
			ipPool := &netop.IPPool{}
			if err := inventory.Reader.Get(ctx, client.ObjectKey{Name: poolRef.Name}, ipPool); err != nil {
				return resolvedSource, err
			}
			firstAddress, err := ipv4(ipPool.Spec.StartingAddress)
			if err != nil || ipPool.Spec.AddressCount < 1 || ipPool.Spec.AddressCount > 1<<32 {
				return resolvedSource, invalid("IPPool %s has invalid IPv4 range", poolRef.Name)
			}
			lastAddressBytes := firstAddress.As4()
			lastAddressValue := uint64(binary.BigEndian.Uint32(lastAddressBytes[:])) + uint64(ipPool.Spec.AddressCount) - 1
			if lastAddressValue > 1<<32-1 {
				return resolvedSource, invalid("IPPool %s range overflows IPv4", poolRef.Name)
			}
			binary.BigEndian.PutUint32(lastAddressBytes[:], uint32(lastAddressValue))
			resolvedSource.Ranges = append(resolvedSource.Ranges, api.IPAddressRange{Start: firstAddress.String(), End: netip.AddrFrom4(lastAddressBytes).String()})
		}
		sort.Slice(resolvedSource.Ranges, func(leftIndex, rightIndex int) bool {
			left := netip.MustParseAddr(resolvedSource.Ranges[leftIndex].Start)
			right := netip.MustParseAddr(resolvedSource.Ranges[rightIndex].Start)
			return left.Compare(right) < 0
		})
	default:
		return resolvedSource, invalid("unsupported IP assignment mode %q", vdnSpec.IPAssignmentMode)
	}
	return resolvedSource, nil
}
