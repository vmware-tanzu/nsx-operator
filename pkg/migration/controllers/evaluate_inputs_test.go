/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/transition"
)

func TestEvaluatePersistsInputDiagnostics(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	obj := &api.ControlPlaneNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, Generation: 1},
		Spec: api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionEvaluate},
			NetworkConfiguration: &api.ControlPlaneNetworkConfiguration{ControlPlaneAndServiceDHCPNetworks: []api.DHCPNetworkConfiguration{{VDNName: "cp-vdn"}}}},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(obj).WithObjects(obj).Build()
	engine := &transition.Engine{Enabled: true, Handlers: map[transition.Key]transition.Handler{
		{Stage: transition.Infrastructure, Action: api.ActionEvaluate}: transition.EvaluateInputs{},
	}}
	r := &Reconciler{Client: c, Engine: engine, Stage: transition.Infrastructure}
	key := types.NamespacedName{Name: obj.Name}
	for range 2 {
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		require.NoError(t, err)
	}
	require.NoError(t, c.Get(ctx, key, obj))
	require.Equal(t, "NeedsInput", obj.Status.Phase)
	require.Equal(t, int64(1), obj.Status.ObservedGeneration)
	require.Len(t, obj.Status.RequiredInputs, 1)
	require.Contains(t, obj.Status.RequiredInputs[0].FieldName, "cp-vdn")
	require.Contains(t, obj.Status.RequiredInputs[0].Description, "gatewayAddress")
}
