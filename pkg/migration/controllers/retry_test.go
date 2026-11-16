/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package controllers

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/transition"
)

func TestPersistentFailurePreservesCheckpointAndBackoff(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	obj := &api.ControlPlaneNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, Generation: 1},
		Spec:       api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionEvaluate}},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(obj).WithObjects(obj).Build()
	failure := errors.New("NSX unavailable")
	calls := 0
	recover := false
	e := &transition.Engine{Enabled: true, Handlers: map[transition.Key]transition.Handler{
		{Stage: transition.Infrastructure, Action: api.ActionEvaluate}: actionHandlerFunc(func(_ context.Context, req transition.Request) (transition.Result, error) {
			calls++
			require.Equal(t, api.ActionStateRunning, req.Status.ActionState)
			if recover {
				return transition.Result{Outcome: transition.OutcomeSucceeded}, nil
			}
			return transition.Result{}, failure
		}),
	}}
	r := &Reconciler{Client: c, Engine: e, Stage: transition.Infrastructure}
	key := types.NamespacedName{Name: obj.Name}
	request := ctrl.Request{NamespacedName: key}
	result, err := r.Reconcile(ctx, request)
	require.NoError(t, err)
	require.Positive(t, result.RequeueAfter, "status events are filtered, so checkpoint execution must be explicitly queued")
	require.Zero(t, calls)
	_, err = r.Reconcile(ctx, request)
	require.ErrorIs(t, err, failure)
	require.NoError(t, c.Get(ctx, key, obj))
	failed := obj.DeepCopy()
	for i := 0; i < 3; i++ {
		// A fresh controller resumes the failed checkpoint without publishing Running.
		r = &Reconciler{Client: c, Engine: e, Stage: transition.Infrastructure}
		result, err = r.Reconcile(ctx, request)
		require.ErrorIs(t, err, failure, "every failed retry must retain queue error backoff")
		require.Zero(t, result)
		require.NoError(t, c.Get(ctx, key, obj))
		require.Equal(t, failed.ResourceVersion, obj.ResourceVersion, "identical errors must not cause new status events")
		require.Equal(t, failed.Status, obj.Status)
	}
	require.Equal(t, 4, calls)
	recover = true
	_, err = r.Reconcile(ctx, request)
	require.NoError(t, err)
	require.NoError(t, c.Get(ctx, key, obj))
	require.Equal(t, api.ActionStateSucceeded, obj.Status.ActionState)
	require.Equal(t, "Evaluated", obj.Status.Phase)
}

func TestTransitionEvents(t *testing.T) {
	old := &api.ControlPlaneNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, Generation: 1, ResourceVersion: "1"},
	}
	for _, tc := range []struct {
		name    string
		change  func(*api.ControlPlaneNetworkTransition)
		enqueue bool
	}{
		{"resync", func(*api.ControlPlaneNetworkTransition) {}, false},
		{"status only", func(o *api.ControlPlaneNetworkTransition) {
			o.Status = &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{ActionState: api.ActionStateFailed}}
		}, false},
		{"spec generation", func(o *api.ControlPlaneNetworkTransition) { o.Generation++ }, true},
		{"annotations", func(o *api.ControlPlaneNetworkTransition) { o.Annotations = map[string]string{"approval": "true"} }, true},
		{"labels", func(o *api.ControlPlaneNetworkTransition) { o.Labels = map[string]string{"owner": "migration"} }, true},
		{"deletion", func(o *api.ControlPlaneNetworkTransition) { now := metav1.Now(); o.DeletionTimestamp = &now }, true},
		{"finalizers", func(o *api.ControlPlaneNetworkTransition) {
			o.Finalizers = []string{"migration.network.vmware.com/cleanup"}
		}, true},
		{"replacement", func(o *api.ControlPlaneNetworkTransition) { o.UID = "replacement" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			updated := old.DeepCopy()
			updated.ResourceVersion = "2"
			tc.change(updated)
			require.Equal(t, tc.enqueue, transitionEvents().Update(event.UpdateEvent{ObjectOld: old, ObjectNew: updated}))
		})
	}
	require.True(t, transitionEvents().Create(event.CreateEvent{Object: old}), "startup must recover persisted checkpoints")
	require.True(t, transitionEvents().Delete(event.DeleteEvent{Object: old}))
}
