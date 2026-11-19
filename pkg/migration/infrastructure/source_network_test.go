/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package infrastructure

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	netop "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestKubernetesSourceNetworksRequiresUniqueDVPGMatch(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, netop.AddToScheme(scheme))
	vdn := &netop.VSphereDistributedNetwork{ObjectMeta: metav1.ObjectMeta{Name: "source-vdn"}, Spec: netop.VSphereDistributedNetworkSpec{PortGroupID: "dvportgroup-1"}}
	ref := netop.NetworkProviderReference{APIGroup: netop.GroupName, Kind: "VSphereDistributedNetwork", Name: vdn.Name}
	network := &netop.Network{ObjectMeta: metav1.ObjectMeta{Namespace: "vmware-system-netop", Name: "source-network"}, Spec: netop.NetworkSpec{Type: netop.NetworkTypeVDS, ProviderRef: ref}}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(vdn, network).Build()
	resolver := KubernetesSourceNetworks{Reader: kubeClient, Namespace: network.Namespace}

	key, err := resolver.ForBacking(ctx, "dvportgroup-1")
	require.NoError(t, err)
	require.Equal(t, client.ObjectKeyFromObject(network), key)
	key, err = resolver.ForBacking(ctx, "source-vdn")
	require.NoError(t, err, "FLB network identifiers can be VDN names")
	require.Equal(t, client.ObjectKeyFromObject(network), key)
	_, err = resolver.ForBacking(ctx, "dvportgroup-other")
	require.ErrorContains(t, err, "no managed source Network")

	second := network.DeepCopy()
	second.Name = "another-network"
	second.ResourceVersion = ""
	require.NoError(t, kubeClient.Create(ctx, second))
	_, err = resolver.ForBacking(ctx, "dvportgroup-1")
	require.ErrorContains(t, err, "multiple source Networks")
}
