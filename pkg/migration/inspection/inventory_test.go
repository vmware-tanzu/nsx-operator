/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package inspection

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	netop "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
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
func TestKubernetesInventory(t *testing.T) {
	ctx := context.Background()
	newSourceNetwork := func(networkName, namespaceName string) *netop.Network {
		return &netop.Network{ObjectMeta: metav1.ObjectMeta{Name: networkName, Namespace: namespaceName}, Spec: netop.NetworkSpec{Type: netop.NetworkTypeVDS, ProviderRef: netop.NetworkProviderReference{APIGroup: netop.GroupName, Kind: "VSphereDistributedNetwork", Name: "a"}}}
	}
	vdnResource := &netop.VSphereDistributedNetwork{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Spec: netop.VSphereDistributedNetworkSpec{PortGroupID: "dvportgroup-1", IPAssignmentMode: netop.IPAssignmentModeDHCP}}
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
	vdnResource.Spec = netop.VSphereDistributedNetworkSpec{PortGroupID: "dvportgroup-1", Gateway: "192.168.10.1", SubnetMask: "255.255.255.0", IPPools: []netop.IPPoolReference{{Name: "pool"}}}
	require.NoError(t, kubeClient.Update(ctx, vdnResource))
	ipPool := &netop.IPPool{ObjectMeta: metav1.ObjectMeta{Name: "pool"}, Spec: netop.IPPoolSpec{StartingAddress: "192.168.10.100", AddressCount: 101}}
	require.NoError(t, kubeClient.Create(ctx, ipPool))
	resolvedSource, err = inventory.Resolve(ctx, "a")
	require.NoError(t, err)
	require.False(t, resolvedSource.DHCP)
	require.Equal(t, "192.168.10.200", resolvedSource.Ranges[0].End)
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
		vcenterTopologyReader := VCenterTopology{Properties: property.DefaultCollector(c)}
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
		require.NotEmpty(t, dvpgTopology.ClusterIDs)
		_, err = vcenterTopologyReader.ResolvePortGroup(ctx, "dvportgroup-does-not-exist")
		require.Error(t, err)
	}, model)
}
