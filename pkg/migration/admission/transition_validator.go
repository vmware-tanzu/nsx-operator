/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Package admission validates migration requests before they are persisted.
package admission

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

const TransitionValidationPath = "/validate-migration-transition"

// TransitionValidator validates transition action prerequisites using the old object
// supplied by the API server. Existing CRD validation freezes input changes.
type TransitionValidator struct{}

func (TransitionValidator) Handle(_ context.Context, req admission.Request) admission.Response {
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
	isNamespace := false
	switch req.Resource.Resource {
	case "controlplanenetworktransitions":
		var oldObj, newObj api.ControlPlaneNetworkTransition
		if err := decodeObjects(req, &oldObj, &newObj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		oldAction, newAction, oldGeneration = oldObj.Spec.Action, newObj.Spec.Action, oldObj.Generation
		if oldObj.Status != nil {
			status = &oldObj.Status.TransitionStatus
		}
	case "namespacenetworktransitions":
		isNamespace = true
		var oldObj, newObj api.NamespaceNetworkTransition
		if err := decodeObjects(req, &oldObj, &newObj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		oldAction, newAction, oldGeneration = oldObj.Spec.Action, newObj.Spec.Action, oldObj.Generation
		if oldObj.Status != nil {
			status = &oldObj.Status.TransitionStatus
		}
	default:
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("unexpected migration resource %q", req.Resource.Resource))
	}
	if req.Operation == admissionv1.Delete {
		// A requested Execute/Rollback with no matching current checkpoint
		// cannot be distinguished from an in-flight or lost status update by
		// this single-object guard. Preserve the transition for recovery.
		if (oldAction == api.ActionExecute || oldAction == api.ActionRollback) &&
			(status == nil || status.LastAction != oldAction || oldGeneration < 1 || status.ActionGeneration != oldGeneration) {
			return admission.Denied("Resolve the unfinished Execute or Rollback before deleting the transition")
		}
		if status != nil && (status.LastAction == api.ActionExecute || status.LastAction == api.ActionRollback) {
			completedPhase := "Completed"
			if status.LastAction == api.ActionRollback {
				completedPhase = "RolledBack"
			} else if !isNamespace {
				completedPhase = "InfraTransitionComplete"
			}
			if status.ActionState != api.ActionStateSucceeded || status.ActionGeneration < 1 || status.Phase != completedPhase {
				return admission.Denied("Resolve the unfinished Execute or Rollback before deleting the transition")
			}
		}
		return admission.Allowed("no unfinished Execute or Rollback checkpoint")
	}
	if req.Operation == admissionv1.Create {
		if newAction != api.ActionEvaluate {
			return admission.Denied("A transition must start with Evaluate")
		}
		return admission.Allowed("new transition starts with Evaluate")
	}
	// Status-only updates and unchanged actions must remain recoverable even
	// when the stored request is blocked or has a historical checkpoint.
	if oldAction == newAction {
		return admission.Allowed("action unchanged")
	}
	if oldAction == api.ActionEvaluate && newAction == api.ActionPlan {
		if oldGeneration < 1 || status == nil || status.LastAction != api.ActionEvaluate ||
			status.ActionState != api.ActionStateSucceeded || status.ActionGeneration != oldGeneration ||
			status.ObservedGeneration != oldGeneration {
			return admission.Denied("Complete Evaluate successfully for the current inputs before requesting Plan")
		}
		return admission.Allowed("current Evaluate succeeded")
	}

	// Namespace rollback may be requested during or after an earlier action,
	// including while Plan is pending. The existing CRD guards the irreversible
	// checkpoint; actual rollback safety is checked by the recovery handler.
	if newAction == api.ActionRollback && isNamespace && status != nil &&
		status.LastAction != "" && status.IrreversibleAction == "" &&
		(status.ActionState == api.ActionStateRunning || status.ActionState == api.ActionStateFailed || status.ActionState == api.ActionStateSucceeded) {
		return admission.Allowed("namespace rollback requested")
	}

	var prerequisite api.Action
	switch newAction {
	case api.ActionDerive:
		prerequisite = api.ActionPlan
	case api.ActionExecute:
		prerequisite = api.ActionDerive
	case api.ActionCommit:
		if isNamespace {
			prerequisite = api.ActionExecute
		}
	case api.ActionFinalize:
		if !isNamespace {
			prerequisite = api.ActionExecute
		}
	case api.ActionEvaluate:
		if isNamespace {
			prerequisite = api.ActionRollback
		}
	}
	if prerequisite == "" || oldAction != prerequisite || status == nil ||
		status.LastAction != prerequisite || status.ActionState != api.ActionStateSucceeded ||
		oldGeneration < 1 || status.ActionGeneration != oldGeneration ||
		(newAction == api.ActionEvaluate && status.IrreversibleAction != "") {
		return admission.Denied(fmt.Sprintf("Complete the prerequisite for %s before changing the requested action", newAction))
	}
	return admission.Allowed("action prerequisite succeeded")
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
