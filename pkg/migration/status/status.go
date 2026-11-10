/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Package status provides shared reporting primitives for migration controllers.
// It records reported outcomes; action dispatch and external effects belong to handlers.
package status

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

// Observe records the inspected generation, phase and a condition without
// changing the last accepted action or its execution checkpoint. In particular,
// reporting a blocked request does not turn an older action into a new success.
// SetStatusCondition preserves lastTransitionTime when condition status is unchanged.
func Observe(status *api.TransitionStatus, generation int64, phase string, condition metav1.Condition) error {
	if status == nil {
		return fmt.Errorf("transition status is required")
	}
	if generation < 1 || generation < status.ObservedGeneration || generation < status.ActionGeneration {
		return fmt.Errorf("observed generation %d is invalid or stale", generation)
	}
	if phase == "" || condition.Type == "" || condition.Reason == "" {
		return fmt.Errorf("phase, condition type and reason are required")
	}
	if condition.Status != metav1.ConditionTrue && condition.Status != metav1.ConditionFalse && condition.Status != metav1.ConditionUnknown {
		return fmt.Errorf("invalid condition status %q", condition.Status)
	}
	condition.ObservedGeneration = generation
	status.ObservedGeneration, status.Phase = generation, phase
	meta.SetStatusCondition(&status.Conditions, condition)
	return nil
}

// RecordAction records an action/generation checkpoint and its reported state.
// It does not select an action, advance a phase, or establish an irreversible
// checkpoint. It is shared across CR kinds; typed status writers enforce action scope.
// Rollback is namespace-only. Callers must verify restoration and invalidate the
// previous plan before recording Rollback/Succeeded, which reopens input editing.
// Callers must persist Running before executing a newly accepted action.
func RecordAction(status *api.TransitionStatus, action api.Action, state api.ActionState, generation int64) error {
	if status == nil {
		return fmt.Errorf("transition status is required")
	}
	switch action {
	case api.ActionEvaluate, api.ActionPlan, api.ActionDerive, api.ActionExecute, api.ActionRollback, api.ActionCommit, api.ActionFinalize:
	default:
		return fmt.Errorf("invalid action %q", action)
	}
	switch state {
	case api.ActionStateRunning, api.ActionStateSucceeded, api.ActionStateFailed:
	default:
		return fmt.Errorf("invalid action state %q", state)
	}
	if generation < 1 || generation < status.ActionGeneration || generation < status.ObservedGeneration {
		return fmt.Errorf("action generation %d is invalid or stale", generation)
	}
	if status.IrreversibleAction != "" && status.IrreversibleAction != string(action) {
		return fmt.Errorf("only irreversible action %s may resume", status.IrreversibleAction)
	}
	status.LastAction, status.ActionState, status.ActionGeneration = action, state, generation
	status.ObservedGeneration = generation
	return nil
}

// RecordWorkflow preserves the first accepted NSX handle for each source VDN.
// Replaying the same handle is a no-op; replacing it requires a separately
// defined recovery/reset operation, rather than silently losing rollback identity.
func RecordWorkflow(status *api.TransitionStatus, workflow api.NSXWorkflowStatus) error {
	if status == nil {
		return fmt.Errorf("transition status is required")
	}
	if workflow.VDNName == "" || workflow.WorkflowID == "" {
		return fmt.Errorf("VDN and workflow ID are required")
	}
	for _, entry := range status.NSXWorkflows {
		if entry.VDNName == workflow.VDNName {
			if entry.WorkflowID != workflow.WorkflowID {
				return fmt.Errorf("VDN %s already records workflow %s", entry.VDNName, entry.WorkflowID)
			}
			return nil
		}
	}
	if len(status.NSXWorkflows) >= 1000 {
		return fmt.Errorf("workflow status exceeds 1000 VDNs")
	}
	status.NSXWorkflows = append(status.NSXWorkflows, workflow)
	return nil
}

// RecordNamespace updates one namespace's progress, retaining all other records.
// Missing records remain Pending to consumers. A per-namespace outcome never
// promotes the CR-level phase or claims that the entire action succeeded.
func RecordNamespace(batch []string, status *api.NamespaceNetworkTransitionStatus, entry api.NamespaceStatus) error {
	if status == nil {
		return fmt.Errorf("namespace transition status is required")
	}
	member := false
	for _, name := range batch {
		if name == entry.Name {
			member = true
			break
		}
	}
	if entry.Name == "" || !member {
		return fmt.Errorf("namespace %q is outside the transition batch", entry.Name)
	}
	for i := range status.NamespaceStatuses {
		if status.NamespaceStatuses[i].Name == entry.Name {
			status.NamespaceStatuses[i] = entry
			return nil
		}
	}
	status.NamespaceStatuses = append(status.NamespaceStatuses, entry)
	return nil
}
