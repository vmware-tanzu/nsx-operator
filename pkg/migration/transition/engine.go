/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Package transition provides the shared declarative action dispatcher.
package transition

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

// Stage identifies the infrastructure or namespace orchestration boundary.
type Stage string

const (
	// Infrastructure owns control plane transition and cluster finalization.
	Infrastructure Stage = "Infrastructure"
	// Namespaces owns tenant namespace batches.
	Namespaces Stage = "Namespaces"
)

// Request carries a persisted transition to an idempotent action handler.
type Request struct {
	Stage      Stage
	Object     client.Object
	Action     api.Action
	Generation int64
	Status     *api.TransitionStatus
}

// Outcome distinguishes completion, ongoing work, and a wait for administrator input.
type Outcome string

const (
	// OutcomeSucceeded means the requested action completed.
	OutcomeSucceeded Outcome = "Succeeded"
	// OutcomeInProgress means the action needs further reconciliation.
	OutcomeInProgress Outcome = "InProgress"
	// OutcomeCheckpointIrreversible requests persistence of the point of no return.
	// The handler must return before deleting workflows, then resume from that checkpoint.
	OutcomeCheckpointIrreversible Outcome = "CheckpointIrreversible"
	// OutcomeNeedsInput means requiredInputs must be supplied before retrying.
	OutcomeNeedsInput Outcome = "NeedsInput"
)

// Result reports an explicit execution outcome. A zero outcome is invalid for handlers.
// RequeueAfter applies only to InProgress; NeedsInput waits for an object update.
type Result struct {
	Outcome      Outcome
	RequeueAfter time.Duration
}

// Handler performs one resumable action. It must use stable external identifiers
// and tolerate replay after a crash or status update conflict.
// Returned errors are published in status and must not contain credentials or tokens.
// Admission webhooks validate action requests before Kubernetes accepts them.
// Handlers inspect existing SubnetPort, NetworkInterface, reservation,
// NetworkResourceTransition and NSX workflow state on every reconciliation. They
// use stable identities for missing resources and verify realized/observed state
// before Succeeded. A lost status write reruns the same level-driven handler;
// there is no per-step journal, stored completion snapshot or status.recovery.
// Commit and Finalize must return CheckpointIrreversible after preflight checks,
// then wait for the persisted irreversibleAction before irreversible changes.
// irreversibleAction is engine-owned; handlers must not assign or clear it.
// Commit handlers must verify approval/onboarding and shared-workflow ownership,
// reconcile deletions from the recorded workflow identities and current NSX state.
type Handler interface {
	Reconcile(context.Context, Request) (Result, error)
}

// Key selects a stage-specific action implementation.
type Key struct {
	Stage  Stage
	Action api.Action
}

// Engine dispatches actions without automatically advancing review gates.
// An empty Handlers map is a safe scaffold: every action reports unavailable.
type Engine struct {
	mu       sync.Mutex
	Enabled  bool
	Handlers map[Key]Handler
}

// LockReconcile serializes the controllers' dependency reads, dispatch, and status writes.
// Execute exclusivity is enforced by the separately supplied admission webhook;
// this mutex serializes reconciles within the elected controller process only.
func (e *Engine) LockReconcile() func() {
	e.mu.Lock()
	return e.mu.Unlock
}

// Reconcile updates shared status and dispatches only a valid, enabled operation.
func (e *Engine) Reconcile(ctx context.Context, req Request) (Result, error) {
	observeGeneration(req.Status, req.Generation)
	if !e.Enabled {
		Block(req.Status, req.Generation, "FeatureDisabled", "Supervisor network migration is disabled")
		return Result{}, nil
	}
	if req.Status.IrreversibleAction != "" && string(req.Action) != req.Status.IrreversibleAction {
		reason := "MigrationCommitted"
		if req.Status.IrreversibleAction == string(api.ActionFinalize) {
			reason = "MigrationFinalized"
		}
		Block(req.Status, req.Generation, reason, "The irreversible checkpoint permits only resuming "+string(req.Status.IrreversibleAction))
		return Result{}, nil
	}
	// Check before the completed-action shortcut as older stored objects may
	// contain rollback checkpoints from before infrastructure rollback was removed.
	if req.Stage == Infrastructure && req.Action == api.ActionRollback {
		Block(req.Status, req.Generation, "InfrastructureRollbackUnsupported", "Infrastructure Rollback is unsupported; retry the failed forward action after remediation or use WCP backup and restore")
		return Result{}, nil
	}
	// Validate scope before accepting a completed checkpoint, too.
	if (req.Action == api.ActionCommit && req.Stage != Namespaces) || (req.Action == api.ActionFinalize && req.Stage != Infrastructure) {
		Block(req.Status, req.Generation, "InvalidAction", "Requested action is not supported by this transition kind")
		return Result{}, nil
	}
	if (req.Action == api.ActionCommit || req.Action == api.ActionFinalize) &&
		req.Status.LastAction == req.Action && req.Status.ActionState == api.ActionStateSucceeded && req.Status.IrreversibleAction != string(req.Action) {
		// Do not infer the point of no return from a success flag or replay
		// destructive work when its durable checkpoint is missing.
		Block(req.Status, req.Generation, "MissingIrreversibleCheckpoint", "Completed irreversible action has no persisted checkpoint; recovery requires investigation")
		commitCondition(req, metav1.ConditionFalse, "MissingIrreversibleCheckpoint", "Namespace commit completion cannot be verified without its irreversible checkpoint")
		return Result{}, nil
	}
	if req.Status.ActionState == api.ActionStateSucceeded && req.Status.ActionGeneration == req.Generation && req.Status.LastAction == req.Action {
		req.Status.Phase = completedPhase(req.Stage, req.Action)
		commitCondition(req, metav1.ConditionTrue, "CommitCompleted", "Namespace migration commit completed")
		infraCondition(req, metav1.ConditionTrue, "CutoverVerified", "Control plane infrastructure transition completed")
		condition(req.Status, req.Generation, metav1.ConditionTrue, "ActionCompleted", "Requested migration action completed")
		return Result{Outcome: OutcomeSucceeded}, nil
	}
	if !allowed(req.Stage, req.Action, req.Status.LastAction, req.Status.ActionState) {
		Block(req.Status, req.Generation, "InvalidAction", "Requested action is not permitted by the last action and its execution state")
		return Result{}, nil
	}
	handler := e.Handlers[Key{Stage: req.Stage, Action: req.Action}]
	if handler == nil {
		Block(req.Status, req.Generation, "ActionNotImplemented", "Migration service for the requested action is not implemented")
		return Result{}, nil
	}
	if req.Status.LastAction != req.Action || (req.Status.ActionState != api.ActionStateRunning && req.Status.ActionState != api.ActionStateFailed) || req.Status.ActionGeneration != req.Generation {
		// Return before invoking the handler so the controller persists the
		// checkpoint even if the process crashes during external provisioning.
		req.Status.LastAction = req.Action
		req.Status.ActionState = api.ActionStateRunning
		req.Status.ActionGeneration = req.Generation
		req.Status.Phase = activePhase(req.Action)
		infraCondition(req, metav1.ConditionFalse, "CutoverInProgress", "Control plane infrastructure transition is in progress")
		commitCondition(req, metav1.ConditionFalse, "CommitInProgress", "Namespace commit is checkpointed for prerequisite verification")
		condition(req.Status, req.Generation, metav1.ConditionFalse, "InProgress", "Migration action is checkpointed for execution")
		return Result{Outcome: OutcomeInProgress, RequeueAfter: time.Second}, nil
	}
	// A failed attempt already has a durable action/generation checkpoint. Retry
	// it directly so another identical failure does not publish Running/Failed
	// transitions or reset the controller's error backoff.
	req.Status.ActionState = api.ActionStateRunning
	req.Status.Phase = activePhase(req.Action)
	irreversible := req.Status.IrreversibleAction
	result, err := handler.Reconcile(ctx, req)
	if req.Status.IrreversibleAction != irreversible {
		// A handler receives mutable status for progress reporting, but cannot
		// create or erase the boundary that authorizes destructive work. Restore
		// the durable value even on failure so recovery remains possible.
		req.Status.IrreversibleAction = irreversible
		err = errors.Join(err, fmt.Errorf("handler must not mutate irreversibleAction; return CheckpointIrreversible to request persistence"))
	}
	if err == nil && result.Outcome != OutcomeSucceeded && result.Outcome != OutcomeInProgress && result.Outcome != OutcomeNeedsInput && result.Outcome != OutcomeCheckpointIrreversible {
		err = fmt.Errorf("handler returned invalid outcome %q", result.Outcome)
	}
	if err == nil && result.Outcome == OutcomeCheckpointIrreversible && req.Action != api.ActionCommit && req.Action != api.ActionFinalize {
		err = fmt.Errorf("action %s cannot establish an irreversible checkpoint", req.Action)
	}
	if err == nil && result.Outcome == OutcomeSucceeded && (req.Action == api.ActionCommit || req.Action == api.ActionFinalize) && irreversible != string(req.Action) {
		err = fmt.Errorf("action %s cannot succeed without a persisted irreversible checkpoint", req.Action)
	}

	if err != nil {
		req.Status.ActionState = api.ActionStateFailed
		req.Status.Phase = "Failed"
		if req.Action == api.ActionEvaluate {
			req.Status.Phase = "EvaluationFailed"
		}
		infraCondition(req, metav1.ConditionFalse, "CutoverFailed", "Control plane infrastructure transition failed")
		if req.Action == api.ActionCommit {
			req.Status.Phase = "Committing"
			commitCondition(req, metav1.ConditionFalse, "CommitFailed", "Namespace commit failed; resume Commit from its recorded workflow identities")
		}
		condition(req.Status, req.Generation, metav1.ConditionFalse, "ActionFailed", failureMessage(req.Action, err))
		return Result{}, fmt.Errorf("reconciling %s %s: %w", req.Stage, req.Action, err)
	}
	switch result.Outcome {
	case OutcomeCheckpointIrreversible:
		req.Status.IrreversibleAction = string(req.Action)
		req.Status.ActionState = api.ActionStateRunning
		req.Status.Phase = activePhase(req.Action)
		infraCondition(req, metav1.ConditionFalse, "CutoverInProgress", "Control plane infrastructure transition is in progress")
		commitCondition(req, metav1.ConditionFalse, "CommitInProgress", "Namespace commit has crossed its irreversible checkpoint")
		condition(req.Status, req.Generation, metav1.ConditionFalse, "IrreversibleCheckpoint", "Persist the point of no return before irreversible external changes")
		return Result{Outcome: OutcomeInProgress, RequeueAfter: time.Second}, nil
	case OutcomeNeedsInput:
		req.Status.ActionState = api.ActionStateRunning
		req.Status.Phase = "NeedsInput"
		if req.Action == api.ActionCommit {
			req.Status.Phase = "Committing"
			commitCondition(req, metav1.ConditionFalse, "NeedsInput", "Namespace commit requires administrator input; resume Commit")
		}
		condition(req.Status, req.Generation, metav1.ConditionFalse, "NeedsInput", "Supply the administrator inputs listed in requiredInputs")
		return Result{Outcome: OutcomeNeedsInput}, nil
	case OutcomeInProgress:
		req.Status.ActionState = api.ActionStateRunning
		req.Status.Phase = activePhase(req.Action)
		infraCondition(req, metav1.ConditionFalse, "CutoverInProgress", "Control plane infrastructure transition is in progress")
		commitCondition(req, metav1.ConditionFalse, "CommitInProgress", "Namespace commit is in progress; inspect irreversibleAction for rollback eligibility")
		condition(req.Status, req.Generation, metav1.ConditionFalse, "InProgress", "Migration action is in progress")
		if result.RequeueAfter <= 0 {
			result.RequeueAfter = 30 * time.Second
		}
		return result, nil
	}
	req.Status.RequiredInputs = nil
	req.Status.ActionState = api.ActionStateSucceeded
	req.Status.LastAction = req.Action
	req.Status.ActionGeneration = req.Generation
	req.Status.Phase = completedPhase(req.Stage, req.Action)
	commitCondition(req, metav1.ConditionTrue, "CommitCompleted", "Namespace migration commit completed")
	infraCondition(req, metav1.ConditionTrue, "CutoverVerified", "Control plane infrastructure transition completed")
	condition(req.Status, req.Generation, metav1.ConditionTrue, "ActionCompleted", "Requested migration action completed")
	return Result{Outcome: OutcomeSucceeded}, nil
}

// failureMessage keeps handler context within the Kubernetes condition message limit.
func failureMessage(action api.Action, err error) string {
	message := fmt.Sprintf("%s failed: %v", action, err)
	const maxMessageBytes = 32768
	if len(message) > maxMessageBytes {
		return strings.ToValidUTF8(message[:maxMessageBytes-3], "") + "..."
	}
	return message
}

// Block records a current prerequisite failure while preserving the last action and its execution state.
func Block(status *api.TransitionStatus, generation int64, reason, message string) {
	observeGeneration(status, generation)
	status.Phase = "Blocked"
	condition(status, generation, metav1.ConditionFalse, reason, message)
}

// Input diagnostics describe the inspected spec generation. Discard old hints
// before advancing freshness; handlers may publish revalidated hints afterward.
func observeGeneration(status *api.TransitionStatus, generation int64) {
	if status.ObservedGeneration != generation {
		status.RequiredInputs = nil
		status.OptionalInputs = nil
	}
	status.ObservedGeneration = generation
}

func condition(status *api.TransitionStatus, generation int64, value metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&status.Conditions, metav1.Condition{Type: "Ready", Status: value,
		ObservedGeneration: generation, Reason: reason, Message: message})
}

// infraCondition exposes the design's Stage 1 cutover gate for dependent controllers.
func infraCondition(req Request, value metav1.ConditionStatus, reason, message string) {
	if req.Stage == Infrastructure && req.Action == api.ActionExecute {
		meta.SetStatusCondition(&req.Status.Conditions, metav1.Condition{Type: "InfraTransitionComplete", Status: value,
			ObservedGeneration: req.Generation, Reason: reason, Message: message})
	}
}

// commitCondition records batch commit progress without claiming external effects
// until a registered handler confirms that every required workflow is committed.
func commitCondition(req Request, value metav1.ConditionStatus, reason, message string) {
	if req.Stage == Namespaces && req.Action == api.ActionCommit {
		meta.SetStatusCondition(&req.Status.Conditions, metav1.Condition{Type: "NamespaceMigrationCommitted", Status: value,
			ObservedGeneration: req.Generation, Reason: reason, Message: message})
	}
}

func allowed(stage Stage, action, last api.Action, state api.ActionState) bool {
	if (action == api.ActionFinalize && stage != Infrastructure) || ((action == api.ActionCommit || action == api.ActionRollback) && stage != Namespaces) {
		return false
	}
	if last == "" {
		return state == "" && action == api.ActionEvaluate
	}
	switch state {
	case api.ActionStateRunning, api.ActionStateFailed:
		return action == last || (action == api.ActionRollback &&
			(last == api.ActionEvaluate || last == api.ActionPlan || last == api.ActionDerive || last == api.ActionExecute || last == api.ActionCommit || last == api.ActionFinalize))
	case api.ActionStateSucceeded:
		switch action {
		case api.ActionEvaluate:
			return last == api.ActionEvaluate || last == api.ActionRollback
		case api.ActionPlan:
			return last == api.ActionEvaluate || last == api.ActionPlan
		case api.ActionDerive:
			return last == api.ActionPlan || last == api.ActionDerive
		case api.ActionExecute:
			return last == api.ActionDerive || last == api.ActionExecute
		case api.ActionRollback:
			return last == api.ActionEvaluate || last == api.ActionPlan || last == api.ActionDerive || last == api.ActionExecute || last == api.ActionRollback || last == api.ActionCommit || last == api.ActionFinalize
		case api.ActionCommit:
			return last == api.ActionExecute || last == api.ActionCommit
		case api.ActionFinalize:
			return last == api.ActionExecute || last == api.ActionFinalize
		}
	}
	return false
}

func activePhase(action api.Action) string {
	return map[api.Action]string{api.ActionEvaluate: "Evaluating", api.ActionPlan: "Planning", api.ActionDerive: "Deriving",
		api.ActionExecute: "Executing", api.ActionRollback: "RollingBack", api.ActionCommit: "Committing", api.ActionFinalize: "Finalizing"}[action]
}

func completedPhase(stage Stage, action api.Action) string {
	if action == api.ActionExecute && stage == Infrastructure {
		return "InfraTransitionComplete"
	}
	return map[api.Action]string{api.ActionEvaluate: "Evaluated", api.ActionPlan: "PlanReady", api.ActionDerive: "Derived",
		api.ActionExecute: "Completed", api.ActionRollback: "RolledBack", api.ActionCommit: "Committed", api.ActionFinalize: "Finalized"}[action]
}
