/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Package topology defines the live topology discovery contract shared by
// migration preflight and the later action handlers.
package topology

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/types"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

type ResourceKind string

const (
	ResourceVDN                       ResourceKind = "VDN"
	ResourceDVPG                      ResourceKind = "DVPG"
	ResourceNSXWorkflow               ResourceKind = "NSXWorkflow"
	ResourceVDNSubnetLink             ResourceKind = "VDNSubnetLink"
	ResourceSubnetIPReservation       ResourceKind = "SubnetIPReservation"
	ResourceNetworkResourceTransition ResourceKind = "NetworkResourceTransition"
)

// ResourceIdentity is a stable identity for a resource a batch may mutate.
// For a DVPG, Key must use source vCenter UUID + DVPG MoID, not its display
// name, VDN name, or VLAN ID. Other resources use their canonical backing key.
type ResourceIdentity struct {
	Kind       ResourceKind
	Key        string
	VDNName    string // Diagnostic association; not part of identity.
	WorkflowID string // Diagnostic association; not part of identity.
}

// ConsumerIdentity records a live consumer of a discovered resource. Namespace
// UID distinguishes a deleted/recreated Namespace with the same name.
type ConsumerIdentity struct {
	Resource     ResourceIdentity
	APIGroup     string
	Kind         string
	Namespace    string
	NamespaceUID types.UID
	Name         string
}

// BatchSnapshot is a read-only, point-in-time view. Complete is true only when
// the implementation has resolved all batch VDNs, physical DVPG identities,
// VDN/subnet bindings, relevant reservations, workflows, and their live
// consumers. It is never persisted as migration status.
type BatchSnapshot struct {
	Complete  bool
	Resources []ResourceIdentity
	Consumers []ConsumerIdentity
}

func (s BatchSnapshot) Validate() error {
	if !s.Complete {
		return fmt.Errorf("topology discovery did not return a complete snapshot")
	}
	for _, resource := range s.Resources {
		if resource.Kind == "" || resource.Key == "" {
			return fmt.Errorf("topology discovery returned a resource without a stable kind and identity")
		}
	}
	for _, consumer := range s.Consumers {
		if consumer.Resource.Kind == "" || consumer.Resource.Key == "" || consumer.Kind == "" || consumer.Name == "" {
			return fmt.Errorf("topology discovery returned an incomplete live consumer identity")
		}
	}
	return nil
}

// Discoverer must perform fresh read-only discovery for each call. It must not
// rely on a periodically synchronized topology cache for admission decisions.
// Callers invoke it at Evaluate/Execute/Rollback preflight; handlers must
// rediscover immediately before each external mutation because this snapshot
// can become stale. The result's Resources are the mutable-resource claims used
// to detect overlap with other unresolved batches. Consumers are passed to the
// action handler so it can apply action-specific safety checks before mutation.
type Discoverer interface {
	DiscoverBatch(context.Context, *api.NamespaceNetworkTransition) (BatchSnapshot, error)
}
