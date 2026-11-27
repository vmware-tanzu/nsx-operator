/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package infrastructure

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	netop "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/inspection"
)

type fixedSourceNetwork struct {
	key client.ObjectKey
	err error
}

func (source fixedSourceNetwork) ForBacking(context.Context, string) (client.ObjectKey, error) {
	return source.key, source.err
}

func TestNetOperatorSNATTrustsUserIPWhenNoManagedNetworkExists(t *testing.T) {
	resource := evaluationTransition()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(resource).Build()
	service := NetOperatorSNAT{Client: kubeClient, Reader: kubeClient, Networks: fixedSourceNetwork{err: fmt.Errorf("%w: backend", ErrNoManagedSourceNetwork)}}
	accepted, ready, err := service.Ensure(context.Background(), resource, inspection.LBSNATDecision{Selection: inspection.UseConfiguredLBSNATIP, IPAddress: "192.0.2.40", AllocationSource: "backend"})
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, "192.0.2.40", accepted.IPAddress)
	require.Nil(t, accepted.AllocationRef)
}

func TestNetOperatorSNATReusesReadySourceAllocation(t *testing.T) {
	ctx := context.Background()
	resource := evaluationTransition()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	require.NoError(t, netop.AddToScheme(scheme))
	network := &netop.Network{ObjectMeta: metav1.ObjectMeta{Namespace: "vmware-system-netop", Name: "flb-backend", UID: "backend-uid"}, Spec: netop.NetworkSpec{Type: netop.NetworkTypeVDS}}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(resource, &netop.IPAddressAllocation{}).WithObjects(resource, network).Build()
	service := NetOperatorSNAT{Client: kubeClient, Reader: kubeClient, Networks: fixedSourceNetwork{key: client.ObjectKey{Namespace: "vmware-system-netop", Name: "flb-backend"}}}
	decision := inspection.LBSNATDecision{Selection: inspection.AllocateFromBackendNetwork, AllocationSource: "dvportgroup-1"}

	allocation, ready, err := service.Ensure(ctx, resource, decision)
	require.NoError(t, err)
	require.False(t, ready)
	require.Empty(t, allocation.IPAddress)
	var allocations netop.IPAddressAllocationList
	require.NoError(t, kubeClient.List(ctx, &allocations))
	require.Len(t, allocations.Items, 1)
	stored := &allocations.Items[0]
	require.Equal(t, "flb-backend", stored.Spec.PoolRef.Name)
	require.Equal(t, string(resource.UID), stored.Labels[sourceSNATOwnerLabel])
	stored.Status.IPAddress = "192.0.2.10"
	stored.Status.Conditions = []netop.IPAddressAllocationCondition{{Type: netop.IPAddressAllocationReady, Status: corev1.ConditionTrue}}
	require.NoError(t, kubeClient.Status().Update(ctx, stored))

	allocation, ready, err = service.Ensure(ctx, resource, decision)
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, "192.0.2.10", allocation.IPAddress)
	require.Equal(t, stored.Name, allocation.AllocationRef.Name)
	resource.Status.LBSNATIPAllocations = []api.IPAllocationStatus{allocation}
	allocation, ready, err = service.Ensure(ctx, resource, inspection.LBSNATDecision{Selection: inspection.ReuseAcceptedLBSNATIP})
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, "192.0.2.10", allocation.IPAddress)
	require.NoError(t, kubeClient.List(ctx, &allocations))
	require.Len(t, allocations.Items, 1)
}

func TestNetOperatorSNATTriesNextVIPWhenFirstIsUnavailable(t *testing.T) {
	ctx := context.Background()
	resource := evaluationTransition()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	require.NoError(t, netop.AddToScheme(scheme))
	network := &netop.Network{ObjectMeta: metav1.ObjectMeta{Namespace: "vmware-system-netop", Name: "flb-frontend", UID: "frontend-uid"}, Spec: netop.NetworkSpec{Type: netop.NetworkTypeVDS}}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(resource, &netop.IPAddressAllocation{}).WithObjects(resource, network).Build()
	service := NetOperatorSNAT{Client: kubeClient, Reader: kubeClient, Networks: fixedSourceNetwork{key: client.ObjectKey{Namespace: "vmware-system-netop", Name: "flb-frontend"}}}
	decision := inspection.LBSNATDecision{Selection: inspection.AllocateFromVIPRange, AllocationSource: "dvportgroup-2", VIPRanges: []inspection.VIPRange{{Address: "192.0.2.100", Count: 2}}}

	_, ready, err := service.Ensure(ctx, resource, decision)
	require.NoError(t, err)
	require.False(t, ready)
	var allocations netop.IPAddressAllocationList
	require.NoError(t, kubeClient.List(ctx, &allocations))
	require.Len(t, allocations.Items, 1)
	first := &allocations.Items[0]
	require.Equal(t, "192.0.2.100", first.Spec.RequestedIP)
	first.Status.Conditions = []netop.IPAddressAllocationCondition{{Type: netop.IPAddressAllocationFail, Status: corev1.ConditionTrue, Reason: netop.IPAddressAllocationConditionFailureReasonCannotAllocIP}}
	require.NoError(t, kubeClient.Status().Update(ctx, first))

	_, ready, err = service.Ensure(ctx, resource, decision)
	require.NoError(t, err)
	require.False(t, ready)
	require.NoError(t, kubeClient.List(ctx, &allocations))
	require.Len(t, allocations.Items, 2)
}
