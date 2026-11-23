/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package inspection

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

type flbNetworkReaderStub struct {
	networks FLBNetworks
	err      error
	calls    int
}

func (reader *flbNetworkReaderStub) LoadBalancerNetworks(context.Context) (FLBNetworks, error) {
	reader.calls++
	return reader.networks, reader.err
}

func TestEvaluateControlPlaneSelectsVIPFallbackAndReportsWarning(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	transition := &api.ControlPlaneNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, UID: "cp", Generation: 1},
		Spec:       api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionEvaluate}, NetworkConfiguration: &api.ControlPlaneNetworkConfiguration{ControlPlaneAndServiceDHCPNetworks: []api.DHCPNetworkConfiguration{validDHCPInput("a")}}},
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(transition).WithObjects(transition).Build()
	flbReader := &flbNetworkReaderStub{networks: FLBNetworks{
		VCenterUUID: "vc-uuid", FrontendNetwork: "frontend", BackendNetwork: "backend",
		VIPRanges: []VIPRange{{Address: "172.16.0.200", Count: 54}},
	}}
	service := Service{
		Inventory:           &inventoryStub{sources: map[string]DiscoveredSource{"a": sourceForVDN("a")}},
		TargetInventory:     targetInventoryStub{},
		FLBNetworkInventory: flbReader,
		StatusClient:        kubeClient,
	}
	result, err := service.EvaluateControlPlane(ctx, transition, []string{"a"})
	require.NoError(t, err)
	require.Equal(t, AllocateFromVIPRange, result.LBSNAT.Selection)
	require.Equal(t, 1, flbReader.calls)
	require.NotEmpty(t, result.Warnings)
	require.Contains(t, result.OptionalInputs, api.OptionalInput{
		FieldName:   "spec.networkConfiguration.lbSNATIPs",
		Description: "Optionally supply an IPv4 LB SNAT IP; if omitted, the migrator allocates one from the FLB VIP range. Verify routing from the FLB backend to that address.",
	})
	require.NoError(t, kubeClient.Get(ctx, client.ObjectKeyFromObject(transition), transition))
	require.Contains(t, transition.Status.Conditions[0].Message, "Verify routing")
	require.Equal(t, "NetworkInspectionCompleteWithWarnings", transition.Status.Conditions[0].Reason)
	require.Contains(t, transition.Status.OptionalInputs, api.OptionalInput{FieldName: "spec.networkConfiguration.lbSNATIPs", Description: result.OptionalInputs[len(result.OptionalInputs)-1].Description})

	transition.Spec.NetworkConfiguration.LBSNATIPs = []string{"192.0.2.25"}
	transition.Generation = 2
	require.NoError(t, kubeClient.Update(ctx, transition))
	result, err = service.EvaluateControlPlane(ctx, transition, []string{"a"})
	require.NoError(t, err)
	require.Equal(t, UseConfiguredLBSNATIP, result.LBSNAT.Selection)
	for _, optionalInput := range result.OptionalInputs {
		require.NotEqual(t, "spec.networkConfiguration.lbSNATIPs", optionalInput.FieldName)
	}
	require.NoError(t, kubeClient.Get(ctx, client.ObjectKeyFromObject(transition), transition))
	for _, optionalInput := range transition.Status.OptionalInputs {
		require.NotEqual(t, "spec.networkConfiguration.lbSNATIPs", optionalInput.FieldName)
	}
}

func TestEvaluateControlPlaneReusesAcceptedLBSNATIP(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	transition := &api.ControlPlaneNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, UID: "cp", Generation: 1},
		Spec:       api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionEvaluate}},
		Status: &api.ControlPlaneNetworkTransitionStatus{LBSNATIPAllocations: []api.IPAllocationStatus{{
			IPAddress: "192.0.2.25",
		}}},
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(transition).WithObjects(transition).Build()
	flbReader := &flbNetworkReaderStub{err: errors.New("must not rediscover or allocate SNAT")}
	service := Service{
		Inventory:           &inventoryStub{sources: map[string]DiscoveredSource{"a": sourceForVDN("a")}},
		TargetInventory:     targetInventoryStub{},
		FLBNetworkInventory: flbReader,
		StatusClient:        kubeClient,
	}

	result, err := service.EvaluateControlPlane(ctx, transition, []string{"a"})
	require.NoError(t, err)
	require.NotNil(t, result.LBSNAT)
	require.Equal(t, ReuseAcceptedLBSNATIP, result.LBSNAT.Selection)
	require.Equal(t, "192.0.2.25", result.LBSNAT.IPAddress)
	require.Equal(t, 0, flbReader.calls)
}

func TestEvaluateReportsBothCRDsAndRejectsStaleSnapshots(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	controlPlaneTransition := &api.ControlPlaneNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "supervisor-vds-to-vpc", UID: "cp", Generation: 1}, Spec: api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionEvaluate}}}
	namespaceTransition := &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "batch", UID: "ns", Generation: 1}, Spec: api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionEvaluate}, Namespaces: []string{"tenant"}}}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(controlPlaneTransition, namespaceTransition).WithObjects(controlPlaneTransition, namespaceTransition).Build()
	inventory := &inventoryStub{sources: map[string]DiscoveredSource{"a": sourceForVDN("a")}}
	flbReader := &flbNetworkReaderStub{networks: FLBNetworks{
		VCenterUUID: "vc-uuid", FrontendNetwork: "frontend", BackendNetwork: "backend",
		VIPRanges: []VIPRange{{Address: "172.16.0.200", Count: 54}},
	}}
	inspectionService := Service{Inventory: inventory, TargetInventory: targetInventoryStub{}, FLBNetworkInventory: flbReader, StatusClient: kubeClient}
	require.NoError(t, kubeClient.Get(ctx, client.ObjectKeyFromObject(controlPlaneTransition), controlPlaneTransition))
	_, err := inspectionService.EvaluateControlPlane(ctx, controlPlaneTransition, []string{"a"})
	require.NoError(t, err)
	require.Nil(t, controlPlaneTransition.Status)
	freshControlPlaneTransition := &api.ControlPlaneNetworkTransition{}
	require.NoError(t, kubeClient.Get(ctx, client.ObjectKeyFromObject(controlPlaneTransition), freshControlPlaneTransition))
	require.Equal(t, api.PhaseNeedsInput, freshControlPlaneTransition.Status.Phase)
	require.Len(t, freshControlPlaneTransition.Status.OptionalInputs, 5)
	require.Equal(t, "spec.networkConfiguration.controlPlaneAndServiceDHCPNetworks[vdnName=a]", freshControlPlaneTransition.Status.RequiredInputs[0].FieldName)
	require.Contains(t, freshControlPlaneTransition.Status.RequiredInputs[0].Description, "spec.networkConfiguration.controlPlaneAndServiceDHCPNetworks")
	_, err = inspectionService.EvaluateControlPlane(ctx, controlPlaneTransition, []string{"a"})
	require.Error(t, err)
	freshControlPlaneTransition.Spec.NetworkConfiguration = &api.ControlPlaneNetworkConfiguration{ControlPlaneAndServiceDHCPNetworks: []api.DHCPNetworkConfiguration{validDHCPInput("a")}}
	freshControlPlaneTransition.Generation = 2
	require.NoError(t, kubeClient.Update(ctx, freshControlPlaneTransition))
	inspectionResult, err := inspectionService.EvaluateControlPlane(ctx, freshControlPlaneTransition, []string{"a"})
	require.NoError(t, err)
	require.True(t, inspectionResult.Ready())
	require.Equal(t, "192.168.10.0/24", inspectionResult.Networks[0].Addressing.CIDR.String())
	require.NoError(t, kubeClient.Get(ctx, client.ObjectKeyFromObject(controlPlaneTransition), freshControlPlaneTransition))
	require.Empty(t, freshControlPlaneTransition.Status.RequiredInputs)
	require.Len(t, freshControlPlaneTransition.Status.OptionalInputs, 5)
	require.Contains(t, freshControlPlaneTransition.Status.OptionalInputs[0].FieldName, "resourceNaming.networks[vdnName=a]")
	snatDiscoveryCalls := flbReader.calls
	require.NoError(t, kubeClient.Get(ctx, client.ObjectKeyFromObject(namespaceTransition), namespaceTransition))
	namespaceResult, err := inspectionService.EvaluateNamespaces(ctx, namespaceTransition)
	require.NoError(t, err)
	require.Nil(t, namespaceResult.LBSNAT)
	require.Equal(t, snatDiscoveryCalls, flbReader.calls)
	require.NoError(t, kubeClient.Get(ctx, client.ObjectKeyFromObject(namespaceTransition), namespaceTransition))
	require.Equal(t, api.PhaseNeedsInput, namespaceTransition.Status.Phase)
	require.Equal(t, "spec.networkConfiguration.namespaceDHCPNetworks[vdnName=a]", namespaceTransition.Status.RequiredInputs[0].FieldName)
	require.Contains(t, namespaceTransition.Status.RequiredInputs[0].Description, "spec.networkConfiguration.namespaceDHCPNetworks")
	require.Empty(t, namespaceTransition.Status.LastAction)
	namespaceTransition.Spec.NetworkConfiguration = &api.NamespaceNetworkConfiguration{NamespaceDHCPNetworks: []api.DHCPNetworkConfiguration{validDHCPInput("a")}}
	namespaceTransition.Generation = 2
	require.NoError(t, kubeClient.Update(ctx, namespaceTransition))
	namespaceResult, err = inspectionService.EvaluateNamespaces(ctx, namespaceTransition)
	require.NoError(t, err)
	require.Nil(t, namespaceResult.LBSNAT)
	require.Equal(t, snatDiscoveryCalls, flbReader.calls)
	require.NoError(t, kubeClient.Get(ctx, client.ObjectKeyFromObject(namespaceTransition), namespaceTransition))
	require.Equal(t, api.PhaseEvaluating, namespaceTransition.Status.Phase)
	require.Empty(t, namespaceTransition.Status.RequiredInputs)
	require.Len(t, namespaceTransition.Status.OptionalInputs, 4)
	// Correcting a syntax-valid but incompatible value must trigger a fresh
	// inspection and clear its failure without claiming action completion.
	namespaceTransition.Spec.NetworkConfiguration.NamespaceDHCPNetworks[0].GatewayAddress = "192.168.11.1"
	namespaceTransition.Generation = 3
	require.NoError(t, kubeClient.Update(ctx, namespaceTransition))
	inspectionResult, err = inspectionService.EvaluateNamespaces(ctx, namespaceTransition)
	require.NoError(t, err)
	require.False(t, inspectionResult.Ready())
	require.NoError(t, kubeClient.Get(ctx, client.ObjectKeyFromObject(namespaceTransition), namespaceTransition))
	require.Equal(t, api.PhaseEvaluationFailed, namespaceTransition.Status.Phase)
	namespaceTransition.Spec.NetworkConfiguration.NamespaceDHCPNetworks[0].GatewayAddress = "192.168.10.1"
	namespaceTransition.Generation = 4
	require.NoError(t, kubeClient.Update(ctx, namespaceTransition))
	inspectionResult, err = inspectionService.EvaluateNamespaces(ctx, namespaceTransition)
	require.NoError(t, err)
	require.True(t, inspectionResult.Ready())
	require.NoError(t, kubeClient.Get(ctx, client.ObjectKeyFromObject(namespaceTransition), namespaceTransition))
	require.Equal(t, api.PhaseEvaluating, namespaceTransition.Status.Phase)
	require.Equal(t, int64(4), namespaceTransition.Status.ObservedGeneration)
	require.Empty(t, namespaceTransition.Status.LastAction)
	inventory.err = errors.New("transport unavailable")
	_, err = inspectionService.EvaluateNamespaces(ctx, namespaceTransition)
	require.Error(t, err)
	require.NoError(t, kubeClient.Get(ctx, client.ObjectKeyFromObject(namespaceTransition), namespaceTransition))
	require.Equal(t, api.PhaseBlocked, namespaceTransition.Status.Phase)
	require.Equal(t, metav1.ConditionUnknown, namespaceTransition.Status.Conditions[0].Status)
	require.Empty(t, namespaceTransition.Status.OptionalInputs)
	require.Empty(t, namespaceTransition.Status.RequiredInputs)
}
