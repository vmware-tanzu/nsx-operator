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

func TestEvaluateReportsBothCRDsAndRejectsStaleSnapshots(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	controlPlaneTransition := &api.ControlPlaneNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "supervisor-vds-to-vpc", UID: "cp", Generation: 1}, Spec: api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionEvaluate}}}
	namespaceTransition := &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "batch", UID: "ns", Generation: 1}, Spec: api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionEvaluate}, Namespaces: []string{"tenant"}}}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(controlPlaneTransition, namespaceTransition).WithObjects(controlPlaneTransition, namespaceTransition).Build()
	inventory := &inventoryStub{sources: map[string]Source{"a": sourceForVDN("a")}}
	inspectionService := Service{Inventory: inventory}
	require.NoError(t, kubeClient.Get(ctx, client.ObjectKeyFromObject(controlPlaneTransition), controlPlaneTransition))
	_, err := inspectionService.EvaluateControlPlane(ctx, kubeClient, controlPlaneTransition, []string{"a"})
	require.NoError(t, err)
	require.Nil(t, controlPlaneTransition.Status)
	freshControlPlaneTransition := &api.ControlPlaneNetworkTransition{}
	require.NoError(t, kubeClient.Get(ctx, client.ObjectKeyFromObject(controlPlaneTransition), freshControlPlaneTransition))
	require.Equal(t, api.PhaseNeedsInput, freshControlPlaneTransition.Status.Phase)
	require.Contains(t, freshControlPlaneTransition.Status.RequiredInputs[0].Key, "controlPlaneAndServiceDHCPNetworks/")
	require.Contains(t, freshControlPlaneTransition.Status.RequiredInputs[0].Description, "spec.networkConfiguration.controlPlaneAndServiceDHCPNetworks")
	_, err = inspectionService.EvaluateControlPlane(ctx, kubeClient, controlPlaneTransition, []string{"a"})
	require.Error(t, err)
	freshControlPlaneTransition.Spec.NetworkConfiguration = &api.ControlPlaneNetworkConfiguration{ControlPlaneAndServiceDHCPNetworks: []api.DHCPNetworkConfiguration{validDHCPInput("a")}}
	freshControlPlaneTransition.Generation = 2
	require.NoError(t, kubeClient.Update(ctx, freshControlPlaneTransition))
	inspectionResult, err := inspectionService.EvaluateControlPlane(ctx, kubeClient, freshControlPlaneTransition, []string{"a"})
	require.NoError(t, err)
	require.True(t, inspectionResult.Ready())
	require.Equal(t, "192.168.10.0/24", inspectionResult.Networks[0].Addressing.CIDR.String())
	require.NoError(t, kubeClient.Get(ctx, client.ObjectKeyFromObject(controlPlaneTransition), freshControlPlaneTransition))
	require.Empty(t, freshControlPlaneTransition.Status.RequiredInputs)
	require.NoError(t, kubeClient.Get(ctx, client.ObjectKeyFromObject(namespaceTransition), namespaceTransition))
	_, err = inspectionService.EvaluateNamespaces(ctx, kubeClient, namespaceTransition)
	require.NoError(t, err)
	require.NoError(t, kubeClient.Get(ctx, client.ObjectKeyFromObject(namespaceTransition), namespaceTransition))
	require.Equal(t, api.PhaseNeedsInput, namespaceTransition.Status.Phase)
	require.Contains(t, namespaceTransition.Status.RequiredInputs[0].Key, "namespaceDHCPNetworks/")
	require.Contains(t, namespaceTransition.Status.RequiredInputs[0].Description, "spec.networkConfiguration.namespaceDHCPNetworks")
	require.Empty(t, namespaceTransition.Status.LastAction)
	namespaceTransition.Spec.NetworkConfiguration = &api.NamespaceNetworkConfiguration{NamespaceDHCPNetworks: []api.DHCPNetworkConfiguration{validDHCPInput("a")}}
	namespaceTransition.Generation = 2
	require.NoError(t, kubeClient.Update(ctx, namespaceTransition))
	_, err = inspectionService.EvaluateNamespaces(ctx, kubeClient, namespaceTransition)
	require.NoError(t, err)
	require.NoError(t, kubeClient.Get(ctx, client.ObjectKeyFromObject(namespaceTransition), namespaceTransition))
	require.Equal(t, api.PhaseEvaluating, namespaceTransition.Status.Phase)
	require.Empty(t, namespaceTransition.Status.RequiredInputs)
	// Correcting a syntax-valid but incompatible value must trigger a fresh
	// inspection and clear its failure without claiming action completion.
	namespaceTransition.Spec.NetworkConfiguration.NamespaceDHCPNetworks[0].GatewayAddress = "192.168.11.1"
	namespaceTransition.Generation = 3
	require.NoError(t, kubeClient.Update(ctx, namespaceTransition))
	inspectionResult, err = inspectionService.EvaluateNamespaces(ctx, kubeClient, namespaceTransition)
	require.NoError(t, err)
	require.False(t, inspectionResult.Ready())
	require.NoError(t, kubeClient.Get(ctx, client.ObjectKeyFromObject(namespaceTransition), namespaceTransition))
	require.Equal(t, api.PhaseEvaluationFailed, namespaceTransition.Status.Phase)
	namespaceTransition.Spec.NetworkConfiguration.NamespaceDHCPNetworks[0].GatewayAddress = "192.168.10.1"
	namespaceTransition.Generation = 4
	require.NoError(t, kubeClient.Update(ctx, namespaceTransition))
	inspectionResult, err = inspectionService.EvaluateNamespaces(ctx, kubeClient, namespaceTransition)
	require.NoError(t, err)
	require.True(t, inspectionResult.Ready())
	require.NoError(t, kubeClient.Get(ctx, client.ObjectKeyFromObject(namespaceTransition), namespaceTransition))
	require.Equal(t, api.PhaseEvaluating, namespaceTransition.Status.Phase)
	require.Equal(t, int64(4), namespaceTransition.Status.ObservedGeneration)
	require.Empty(t, namespaceTransition.Status.LastAction)
	inventory.err = errors.New("transport unavailable")
	_, err = inspectionService.EvaluateNamespaces(ctx, kubeClient, namespaceTransition)
	require.Error(t, err)
	require.NoError(t, kubeClient.Get(ctx, client.ObjectKeyFromObject(namespaceTransition), namespaceTransition))
	require.Equal(t, api.PhaseBlocked, namespaceTransition.Status.Phase)
	require.Equal(t, metav1.ConditionUnknown, namespaceTransition.Status.Conditions[0].Status)
}
