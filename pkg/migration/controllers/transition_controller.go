/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Package controllers wires stage-specific transitions to the shared engine.
package controllers

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/transition"
)

// Reconciler adapts either cluster-scoped transition kind to the shared engine.
type Reconciler struct {
	client.Client
	// APIReader bypasses the cache for dependency and checkpoint decisions.
	APIReader client.Reader
	Engine    *transition.Engine
	Stage     transition.Stage
}

// +kubebuilder:rbac:groups=migration.network.vmware.com,resources=controlplanenetworktransitions;namespacenetworktransitions,verbs=get;list;watch
// +kubebuilder:rbac:groups=migration.network.vmware.com,resources=controlplanenetworktransitions/status;namespacenetworktransitions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;create;update,namespace=vmware-system-nsx
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile persists observed status separately from desired state.
func (r *Reconciler) Reconcile(ctx context.Context, key ctrl.Request) (ctrl.Result, error) {
	unlock := r.Engine.LockReconcile()
	defer unlock()
	var obj client.Object
	switch r.Stage {
	case transition.Infrastructure:
		obj = &api.ControlPlaneNetworkTransition{}
	case transition.Namespaces:
		obj = &api.NamespaceNetworkTransition{}
	default:
		return ctrl.Result{}, fmt.Errorf("unsupported transition stage %q", r.Stage)
	}
	if err := r.reader().Get(ctx, key.NamespacedName, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !obj.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, nil
	}
	previous := obj.DeepCopyObject()
	var status *api.TransitionStatus
	var action api.Action
	switch resource := obj.(type) {
	case *api.ControlPlaneNetworkTransition:
		if resource.Status == nil {
			resource.Status = &api.ControlPlaneNetworkTransitionStatus{}
		}
		status, action = &resource.Status.TransitionStatus, resource.Spec.Action
	case *api.NamespaceNetworkTransition:
		if resource.Status == nil {
			resource.Status = &api.NamespaceNetworkTransitionStatus{}
		}
		status, action = &resource.Status.TransitionStatus, resource.Spec.Action
	}
	// Terminal batch records are retained for audit after infrastructure finalization.
	// New requests still pass dependency gates; recovery uses action-specific checks.
	if isBatchActionCompleted(r.Stage, status, action, obj.GetGeneration()) {
		return ctrl.Result{}, nil
	}
	request := transition.Request{Stage: r.Stage, Object: obj, Action: action, Generation: obj.GetGeneration(), Status: status}
	var result transition.Result
	var reconcileErr error
	blocked, err := r.dependencyBlock(ctx, action, status)
	if err != nil {
		return ctrl.Result{}, err
	}
	if r.Engine.Enabled && blocked.IsBlocked() {
		transition.Block(status, obj.GetGeneration(), string(blocked.Reason), blocked.Message)
		result.RequeueAfter = 30 * time.Second
	} else {
		if err := r.checkCurrentRequest(ctx, request); err != nil {
			return ctrl.Result{}, err
		}
		if r.Engine.Enabled && r.Stage == transition.Namespaces && action == api.ActionExecute &&
			r.Engine.Handlers[transition.Key{Stage: transition.Namespaces, Action: api.ActionExecute}] != nil {
			waiting, err := r.namespaceExecuteBlock(ctx, obj.(*api.NamespaceNetworkTransition), status)
			if err != nil {
				return ctrl.Result{}, err
			}
			if waiting != "" {
				transition.Block(status, obj.GetGeneration(), "ExecuteInProgress", waiting)
				result.RequeueAfter = 30 * time.Second
			} else {
				result, reconcileErr = r.Engine.Reconcile(ctx, request)
			}
		} else if r.Engine.Enabled && r.Stage == transition.Namespaces && action == api.ActionRollback &&
			r.Engine.Handlers[transition.Key{Stage: transition.Namespaces, Action: api.ActionRollback}] != nil {
			waiting, err := r.namespaceRollbackBlock(ctx, obj.(*api.NamespaceNetworkTransition))
			if err != nil {
				return ctrl.Result{}, err
			}
			if waiting != "" {
				transition.Block(status, obj.GetGeneration(), "ExecuteInProgress", waiting)
				result.RequeueAfter = 30 * time.Second
			} else {
				result, reconcileErr = r.Engine.Reconcile(ctx, request)
			}
		} else {
			result, reconcileErr = r.Engine.Reconcile(ctx, request)
		}
	}
	if !equality.Semantic.DeepEqual(previous, obj) {
		if err := r.Status().Update(ctx, obj); err != nil {
			return ctrl.Result{}, fmt.Errorf("persisting transition status: %w", err)
		}
	}
	return ctrl.Result{RequeueAfter: result.RequeueAfter}, reconcileErr
}

// namespaceExecuteBlock closes the race in admission's list check. The reconcile
// lock stays held through this live read, the Execute checkpoint, and its status
// write, so simultaneous requests cannot both claim the namespace Execute slot.
func (r *Reconciler) namespaceExecuteBlock(ctx context.Context, current *api.NamespaceNetworkTransition, status *api.TransitionStatus) (string, error) {
	currentStarted := namespaceExecuteStarted(status, current.Generation)
	var namespaces api.NamespaceNetworkTransitionList
	if err := r.reader().List(ctx, &namespaces); err != nil {
		return "", fmt.Errorf("listing NamespaceNetworkTransition objects to check Execute exclusivity: %w", err)
	}
	for i := range namespaces.Items {
		other := &namespaces.Items[i]
		if other.Name == current.Name || !other.DeletionTimestamp.IsZero() {
			continue
		}
		var otherStatus *api.TransitionStatus
		if other.Status != nil {
			otherStatus = &other.Status.TransitionStatus
		}
		if other.Spec.Action == api.ActionRollback && !completedRollback(otherStatus, other.Generation) {
			return fmt.Sprintf("NamespaceNetworkTransition %q has an unfinished Rollback; %q must wait", other.Name, current.Name), nil
		}
		if other.Spec.Action != api.ActionExecute {
			continue
		}
		if namespaceExecuteStarted(otherStatus, other.Generation) {
			return fmt.Sprintf("NamespaceNetworkTransition %q has an unfinished Execute; %q must wait", other.Name, current.Name), nil
		}
		if completedExecute(otherStatus, other.Generation) {
			continue
		}
		// Both requests may have passed admission before either status checkpoint
		// was written. Let exactly one stable name claim the slot.
		if !currentStarted && canRequestExecute(otherStatus) && other.Name < current.Name {
			return fmt.Sprintf("NamespaceNetworkTransition %q has priority under simultaneous Execute requests; %q must wait", other.Name, current.Name), nil
		}
	}
	return "", nil
}

// namespaceRollbackBlock prevents Rollback from overlapping another batch's
// active Execute. The shared reconcile lock ensures that one pending action
// persists its checkpoint before another request is arbitrated.
func (r *Reconciler) namespaceRollbackBlock(ctx context.Context, current *api.NamespaceNetworkTransition) (string, error) {
	var namespaces api.NamespaceNetworkTransitionList
	if err := r.reader().List(ctx, &namespaces); err != nil {
		return "", fmt.Errorf("listing NamespaceNetworkTransition objects to check Rollback exclusivity: %w", err)
	}
	for i := range namespaces.Items {
		other := &namespaces.Items[i]
		if other.Name == current.Name || !other.DeletionTimestamp.IsZero() || other.Spec.Action != api.ActionExecute {
			continue
		}
		var otherStatus *api.TransitionStatus
		if other.Status != nil {
			otherStatus = &other.Status.TransitionStatus
		}
		if namespaceExecuteStarted(otherStatus, other.Generation) {
			return fmt.Sprintf("NamespaceNetworkTransition %q has an unfinished Execute; Rollback for %q must wait", other.Name, current.Name), nil
		}
	}
	return "", nil
}

func canRequestExecute(status *api.TransitionStatus) bool {
	if status == nil {
		return false
	}
	if status.LastAction == api.ActionEvaluate && status.ActionState == api.ActionStateSucceeded {
		return true
	}
	return status.LastAction == api.ActionExecute &&
		(status.ActionState == api.ActionStateRunning || status.ActionState == api.ActionStateFailed || status.ActionState == api.ActionStateSucceeded)
}

func namespaceExecuteStarted(status *api.TransitionStatus, generation int64) bool {
	return status != nil && status.LastAction == api.ActionExecute && status.ActionGeneration == generation &&
		(status.ActionState == api.ActionStateRunning || status.ActionState == api.ActionStateFailed)
}

func completedExecute(status *api.TransitionStatus, generation int64) bool {
	return status != nil && status.LastAction == api.ActionExecute && status.ActionState == api.ActionStateSucceeded &&
		status.ActionGeneration == generation && terminalNamespaceExecutePhase(status.Phase)
}

func completedRollback(status *api.TransitionStatus, generation int64) bool {
	return status != nil && status.LastAction == api.ActionRollback && status.ActionState == api.ActionStateSucceeded &&
		status.ActionGeneration == generation && status.Phase == api.PhaseRolledBack
}

func terminalNamespaceExecutePhase(phase string) bool {
	switch phase {
	case api.PhaseCompleted, api.PhaseCommitted, api.PhaseRolledBack:
		return true
	default:
		return false
	}
}

func (r *Reconciler) reader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

// BlockReason identifies why a transition is waiting on a dependency.
type BlockReason string

const (
	ReasonNone                   BlockReason = ""
	ReasonDependencyNotReady     BlockReason = "DependencyNotReady"
	ReasonDependencyNotClosed    BlockReason = "DependencyNotClosed"
	ReasonInfrastructureNotReady BlockReason = "InfrastructureNotReady"
)

// DependencyBlock describes an unsatisfied cross-transition prerequisite.
type DependencyBlock struct {
	Reason  BlockReason
	Message string
}

func (b *DependencyBlock) IsBlocked() bool {
	return b != nil && b.Reason != ReasonNone
}

// isBatchActionCompleted recognizes a terminal action for the current generation.
func isBatchActionCompleted(stage transition.Stage, status *api.TransitionStatus, action api.Action, generation int64) bool {
	if stage != transition.Namespaces || status == nil ||
		status.ActionState != api.ActionStateSucceeded || status.ActionGeneration != generation || status.LastAction != action {
		return false
	}
	switch action {
	case api.ActionExecute:
		return status.Phase == "Completed"
	case api.ActionRollback:
		return status.Phase == "RolledBack"
	case api.ActionCommit:
		return status.Phase == "Committed" && status.IrreversibleAction == string(api.ActionCommit)
	default:
		return false
	}
}

// dependencyBlock checks durable CR records. Mutating handlers must repeat live
// ownership, inventory, and dependency checks immediately before external changes.
func (r *Reconciler) dependencyBlock(ctx context.Context, action api.Action, status *api.TransitionStatus) (*DependencyBlock, error) {
	if !r.Engine.Enabled {
		return nil, nil
	}
	if r.Stage == transition.Infrastructure {
		if status.IrreversibleAction == string(action) {
			return nil, nil // An accepted irreversible operation must remain recoverable.
		}
		if action != api.ActionFinalize {
			return nil, nil
		}
		var batches api.NamespaceNetworkTransitionList
		if err := r.reader().List(ctx, &batches); err != nil {
			return nil, fmt.Errorf("listing namespace transition dependencies: %w", err)
		}
		for i := range batches.Items {
			batch := &batches.Items[i]
			var s *api.TransitionStatus
			if batch.Status != nil {
				s = &batch.Status.TransitionStatus
			}
			rolledBack := batch.Status != nil && batch.Status.RollbackComplete && batchRollbackCheckpointApplies(s, batch.Spec.Action, batch.Generation)
			committed := isBatchActionCompleted(transition.Namespaces, s, api.ActionCommit, batch.Generation) && batch.Spec.Action == api.ActionCommit
			// A fully restored historical namespace batch does not require commit.
			if !batch.DeletionTimestamp.IsZero() || (!rolledBack && !committed) {
				return &DependencyBlock{Reason: ReasonDependencyNotClosed, Message: "Namespace batch " + batch.Name + " must be committed or fully rolled back before infrastructure finalization"}, nil
			}
		}
		return nil, nil
	}
	if action == api.ActionCommit && status.IrreversibleAction == string(api.ActionCommit) {
		return nil, nil // Irreversible commit recovery must not be stranded by Stage 1 changes.
	}
	if action == api.ActionRollback {
		// Namespace recovery may proceed while infrastructure is failed,
		// but not after infrastructure crossed its irreversible boundary.
		var infra api.ControlPlaneNetworkTransition
		if err := r.reader().Get(ctx, types.NamespacedName{Name: api.ControlPlaneNetworkTransitionName}, &infra); err != nil {
			if apierrors.IsNotFound(err) {
				return &DependencyBlock{Reason: ReasonDependencyNotReady, Message: "Infrastructure transition is missing; rollback safety cannot be verified"}, nil
			}
			return nil, err
		}
		if !infra.DeletionTimestamp.IsZero() || (infra.Status != nil && (infra.Status.IrreversibleAction != "" || (infra.Status.LastAction == api.ActionFinalize && infra.Status.ActionState == api.ActionStateSucceeded))) {
			return &DependencyBlock{Reason: ReasonDependencyNotReady, Message: "Infrastructure is being deleted or finalized; namespace rollback is unsafe"}, nil
		}
		return nil, nil
	}
	ready, err := r.infrastructureReady(ctx)
	if err != nil {
		return nil, err
	}
	if !ready {
		return &DependencyBlock{Reason: ReasonInfrastructureNotReady, Message: "Control plane network transition must complete before namespace migration"}, nil
	}
	return nil, nil
}

// batchRollbackCheckpointApplies keeps the restored checkpoint valid during
// Evaluate, but not after a new mutating action is requested. A completed
// Rollback remains valid for its own generation.
func batchRollbackCheckpointApplies(status *api.TransitionStatus, action api.Action, generation int64) bool {
	switch action {
	case api.ActionEvaluate:
		return true
	case api.ActionRollback:
		return isBatchActionCompleted(transition.Namespaces, status, api.ActionRollback, generation)
	default:
		return false
	}
}

func (r *Reconciler) infrastructureReady(ctx context.Context) (bool, error) {
	var resource api.ControlPlaneNetworkTransition
	if err := r.reader().Get(ctx, types.NamespacedName{Name: api.ControlPlaneNetworkTransitionName}, &resource); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("getting infrastructure transition: %w", err)
	}
	status := resource.Status
	if !resource.DeletionTimestamp.IsZero() || status == nil {
		return false, nil
	}
	// An early Finalize request may be waiting for namespace batches. Until it
	// is accepted, the last successful Execute still supplies their infrastructure.
	// Otherwise marking Finalize blocked would deadlock the batches it awaits.
	if resource.Spec.Action == api.ActionFinalize && status.LastAction == api.ActionExecute &&
		status.ActionState == api.ActionStateSucceeded && status.ActionGeneration < resource.Generation && status.IrreversibleAction == "" &&
		meta.IsStatusConditionTrue(status.Conditions, "InfraTransitionComplete") {
		return true, nil
	}
	return status.ActionState == api.ActionStateSucceeded &&
		status.ActionGeneration == resource.Generation && status.LastAction == api.ActionExecute &&
		status.Phase == "InfraTransitionComplete" && meta.IsStatusConditionTrue(status.Conditions, "InfraTransitionComplete"), nil
}

// transitionEvents ignores controller-owned status updates. Checkpoints and
// progress use explicit requeues; errors retain the work queue's retry backoff.
// Metadata updates still wake input waits and lifecycle reconciliation.
func transitionEvents() predicate.Predicate {
	return predicate.Funcs{UpdateFunc: func(e event.UpdateEvent) bool {
		if e.ObjectOld == nil || e.ObjectNew == nil {
			return false
		}
		old, current := e.ObjectOld, e.ObjectNew
		return old.GetUID() != current.GetUID() || old.GetGeneration() != current.GetGeneration() ||
			!equality.Semantic.DeepEqual(old.GetDeletionTimestamp(), current.GetDeletionTimestamp()) ||
			!equality.Semantic.DeepEqual(old.GetFinalizers(), current.GetFinalizers()) ||
			!equality.Semantic.DeepEqual(old.GetAnnotations(), current.GetAnnotations()) ||
			!equality.Semantic.DeepEqual(old.GetLabels(), current.GetLabels())
	}}
}

// SetupWithManager registers the stage's independent work queue.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	switch r.Stage {
	case transition.Infrastructure:
		return ctrl.NewControllerManagedBy(mgr).WithEventFilter(transitionEvents()).For(&api.ControlPlaneNetworkTransition{}).Complete(r)
	case transition.Namespaces:
		return ctrl.NewControllerManagedBy(mgr).WithEventFilter(transitionEvents()).For(&api.NamespaceNetworkTransition{}).Complete(r)
	default:
		return fmt.Errorf("unsupported transition stage %q", r.Stage)
	}
}

// checkCurrentRequest avoids dispatching an outdated or deleting transition.
func (r *Reconciler) checkCurrentRequest(ctx context.Context, req transition.Request) error {
	// Re-read intent after action acceptance, before dispatching a handler. Concrete
	// handlers must repeat relevant identity/state checks before external mutations.
	live := req.Object.DeepCopyObject().(client.Object)
	if err := r.reader().Get(ctx, client.ObjectKeyFromObject(req.Object), live); err != nil {
		return err
	}
	var action api.Action
	switch v := live.(type) {
	case *api.ControlPlaneNetworkTransition:
		action = v.Spec.Action
	case *api.NamespaceNetworkTransition:
		action = v.Spec.Action
	default:
		return fmt.Errorf("unsupported transition kind %T", live)
	}
	if live.GetUID() != req.Object.GetUID() || live.GetResourceVersion() != req.Object.GetResourceVersion() ||
		live.GetGeneration() != req.Generation || action != req.Action || !live.GetDeletionTimestamp().IsZero() {
		return fmt.Errorf("transition changed before handler dispatch")
	}
	return nil
}
