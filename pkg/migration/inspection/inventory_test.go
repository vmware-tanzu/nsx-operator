/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package inspection

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	netop "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type topologyStub struct{}

func (topologyStub) ResolvePortGroup(_ context.Context, dvpgID string) (Topology, error) {
	dvpgTopology := sourceForVDN("a").Topologies[0]
	dvpgTopology.PortGroupID = dvpgID
	return dvpgTopology, nil
}

func TestKubernetesInventoryChecksVLANTypeWithoutVLANID(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, netop.AddToScheme(scheme))
	vdn := &netop.VSphereDistributedNetwork{ObjectMeta: metav1.ObjectMeta{Name: "vdn"}, Spec: netop.VSphereDistributedNetworkSpec{PortGroupID: "dvportgroup-1", IPAssignmentMode: netop.IPAssignmentModeDHCP}, Status: netop.VSphereDistributedNetworkStatus{DefaultPortConfig: &netop.VSphereDistributedPortConfig{Vlan: &netop.VlanSpec{Type: netop.VLANTypeStandard}}}}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(vdn).Build()
	inventory := KubernetesInventory{Reader: kubeClient, Topology: topologyStub{}}
	_, err := inventory.Resolve(context.Background(), "vdn")
	require.NoError(t, err)
	vdn.Status.DefaultPortConfig.Vlan.Type = netop.VLANTypePrivate
	require.NoError(t, kubeClient.Update(context.Background(), vdn))
	_, err = inventory.Resolve(context.Background(), "vdn")
	require.ErrorAs(t, err, new(*ConfigurationError))
}

type zoneResolverStub struct{ zoneIDs []string }

func (resolver zoneResolverStub) ResolveZones(_ context.Context, clusterIDs []string) (map[string]string, error) {
	zones := make(map[string]string)
	for index, clusterID := range clusterIDs {
		if index < len(resolver.zoneIDs) {
			zones[clusterID] = resolver.zoneIDs[index]
		}
	}
	return zones, nil
}

type propertyReaderStub struct {
	portGroup         mo.DistributedVirtualPortgroup
	distributedSwitch mo.DistributedVirtualSwitch
	hostSystem        mo.HostSystem
}

type multiClusterPropertyReader struct{ propertyReaderStub }

func (reader multiClusterPropertyReader) RetrieveOne(ctx context.Context, reference types.ManagedObjectReference, properties []string, destination any) error {
	if host, ok := destination.(*mo.HostSystem); ok {
		cluster := types.ManagedObjectReference{Type: "ClusterComputeResource", Value: "domain-c" + reference.Value[len(reference.Value)-1:]}
		host.Parent = &cluster
		return nil
	}
	return reader.propertyReaderStub.RetrieveOne(ctx, reference, properties, destination)
}

func (reader propertyReaderStub) RetrieveOne(_ context.Context, reference types.ManagedObjectReference, _ []string, destination any) error {
	switch value := destination.(type) {
	case *mo.DistributedVirtualPortgroup:
		*value = reader.portGroup
	case *mo.DistributedVirtualSwitch:
		*value = reader.distributedSwitch
	case *mo.HostSystem:
		*value = reader.hostSystem
	default:
		return fmt.Errorf("unsupported property destination %T", destination)
	}
	if reference.Value == "missing" {
		return fmt.Errorf("object not found")
	}
	return nil
}

type recordingZoneResolver struct {
	calledWithClusters []string
}

func (resolver *recordingZoneResolver) ResolveZones(_ context.Context, clusterIDs []string) (map[string]string, error) {
	resolver.calledWithClusters = append([]string(nil), clusterIDs...)
	return map[string]string{"domain-c1": "zone-a"}, nil
}
func TestKubernetesInventory(t *testing.T) {
	ctx := context.Background()
	newSourceNetwork := func(networkName, namespaceName string) *netop.Network {
		return &netop.Network{ObjectMeta: metav1.ObjectMeta{Name: networkName, Namespace: namespaceName}, Spec: netop.NetworkSpec{Type: netop.NetworkTypeVDS, ProviderRef: netop.NetworkProviderReference{APIGroup: netop.GroupName, Kind: "VSphereDistributedNetwork", Name: "a"}}}
	}
	vlanID := int32(100)
	vdnResource := &netop.VSphereDistributedNetwork{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Spec: netop.VSphereDistributedNetworkSpec{PortGroupID: "dvportgroup-1", IPAssignmentMode: netop.IPAssignmentModeDHCP}, Status: netop.VSphereDistributedNetworkStatus{DefaultPortConfig: &netop.VSphereDistributedPortConfig{Vlan: &netop.VlanSpec{Type: netop.VLANTypeStandard, VlanID: &vlanID}}}}
	scheme := runtime.NewScheme()
	require.NoError(t, netop.AddToScheme(scheme))
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(newSourceNetwork("n1", "ns1"), newSourceNetwork("n2", "ns2"), vdnResource).Build()
	inventory := KubernetesInventory{Reader: kubeClient, Topology: topologyStub{}}
	vdns, err := inventory.NamespaceVDNs(ctx, []string{"ns2", "ns1"})
	require.NoError(t, err)
	require.Equal(t, []string{"a"}, vdns)
	resolvedSource, err := inventory.Resolve(ctx, "a")
	require.NoError(t, err)
	require.True(t, resolvedSource.DHCP)
	require.Len(t, resolvedSource.Topologies, 1)
	require.Equal(t, "dvportgroup-1", resolvedSource.Topologies[0].PortGroupID)
	_, err = inventory.NamespaceVDNs(ctx, []string{"missing"})
	require.ErrorAs(t, err, new(*ConfigurationError))
	vdnResource.Spec = netop.VSphereDistributedNetworkSpec{PortGroupID: "dvportgroup-1", Gateway: "192.168.10.1", SubnetMask: "255.255.255.0", IPPools: []netop.IPPoolReference{{Name: "pool-high"}, {Name: "pool-low"}}}
	require.NoError(t, kubeClient.Update(ctx, vdnResource))
	ipPool := &netop.IPPool{ObjectMeta: metav1.ObjectMeta{Name: "pool-high"}, Spec: netop.IPPoolSpec{StartingAddress: "192.168.10.100", AddressCount: 101}}
	lowPool := &netop.IPPool{ObjectMeta: metav1.ObjectMeta{Name: "pool-low"}, Spec: netop.IPPoolSpec{StartingAddress: "192.168.10.20", AddressCount: 5}}
	require.NoError(t, kubeClient.Create(ctx, ipPool))
	require.NoError(t, kubeClient.Create(ctx, lowPool))
	resolvedSource, err = inventory.Resolve(ctx, "a")
	require.NoError(t, err)
	require.Equal(t, []string{"192.168.10.20", "192.168.10.100"}, []string{resolvedSource.Ranges[0].Start, resolvedSource.Ranges[1].Start})
	require.False(t, resolvedSource.DHCP)
	require.Equal(t, "192.168.10.200", resolvedSource.Ranges[1].End)
	inspectionResult, err := (Service{Inventory: inventory}).InspectNamespaces(ctx, []string{"ns1", "ns2"}, nil)
	require.NoError(t, err)
	require.True(t, inspectionResult.Ready())
	ipPool.Spec = netop.IPPoolSpec{StartingAddress: "255.255.255.250", AddressCount: 10}
	require.NoError(t, kubeClient.Update(ctx, ipPool))
	_, err = inventory.Resolve(ctx, "a")
	require.ErrorContains(t, err, "overflows")
	vdnResource.Spec = netop.VSphereDistributedNetworkSpec{PortGroupID: "dvportgroup-1", IPAssignmentMode: netop.IPAssignmentModeNone}
	require.NoError(t, kubeClient.Update(ctx, vdnResource))
	_, err = inventory.Resolve(ctx, "a")
	require.ErrorAs(t, err, new(*ConfigurationError))
}

func TestVCenterTopologyWithSimulator(t *testing.T) {
	model := simulator.VPX()
	model.Host = 0
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		finder := find.NewFinder(c, true)
		dc, err := finder.DefaultDatacenter(ctx)
		require.NoError(t, err)
		finder.SetDatacenter(dc)
		groups, err := finder.NetworkList(ctx, "*")
		require.NoError(t, err)
		require.NotEmpty(t, groups)
		vcenterTopologyReader := VCenterTopology{VCenterUUID: "vc-uuid", Properties: property.DefaultCollector(c), Zones: zoneResolverStub{zoneIDs: []string{"zone-a"}}}
		var dvpgID string
		for _, networkObject := range groups {
			if networkObject.Reference().Type == "DistributedVirtualPortgroup" {
				dvpgID = networkObject.Reference().Value
				break
			}
		}
		require.NotEmpty(t, dvpgID)
		dvpgTopology, err := vcenterTopologyReader.ResolvePortGroup(ctx, dvpgID)
		require.NoError(t, err)
		require.NotEmpty(t, dvpgTopology.SwitchID)
		require.NotEmpty(t, dvpgTopology.SwitchUUID)
		require.Equal(t, "vc-uuid", dvpgTopology.VCenterUUID)
		require.NotEmpty(t, dvpgTopology.ClusterIDs)
		require.Equal(t, []string{"zone-a"}, dvpgTopology.ZoneIDs)
		_, err = vcenterTopologyReader.ResolvePortGroup(ctx, "dvportgroup-does-not-exist")
		require.Error(t, err)
	}, model)
}

func TestVCenterTopologyResolvesClustersAndZone(t *testing.T) {
	distributedSwitchRef := types.ManagedObjectReference{Type: "VmwareDistributedVirtualSwitch", Value: "dvs-1"}
	hostRef := types.ManagedObjectReference{Type: "HostSystem", Value: "host-1"}
	clusterRef := types.ManagedObjectReference{Type: "ClusterComputeResource", Value: "domain-c1"}
	reader := propertyReaderStub{
		portGroup: mo.DistributedVirtualPortgroup{Config: types.DVPortgroupConfigInfo{
			Name:                     "dvpg-workload",
			DistributedVirtualSwitch: &distributedSwitchRef,
		}},
		distributedSwitch: mo.DistributedVirtualSwitch{Uuid: "dvs-uuid-1", Summary: types.DVSSummary{HostMember: []types.ManagedObjectReference{hostRef}}},
		hostSystem:        mo.HostSystem{ManagedEntity: mo.ManagedEntity{Parent: &clusterRef}},
	}
	zoneResolver := &recordingZoneResolver{}
	result, err := (VCenterTopology{VCenterUUID: "vc-uuid", Properties: reader, Zones: zoneResolver}).ResolvePortGroup(context.Background(), "dvportgroup-1")
	require.NoError(t, err)
	require.Equal(t, "vc-uuid", result.VCenterUUID)
	require.Equal(t, "dvpg-workload", result.PortGroupName)
	require.Equal(t, []string{"zone-a"}, result.ZoneIDs)
	require.Equal(t, []string{"domain-c1"}, result.ClusterIDs)
	require.Equal(t, []string{"domain-c1"}, zoneResolver.calledWithClusters)
	_, err = (VCenterTopology{VCenterUUID: "vc-uuid", Properties: reader, Zones: zoneResolverStub{}}).ResolvePortGroup(context.Background(), "dvportgroup-1")
	require.ErrorAs(t, err, new(*ConfigurationError))
}

func TestVCenterTopologyUsesSupervisorZonesOnly(t *testing.T) {
	switchRef := types.ManagedObjectReference{Type: "VmwareDistributedVirtualSwitch", Value: "dvs-1"}
	reader := multiClusterPropertyReader{propertyReaderStub{
		portGroup: mo.DistributedVirtualPortgroup{Config: types.DVPortgroupConfigInfo{DistributedVirtualSwitch: &switchRef}},
		distributedSwitch: mo.DistributedVirtualSwitch{Uuid: "vds-uuid", Summary: types.DVSSummary{HostMember: []types.ManagedObjectReference{
			{Type: "HostSystem", Value: "host-1"}, {Type: "HostSystem", Value: "host-2"},
		}}},
	}}
	readerWithZones := VCenterTopology{VCenterUUID: "vc-uuid", Properties: reader, Zones: zoneResolverStub{zoneIDs: []string{"zone-1"}}}
	topology, err := readerWithZones.ResolvePortGroup(context.Background(), "dvpg-1")
	require.NoError(t, err)
	require.Equal(t, []string{"domain-c1", "domain-c2"}, topology.ClusterIDs)
	require.Equal(t, []string{"zone-1"}, topology.ZoneIDs)

	readerWithZones.Zones = zoneResolverStub{zoneIDs: []string{"zone-1", "zone-2"}}
	topology, err = readerWithZones.ResolvePortGroup(context.Background(), "dvpg-1")
	require.NoError(t, err)
	require.Equal(t, []string{"domain-c1", "domain-c2"}, topology.ClusterIDs)
	require.Equal(t, []string{"zone-1", "zone-2"}, topology.ZoneIDs)
}
