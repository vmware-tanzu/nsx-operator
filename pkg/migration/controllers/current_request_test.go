/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	vpc "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/transition"
)

func runningController(t *testing.T, action api.Action) (*Reconciler, ctrl.Request, *api.ControlPlaneNetworkTransition) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, vpc.AddToScheme(scheme))
	obj := &api.ControlPlaneNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, UID: "infra", Generation: 3}, Spec: api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: action}}, Status: &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{LastAction: action, ActionState: api.ActionStateRunning, ActionGeneration: 3}}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(obj, &vpc.SubnetPort{}).WithObjects(obj).Build()
	r := &Reconciler{Client: c, APIReader: c, Engine: &transition.Engine{Enabled: true, Handlers: map[transition.Key]transition.Handler{}}, Stage: transition.Infrastructure}
	return r, ctrl.Request{NamespacedName: types.NamespacedName{Name: obj.Name}}, obj
}

func TestCurrentRequestRejectsChangedIntent(t *testing.T) {
	ctx := context.Background()
	r, _, obj := runningController(t, api.ActionExecute)
	req := transition.Request{Object: obj, Action: api.ActionExecute, Generation: obj.Generation}
	require.NoError(t, r.checkCurrentRequest(ctx, req))
	live := obj.DeepCopy()
	live.Generation++
	require.NoError(t, r.Update(ctx, live))
	require.ErrorContains(t, r.checkCurrentRequest(ctx, req), "transition changed")
}

func TestDeletingTransitionIsNotDispatched(t *testing.T) {
	ctx := context.Background()
	r, key, obj := runningController(t, api.ActionExecute)
	r.Engine.Handlers[transition.Key{Stage: r.Stage, Action: api.ActionExecute}] = actionHandlerFunc(func(context.Context, transition.Request) (transition.Result, error) {
		t.Fatal("deleted transition dispatched")
		return transition.Result{}, nil
	})
	obj.Finalizers = []string{"example.test/keep-for-test"}
	require.NoError(t, r.Update(ctx, obj))
	require.NoError(t, r.Delete(ctx, obj))
	_, err := r.Reconcile(ctx, key)
	require.NoError(t, err)
	current := &api.ControlPlaneNetworkTransition{}
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(obj), current))
	require.Equal(t, obj.Status, current.Status)
}
