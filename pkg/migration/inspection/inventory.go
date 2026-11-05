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
		if namespaceName == "" {
			return nil, fmt.Errorf("namespace must not be empty")
		}
		networkList := &netop.NetworkList{}
		if err := inventory.Reader.List(ctx, networkList, client.InNamespace(namespaceName)); err != nil {
			return nil, err
		}
		if len(networkList.Items) == 0 {
			return nil, invalid("namespace %s has no source Networks", namespaceName)
		}
		for _, sourceNetwork := range networkList.Items {
			networkSpec := sourceNetwork.Spec
			providerRef := networkSpec.ProviderRef
			if networkSpec.Type != netop.NetworkTypeVDS || providerRef.APIGroup != netop.GroupName || providerRef.Kind != "VSphereDistributedNetwork" || providerRef.Name == "" || providerRef.Namespace != "" || (providerRef.APIVersion != "" && providerRef.APIVersion != netAPIVersion && providerRef.APIVersion != "v1alpha1") {
				return nil, invalid("Network %s/%s has unsupported source provider", namespaceName, sourceNetwork.GetName())
			}
			uniqueVDNs[providerRef.Name] = struct{}{}
		}
	}
	vdns := make([]string, 0, len(uniqueVDNs))
	for vdnName := range uniqueVDNs {
		vdns = append(vdns, vdnName)
	}
	sort.Strings(vdns)
	return vdns, nil
}

func (inventory KubernetesInventory) Resolve(ctx context.Context, vdnName string) (Source, error) {
	resolvedSource := Source{VDNName: vdnName}
	if inventory.Reader == nil {
		return resolvedSource, fmt.Errorf("Kubernetes reader is required")
	}
	vdnResource := &netop.VSphereDistributedNetwork{}
	if err := inventory.Reader.Get(ctx, client.ObjectKey{Name: vdnName}, vdnResource); err != nil {
		return resolvedSource, err
	}
	vdnSpec := vdnResource.Spec
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
		if len(vdnSpec.IPPools) != 0 || vdnSpec.Gateway != "" || vdnSpec.SubnetMask != "" {
			return resolvedSource, invalid("DHCP VDN has conflicting static configuration")
		}
		resolvedSource.DHCP = true
	case "", netop.IPAssignmentModeStaticPool: // Documented Net Operator default is staticpool.
		resolvedSource.Mask, resolvedSource.Gateway = vdnSpec.SubnetMask, vdnSpec.Gateway
		if len(vdnSpec.IPPools) == 0 {
			return resolvedSource, invalid("static VDN has no IP pools")
		}
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
			return resolvedSource.Ranges[leftIndex].Start < resolvedSource.Ranges[rightIndex].Start
		})
	default:
		return resolvedSource, invalid("unsupported IP assignment mode %q", vdnSpec.IPAssignmentMode)
	}
	return resolvedSource, nil
}
