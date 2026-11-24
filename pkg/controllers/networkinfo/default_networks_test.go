/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package networkinfo

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestDefaultNetworksRequireReadyConfiguredBacking(t *testing.T) {
	const ns = "team"
	const target = "/orgs/default/projects/p/vpcs/v"
	ctx := context.Background()
	nc := &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{Subnets: []v1alpha1.SharedSubnet{
		{Path: target + "/subnets/s", PodDefault: true, VMDefault: true},
	}}}
	names := []string{"retained"}
	ready := []v1alpha1.Condition{{Type: v1alpha1.Ready, Status: corev1.ConditionTrue}}
	pod := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{Name: servicecommon.DefaultPodSubnetSet, Namespace: ns, Labels: map[string]string{servicecommon.LabelDefaultNetwork: servicecommon.DefaultPodNetwork}},
		Spec:       v1alpha1.SubnetSetSpec{SubnetNames: &names},
		Status:     v1alpha1.SubnetSetStatus{Conditions: ready},
	}
	vm := pod.DeepCopy()
	vm.Name = servicecommon.DefaultVMSubnetSet
	vm.Labels[servicecommon.LabelDefaultNetwork] = servicecommon.DefaultVMNetwork
	shared := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: names[0], Namespace: ns, Annotations: map[string]string{servicecommon.AnnotationAssociatedResource: "p:v:s"}},
		Status:     v1alpha1.SubnetStatus{Conditions: ready, GatewayAddresses: []string{"192.0.2.1"}},
	}
	check := func(config *v1alpha1.VPCNetworkConfiguration, objects ...client.Object) error {
		r := createNetworkInfoReconciler(objects)
		return r.checkDefaultNetworks(ctx, ns, config, target, map[string]v1alpha1.IPAddressType{servicecommon.DefaultPodNetwork: v1alpha1.IPAddressTypeIPv4, servicecommon.DefaultVMNetwork: v1alpha1.IPAddressTypeIPv4})
	}
	require.NoError(t, check(nc, pod, vm, shared))
	require.Error(t, check(nc, pod, shared))
	duplicate := pod.DeepCopy()
	duplicate.Name = "also-default"
	require.Error(t, check(nc, pod, duplicate, vm, shared))
	unready := vm.DeepCopy()
	unready.Status.Conditions = nil
	require.Error(t, check(nc, pod, unready, shared))
	old := shared.DeepCopy()
	old.Annotations[servicecommon.AnnotationAssociatedResource] = "p:v:old"
	require.Error(t, check(nc, pod, vm, old))
	delayed := shared.DeepCopy()
	delayed.Status.GatewayAddresses = nil
	require.Error(t, check(nc, pod, vm, delayed))
	foreign := nc.DeepCopy()
	foreign.Spec.Subnets[0].Path = "/orgs/default/projects/p/vpcs/other/subnets/s"
	require.Error(t, check(foreign, pod, vm, shared))

	// A native auto-allocated default may be ready for its first allocation with
	// no backend subnet yet. The same set cannot satisfy an explicit shared target.
	pod.Spec.SubnetNames, vm.Spec.SubnetNames = nil, nil
	pod.Spec.IPAddressType, vm.Spec.IPAddressType = v1alpha1.IPAddressTypeIPv4, v1alpha1.IPAddressTypeIPv4
	require.NoError(t, check(&v1alpha1.VPCNetworkConfiguration{}, pod, vm))
	require.Error(t, check(nc, pod, vm, shared))
}

func TestMigratedSharedDefaultPreservesExistingAutoSet(t *testing.T) {
	ctx := context.Background()
	ss := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team", Name: servicecommon.DefaultPodSubnetSet, UID: "retained", Labels: map[string]string{servicecommon.LabelDefaultNetwork: servicecommon.DefaultPodNetwork}},
		Spec:       v1alpha1.SubnetSetSpec{IPAddressType: v1alpha1.IPAddressTypeIPv4, IPv4SubnetSize: 32},
	}
	r := createNetworkInfoReconciler([]client.Object{ss})
	require.NoError(t, r.updateDefaultSubnetSet(ctx, servicecommon.DefaultPodNetwork, ss.Namespace, &v1alpha1.VPCNetworkConfiguration{}, true, IPAddressTypeNone, true))
	out := &v1alpha1.SubnetSet{}
	require.NoError(t, r.Client.Get(ctx, client.ObjectKeyFromObject(ss), out))
	require.Equal(t, ss.Spec, out.Spec)
	require.Equal(t, ss.UID, out.UID)
}
