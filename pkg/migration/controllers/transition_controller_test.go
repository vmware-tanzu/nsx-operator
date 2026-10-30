/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package controllers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/transition"
)

func TestReconcileStatus(t *testing.T) {
	for _, stage := range []transition.Stage{transition.Infrastructure, transition.Namespaces} {
		t.Run(string(stage), func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, api.AddToScheme(scheme))
			infrastructure := &api.ControlPlaneNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, Generation: 1},
				Spec: api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionEvaluate}}}
			namespaces := &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "batch", Generation: 1},
				Spec: api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionEvaluate}, Namespaces: []string{"tenant"}}}
			c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(infrastructure, namespaces).WithObjects(infrastructure, namespaces).Build()
			r := Reconciler{Client: c, Engine: &transition.Engine{}, Stage: stage}
			name := api.ControlPlaneNetworkTransitionName
			var obj client.Object = &api.ControlPlaneNetworkTransition{}
			if stage == transition.Namespaces {
				name = "batch"
				obj = &api.NamespaceNetworkTransition{}
			}
			request := ctrl.Request{NamespacedName: types.NamespacedName{Name: name}}
			_, err := r.Reconcile(context.Background(), request)
			require.NoError(t, err)
			require.NoError(t, c.Get(context.Background(), request.NamespacedName, obj))
			var status *api.TransitionStatus
			switch resource := obj.(type) {
			case *api.ControlPlaneNetworkTransition:
				status = &resource.Status.TransitionStatus
				require.Equal(t, infrastructure.Spec, resource.Spec)
			case *api.NamespaceNetworkTransition:
				status = &resource.Status.TransitionStatus
				require.Equal(t, namespaces.Spec, resource.Spec)
			}
			require.NotNil(t, status)
			require.Equal(t, "FeatureDisabled", meta.FindStatusCondition(status.Conditions, "Ready").Reason)
			version := obj.GetResourceVersion()
			_, err = r.Reconcile(context.Background(), request)
			require.NoError(t, err)
			require.NoError(t, c.Get(context.Background(), request.NamespacedName, obj))
			require.Equal(t, version, obj.GetResourceVersion(), "unchanged status must not produce a write loop")
			_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "deleted"}})
			require.NoError(t, err)
		})
	}
}

func TestNamespaceInfrastructureGate(t *testing.T) {
	for _, state := range []api.ActionState{"", api.ActionStateRunning, api.ActionStateFailed, api.ActionStateSucceeded} {
		ready := state == api.ActionStateSucceeded
		name := string(state)
		if name == "" {
			name = "missing status"
		}
		t.Run(name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, api.AddToScheme(scheme))
			infrastructure := &api.ControlPlaneNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, Generation: 3}}
			if state != "" {
				infrastructure.Status = &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{Phase: "InfraTransitionComplete", LastAction: api.ActionExecute, ActionState: state, ActionGeneration: 3,
					Conditions: []metav1.Condition{{Type: "InfraTransitionComplete", Status: metav1.ConditionTrue, Reason: "ActionCompleted", LastTransitionTime: metav1.Now()}}}}
			}
			batch := &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "batch", Generation: 1}, Spec: api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionEvaluate}}}
			c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(batch).WithObjects(infrastructure, batch).Build()
			r := Reconciler{Client: c, Engine: &transition.Engine{Enabled: true}, Stage: transition.Namespaces}
			result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "batch"}})
			require.NoError(t, err)
			require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "batch"}, batch))
			reason := "InfrastructureNotReady"
			if ready {
				reason = "ActionNotImplemented"
			}
			require.Equal(t, reason, meta.FindStatusCondition(batch.Status.Conditions, "Ready").Reason)
			require.Equal(t, !ready, result.RequeueAfter > 0)
		})
	}
}

func TestCompletedNamespaceBatchPreserved(t *testing.T) {
	for _, tc := range []struct {
		name       string
		action     api.Action
		phase      string
		enabled    bool
		generation int64
		blocked    bool
	}{
		{"completed after finalization", api.ActionExecute, "Completed", true, 4, false},
		{"completed with feature disabled", api.ActionExecute, "Completed", false, 4, false},
		{"committed after finalization", api.ActionCommit, "Committed", true, 4, false},
		{"committed with feature disabled", api.ActionCommit, "Committed", false, 4, false},
		{"rolled back after finalization", api.ActionRollback, "RolledBack", true, 4, false},
		{"new generation still requires infrastructure", api.ActionExecute, "Completed", true, 5, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, api.AddToScheme(scheme))
			condition := metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "ActionCompleted", LastTransitionTime: metav1.Now()}
			infra := &api.ControlPlaneNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, Generation: 6},
				Status: &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{Phase: "Finalized", LastAction: api.ActionFinalize, ActionState: api.ActionStateSucceeded, ActionGeneration: 6, Conditions: []metav1.Condition{condition}}}}
			batch := &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "batch", Generation: tc.generation},
				Spec: api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: tc.action}},
				Status: &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{
					Phase: tc.phase, LastAction: tc.action, ActionState: api.ActionStateSucceeded, ActionGeneration: 4, Conditions: []metav1.Condition{condition}},
					NamespaceStatuses: []api.NamespaceStatus{{Name: "tenant", Phase: tc.phase}}}}
			if tc.action == api.ActionCommit {
				batch.Status.IrreversibleAction = string(api.ActionCommit)
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(batch).WithObjects(infra, batch).Build()
			key := types.NamespacedName{Name: "batch"}
			require.NoError(t, c.Get(context.Background(), key, batch))
			previous := batch.DeepCopy()
			r := Reconciler{Client: c, Engine: &transition.Engine{Enabled: tc.enabled}, Stage: transition.Namespaces}
			result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
			require.NoError(t, err)
			require.NoError(t, c.Get(context.Background(), key, batch))
			if tc.blocked {
				require.Positive(t, result.RequeueAfter)
				require.Equal(t, "InfrastructureNotReady", meta.FindStatusCondition(batch.Status.Conditions, "Ready").Reason)
			} else {
				require.Zero(t, result.RequeueAfter)
				require.Equal(t, previous.Status, batch.Status)
				require.Equal(t, previous.ResourceVersion, batch.ResourceVersion)
			}
		})
	}
}

type actionHandlerFunc func(context.Context, transition.Request) (transition.Result, error)

func (f actionHandlerFunc) Reconcile(ctx context.Context, req transition.Request) (transition.Result, error) {
	return f(ctx, req)
}

func TestHandlerWaitsForPersistedCheckpoint(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	resource := &api.ControlPlaneNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, Generation: 3},
		Spec:   api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionDerive}},
		Status: &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{LastAction: api.ActionPlan, ActionState: api.ActionStateSucceeded, ActionGeneration: 2}}}
	checkpointFailure := errors.New("status write failed")
	rejectCheckpoint := true
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(resource).WithObjects(resource).
		WithInterceptorFuncs(interceptor.Funcs{SubResourceUpdate: func(ctx context.Context, c client.Client, subresource string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if rejectCheckpoint {
				return checkpointFailure
			}
			return c.SubResource(subresource).Update(ctx, obj, opts...)
		}}).Build()
	calls := 0
	key := types.NamespacedName{Name: resource.Name}
	engine := &transition.Engine{Enabled: true, Handlers: map[transition.Key]transition.Handler{
		{Stage: transition.Infrastructure, Action: api.ActionDerive}: actionHandlerFunc(func(ctx context.Context, req transition.Request) (transition.Result, error) {
			calls++
			var persisted api.ControlPlaneNetworkTransition
			require.NoError(t, c.Get(ctx, key, &persisted))
			require.Equal(t, api.ActionDerive, persisted.Status.LastAction)
			require.Equal(t, api.ActionStateRunning, persisted.Status.ActionState)
			require.Equal(t, api.ActionDerive, req.Status.LastAction)
			require.Equal(t, api.ActionStateRunning, req.Status.ActionState)
			return transition.Result{Outcome: transition.OutcomeSucceeded}, nil
		}),
	}}
	reconcile := func() (ctrl.Result, error) {
		// Each call constructs a new controller to exercise restart recovery.
		r := Reconciler{Client: c, Engine: engine, Stage: transition.Infrastructure}
		return r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
	}
	_, err := reconcile()
	require.ErrorIs(t, err, checkpointFailure)
	require.Zero(t, calls)
	require.NoError(t, c.Get(ctx, key, resource))
	require.Equal(t, api.ActionPlan, resource.Status.LastAction)
	require.Equal(t, api.ActionStateSucceeded, resource.Status.ActionState)

	rejectCheckpoint = false
	result, err := reconcile()
	require.NoError(t, err)
	require.Positive(t, result.RequeueAfter)
	require.Zero(t, calls)
	require.NoError(t, c.Get(ctx, key, resource))
	require.Equal(t, api.ActionDerive, resource.Status.LastAction)

	_, err = reconcile()
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.NoError(t, c.Get(ctx, key, resource))
	require.Equal(t, api.ActionStateSucceeded, resource.Status.ActionState)
	require.Equal(t, api.ActionDerive, resource.Status.LastAction)
}

func TestInfrastructureGateUsesSingleton(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	other := &api.ControlPlaneNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "another-transition", Generation: 1},
		Status: &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{
			LastAction: api.ActionExecute, ActionState: api.ActionStateSucceeded, ActionGeneration: 1, Phase: "InfraTransitionComplete",
			Conditions: []metav1.Condition{{Type: "InfraTransitionComplete", Status: metav1.ConditionTrue, Reason: "ActionCompleted", LastTransitionTime: metav1.Now()}},
		}}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(other).Build()
	r := Reconciler{Client: c}
	ready, err := r.infrastructureReady(context.Background())
	require.NoError(t, err)
	require.False(t, ready, "another successful transition must not satisfy the singleton dependency")
}

func TestNamespaceCommitCheckpointAndInfrastructureRecovery(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	batch := &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "batch", Generation: 5},
		Spec: api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionCommit}},
		Status: &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{
			LastAction: api.ActionExecute, ActionState: api.ActionStateSucceeded, ActionGeneration: 4, Phase: "Completed"}}}
	checkpointFailure := errors.New("status write failed")
	rejectCheckpoint := false
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(batch).WithObjects(batch).
		WithInterceptorFuncs(interceptor.Funcs{SubResourceUpdate: func(ctx context.Context, c client.Client, subresource string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if rejectCheckpoint {
				return checkpointFailure
			}
			return c.SubResource(subresource).Update(ctx, obj, opts...)
		}}).Build()
	key := types.NamespacedName{Name: batch.Name}
	calls := 0
	engine := &transition.Engine{Enabled: true, Handlers: map[transition.Key]transition.Handler{
		{Stage: transition.Namespaces, Action: api.ActionCommit}: actionHandlerFunc(func(ctx context.Context, req transition.Request) (transition.Result, error) {
			if req.Status.IrreversibleAction == "" {
				return transition.Result{Outcome: transition.OutcomeCheckpointIrreversible}, nil
			}
			calls++
			var persisted api.NamespaceNetworkTransition
			require.NoError(t, c.Get(ctx, key, &persisted))
			require.Equal(t, api.ActionCommit, persisted.Status.LastAction)
			require.Equal(t, api.ActionStateRunning, persisted.Status.ActionState)
			require.Equal(t, "Committing", persisted.Status.Phase)
			require.Equal(t, string(api.ActionCommit), persisted.Status.IrreversibleAction)
			return transition.Result{Outcome: transition.OutcomeSucceeded}, nil
		}),
	}}
	reconcile := func() (ctrl.Result, error) {
		r := Reconciler{Client: c, Engine: engine, Stage: transition.Namespaces}
		return r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
	}
	_, err := reconcile()
	require.NoError(t, err)
	require.NoError(t, c.Get(ctx, key, batch))
	require.Equal(t, "InfrastructureNotReady", meta.FindStatusCondition(batch.Status.Conditions, "Ready").Reason)
	require.Equal(t, api.ActionExecute, batch.Status.LastAction)
	require.Zero(t, calls)
	infra := &api.ControlPlaneNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, Generation: 3},
		Status: &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{
			LastAction: api.ActionExecute, ActionState: api.ActionStateSucceeded, ActionGeneration: 3, Phase: "InfraTransitionComplete",
			Conditions: []metav1.Condition{{Type: "InfraTransitionComplete", Status: metav1.ConditionTrue, Reason: "ActionCompleted", LastTransitionTime: metav1.Now()}},
		}}}
	require.NoError(t, c.Create(ctx, infra))
	rejectCheckpoint = true
	_, err = reconcile()
	require.ErrorIs(t, err, checkpointFailure)
	require.Zero(t, calls)
	require.NoError(t, c.Get(ctx, key, batch))
	require.Equal(t, api.ActionExecute, batch.Status.LastAction)
	rejectCheckpoint = false
	result, err := reconcile()
	require.NoError(t, err)
	require.Positive(t, result.RequeueAfter)
	require.Zero(t, calls)
	rejectCheckpoint = true
	_, err = reconcile()
	require.ErrorIs(t, err, checkpointFailure)
	require.Zero(t, calls, "a failed PONR write must prevent irreversible work")
	require.NoError(t, c.Get(ctx, key, batch))
	require.Empty(t, batch.Status.IrreversibleAction)
	rejectCheckpoint = false
	_, err = reconcile()
	require.NoError(t, err)
	require.Zero(t, calls)
	// A later infrastructure change must not strand an irreversible batch commit.
	infra.Generation = 4
	infra.Spec.Action = api.ActionFinalize
	require.NoError(t, c.Update(ctx, infra))
	_, err = reconcile()
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.NoError(t, c.Get(ctx, key, batch))
	require.Equal(t, "Committed", batch.Status.Phase)
	require.True(t, meta.IsStatusConditionTrue(batch.Status.Conditions, "NamespaceMigrationCommitted"))
}

func TestInfrastructureDependencies(t *testing.T) {
	for _, tc := range []struct {
		name                string
		action, batchAction api.Action
		state               api.ActionState
		irreversible        api.Action
		stale, blocked      bool
	}{
		{"finalize during execution", api.ActionFinalize, api.ActionExecute, api.ActionStateRunning, "", false, true},
		{"finalize after cutover", api.ActionFinalize, api.ActionExecute, api.ActionStateSucceeded, "", false, true},
		{"finalize during commit", api.ActionFinalize, api.ActionCommit, api.ActionStateRunning, api.ActionCommit, false, true},
		{"finalize during commit failure", api.ActionFinalize, api.ActionCommit, api.ActionStateFailed, api.ActionCommit, false, true},
		{"finalize stale commit", api.ActionFinalize, api.ActionCommit, api.ActionStateSucceeded, api.ActionCommit, true, true},
		{"finalize after commit", api.ActionFinalize, api.ActionCommit, api.ActionStateSucceeded, api.ActionCommit, false, false},
		{"finalize ignores restored history", api.ActionFinalize, api.ActionRollback, api.ActionStateSucceeded, "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			require.NoError(t, api.AddToScheme(scheme))
			infra := &api.ControlPlaneNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, Generation: 5},
				Spec:   api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: tc.action}},
				Status: &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{LastAction: api.ActionExecute, ActionState: api.ActionStateSucceeded, ActionGeneration: 4}}}
			batch := &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "batch", Generation: 3},
				Spec:   api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: tc.batchAction}},
				Status: &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{LastAction: tc.batchAction, ActionState: tc.state, ActionGeneration: 3, IrreversibleAction: string(tc.irreversible)}}}
			if tc.stale {
				batch.Generation++
			}
			meta.SetStatusCondition(&infra.Status.Conditions, metav1.Condition{
				Type: "InfraTransitionComplete", Status: metav1.ConditionTrue,
				ObservedGeneration: 4, Reason: "CutoverVerified", Message: "Cutover verified",
				LastTransitionTime: metav1.NewTime(time.Unix(1700000000, 0)),
			})
			milestone := *meta.FindStatusCondition(infra.Status.Conditions, "InfraTransitionComplete")
			cl := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(infra, batch).WithObjects(infra, batch).Build()
			calls := 0
			engine := &transition.Engine{Enabled: true, Handlers: map[transition.Key]transition.Handler{{Stage: transition.Infrastructure, Action: tc.action}: actionHandlerFunc(func(context.Context, transition.Request) (transition.Result, error) {
				calls++
				return transition.Result{Outcome: transition.OutcomeInProgress}, nil
			})}}
			r := Reconciler{Client: cl, APIReader: cl, Engine: engine, Stage: transition.Infrastructure}
			for i := 0; i < 2; i++ {
				_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: infra.Name}})
				require.NoError(t, err)
			}
			require.NoError(t, cl.Get(ctx, types.NamespacedName{Name: infra.Name}, infra))
			if tc.blocked {
				require.Zero(t, calls)
				require.Equal(t, milestone, *meta.FindStatusCondition(infra.Status.Conditions, "InfraTransitionComplete"), "blocked requests must preserve the cutover milestone")
				require.Equal(t, api.ActionExecute, infra.Status.LastAction, "dependency rejection must precede action acceptance")
				require.Equal(t, "DependencyNotReady", meta.FindStatusCondition(infra.Status.Conditions, "Ready").Reason)
			} else {
				require.Equal(t, 1, calls)
			}
		})
	}
}

func TestInfrastructureRollbackRejected(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	infra := &api.ControlPlaneNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, Generation: 5},
		Spec:       api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionRollback}},
		Status:     &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{LastAction: api.ActionExecute, ActionState: api.ActionStateSucceeded, ActionGeneration: 4}},
	}
	batch := &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "active-batch"},
		Status: &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{LastAction: api.ActionExecute, ActionState: api.ActionStateRunning}}}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(infra).WithObjects(infra, batch).Build()
	engine := &transition.Engine{Enabled: true, Handlers: map[transition.Key]transition.Handler{
		{Stage: transition.Infrastructure, Action: api.ActionRollback}: actionHandlerFunc(func(context.Context, transition.Request) (transition.Result, error) {
			t.Fatal("infrastructure rollback must never dispatch")
			return transition.Result{}, nil
		}),
	}}
	r := Reconciler{Client: cl, APIReader: cl, Engine: engine, Stage: transition.Infrastructure}
	for i := 0; i < 2; i++ {
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: infra.Name}})
		require.NoError(t, err)
	}
	require.NoError(t, cl.Get(ctx, types.NamespacedName{Name: infra.Name}, infra))
	require.Equal(t, "InfrastructureRollbackUnsupported", meta.FindStatusCondition(infra.Status.Conditions, "Ready").Reason)
	require.Equal(t, api.ActionExecute, infra.Status.LastAction)
	require.Equal(t, api.ActionStateSucceeded, infra.Status.ActionState)
	require.EqualValues(t, 4, infra.Status.ActionGeneration)
}

func TestNamespaceRollbackRecoveryGate(t *testing.T) {
	for _, checkpoint := range []api.Action{"", api.ActionFinalize} {
		for _, state := range []api.ActionState{api.ActionStateRunning, api.ActionStateFailed} {
			t.Run(string(checkpoint)+"/"+string(state), func(t *testing.T) {
				ctx := context.Background()
				scheme := runtime.NewScheme()
				require.NoError(t, api.AddToScheme(scheme))
				infra := &api.ControlPlaneNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName},
					Status: &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{LastAction: api.ActionExecute, ActionState: api.ActionStateFailed, IrreversibleAction: string(checkpoint)}}}
				batch := &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "batch", Generation: 3},
					Spec:   api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionRollback}},
					Status: &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{LastAction: api.ActionRollback, ActionState: state, ActionGeneration: 3}}}
				cl := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(batch).WithObjects(infra, batch).Build()
				calls := 0
				e := &transition.Engine{Enabled: true, Handlers: map[transition.Key]transition.Handler{{Stage: transition.Namespaces, Action: api.ActionRollback}: actionHandlerFunc(func(context.Context, transition.Request) (transition.Result, error) {
					calls++
					return transition.Result{Outcome: transition.OutcomeSucceeded}, nil
				})}}
				r := Reconciler{Client: cl, Engine: e, Stage: transition.Namespaces}
				for i := 0; i < 2; i++ {
					_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "batch"}})
					require.NoError(t, err)
				}
				require.NoError(t, cl.Get(ctx, types.NamespacedName{Name: "batch"}, batch))
				if checkpoint == "" {
					require.Equal(t, 1, calls)
					require.Equal(t, "RolledBack", batch.Status.Phase)
				} else {
					require.Zero(t, calls)
					require.Equal(t, "DependencyNotReady", meta.FindStatusCondition(batch.Status.Conditions, "Ready").Reason)
				}
			})
		}
	}
}

func TestPendingFinalizeDoesNotStrandNamespaceExecution(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	infra := &api.ControlPlaneNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, Generation: 5},
		Spec:   api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionFinalize}},
		Status: &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{LastAction: api.ActionExecute, ActionState: api.ActionStateSucceeded, ActionGeneration: 4, Phase: "InfraTransitionComplete", Conditions: []metav1.Condition{{Type: "InfraTransitionComplete", Status: metav1.ConditionTrue, ObservedGeneration: 4}}}}}
	batch := &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "batch", Generation: 3},
		Spec:   api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}},
		Status: &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{LastAction: api.ActionExecute, ActionState: api.ActionStateRunning, ActionGeneration: 3}}}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(infra, batch).WithObjects(infra, batch).Build()
	calls := 0
	e := &transition.Engine{Enabled: true, Handlers: map[transition.Key]transition.Handler{{Stage: transition.Namespaces, Action: api.ActionExecute}: actionHandlerFunc(func(context.Context, transition.Request) (transition.Result, error) {
		calls++
		return transition.Result{Outcome: transition.OutcomeSucceeded}, nil
	})}}
	infraController := Reconciler{Client: cl, APIReader: cl, Engine: e, Stage: transition.Infrastructure}
	_, err := infraController.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: infra.Name}})
	require.NoError(t, err)
	require.NoError(t, cl.Get(ctx, types.NamespacedName{Name: infra.Name}, infra))
	require.Equal(t, "Blocked", infra.Status.Phase)
	require.Equal(t, api.ActionExecute, infra.Status.LastAction)
	namespaceController := Reconciler{Client: cl, APIReader: cl, Engine: e, Stage: transition.Namespaces}
	_, err = namespaceController.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: batch.Name}})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.NoError(t, cl.Get(ctx, types.NamespacedName{Name: batch.Name}, batch))
	require.Equal(t, "Completed", batch.Status.Phase)
}

// Generic readiness does not substitute for the design's infrastructure milestone.
func TestInfrastructureGateRequiresCutoverMilestone(t *testing.T) {
	for _, conditionType := range []string{"Ready", "InfraTransitionComplete"} {
		t.Run(conditionType, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, api.AddToScheme(scheme))
			infra := &api.ControlPlaneNetworkTransition{
				ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, Generation: 4},
				Status: &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{
					LastAction: api.ActionExecute, ActionState: api.ActionStateSucceeded, ActionGeneration: 4,
					Phase: "InfraTransitionComplete", Conditions: []metav1.Condition{{Type: conditionType, Status: metav1.ConditionTrue}},
				}},
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(infra).Build()
			r := Reconciler{Client: c}
			ready, err := r.infrastructureReady(context.Background())
			require.NoError(t, err)
			require.Equal(t, conditionType == "InfraTransitionComplete", ready)
		})
	}
}

type failingReader struct {
	client.Client
	err error
}

func (c failingReader) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return c.err
}

func TestReconcilePropagatesReadErrors(t *testing.T) {
	failure := errors.New("API unavailable")
	r := &Reconciler{Client: failingReader{err: failure}, Engine: &transition.Engine{Enabled: true}, Stage: transition.Infrastructure}
	result, err := r.Reconcile(context.Background(), ctrl.Request{})
	require.ErrorIs(t, err, failure)
	require.Zero(t, result)
}
