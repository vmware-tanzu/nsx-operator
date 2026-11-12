/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package transition

import (
	"context"
	"fmt"
	"sort"

	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

// WorkflowDeleter removes an NSX conversion workflow by its persisted identity.
// Implementations must treat an already absent, previously recorded workflow as
// success so a Commit can resume after a crash between DELETE and status update.
type WorkflowDeleter interface {
	DeleteWorkflow(context.Context, string) error
}

// NamespaceCommitHandler closes rollback for one completed namespace batch.
// The engine persists IrreversibleAction between the preflight and deletion
// calls; this handler never sets or clears that checkpoint itself.
type NamespaceCommitHandler struct {
	Reader    client.Reader
	Workflows WorkflowDeleter
}

func (h *NamespaceCommitHandler) Reconcile(ctx context.Context, req Request) (Result, error) {
	if req.Stage != Namespaces || req.Action != api.ActionCommit {
		return Result{}, fmt.Errorf("namespace Commit handler received %s/%s", req.Stage, req.Action)
	}
	batch, ok := req.Object.(*api.NamespaceNetworkTransition)
	if !ok || batch == nil || req.Status == nil {
		return Result{}, fmt.Errorf("namespace Commit requires a NamespaceNetworkTransition and status")
	}
	if h.Workflows == nil {
		return Result{}, fmt.Errorf("NSX workflow deletion is unavailable")
	}

	workflowIDs, err := validateCommitWorkflowIDs(req.Status.NSXWorkflows)
	if err != nil {
		return Result{}, err
	}
	if req.Status.IrreversibleAction == "" {
		if batch.Status == nil {
			return Result{}, fmt.Errorf("namespace Commit requires per-Namespace execution status")
		}
		if err := h.validateCommit(ctx, batch, workflowIDs); err != nil {
			return Result{}, err
		}
		return Result{Outcome: OutcomeCheckpointIrreversible}, nil
	}
	if req.Status.IrreversibleAction != string(api.ActionCommit) {
		return Result{}, fmt.Errorf("namespace Commit cannot resume irreversible action %q", req.Status.IrreversibleAction)
	}

	for _, workflowID := range workflowIDs {
		if err := h.Workflows.DeleteWorkflow(ctx, workflowID); err != nil {
			return Result{}, fmt.Errorf("committing workflow %q: %w", workflowID, err)
		}
	}
	return Result{Outcome: OutcomeSucceeded}, nil
}

func (h *NamespaceCommitHandler) validateCommit(ctx context.Context, batch *api.NamespaceNetworkTransition, workflowIDs []string) error {
	if len(batch.Spec.Namespaces) == 0 {
		return fmt.Errorf("namespace Commit has an empty batch")
	}
	completed := make(map[string]bool, len(batch.Status.NamespaceStatuses))
	for _, namespace := range batch.Status.NamespaceStatuses {
		completed[namespace.Name] = namespace.Phase == "Completed"
	}
	for _, namespace := range batch.Spec.Namespaces {
		if !completed[namespace] {
			return fmt.Errorf("namespace %q has not completed verified network execution", namespace)
		}
	}
	if h.Reader == nil {
		return fmt.Errorf("namespace Commit requires a live reader for shared-workflow checks")
	}
	var batches api.NamespaceNetworkTransitionList
	if err := h.Reader.List(ctx, &batches); err != nil {
		return fmt.Errorf("listing namespace workflow consumers: %w", err)
	}
	for i := range batches.Items {
		other := &batches.Items[i]
		if other.Name == batch.Name || other.Status == nil {
			continue
		}
		for _, workflow := range other.Status.NSXWorkflows {
			if !containsWorkflow(workflowIDs, workflow.WorkflowID) {
				continue
			}
			if other.Status.IrreversibleAction == string(api.ActionCommit) &&
				other.Status.Phase == "Committed" && other.Status.ActionState == api.ActionStateSucceeded {
				continue
			}
			return fmt.Errorf("workflow %q is still referenced by namespace transition %q", workflow.WorkflowID, other.Name)
		}
	}
	return nil
}

func validateCommitWorkflowIDs(workflows []api.NSXWorkflowStatus) ([]string, error) {
	if len(workflows) == 0 {
		return nil, fmt.Errorf("namespace Commit has no recorded NSX workflows")
	}
	if len(workflows) > 1000 {
		return nil, fmt.Errorf("namespace Commit exceeds the workflow limit")
	}
	ids := make([]string, 0, len(workflows))
	seen := make(map[string]struct{}, len(workflows))
	for _, workflow := range workflows {
		if workflow.VDNName == "" || workflow.WorkflowID == "" {
			return nil, fmt.Errorf("namespace Commit contains an incomplete workflow identity")
		}
		if _, exists := seen[workflow.WorkflowID]; exists {
			return nil, fmt.Errorf("namespace Commit contains duplicate workflow ID %q", workflow.WorkflowID)
		}
		seen[workflow.WorkflowID] = struct{}{}
		ids = append(ids, workflow.WorkflowID)
	}
	sort.Strings(ids)
	return ids, nil
}

func containsWorkflow(ids []string, candidate string) bool {
	for _, id := range ids {
		if id == candidate {
			return true
		}
	}
	return false
}
