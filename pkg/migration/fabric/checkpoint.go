/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package fabric

import "context"

// CheckpointKey identifies one preparation within one transition incarnation.
// Identity distinguishes multiple fabric preparations in the same transition.
type CheckpointKey struct {
	TransitionUID    string `json:"transitionUID"`
	ActionGeneration int64  `json:"actionGeneration"`
	Identity         string `json:"identity"`
}

// ResourceOrigin records what can be proved about a selected resource. Unknown
// is never evidence that a resource may be deleted by migration cleanup.
type ResourceOrigin string

const (
	ResourceOriginUnknown ResourceOrigin = "Unknown"
	ResourceOriginCreated ResourceOrigin = "Created"
	ResourceOriginAdopted ResourceOrigin = "Adopted"
)

// ResourceCheckpoint records the selected canonical path and verified origin.
// A resource's existence, a matching name, or shared cluster tags alone do not
// prove that this transition created it. An ambiguous create remains Unknown
// until authoritative evidence establishes its origin.
type ResourceCheckpoint struct {
	Kind   Kind
	Path   string
	Origin ResourceOrigin
}

// PreparationCheckpoint pins the selection and origin of each resource. A
// selected path with Unknown origin is a durable intent, not a completed write.
// The caller must preserve a recorded path and must not downgrade or guess its
// origin during retry, restart, cancellation, or concurrent reconciliation.
type PreparationCheckpoint struct {
	Key        CheckpointKey        `json:"key"`
	PlanDigest string               `json:"planDigest,omitempty"`
	Identities *FrozenIdentities    `json:"identities,omitempty"`
	Resources  []ResourceCheckpoint `json:"resources,omitempty"`
}

// PreparationCheckpointStore is the integration contract for durable state.
// This package does not provide a store or use it in Prepare yet. Execute must
// select an authoritative durable backing that satisfies this contract before
// enabling mutation; an in-memory implementation is insufficient.
//
// Load returns an opaque revision and found=false for an absent checkpoint.
// Save atomically compares that revision, persists the complete checkpoint and
// returns the new revision. For creation, expectedRevision is empty and Save
// must fail if a record already exists. Conflicts must never overwrite a newer
// checkpoint. The caller persists the selected path with Unknown origin before
// the first NSX write, then records Created or Adopted only with evidence that
// survives restart. It re-reads and validates NSX state before every operation.
type PreparationCheckpointStore interface {
	Load(ctx context.Context, key CheckpointKey) (checkpoint PreparationCheckpoint, revision string, found bool, err error)
	Save(ctx context.Context, checkpoint PreparationCheckpoint, expectedRevision string) (revision string, err error)
}
