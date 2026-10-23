/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package transition

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

type handlerFunc func(context.Context, Request) (Result, error)

func (f handlerFunc) Reconcile(ctx context.Context, req Request) (Result, error) { return f(ctx, req) }

func TestEngineSafetyGates(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		stage   Stage
		action  api.Action
		last    api.Action
		reason  string
	}{
		{"disabled", false, Infrastructure, api.ActionEvaluate, "", "FeatureDisabled"},
		{"execute before derive", true, Infrastructure, api.ActionExecute, api.ActionEvaluate, "InvalidAction"},
		{"plan before evaluation", true, Infrastructure, api.ActionPlan, "", "InvalidAction"},
		{"infrastructure commit", true, Infrastructure, api.ActionCommit, api.ActionExecute, "InvalidAction"},
		{"commit before execute", true, Namespaces, api.ActionCommit, api.ActionDerive, "InvalidAction"},
		{"missing commit implementation", true, Namespaces, api.ActionCommit, api.ActionExecute, "ActionNotImplemented"},
		{"namespace finalize", true, Namespaces, api.ActionFinalize, api.ActionExecute, "InvalidAction"},
		{"rollback after finalization", true, Infrastructure, api.ActionRollback, api.ActionFinalize, "MigrationFinalized"},
		{"missing implementation", true, Infrastructure, api.ActionEvaluate, "", "ActionNotImplemented"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status := &api.TransitionStatus{LastAction: tc.last}
			if tc.last == api.ActionFinalize {
				status.IrreversibleAction = string(api.ActionFinalize)
			}
			if tc.last != "" {
				status.ActionState = api.ActionStateSucceeded
			}
			e := Engine{Enabled: tc.enabled}
			_, err := e.Reconcile(context.Background(), Request{Stage: tc.stage, Action: tc.action, Generation: 2, Status: status})
			require.NoError(t, err)
			require.Equal(t, tc.last, status.LastAction)
			require.Equal(t, "Blocked", status.Phase)
			condition := meta.FindStatusCondition(status.Conditions, "Ready")
			require.NotNil(t, condition)
			require.Equal(t, tc.reason, condition.Reason)
			require.Equal(t, int64(2), condition.ObservedGeneration)
		})
	}
}

func TestEngineProgressAndRestart(t *testing.T) {
	status := &api.TransitionStatus{}
	ctx := context.Background()
	actions := []api.Action{api.ActionEvaluate, api.ActionPlan, api.ActionDerive, api.ActionExecute, api.ActionFinalize}
	phases := []string{"Evaluated", "PlanReady", "Derived", "InfraTransitionComplete", "Finalized"}
	calls := 0
	for i, action := range actions {
		handler := handlerFunc(func(gotCtx context.Context, req Request) (Result, error) {
			require.Equal(t, ctx, gotCtx)
			require.Equal(t, action, req.Action)
			if action == api.ActionFinalize && req.Status.IrreversibleAction == "" {
				return Result{Outcome: OutcomeCheckpointIrreversible}, nil
			}
			calls++
			req.Status.NSXWorkflows = []api.NSXWorkflowStatus{{VDN: "dvs-1", WorkflowID: "persisted-workflow"}}
			return Result{Outcome: OutcomeSucceeded}, nil
		})
		request := Request{Stage: Infrastructure, Action: action, Generation: int64(i + 1), Status: status}
		engine := Engine{Enabled: true, Handlers: map[Key]Handler{{Infrastructure, action}: handler}}
		result, err := engine.Reconcile(ctx, request)
		require.NoError(t, err)
		require.Positive(t, result.RequeueAfter)
		require.Equal(t, i, calls, "the checkpoint must be persisted before the handler runs")
		require.Equal(t, action, status.LastAction)
		require.Equal(t, api.ActionStateRunning, status.ActionState)
		_, err = engine.Reconcile(ctx, request)
		require.NoError(t, err)
		if action == api.ActionFinalize {
			_, err = engine.Reconcile(ctx, request)
			require.NoError(t, err)
		}
		require.Equal(t, api.ActionStateSucceeded, status.ActionState)
		require.Equal(t, phases[i], status.Phase)
		if action == api.ActionExecute {
			milestone := meta.FindStatusCondition(status.Conditions, "InfraTransitionComplete")
			require.NotNil(t, milestone)
			require.Equal(t, metav1.ConditionTrue, milestone.Status)
			require.Equal(t, request.Generation, milestone.ObservedGeneration)
		}
		// A fresh dispatcher resumes from the persisted status without repeating a completed action.
		restarted := Engine{Enabled: true, Handlers: engine.Handlers}
		_, err = restarted.Reconcile(ctx, request)
		require.NoError(t, err)
		require.Equal(t, i+1, calls)
		require.Equal(t, "persisted-workflow", status.NSXWorkflows[0].WorkflowID)
	}
}

func TestHandlerResults(t *testing.T) {
	failure := errors.New("NSX unavailable")
	for _, tc := range []struct {
		name   string
		result Result
		err    error
		phase  string
	}{
		{"polling", Result{Outcome: OutcomeInProgress, RequeueAfter: time.Second}, nil, "Deriving"},
		{"failure", Result{}, failure, "Failed"},
		{"success", Result{Outcome: OutcomeSucceeded}, nil, "Derived"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status := &api.TransitionStatus{LastAction: api.ActionDerive, ActionState: api.ActionStateRunning, ActionGeneration: 2}
			engine := Engine{Enabled: true, Handlers: map[Key]Handler{{Infrastructure, api.ActionDerive}: handlerFunc(func(context.Context, Request) (Result, error) { return tc.result, tc.err })}}
			result, err := engine.Reconcile(context.Background(), Request{Stage: Infrastructure, Action: api.ActionDerive, Generation: 2, Status: status})
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				require.Contains(t, meta.FindStatusCondition(status.Conditions, "Ready").Message, tc.err.Error())
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.result, result)
			require.Equal(t, tc.phase, status.Phase)
			expectedState := api.ActionStateSucceeded
			if tc.err != nil {
				expectedState = api.ActionStateFailed
			} else if tc.result.RequeueAfter > 0 {
				expectedState = api.ActionStateRunning
			}
			require.Equal(t, expectedState, status.ActionState)
			if tc.err != nil || tc.result.RequeueAfter > 0 {
				require.Equal(t, api.ActionDerive, status.LastAction)
			}
		})
	}
}

func TestRollbackPartialActionAfterRestart(t *testing.T) {
	failure := errors.New("failed after provisioning")
	for _, action := range []api.Action{api.ActionDerive, api.ActionExecute} {
		for _, failed := range []bool{false, true} {
			name := string(action) + "/interrupted"
			if failed {
				name = string(action) + "/failed"
			}
			t.Run(name, func(t *testing.T) {
				completed := api.ActionPlan
				if action == api.ActionExecute {
					completed = api.ActionDerive
				}
				status := &api.TransitionStatus{LastAction: completed, ActionState: api.ActionStateSucceeded, ActionGeneration: 2}
				calls := 0
				engine := Engine{Enabled: true, Handlers: map[Key]Handler{
					{Infrastructure, action}: handlerFunc(func(context.Context, Request) (Result, error) {
						calls++
						return Result{}, failure
					}),
				}}
				request := Request{Stage: Infrastructure, Action: action, Generation: 3, Status: status}
				result, err := engine.Reconcile(context.Background(), request)
				require.NoError(t, err)
				require.Positive(t, result.RequeueAfter)
				require.Zero(t, calls)
				require.Equal(t, action, status.LastAction)
				require.Equal(t, api.ActionStateRunning, status.ActionState)
				if failed {
					_, err = engine.Reconcile(context.Background(), request)
					require.ErrorIs(t, err, failure)
					require.Equal(t, action, status.LastAction)
					require.Equal(t, api.ActionStateFailed, status.ActionState)
				}
				// Simulate reading the persisted checkpoint in a new process.
				status = status.DeepCopy()
				rollbackCalls := 0
				restarted := Engine{Enabled: true, Handlers: map[Key]Handler{
					{Infrastructure, api.ActionRollback}: handlerFunc(func(_ context.Context, req Request) (Result, error) {
						rollbackCalls++
						require.Equal(t, api.ActionRollback, req.Status.LastAction)
						require.Equal(t, api.ActionStateRunning, req.Status.ActionState)
						return Result{Outcome: OutcomeSucceeded}, nil
					}),
				}}
				request = Request{Stage: Infrastructure, Action: api.ActionRollback, Generation: 4, Status: status}
				result, err = restarted.Reconcile(context.Background(), request)
				require.NoError(t, err)
				require.Positive(t, result.RequeueAfter)
				require.Zero(t, rollbackCalls)
				_, err = restarted.Reconcile(context.Background(), request)
				require.NoError(t, err)
				require.Equal(t, 1, rollbackCalls)
				require.Equal(t, "RolledBack", status.Phase)
				require.Equal(t, api.ActionRollback, status.LastAction)
				require.Equal(t, api.ActionStateSucceeded, status.ActionState)
			})
		}
	}
}

func TestCannotSkipUnsuccessfulAction(t *testing.T) {
	for _, state := range []api.ActionState{api.ActionStateRunning, api.ActionStateFailed} {
		for _, tc := range []struct {
			name            string
			last, requested api.Action
		}{
			{"rollback during finalization", api.ActionFinalize, api.ActionRollback},
			{"execute during derivation", api.ActionDerive, api.ActionExecute},
			{"derive during rollback", api.ActionRollback, api.ActionDerive},
		} {
			t.Run(string(state)+"/"+tc.name, func(t *testing.T) {
				status := &api.TransitionStatus{LastAction: tc.last, ActionState: state}
				if tc.last == api.ActionFinalize {
					status.IrreversibleAction = string(api.ActionFinalize)
				}
				engine := Engine{Enabled: true}
				_, err := engine.Reconcile(context.Background(), Request{Stage: Infrastructure, Action: tc.requested, Generation: 4, Status: status})
				require.NoError(t, err)
				require.Equal(t, tc.last, status.LastAction)
				require.Equal(t, state, status.ActionState)
				reason := "InvalidAction"
				if tc.last == api.ActionFinalize {
					reason = "MigrationFinalized"
				}
				require.Equal(t, reason, meta.FindStatusCondition(status.Conditions, "Ready").Reason)
			})
		}
	}
}

func TestRetryFailedAction(t *testing.T) {
	status := &api.TransitionStatus{LastAction: api.ActionDerive, ActionState: api.ActionStateFailed, ActionGeneration: 3}
	calls := 0
	engine := Engine{Enabled: true, Handlers: map[Key]Handler{
		{Infrastructure, api.ActionDerive}: handlerFunc(func(_ context.Context, req Request) (Result, error) {
			calls++
			require.Equal(t, api.ActionStateRunning, req.Status.ActionState)
			return Result{Outcome: OutcomeSucceeded}, nil
		}),
	}}
	request := Request{Stage: Infrastructure, Action: api.ActionDerive, Generation: 3, Status: status}
	result, err := engine.Reconcile(context.Background(), request)
	require.NoError(t, err)
	require.Positive(t, result.RequeueAfter)
	require.Zero(t, calls)
	require.Equal(t, api.ActionStateRunning, status.ActionState)
	_, err = engine.Reconcile(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, api.ActionStateSucceeded, status.ActionState)
	require.Equal(t, api.ActionDerive, status.LastAction)
	_, err = engine.Reconcile(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
}

func TestNeedsInputResumesAfterInputUpdate(t *testing.T) {
	status := &api.TransitionStatus{}
	request := Request{Stage: Infrastructure, Action: api.ActionEvaluate, Generation: 1, Status: status}
	inputsSupplied := false
	calls := 0
	engine := Engine{Enabled: true, Handlers: map[Key]Handler{
		{Infrastructure, api.ActionEvaluate}: handlerFunc(func(_ context.Context, req Request) (Result, error) {
			calls++
			if !inputsSupplied {
				req.Status.RequiredInputs = []api.RequiredInput{{Key: "gatewayAddress", Description: "Supply the gateway"}}
				return Result{Outcome: OutcomeNeedsInput, RequeueAfter: time.Second}, nil
			}
			return Result{Outcome: OutcomeSucceeded}, nil
		}),
	}}
	_, err := engine.Reconcile(context.Background(), request)
	require.NoError(t, err)
	require.Zero(t, calls, "checkpoint must precede execution")
	result, err := engine.Reconcile(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, OutcomeNeedsInput, result.Outcome)
	require.Zero(t, result.RequeueAfter, "input waits must not poll")
	require.Equal(t, "NeedsInput", status.Phase)
	require.Equal(t, api.ActionStateRunning, status.ActionState)
	require.Equal(t, api.ActionEvaluate, status.LastAction)
	require.Len(t, status.RequiredInputs, 1)
	require.False(t, meta.IsStatusConditionTrue(status.Conditions, "Ready"))
	request.Action = api.ActionPlan
	_, err = engine.Reconcile(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, "InvalidAction", meta.FindStatusCondition(status.Conditions, "Ready").Reason)
	require.Equal(t, 1, calls)
	// A spec update fills missing input and resumes the same action after restart.
	status = status.DeepCopy()
	request.Status, request.Action, request.Generation = status, api.ActionEvaluate, 3
	inputsSupplied = true
	_, err = engine.Reconcile(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	result, err = engine.Reconcile(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, OutcomeSucceeded, result.Outcome)
	require.Equal(t, api.ActionStateSucceeded, status.ActionState)
	require.Equal(t, "Evaluated", status.Phase)
	require.True(t, meta.IsStatusConditionTrue(status.Conditions, "Ready"))
	require.Empty(t, status.RequiredInputs)
}

func TestHandlerMustDeclareOutcome(t *testing.T) {
	status := &api.TransitionStatus{LastAction: api.ActionEvaluate, ActionState: api.ActionStateRunning, ActionGeneration: 1}
	engine := Engine{Enabled: true, Handlers: map[Key]Handler{
		{Infrastructure, api.ActionEvaluate}: handlerFunc(func(context.Context, Request) (Result, error) { return Result{}, nil }),
	}}
	_, err := engine.Reconcile(context.Background(), Request{Stage: Infrastructure, Action: api.ActionEvaluate, Generation: 1, Status: status})
	require.ErrorContains(t, err, "invalid outcome")
	require.Equal(t, api.ActionStateFailed, status.ActionState)
	require.Equal(t, "EvaluationFailed", status.Phase)
	require.False(t, meta.IsStatusConditionTrue(status.Conditions, "Ready"))
}

func TestNamespaceCommitRecovery(t *testing.T) {
	ctx := context.Background()
	status := &api.TransitionStatus{}
	request := Request{Stage: Namespaces, Status: status}
	// Advance through each review gate; no action auto-advances.
	for i, action := range []api.Action{api.ActionEvaluate, api.ActionPlan, api.ActionDerive, api.ActionExecute} {
		request.Action, request.Generation = action, int64(i+1)
		engine := Engine{Enabled: true, Handlers: map[Key]Handler{{Namespaces, action}: handlerFunc(func(context.Context, Request) (Result, error) {
			return Result{Outcome: OutcomeSucceeded}, nil
		})}}
		_, err := engine.Reconcile(ctx, request)
		require.NoError(t, err)
		_, err = engine.Reconcile(ctx, request)
		require.NoError(t, err)
	}
	require.Equal(t, "Completed", status.Phase)
	status.NSXWorkflows = []api.NSXWorkflowStatus{{VDN: "dvs-1", WorkflowID: "workflow-1"}, {VDN: "dvs-2", WorkflowID: "workflow-2"}}
	request.Action, request.Generation = api.ActionCommit, 5
	calls := 0
	failure := errors.New("second workflow deletion failed")
	engine := Engine{Enabled: true, Handlers: map[Key]Handler{{Namespaces, api.ActionCommit}: handlerFunc(func(_ context.Context, req Request) (Result, error) {
		if req.Status.IrreversibleAction == "" {
			return Result{Outcome: OutcomeCheckpointIrreversible}, nil
		}
		calls++
		require.Equal(t, "Committing", req.Status.Phase)
		require.Equal(t, api.ActionCommit, req.Status.LastAction)
		require.Equal(t, "workflow-2", req.Status.NSXWorkflows[1].WorkflowID)
		if calls == 1 {
			return Result{}, failure
		}
		return Result{Outcome: OutcomeSucceeded}, nil
	})}}
	_, err := engine.Reconcile(ctx, request)
	require.NoError(t, err)
	require.Zero(t, calls, "persist action acceptance before calling the handler")
	require.Equal(t, "Committing", status.Phase)
	require.False(t, meta.IsStatusConditionTrue(status.Conditions, "NamespaceMigrationCommitted"))
	_, err = engine.Reconcile(ctx, request)
	require.NoError(t, err)
	require.Zero(t, calls, "persist PONR before irreversible work")
	require.Equal(t, string(api.ActionCommit), status.IrreversibleAction)
	_, err = engine.Reconcile(ctx, request)
	require.ErrorIs(t, err, failure)
	require.Equal(t, api.ActionStateFailed, status.ActionState)
	require.Equal(t, "Committing", status.Phase)
	require.Equal(t, "CommitFailed", meta.FindStatusCondition(status.Conditions, "NamespaceMigrationCommitted").Reason)
	// Restart with the persisted failed checkpoint and exact workflow identities.
	request.Status = status.DeepCopy()
	restarted := Engine{Enabled: true, Handlers: engine.Handlers}
	_, err = restarted.Reconcile(ctx, request)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	_, err = restarted.Reconcile(ctx, request)
	require.NoError(t, err)
	require.Equal(t, "Committed", request.Status.Phase)
	require.Equal(t, api.ActionStateSucceeded, request.Status.ActionState)
	require.True(t, meta.IsStatusConditionTrue(request.Status.Conditions, "NamespaceMigrationCommitted"))
	_, err = restarted.Reconcile(ctx, request)
	require.NoError(t, err)
	require.Equal(t, 2, calls, "completed commit must not repeat workflow deletion")
}

func TestCommitClosesRollbackWindow(t *testing.T) {
	for _, state := range []api.ActionState{api.ActionStateRunning, api.ActionStateFailed, api.ActionStateSucceeded} {
		for _, action := range []api.Action{api.ActionEvaluate, api.ActionPlan, api.ActionDerive, api.ActionExecute, api.ActionRollback, api.ActionFinalize} {
			t.Run(string(state)+"/"+string(action), func(t *testing.T) {
				status := &api.TransitionStatus{LastAction: api.ActionCommit, ActionState: state, ActionGeneration: 5, IrreversibleAction: string(api.ActionCommit)}
				engine := Engine{Enabled: true}
				_, err := engine.Reconcile(context.Background(), Request{Stage: Namespaces, Action: action, Generation: 6, Status: status})
				require.NoError(t, err)
				require.Equal(t, api.ActionCommit, status.LastAction)
				require.Equal(t, state, status.ActionState)
				require.Equal(t, "MigrationCommitted", meta.FindStatusCondition(status.Conditions, "Ready").Reason)
			})
		}
	}
	for _, state := range []api.ActionState{api.ActionStateRunning, api.ActionStateFailed} {
		status := &api.TransitionStatus{LastAction: api.ActionExecute, ActionState: state}
		engine := Engine{Enabled: true}
		_, err := engine.Reconcile(context.Background(), Request{Stage: Namespaces, Action: api.ActionCommit, Generation: 5, Status: status})
		require.NoError(t, err)
		require.Equal(t, api.ActionExecute, status.LastAction)
		require.Equal(t, "InvalidAction", meta.FindStatusCondition(status.Conditions, "Ready").Reason)
	}
}

func TestFailureStatusMessageFitsConditionLimit(t *testing.T) {
	status := &api.TransitionStatus{LastAction: api.ActionDerive, ActionState: api.ActionStateRunning, ActionGeneration: 2}
	failure := errors.New(strings.Repeat("界", 12000))
	engine := Engine{Enabled: true, Handlers: map[Key]Handler{{Infrastructure, api.ActionDerive}: handlerFunc(func(context.Context, Request) (Result, error) {
		return Result{}, failure
	})}}
	_, err := engine.Reconcile(context.Background(), Request{Stage: Infrastructure, Action: api.ActionDerive, Generation: 2, Status: status})
	require.ErrorIs(t, err, failure)
	message := meta.FindStatusCondition(status.Conditions, "Ready").Message
	require.LessOrEqual(t, len(message), 32768)
	require.True(t, utf8.ValidString(message))
	require.True(t, strings.HasPrefix(message, "DERIVE failed: "))
	require.True(t, strings.HasSuffix(message, "..."))
}

func TestRollbackBeforeIrreversibleCheckpoint(t *testing.T) {
	for _, stage := range []Stage{Infrastructure, Namespaces} {
		for _, last := range []api.Action{api.ActionEvaluate, api.ActionPlan, api.ActionCommit, api.ActionFinalize} {
			if (stage == Infrastructure && last == api.ActionCommit) || (stage == Namespaces && last == api.ActionFinalize) {
				continue
			}
			for _, state := range []api.ActionState{api.ActionStateRunning, api.ActionStateFailed, api.ActionStateSucceeded} {
				t.Run(string(stage)+"/"+string(last)+"/"+string(state), func(t *testing.T) {
					status := &api.TransitionStatus{LastAction: last, ActionState: state, ActionGeneration: 1}
					calls := 0
					engine := Engine{Enabled: true, Handlers: map[Key]Handler{{stage, api.ActionRollback}: handlerFunc(func(context.Context, Request) (Result, error) {
						calls++
						return Result{Outcome: OutcomeSucceeded}, nil
					})}}
					req := Request{Stage: stage, Action: api.ActionRollback, Generation: 2, Status: status}
					_, err := engine.Reconcile(context.Background(), req)
					require.NoError(t, err)
					require.Zero(t, calls)
					_, err = engine.Reconcile(context.Background(), req)
					require.NoError(t, err)
					require.Equal(t, 1, calls)
					require.Equal(t, "RolledBack", status.Phase)
				})
			}
		}
	}
}

func TestRejectedCommitPreflightCanRollback(t *testing.T) {
	status := &api.TransitionStatus{LastAction: api.ActionExecute, ActionState: api.ActionStateSucceeded, ActionGeneration: 1}
	failure := errors.New("workflow still required by another batch")
	engine := Engine{Enabled: true, Handlers: map[Key]Handler{{Namespaces, api.ActionCommit}: handlerFunc(func(context.Context, Request) (Result, error) {
		return Result{}, failure
	}), {Namespaces, api.ActionRollback}: handlerFunc(func(context.Context, Request) (Result, error) {
		return Result{Outcome: OutcomeSucceeded}, nil
	})}}
	req := Request{Stage: Namespaces, Action: api.ActionCommit, Generation: 2, Status: status}
	_, err := engine.Reconcile(context.Background(), req)
	require.NoError(t, err)
	_, err = engine.Reconcile(context.Background(), req)
	require.ErrorIs(t, err, failure)
	require.Empty(t, status.IrreversibleAction)
	req.Action, req.Generation = api.ActionRollback, 3
	_, err = engine.Reconcile(context.Background(), req)
	require.NoError(t, err)
	_, err = engine.Reconcile(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, "RolledBack", status.Phase)
}

func TestIrreversibleActionRequiresCheckpointBeforeSuccess(t *testing.T) {
	for _, tc := range []struct {
		stage  Stage
		action api.Action
	}{{Namespaces, api.ActionCommit}, {Infrastructure, api.ActionFinalize}} {
		t.Run(string(tc.action), func(t *testing.T) {
			status := &api.TransitionStatus{LastAction: tc.action, ActionState: api.ActionStateRunning, ActionGeneration: 2}
			engine := Engine{Enabled: true, Handlers: map[Key]Handler{{tc.stage, tc.action}: handlerFunc(func(context.Context, Request) (Result, error) {
				return Result{Outcome: OutcomeSucceeded}, nil
			})}}
			_, err := engine.Reconcile(context.Background(), Request{Stage: tc.stage, Action: tc.action, Generation: 2, Status: status})
			require.ErrorContains(t, err, "without a persisted irreversible checkpoint")
			require.Equal(t, api.ActionStateFailed, status.ActionState)
			require.Empty(t, status.IrreversibleAction)
		})
	}
}
