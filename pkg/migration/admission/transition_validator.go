/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Package admission validates migration requests before they are persisted.
package admission

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	admissionv1 "k8s.io/api/admission/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/namespaces"
)

const TransitionValidationPath = "/validate-migration-transition"

// TransitionValidator validates transition action prerequisites using the old object
// supplied by the API server. Reader is used to find conflicting Execute requests
// across both transition kinds before the new action is persisted.
type TransitionValidator struct {
	Reader client.Reader
}

func (v TransitionValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	// The action edge belongs to the main resource. Status writes and other
	// operations must not be gated by the previous Evaluate checkpoint.
	if (req.Operation != admissionv1.Update && req.Operation != admissionv1.Create && req.Operation != admissionv1.Delete) || req.SubResource != "" {
		return admission.Allowed("not an action update")
	}
	if req.Resource.Group != api.GroupVersion.Group || req.Resource.Version != api.GroupVersion.Version {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("unexpected migration API resource %s", req.Resource.String()))
	}
	var oldAction, newAction api.Action
	var oldGeneration int64
	var status *api.TransitionStatus
	var currentUID types.UID
	isNamespace := false
	hasAcceptedSNATAllocation := false
	rollbackComplete := false
	var namespaceNames []string
	switch req.Resource.Resource {
	case "controlplanenetworktransitions":
		var oldObj, newObj api.ControlPlaneNetworkTransition
		if err := decodeObjects(req, &oldObj, &newObj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		oldAction, newAction, oldGeneration, currentUID = oldObj.Spec.Action, newObj.Spec.Action, oldObj.Generation, oldObj.UID
		if oldObj.Status != nil {
			status = &oldObj.Status.TransitionStatus
			hasAcceptedSNATAllocation = len(oldObj.Status.LBSNATIPAllocations) != 0
		}
	case "namespacenetworktransitions":
		isNamespace = true
		var oldObj, newObj api.NamespaceNetworkTransition
		if err := decodeObjects(req, &oldObj, &newObj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		oldAction, newAction, oldGeneration, currentUID = oldObj.Spec.Action, newObj.Spec.Action, oldObj.Generation, oldObj.UID
		namespaceNames = newObj.Spec.Namespaces
		if oldObj.Status != nil {
			status = &oldObj.Status.TransitionStatus
			rollbackComplete = oldObj.Status.RollbackComplete
		}
	default:
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("unexpected migration resource %q", req.Resource.Resource))
	}
	if req.Operation == admissionv1.Delete {
		if status == nil || status.LastAction == "" {
			if oldAction == api.ActionExecute || oldAction == api.ActionRollback || oldAction == api.ActionCommit || oldAction == api.ActionFinalize {
				return admission.Denied("Resolve the requested migration action before deleting the transition")
			}
			return admission.Allowed("transition has no action checkpoint")
		}
		if (oldAction == api.ActionExecute || oldAction == api.ActionRollback || oldAction == api.ActionCommit || oldAction == api.ActionFinalize) && status.LastAction != oldAction {
			return admission.Denied("Resolve the requested migration action before deleting the transition")
		}
		if status.LastAction == api.ActionEvaluate {
			if hasAcceptedSNATAllocation {
				return admission.Denied("Resolve the unfinished Evaluate or accepted reservation before deleting the transition")
			}
			if status.Phase == api.PhaseEvaluationFailed && status.ActionState != api.ActionStateRunning {
				// Evaluate failures before Execute can be abandoned when no SNAT
				// allocation was accepted. Refine this check when SNAT allocation
				// handling and the cleanup finalizer are wired, so in-flight or
				// unrecorded preparation resources are accounted for too.
				return admission.Allowed("deleting after failed Evaluate with no accepted SNAT reservation")
			}
			if !currentSuccessfulCheckpoint(status, oldGeneration, api.ActionEvaluate) || len(status.RequiredInputs) != 0 {
				return admission.Denied("Resolve the unfinished Evaluate or accepted reservation before deleting the transition")
			}
			return admission.Allowed("current Evaluate completed without an accepted reservation")
		}
		if !currentSuccessfulCheckpoint(status, oldGeneration, status.LastAction) {
			return admission.Denied("Resolve the unfinished migration action before deleting the transition")
		}
		if isNamespace {
			if status.LastAction == api.ActionCommit && status.IrreversibleAction == string(api.ActionCommit) && status.Phase == api.PhaseCommitted {
				return admission.Allowed("namespace batch is committed")
			}
			if status.LastAction == api.ActionRollback && status.IrreversibleAction == "" && status.Phase == api.PhaseRolledBack {
				return admission.Allowed("namespace batch is fully rolled back")
			}
			return admission.Denied("Namespace inputs remain protected until the batch is committed or fully rolled back")
		}
		if status.LastAction == api.ActionFinalize && status.IrreversibleAction == string(api.ActionFinalize) && status.Phase == api.PhaseFinalized {
			return admission.Allowed("infrastructure transition is finalized")
		}
		return admission.Denied("Infrastructure transition must be finalized before deletion")
	}
	if req.Operation == admissionv1.Create {
		if newAction != api.ActionEvaluate {
			return admission.Denied("A transition must start with Evaluate")
		}
		if isNamespace && len(namespaceNames) != 0 {
			if v.Reader == nil {
				return admission.Errored(http.StatusServiceUnavailable, fmt.Errorf("namespace lifecycle reader is not configured"))
			}
			if err := namespaces.Validate(ctx, v.Reader, namespaceNames); err != nil {
				return admission.Denied(fmt.Sprintf("Namespace batch members are unavailable: %v", err))
			}
		}
		return admission.Allowed("new transition starts with Evaluate")
	}
	// Status-only updates and unchanged actions must remain recoverable even
	// when the stored request is blocked or has a historical checkpoint.
	if oldAction == newAction {
		return admission.Allowed("action unchanged")
	}
	if oldAction == api.ActionEvaluate && newAction == api.ActionExecute {
		if !currentSuccessfulCheckpoint(status, oldGeneration, api.ActionEvaluate) || status.ObservedGeneration != oldGeneration || len(status.RequiredInputs) != 0 {
			return admission.Denied("Complete Evaluate successfully for the current inputs before requesting Execute")
		}
		if v.Reader == nil {
			return admission.Errored(http.StatusServiceUnavailable, fmt.Errorf("migration Execute exclusivity reader is not configured"))
		}
		if currentUID == "" {
			return admission.Errored(http.StatusBadRequest, fmt.Errorf("the existing migration transition has no UID"))
		}
		if isNamespace {
			complete, err := stageOneComplete(ctx, v.Reader)
			if err != nil {
				return admission.Errored(http.StatusServiceUnavailable, fmt.Errorf("checking Stage 1 migration completion: %w", err))
			}
			if !complete {
				return admission.Denied("Stage 1 infrastructure migration must complete before requesting namespace Execute")
			}
		}
		conflict, err := findConflictingExecute(ctx, v.Reader, currentUID)
		if err != nil {
			return admission.Errored(http.StatusServiceUnavailable, fmt.Errorf("checking active migration Execute requests: %w", err))
		}
		if conflict != "" {
			return admission.Denied(fmt.Sprintf("Migration transition %s is already using Execute; wait for it to reach a terminal phase", conflict))
		}
		return admission.Allowed("current Evaluate succeeded")
	}
	if oldAction == api.ActionExecute && newAction == api.ActionRollback && isNamespace && status != nil &&
		status.LastAction == api.ActionExecute && oldGeneration > 0 &&
		status.ActionGeneration == oldGeneration && status.ObservedGeneration == oldGeneration &&
		status.IrreversibleAction == "" &&
		(status.ActionState == api.ActionStateRunning || status.ActionState == api.ActionStateFailed || status.ActionState == api.ActionStateSucceeded) {
		return admission.Allowed("namespace Execute may be rolled back before Commit")
	}
	if oldAction == api.ActionExecute && newAction == api.ActionCommit && isNamespace {
		if currentSuccessfulCheckpoint(status, oldGeneration, api.ActionExecute) && status.Phase == api.PhaseCompleted && status.IrreversibleAction == "" {
			return admission.Allowed("current namespace Execute succeeded")
		}
	}
	if oldAction == api.ActionExecute && newAction == api.ActionFinalize && !isNamespace {
		if currentSuccessfulCheckpoint(status, oldGeneration, api.ActionExecute) && status.Phase == api.PhaseInfraTransitionComplete && status.IrreversibleAction == "" {
			return admission.Allowed("current infrastructure Execute succeeded")
		}
	}
	if oldAction == api.ActionRollback && newAction == api.ActionEvaluate && isNamespace {
		if currentSuccessfulCheckpoint(status, oldGeneration, api.ActionRollback) && status.Phase == api.PhaseRolledBack && rollbackComplete && status.IrreversibleAction == "" {
			return admission.Allowed("whole-batch rollback succeeded; inputs may be evaluated again")
		}
		return admission.Denied("Complete and verify whole-batch Rollback, including rollbackComplete, before requesting Evaluate")
	}
	return admission.Denied(fmt.Sprintf("Illegal action change from %s to %s", oldAction, newAction))
}

func stageOneComplete(ctx context.Context, reader client.Reader) (bool, error) {
	transition := &api.ControlPlaneNetworkTransition{}
	if err := reader.Get(ctx, types.NamespacedName{Name: api.ControlPlaneNetworkTransitionName}, transition); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	if transition.DeletionTimestamp != nil || transition.Status == nil {
		return false, nil
	}
	status := &transition.Status.TransitionStatus
	if status.Phase == api.PhaseInfraTransitionComplete {
		return currentSuccessfulCheckpoint(status, transition.Generation, api.ActionExecute), nil
	}
	if status.Phase == api.PhaseFinalizing || status.Phase == api.PhaseFinalized {
		for _, condition := range status.Conditions {
			if condition.Type == api.ConditionInfraTransitionComplete && condition.Status == "True" &&
				condition.ObservedGeneration > 0 && condition.ObservedGeneration <= transition.Generation {
				return true, nil
			}
		}
	}
	return false, nil
}

func findConflictingExecute(ctx context.Context, reader client.Reader, currentUID types.UID) (string, error) {
	conflicts := make([]string, 0)
	controlPlaneTransitions := &api.ControlPlaneNetworkTransitionList{}
	if err := reader.List(ctx, controlPlaneTransitions); err != nil {
		return "", fmt.Errorf("listing control-plane transitions: %w", err)
	}
	for i := range controlPlaneTransitions.Items {
		other := &controlPlaneTransitions.Items[i]
		var otherStatus *api.TransitionStatus
		if other.Status != nil {
			otherStatus = &other.Status.TransitionStatus
		}
		if other.UID != currentUID && executeOccupiesTransition(other.Spec.Action, otherStatus) {
			conflicts = append(conflicts, "controlplanenetworktransitions/"+other.Name)
		}
	}

	namespaceTransitions := &api.NamespaceNetworkTransitionList{}
	if err := reader.List(ctx, namespaceTransitions); err != nil {
		return "", fmt.Errorf("listing namespace transitions: %w", err)
	}
	for i := range namespaceTransitions.Items {
		other := &namespaceTransitions.Items[i]
		var otherStatus *api.TransitionStatus
		if other.Status != nil {
			otherStatus = &other.Status.TransitionStatus
		}
		if other.UID != currentUID && executeOccupiesTransition(other.Spec.Action, otherStatus) {
			conflicts = append(conflicts, "namespacenetworktransitions/"+other.Name)
		}
	}
	if len(conflicts) == 0 {
		return "", nil
	}
	sort.Strings(conflicts)
	return conflicts[0], nil
}

func executeOccupiesTransition(action api.Action, status *api.TransitionStatus) bool {
	if action == api.ActionExecute && (status == nil || !terminalExecutePhase(status.Phase)) {
		return true
	}
	// Rollback is the recovery path for a partial Execute. Keep the same
	// transition occupied until the whole batch has been verified RolledBack.
	if action == api.ActionRollback && (status == nil || status.Phase != api.PhaseRolledBack) {
		return true
	}
	return status != nil && status.LastAction == api.ActionExecute && status.ActionState == api.ActionStateRunning
}

func terminalExecutePhase(phase string) bool {
	switch phase {
	case api.PhaseInfraTransitionComplete, api.PhaseFinalizing, api.PhaseCompleted, api.PhaseCommitted, api.PhaseRolledBack, api.PhaseFinalized:
		return true
	default:
		return false
	}
}

func currentSuccessfulCheckpoint(status *api.TransitionStatus, generation int64, action api.Action) bool {
	return status != nil && generation > 0 && status.LastAction == action &&
		status.ActionState == api.ActionStateSucceeded && status.ActionGeneration == generation &&
		status.ObservedGeneration == generation
}

func decodeObjects(req admission.Request, oldObj, newObj any) error {
	if (req.Operation != admissionv1.Delete && len(req.Object.Raw) == 0) ||
		(req.Operation != admissionv1.Create && len(req.OldObject.Raw) == 0) {
		return fmt.Errorf("the admission request is missing a required object")
	}
	if req.Operation != admissionv1.Create {
		if err := json.Unmarshal(req.OldObject.Raw, oldObj); err != nil {
			return fmt.Errorf("invalid old object: %w", err)
		}
	}
	if req.Operation != admissionv1.Delete {
		if err := json.Unmarshal(req.Object.Raw, newObj); err != nil {
			return fmt.Errorf("invalid new object: %w", err)
		}
	}
	return nil
}
