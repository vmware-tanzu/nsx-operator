/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package controllers

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/transition"
)

// A test handler models authoritative external workflow state across controller
// restarts. Production approval, live ownership checks and NSX deletion belong to
// WBS 1.3.7; this test does not provide a journal or a production workflow client.
func TestIrreversibleRecoveryAcrossStatusFailures(t *testing.T) {
	for _, stage := range []transition.Stage{transition.Namespaces, transition.Infrastructure} {
		t.Run(string(stage), func(t *testing.T) {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			require.NoError(t, api.AddToScheme(scheme))
			action, active, complete := api.ActionCommit, "Committing", "Committed"
			if stage == transition.Infrastructure {
				action, active, complete = api.ActionFinalize, "Finalizing", "Finalized"
			}
			workflows := []api.NSXWorkflowStatus{{VDNName: "network-a", WorkflowID: "workflow-a"}, {VDNName: "network-b", WorkflowID: "workflow-b"}}
			initial := api.TransitionStatus{LastAction: api.ActionExecute, ActionState: api.ActionStateSucceeded, ActionGeneration: 4, NSXWorkflows: workflows}
			var obj client.Object = &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "batch", Generation: 5}, Spec: api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: action}, Namespaces: []string{"tenant"}}, Status: &api.NamespaceNetworkTransitionStatus{TransitionStatus: initial}}
			if stage == transition.Infrastructure {
				obj = &api.ControlPlaneNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, Generation: 5}, Spec: api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: action}}, Status: &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: initial}}
			}
			writeFailure := errors.New("status unavailable")
			failWrite := false
			builder := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(obj).WithObjects(obj).WithInterceptorFuncs(interceptor.Funcs{SubResourceUpdate: func(ctx context.Context, c client.Client, subresource string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if failWrite {
					return writeFailure
				}
				return c.SubResource(subresource).Update(ctx, obj, opts...)
			}})
			if stage == transition.Namespaces {
				builder.WithObjects(&api.ControlPlaneNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, Generation: 4}, Status: &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{LastAction: api.ActionExecute, ActionState: api.ActionStateSucceeded, ActionGeneration: 4, Phase: "InfraTransitionComplete", Conditions: []metav1.Condition{{Type: "InfraTransitionComplete", Status: metav1.ConditionTrue, Reason: "CutoverVerified", LastTransitionTime: metav1.Now()}}}}})
			}
			c := builder.Build()
			key := client.ObjectKeyFromObject(obj)
			read := func() *api.TransitionStatus {
				current := obj.DeepCopyObject().(client.Object)
				require.NoError(t, c.Get(ctx, key, current))
				switch o := current.(type) {
				case *api.NamespaceNetworkTransition:
					return &o.Status.TransitionStatus
				case *api.ControlPlaneNetworkTransition:
					return &o.Status.TransitionStatus
				}
				panic("unknown CR")
			}
			deletions := map[string]int{}
			failSecond := true
			deleteFailure := errors.New("second workflow deletion unavailable")
			handler := actionHandlerFunc(func(_ context.Context, req transition.Request) (transition.Result, error) {
				persisted := read()
				require.Equal(t, action, persisted.LastAction)
				if stage == transition.Infrastructure && persisted.ActionState == api.ActionStateFailed {
					require.Equal(t, "Failed", persisted.Phase)
				} else {
					require.Equal(t, active, persisted.Phase)
				}
				require.Equal(t, workflows, req.Status.NSXWorkflows)
				if req.Status.IrreversibleAction == "" {
					return transition.Result{Outcome: transition.OutcomeCheckpointIrreversible}, nil
				}
				require.Equal(t, string(action), persisted.IrreversibleAction, "checkpoint must be stored before any deletion")
				for _, w := range req.Status.NSXWorkflows {
					if deletions[w.WorkflowID] > 0 {
						continue
					}
					if w.WorkflowID == "workflow-b" && failSecond {
						return transition.Result{}, deleteFailure
					}
					deletions[w.WorkflowID]++
				}
				return transition.Result{Outcome: transition.OutcomeSucceeded}, nil
			})
			reconcile := func() error {
				// Reconstruct both dispatcher and reconciler to model a process restart.
				e := &transition.Engine{Enabled: true, Handlers: map[transition.Key]transition.Handler{{Stage: stage, Action: action}: handler}}
				r := Reconciler{Client: c, APIReader: c, Stage: stage, Engine: e}
				_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
				return err
			}
			require.NoError(t, reconcile()) // Persist action acceptance.
			failWrite = true
			require.ErrorIs(t, reconcile(), writeFailure) // PONR write fails.
			require.Empty(t, deletions)
			require.Empty(t, read().IrreversibleAction)
			failWrite = false
			require.NoError(t, reconcile())
			require.Equal(t, string(action), read().IrreversibleAction)
			require.Empty(t, deletions)
			require.ErrorIs(t, reconcile(), deleteFailure) // First delete succeeds, second fails.
			require.Equal(t, map[string]int{"workflow-a": 1}, deletions)
			require.Equal(t, api.ActionStateFailed, read().ActionState)
			require.Equal(t, string(action), read().IrreversibleAction)
			require.Equal(t, workflows, read().NSXWorkflows)
			if stage == transition.Namespaces {
				require.Equal(t, "Committing", read().Phase)
				require.False(t, meta.IsStatusConditionTrue(read().Conditions, "NamespaceMigrationCommitted"))
			}
			failSecond = false
			failWrite = true
			require.ErrorIs(t, reconcile(), writeFailure) // Deletes finish, completion status is lost.
			require.Equal(t, map[string]int{"workflow-a": 1, "workflow-b": 1}, deletions)
			require.Equal(t, workflows, read().NSXWorkflows, "a lost completion write retains recovery IDs")
			failWrite = false
			require.NoError(t, reconcile()) // Resume without repeating confirmed deletes.
			require.Equal(t, complete, read().Phase)
			require.Equal(t, api.ActionStateSucceeded, read().ActionState)
			require.Equal(t, string(action), read().IrreversibleAction)
			require.Empty(t, read().NSXWorkflows, "clear IDs with the confirmed completion status")
			if stage == transition.Namespaces {
				require.True(t, meta.IsStatusConditionTrue(read().Conditions, "NamespaceMigrationCommitted"))
			}
			require.NoError(t, reconcile())
			require.Equal(t, map[string]int{"workflow-a": 1, "workflow-b": 1}, deletions)
		})
	}
}

func TestCommittedBatchWithoutCheckpointIsNotTerminal(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	batch := &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "batch", Generation: 5}, Spec: api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionCommit}}, Status: &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{LastAction: api.ActionCommit, ActionState: api.ActionStateSucceeded, ActionGeneration: 5, Phase: "Committed"}}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(batch).WithObjects(batch).Build()
	r := Reconciler{Client: c, Engine: &transition.Engine{Enabled: true}, Stage: transition.Namespaces}
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(batch)})
	require.NoError(t, err)
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(batch), batch))
	require.Equal(t, "Blocked", batch.Status.Phase)
	require.Equal(t, "InfrastructureNotReady", meta.FindStatusCondition(batch.Status.Conditions, "Ready").Reason)
}

func TestCommittedBatchRepairsCommitConditionWithoutRepeatingDeletion(t *testing.T) {
	for _, state := range []string{"missing", "false", "stale"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			require.NoError(t, api.AddToScheme(scheme))
			batch := &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "batch", Generation: 5},
				Spec: api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionCommit}},
				Status: &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{
					LastAction: api.ActionCommit, ActionState: api.ActionStateSucceeded, ActionGeneration: 5,
					IrreversibleAction: "Commit", Phase: "Committed",
					NSXWorkflows: []api.NSXWorkflowStatus{{VDNName: "network", WorkflowID: "workflow"}},
				}}}
			if state != "missing" {
				condition := metav1.Condition{Type: "NamespaceMigrationCommitted", Status: metav1.ConditionFalse, ObservedGeneration: 5, Reason: "CommitInProgress", LastTransitionTime: metav1.Now()}
				if state == "stale" {
					condition.Status, condition.ObservedGeneration = metav1.ConditionTrue, 4
				}
				batch.Status.Conditions = []metav1.Condition{condition}
			}
			// Finalized infrastructure must not strand a terminal namespace repair.
			infra := &api.ControlPlaneNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName},
				Status: &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{IrreversibleAction: "Finalize", Phase: "Finalized"}}}
			c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(batch).WithObjects(batch, infra).Build()
			r := Reconciler{Client: c, APIReader: c, Stage: transition.Namespaces,
				Engine: &transition.Engine{Enabled: true, Handlers: map[transition.Key]transition.Handler{
					{Stage: transition.Namespaces, Action: api.ActionCommit}: actionHandlerFunc(func(context.Context, transition.Request) (transition.Result, error) {
						t.Fatal("committed workflow deletion must not run again")
						return transition.Result{}, nil
					}),
				}}}
			key := client.ObjectKeyFromObject(batch)
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
			require.NoError(t, err)
			require.NoError(t, c.Get(ctx, key, batch))
			condition := meta.FindStatusCondition(batch.Status.Conditions, "NamespaceMigrationCommitted")
			require.NotNil(t, condition)
			require.Equal(t, metav1.ConditionTrue, condition.Status)
			require.Equal(t, batch.Generation, condition.ObservedGeneration)
			require.Equal(t, "Committed", batch.Status.Phase)
			require.Equal(t, "Commit", batch.Status.IrreversibleAction)
			require.Empty(t, batch.Status.NSXWorkflows)
			version := batch.ResourceVersion
			_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
			require.NoError(t, err)
			require.NoError(t, c.Get(ctx, key, batch))
			require.Equal(t, version, batch.ResourceVersion, "a repaired terminal record needs no further status writes")
		})
	}
}
