/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package status

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

// UpdateControlPlane patches only status from a previously read CR snapshot.
// The mutation must be side-effect-free. The caller's snapshot is never changed.
// Resource-version conflicts are returned to the reconciler: read a new snapshot
// and recompute progress rather than replaying stale results after a spec change.
func UpdateControlPlane(ctx context.Context, c client.Client, snapshot *api.ControlPlaneNetworkTransition, mutate func(*api.ControlPlaneNetworkTransitionStatus) error) (*api.ControlPlaneNetworkTransition, error) {
	if snapshot == nil || mutate == nil {
		return nil, fmt.Errorf("snapshot and status mutation are required")
	}
	updated := snapshot.DeepCopy()
	if updated.Status == nil {
		updated.Status = &api.ControlPlaneNetworkTransitionStatus{}
	}
	if err := mutate(updated.Status); err != nil {
		return nil, err
	}
	var previous *api.TransitionStatus
	if snapshot.Status != nil {
		previous = &snapshot.Status.TransitionStatus
	}
	if err := validateActionScope(&updated.Status.TransitionStatus, previous, true); err != nil {
		return nil, err
	}
	if err := validateGenerations(&updated.Status.TransitionStatus, previous, snapshot.Generation); err != nil {
		return nil, err
	}
	if snapshot.Status == nil && equality.Semantic.DeepEqual(updated.Status, &api.ControlPlaneNetworkTransitionStatus{}) {
		updated.Status = nil
	}
	if err := patch(ctx, c, snapshot, updated); err != nil {
		return nil, err
	}
	return updated, nil
}

// UpdateNamespace patches only batch status with the same optimistic-concurrency
// contract as UpdateControlPlane. Namespace membership comes from the snapshot's
// immutable spec.namespaces; no aggregate counters are persisted.
func UpdateNamespace(ctx context.Context, c client.Client, snapshot *api.NamespaceNetworkTransition, mutate func(*api.NamespaceNetworkTransitionStatus) error) (*api.NamespaceNetworkTransition, error) {
	if snapshot == nil || mutate == nil {
		return nil, fmt.Errorf("snapshot and status mutation are required")
	}
	updated := snapshot.DeepCopy()
	if updated.Status == nil {
		updated.Status = &api.NamespaceNetworkTransitionStatus{}
	}
	if err := mutate(updated.Status); err != nil {
		return nil, err
	}
	var previous *api.TransitionStatus
	if snapshot.Status != nil {
		previous = &snapshot.Status.TransitionStatus
	}
	if err := validateActionScope(&updated.Status.TransitionStatus, previous, false); err != nil {
		return nil, err
	}
	if err := validateGenerations(&updated.Status.TransitionStatus, previous, snapshot.Generation); err != nil {
		return nil, err
	}
	members := make(map[string]bool, len(snapshot.Spec.Namespaces))
	for _, name := range snapshot.Spec.Namespaces {
		members[name] = true
	}
	for _, entry := range updated.Status.NamespaceStatuses {
		if !members[entry.Name] {
			return nil, fmt.Errorf("namespace %q is outside the transition batch", entry.Name)
		}
	}
	if snapshot.Status == nil && equality.Semantic.DeepEqual(updated.Status, &api.NamespaceNetworkTransitionStatus{}) {
		updated.Status = nil
	}
	if err := patch(ctx, c, snapshot, updated); err != nil {
		return nil, err
	}
	return updated, nil
}

// Preserve unchanged historical checkpoints for diagnosis, but never accept or
// advance an action that is outside this CR kind's scope.
func validateActionScope(status, previous *api.TransitionStatus, infrastructure bool) error {
	if previous != nil && status.LastAction == previous.LastAction && status.ActionState == previous.ActionState && status.ActionGeneration == previous.ActionGeneration {
		return nil
	}
	if infrastructure && (status.LastAction == api.ActionRollback || status.LastAction == api.ActionCommit) {
		return fmt.Errorf("infrastructure checkpoints cannot accept or advance %s; infrastructure reversion uses WCP backup and restore", status.LastAction)
	}
	if !infrastructure && status.LastAction == api.ActionFinalize {
		return fmt.Errorf("namespace checkpoints cannot accept or advance Finalize")
	}
	return nil
}

func validateGenerations(status, previous *api.TransitionStatus, generation int64) error {
	// The mutation callback can assign fields directly instead of using Observe
	// or RecordAction. Enforce monotonic checkpoints at the persistence boundary.
	if previous != nil && (status.ObservedGeneration < previous.ObservedGeneration || status.ActionGeneration < previous.ActionGeneration) {
		return fmt.Errorf("status generations cannot regress")
	}
	if generation < 1 || status.ObservedGeneration < 0 || status.ObservedGeneration > generation || status.ActionGeneration < 0 || status.ActionGeneration > status.ObservedGeneration {
		return fmt.Errorf("status generations must describe the observed CR snapshot")
	}
	if (status.LastAction != "" || status.ActionState != "" || status.ActionGeneration != 0) && (status.LastAction == "" || status.ActionState == "" || status.ActionGeneration == 0) {
		return fmt.Errorf("lastAction, actionState and actionGeneration must form a complete checkpoint")
	}
	for _, condition := range status.Conditions {
		if condition.ObservedGeneration < 0 || condition.ObservedGeneration > generation {
			return fmt.Errorf("condition %s refers to an invalid or future generation", condition.Type)
		}
	}
	return nil
}

func patch(ctx context.Context, c client.Client, snapshot, updated client.Object) error {
	if snapshot.GetUID() == "" || snapshot.GetResourceVersion() == "" {
		return fmt.Errorf("status update requires a persisted snapshot with UID and resourceVersion")
	}
	if equality.Semantic.DeepEqual(snapshot, updated) {
		return nil
	}
	// A JSON merge patch replaces lists atomically. Optimistic locking prevents
	// overwriting concurrent namespace progress, conditions, or workflow handles.
	if err := c.Status().Patch(ctx, updated, client.MergeFromWithOptions(snapshot, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("persisting migration status: %w", err)
	}
	return nil
}
