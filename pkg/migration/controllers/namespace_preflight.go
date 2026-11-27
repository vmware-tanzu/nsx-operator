/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package controllers

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/topology"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/transition"
)

const namespacePreflightRequeue = 30 * time.Second

type preflightOutcome struct {
	ready            bool
	reason           string
	message          string
	stage1Transition *api.ControlPlaneNetworkTransition
	namespaceStates  []transition.NamespaceNetworkState
	topologySnapshot *topology.BatchSnapshot
}

var errTopologyDiscoveryUnavailable = errors.New("shared topology discovery is not configured")

// checkNamespaceBatchPrerequisites verifies the inputs available to this
// controller before Evaluate or Execute begins. It does not establish an
// atomic lock: a cross-CR admission/coordination mechanism is still needed to
// close the race between independent reconciles.
func checkNamespaceBatchPrerequisites(ctx context.Context, c client.Reader, batch *api.NamespaceNetworkTransition, discoverer topology.Discoverer) (preflightOutcome, error) {
	stage1, err := ReadStage1Snapshot(ctx, c)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return preflightOutcome{reason: "Stage1TransitionMissing", message: "Stage 1 transition has not been created"}, nil
		}
		if errors.Is(err, errStage1StatusMissing) {
			return preflightOutcome{reason: "Stage1Incomplete", message: err.Error()}, nil
		}
		return preflightOutcome{}, err
	}
	if reason, message := stage1Readiness(stage1); reason != "" {
		return preflightOutcome{reason: reason, message: message}, nil
	}
	networkProvider, err := ReadCoexistenceNetworkProvider(ctx, c)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return preflightOutcome{reason: "CoexistenceModeNotEnabled", message: "WCP ConfigMap vmware-system-vks-public/supervisor-properties is missing; Stage 2 requires networkProvider=nsx-vpc"}, nil
		}
		return preflightOutcome{}, err
	}
	if networkProvider != "nsx-vpc" {
		return preflightOutcome{
			reason:  "CoexistenceModeNotEnabled",
			message: fmt.Sprintf("WCP ConfigMap vmware-system-vks-public/supervisor-properties has networkProvider=%q; Stage 2 requires nsx-vpc", networkProvider),
		}, nil
	}
	stagingNamespace := &corev1.Namespace{}
	if err := c.Get(ctx, client.ObjectKey{Name: "vmware-system-net-staging"}, stagingNamespace); err != nil {
		if apierrors.IsNotFound(err) {
			return preflightOutcome{reason: "StagingNamespaceMissing", message: "Stage 1 staging Namespace vmware-system-net-staging is missing"}, nil
		}
		return preflightOutcome{}, fmt.Errorf("read Stage 1 staging Namespace: %w", err)
	}
	if !stagingNamespace.DeletionTimestamp.IsZero() {
		return preflightOutcome{reason: "StagingNamespaceTerminating", message: "Stage 1 staging Namespace vmware-system-net-staging is terminating"}, nil
	}

	batchSnapshot, err := ReadNamespaceBatchSnapshot(ctx, c, batch)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return preflightOutcome{reason: "NamespaceMissing", message: err.Error()}, nil
		}
		return preflightOutcome{}, err
	}
	for i := range batchSnapshot.Namespaces {
		namespace := &batchSnapshot.Namespaces[i]
		if namespace.Terminating {
			return preflightOutcome{reason: "NamespaceTerminating", message: fmt.Sprintf("Namespace %q is terminating", namespace.Name)}, nil
		}
		provider, err := ReadNetworkSettingsProvider(ctx, c, namespace.Name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return preflightOutcome{reason: "NetworkSettingsMissing", message: fmt.Sprintf("NetworkSettings/default in Namespace %q is missing; cannot determine its effective provider", namespace.Name)}, nil
			}
			var invalidState *invalidNetworkSettingsError
			if errors.As(err, &invalidState) {
				return preflightOutcome{reason: "NetworkSettingsInvalid", message: invalidState.Error()}, nil
			}
			return preflightOutcome{}, err
		}
		desiredProvider, err := ReadSupervisorNamespaceProvider(ctx, c, namespace.Name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return preflightOutcome{reason: "SupervisorNamespaceMissing", message: fmt.Sprintf("SupervisorNamespace %q is missing; cannot determine its desired provider", namespace.Name)}, nil
			}
			var invalidState *invalidSupervisorNamespaceError
			if errors.As(err, &invalidState) {
				return preflightOutcome{reason: "SupervisorNamespaceInvalid", message: invalidState.Error()}, nil
			}
			return preflightOutcome{}, err
		}
		namespace.DesiredProvider = desiredProvider
		namespace.EffectiveProvider = provider.Provider
		namespace.LegacyProvider = provider.LegacyProvider
		if reason, message := validateProviderPair(batch.Spec.Action, batchSnapshot, namespace, desiredProvider, provider); reason != "" {
			return preflightOutcome{
				reason:  reason,
				message: fmt.Sprintf("Namespace %q: %s", namespace.Name, message),
			}, nil
		}
	}

	topologySnapshot, err := discoverBatchTopology(ctx, discoverer, batch)
	if errors.Is(err, errTopologyDiscoveryUnavailable) {
		return topologyDiscoveryUnavailableOutcome(ctx, batch, batch.Spec.Action), nil
	}
	if err != nil {
		return preflightOutcome{}, fmt.Errorf("discover namespace topology for batch %q: %w", batch.Name, err)
	}
	conflicts, err := findActiveNamespaceBatchConflicts(ctx, c, batch, topologySnapshot, discoverer)
	if err != nil {
		return preflightOutcome{}, err
	}
	if len(conflicts) != 0 {
		return preflightOutcome{
			reason:  "ConflictingBatch",
			message: fmt.Sprintf("Namespace batch shares namespace or mutable topology resources with unresolved transition(s): %v", conflicts),
		}, nil
	}
	message := "Current Stage 1 checkpoint, WCP coexistence registration, target Project and shared LB context, immutable Namespace membership, and live source topology passed"
	if batch.Spec.Action == api.ActionExecute {
		message += "; action handler must recheck live shared resources and consumers before mutation"
	}
	return preflightOutcome{
		ready: true, reason: "KnownChecksPassed",
		message:          message,
		stage1Transition: stage1.Transition,
		namespaceStates:  namespaceNetworkStates(batchSnapshot.Namespaces),
		topologySnapshot: &topologySnapshot,
	}, nil
}

// checkNamespaceRollbackPrerequisites is recovery-specific: it does not gate
// on current Stage 1 readiness, but it does require the immutable batch and
// current provider states to remain identifiable and free of visible conflicts.
func checkNamespaceRollbackPrerequisites(ctx context.Context, c client.Reader, batch *api.NamespaceNetworkTransition, discoverer topology.Discoverer) (preflightOutcome, error) {
	snapshot, err := ReadNamespaceBatchSnapshot(ctx, c, batch)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return preflightOutcome{reason: "NamespaceMissing", message: err.Error()}, nil
		}
		return preflightOutcome{}, err
	}
	for i := range snapshot.Namespaces {
		namespace := &snapshot.Namespaces[i]
		if namespace.Terminating {
			return preflightOutcome{reason: "NamespaceTerminating", message: fmt.Sprintf("Namespace %q is terminating; Rollback cannot verify restoration", namespace.Name)}, nil
		}
		provider, err := ReadNetworkSettingsProvider(ctx, c, namespace.Name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return preflightOutcome{reason: "NetworkSettingsMissing", message: fmt.Sprintf("NetworkSettings/default in Namespace %q is missing; cannot verify Rollback state", namespace.Name)}, nil
			}
			var invalidState *invalidNetworkSettingsError
			if errors.As(err, &invalidState) {
				return preflightOutcome{reason: "NetworkSettingsInvalid", message: invalidState.Error()}, nil
			}
			return preflightOutcome{}, err
		}
		desiredProvider, err := ReadSupervisorNamespaceProvider(ctx, c, namespace.Name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return preflightOutcome{reason: "SupervisorNamespaceMissing", message: fmt.Sprintf("SupervisorNamespace %q is missing; cannot verify Rollback state", namespace.Name)}, nil
			}
			var invalidState *invalidSupervisorNamespaceError
			if errors.As(err, &invalidState) {
				return preflightOutcome{reason: "SupervisorNamespaceInvalid", message: invalidState.Error()}, nil
			}
			return preflightOutcome{}, err
		}
		if reason, message := validateProviderPair(api.ActionRollback, snapshot, namespace, desiredProvider, provider); reason != "" {
			return preflightOutcome{reason: reason, message: fmt.Sprintf("Namespace %q: %s", namespace.Name, message)}, nil
		}
		namespace.DesiredProvider = desiredProvider
		namespace.EffectiveProvider = provider.Provider
		namespace.LegacyProvider = provider.LegacyProvider
	}
	topologySnapshot, err := discoverBatchTopology(ctx, discoverer, batch)
	if errors.Is(err, errTopologyDiscoveryUnavailable) {
		return topologyDiscoveryUnavailableOutcome(ctx, batch, api.ActionRollback), nil
	}
	if err != nil {
		return preflightOutcome{}, fmt.Errorf("discover namespace topology for rollback batch %q: %w", batch.Name, err)
	}
	conflicts, err := findActiveNamespaceBatchConflicts(ctx, c, batch, topologySnapshot, discoverer)
	if err != nil {
		return preflightOutcome{}, err
	}
	if len(conflicts) != 0 {
		return preflightOutcome{reason: "ConflictingBatch", message: fmt.Sprintf("Namespace batch shares namespace or mutable topology resources with unresolved transition(s): %v", conflicts)}, nil
	}
	return preflightOutcome{
		ready: true, reason: "RollbackDependenciesPassed",
		message:          "Batch identity, desired/effective provider restoration state, and live namespace/VDN/DVPG topology checks passed; the rollback handler must rediscover consumers before each reversal",
		namespaceStates:  namespaceNetworkStates(snapshot.Namespaces),
		topologySnapshot: &topologySnapshot,
	}, nil
}

func topologyDiscoveryUnavailableOutcome(ctx context.Context, batch *api.NamespaceNetworkTransition, action api.Action) preflightOutcome {
	ctrl.LoggerFrom(ctx).Error(errTopologyDiscoveryUnavailable, "Blocking namespace migration action until live topology discovery is wired",
		"batch", batch.Name, "action", action)
	return preflightOutcome{
		reason:  "TopologyDiscoveryUnavailable",
		message: "live VDN/DVPG topology discovery is not wired; the action is blocked rather than assuming there are no shared-resource conflicts",
	}
}

func discoverBatchTopology(ctx context.Context, discoverer topology.Discoverer, batch *api.NamespaceNetworkTransition) (topology.BatchSnapshot, error) {
	if discoverer == nil {
		return topology.BatchSnapshot{}, errTopologyDiscoveryUnavailable
	}
	snapshot, err := discoverer.DiscoverBatch(ctx, batch)
	if err != nil {
		return topology.BatchSnapshot{}, err
	}
	if err := snapshot.Validate(); err != nil {
		return topology.BatchSnapshot{}, err
	}
	return snapshot, nil
}

func namespaceNetworkStates(namespaces []NamespaceSnapshot) []transition.NamespaceNetworkState {
	states := make([]transition.NamespaceNetworkState, 0, len(namespaces))
	for _, namespace := range namespaces {
		states = append(states, transition.NamespaceNetworkState{
			Name: namespace.Name, UID: namespace.UID,
			DesiredProvider: namespace.DesiredProvider, EffectiveProvider: namespace.EffectiveProvider,
			LegacyProvider: namespace.LegacyProvider, Progress: namespace.Progress,
			HasProgress: namespace.HasProgress,
		})
	}
	return states
}

func stage1Readiness(snapshot *Stage1Snapshot) (string, string) {
	if snapshot == nil {
		return "Stage1TransitionMissing", "Stage 1 transition status is unavailable"
	}
	condition := findCondition(snapshot.Status.Conditions, api.ConditionInfraTransitionComplete)
	if condition == nil || condition.Status != metav1.ConditionTrue || condition.ObservedGeneration < 1 || condition.ObservedGeneration > snapshot.Generation {
		return "Stage1Incomplete", "Stage 1 has no valid InfraTransitionComplete=True condition"
	}
	if len(snapshot.Status.RequiredInputs) != 0 {
		return "Stage1InputsRequired", "Stage 1 still reports required inputs"
	}

	currentExecute := snapshot.Status.Phase == api.PhaseInfraTransitionComplete &&
		snapshot.Status.LastAction == api.ActionExecute &&
		snapshot.Status.ActionState == api.ActionStateSucceeded &&
		snapshot.Status.ActionGeneration == snapshot.Generation &&
		snapshot.Status.ObservedGeneration == snapshot.Generation &&
		condition.ObservedGeneration == snapshot.Generation
	// A Finalize request advances generation before it can run. Stage 1 may then
	// wait for Stage 2 batches to commit, so those batches must keep using the
	// last successful Execute handoff until Finalize is accepted. Keep this in
	// sync with infrastructureReady in transition_controller.go.
	pendingFinalize := snapshot.RequestedAction == api.ActionFinalize &&
		snapshot.Status.LastAction == api.ActionExecute &&
		snapshot.Status.ActionState == api.ActionStateSucceeded &&
		snapshot.Status.ActionGeneration < snapshot.Generation &&
		snapshot.Status.IrreversibleAction == "" &&
		condition.Status == metav1.ConditionTrue &&
		condition.ObservedGeneration == snapshot.Status.ActionGeneration
	if !currentExecute && !pendingFinalize {
		return "Stage1CheckpointStale", "Stage 1 must be at InfraTransitionComplete with a current successful Execute checkpoint and matching condition generation"
	}
	if snapshot.TargetNSXProjectPath == "" || snapshot.TargetLBProvider == "" || snapshot.PrimaryVPCPath == "" || snapshot.LoadBalancerVPCPath == "" {
		return "Stage1HandoffIncomplete", "Stage 1 target Project, primary VPC, or shared load balancer context is incomplete"
	}
	if snapshot.RequestedLBProvider != "" && snapshot.RequestedLBProvider != snapshot.TargetLBProvider {
		return "Stage1ProviderMismatch", fmt.Sprintf("Stage 1 requested target LB provider %q but resolved %q", snapshot.RequestedLBProvider, snapshot.TargetLBProvider)
	}
	if snapshot.TargetLBProvider != "NSX_LB" {
		return "UnsupportedTargetLBProvider", fmt.Sprintf("Stage 1 resolved unsupported target LB provider %q", snapshot.TargetLBProvider)
	}
	project, ok := nsxProjectIdentity(snapshot.TargetNSXProjectPath)
	if !ok {
		return "InvalidTargetProjectPath", fmt.Sprintf("Stage 1 target Project path %q is not a valid NSX Project path", snapshot.TargetNSXProjectPath)
	}
	if snapshot.TargetNSXProjectID != "" && project.project != snapshot.TargetNSXProjectID {
		return "TargetProjectMismatch", fmt.Sprintf("Stage 1 target Project path %q does not match requested nsxProjectID %q", snapshot.TargetNSXProjectPath, snapshot.TargetNSXProjectID)
	}
	if !nsxVPCBelongsToProject(snapshot.LoadBalancerVPCPath, snapshot.TargetNSXProjectPath) {
		return "SharedLBVPCProjectMismatch", fmt.Sprintf("Stage 1 shared LB VPC %q is not under target Project %q", snapshot.LoadBalancerVPCPath, snapshot.TargetNSXProjectPath)
	}
	if !nsxVPCBelongsToProject(snapshot.PrimaryVPCPath, snapshot.TargetNSXProjectPath) {
		return "PrimaryVPCProjectMismatch", fmt.Sprintf("Stage 1 primary VPC %q is not under target Project %q", snapshot.PrimaryVPCPath, snapshot.TargetNSXProjectPath)
	}
	return "", ""
}

func validateProviderPair(action api.Action, batch *NamespaceBatchSnapshot, namespace *NamespaceSnapshot, desiredProvider string, effective networkSettingsProvider) (string, string) {
	if batch == nil {
		return "BatchSnapshotMissing", "namespace batch snapshot is unavailable"
	}
	if namespace == nil {
		return "NamespaceSnapshotMissing", "namespace state snapshot is unavailable"
	}
	resumeCheckpoint := batch.LastAction == api.ActionExecute &&
		batch.ActionGeneration == batch.Generation && batch.ObservedGeneration == batch.Generation &&
		(batch.ActionState == api.ActionStateRunning || batch.ActionState == api.ActionStateFailed)
	if action == api.ActionEvaluate {
		if desiredProvider == "VSPHERE_VDS" && effective.Provider == "vsphere-distributed" && effective.LegacyProvider == "" {
			return "", ""
		}
		return "NamespaceNotOnSourceNetwork", fmt.Sprintf("Evaluate requires SupervisorNamespace desired VDS and NetworkSettings provider=vsphere-distributed without legacyProvider; got desired=%q provider=%q legacyProvider=%q", desiredProvider, effective.Provider, effective.LegacyProvider)
	}
	desiredVDS := desiredProvider == "VSPHERE_VDS"
	desiredVPC := desiredProvider == "NSX_VPC"
	effectiveVDS := effective.Provider == "vsphere-distributed" && effective.LegacyProvider == ""
	effectiveMigratedVPC := effective.Provider == "vpc" && effective.LegacyProvider == "vsphere-distributed"
	if action == api.ActionExecute {
		if desiredVDS && effectiveVDS {
			return "", ""
		}
		if resumeCheckpoint && desiredVPC && (effectiveVDS || effectiveMigratedVPC) {
			if hasNamespaceMutationCheckpoint(namespace) {
				return "", ""
			}
			return "NamespaceProgressCheckpointMissing", fmt.Sprintf("Namespace desired/effective provider state indicates a partial Execute, but no per-Namespace mutation checkpoint is recorded (phase=%q)", namespace.Progress.Phase)
		}
		if desiredVPC && effective.Provider == "vpc" && effective.LegacyProvider == "" {
			return "NamespaceNotInMigrationScope", "provider is vpc without the VDS legacy provider; native VPC Namespaces are not Stage 2 migration members"
		}
		return "NamespaceProviderStateInvalid", fmt.Sprintf("Execute requires a fresh VDS/VDS state or a current Execute checkpoint with VPC desired state and VDS/VPC effective state; got desired=%q provider=%q legacyProvider=%q", desiredProvider, effective.Provider, effective.LegacyProvider)
	}
	if action == api.ActionRollback {
		if desiredVDS && effectiveVDS {
			return "", ""
		}
		if (desiredVPC || desiredVDS) && (effectiveVDS || effectiveMigratedVPC) && hasNamespaceMutationCheckpoint(namespace) {
			return "", ""
		}
		if (desiredVPC || desiredVDS) && (effectiveVDS || effectiveMigratedVPC) {
			return "NamespaceProgressCheckpointMissing", fmt.Sprintf("Namespace desired/effective provider state indicates a partial Execute, but no per-Namespace mutation checkpoint is recorded (phase=%q)", namespace.Progress.Phase)
		}
		return "RollbackProviderStateInvalid", fmt.Sprintf("Rollback cannot classify desired=%q provider=%q legacyProvider=%q", desiredProvider, effective.Provider, effective.LegacyProvider)
	}
	return "UnsupportedBatchAction", fmt.Sprintf("action %s has no provider-state policy", action)
}

// hasNamespaceMutationCheckpoint distinguishes a Namespace that was actually
// touched by Execute from one whose desired provider was changed outside the
// recorded batch progress. Batch-level Running/Failed state alone is not
// enough to authorize resuming an arbitrary Namespace at a VPC provider.
func hasNamespaceMutationCheckpoint(namespace *NamespaceSnapshot) bool {
	if namespace == nil || !namespace.HasProgress {
		return false
	}
	switch namespace.Progress.Phase {
	case api.PhaseExecuting, api.PhaseCompleted, api.PhaseFailed, api.PhaseRollingBack, api.PhaseRolledBack:
		return true
	default:
		return false
	}
}

type nsxProjectPath struct {
	org     string
	project string
}

func nsxProjectIdentity(path string) (nsxProjectPath, bool) {
	parts := splitNSXPath(path)
	if len(parts) != 4 || parts[0] != "orgs" || parts[2] != "projects" || parts[1] == "" || parts[3] == "" {
		return nsxProjectPath{}, false
	}
	return nsxProjectPath{org: parts[1], project: parts[3]}, true
}

func nsxVPCBelongsToProject(vpcPath, projectPath string) bool {
	project, ok := nsxProjectIdentity(projectPath)
	if !ok {
		return false
	}
	parts := splitNSXPath(vpcPath)
	return len(parts) == 6 && parts[0] == "orgs" && parts[1] == project.org &&
		parts[2] == "projects" && parts[3] == project.project && parts[4] == "vpcs" && parts[5] != ""
}

func splitNSXPath(path string) []string {
	return strings.Split(strings.Trim(path, "/"), "/")
}

func findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

func findActiveNamespaceBatchConflicts(ctx context.Context, c client.Reader, current *api.NamespaceNetworkTransition, currentTopology topology.BatchSnapshot, discoverer topology.Discoverer) ([]string, error) {
	transitions := &api.NamespaceNetworkTransitionList{}
	if err := c.List(ctx, transitions); err != nil {
		return nil, fmt.Errorf("list namespace transitions for batch conflict check: %w", err)
	}
	currentMembers := namespaceSet(current.Spec.Namespaces)
	conflicts := make([]string, 0)
	for i := range transitions.Items {
		other := &transitions.Items[i]
		if other.Name == current.Name && (current.UID == "" || other.UID == current.UID) {
			continue
		}
		if !hasUnresolvedMutation(other) {
			continue
		}
		// An Execute request that has not reached its first mutation checkpoint
		// must wait for existing owners, without blocking their recovery. The
		// engine persists that checkpoint before dispatching a mutating handler.
		if current.Spec.Action == api.ActionRollback && isPendingNamespaceExecute(other) {
			continue
		}
		conflict := false
		for _, name := range other.Spec.Namespaces {
			if _, overlap := currentMembers[name]; overlap {
				conflict = true
				break
			}
		}
		// Evaluate reads shared backing without changing it. Mutating actions
		// compare the resources each action actually plans to modify.
		if !conflict && current.Spec.Action != api.ActionEvaluate && len(currentTopology.Resources) != 0 {
			otherTopology, err := discoverBatchTopology(ctx, discoverer, other)
			if err != nil {
				return nil, fmt.Errorf("discover topology for unresolved batch %q: %w", other.Name, err)
			}
			conflict = topologyResourcesOverlap(currentTopology, otherTopology)
		}
		// Two newly requested Execute actions have not yet persisted an engine
		// checkpoint, so neither is an owner yet. Elect one deterministically
		// from the conflicting pending requests; otherwise both preflights can
		// block each other forever. A checkpointed Execute still wins through
		// the normal unresolved-mutation path above.
		if conflict && isPendingNamespaceExecute(current) && isPendingNamespaceExecute(other) {
			conflict = !pendingNamespaceExecutePrecedes(current, other)
		}
		if conflict {
			conflicts = append(conflicts, other.Name)
		}
	}
	sort.Strings(conflicts)
	return conflicts, nil
}

func pendingNamespaceExecutePrecedes(first, second *api.NamespaceNetworkTransition) bool {
	if first.CreationTimestamp.Equal(&second.CreationTimestamp) {
		if first.Name != second.Name {
			return first.Name < second.Name
		}
		return string(first.UID) < string(second.UID)
	}
	return first.CreationTimestamp.Before(&second.CreationTimestamp)
}

func isPendingNamespaceExecute(batch *api.NamespaceNetworkTransition) bool {
	if batch == nil || batch.Spec.Action != api.ActionExecute || batch.Status == nil {
		return false
	}
	status := batch.Status
	// Require a successful Evaluate from before this request. Preflight may
	// already have advanced observedGeneration and replaced phase with Blocked.
	// Evaluate may retain NSX validation workflow references; their presence
	// does not mean the engine has checkpointed or dispatched Execute.
	if status.LastAction != api.ActionEvaluate || status.ActionState != api.ActionStateSucceeded ||
		status.ActionGeneration <= 0 || status.ActionGeneration >= batch.Generation ||
		status.IrreversibleAction != "" {
		return false
	}
	for _, namespace := range status.NamespaceStatuses {
		switch namespace.Phase {
		case "", api.PhasePending, api.PhaseEvaluated:
		default:
			return false
		}
	}
	return true
}

func topologyResourcesOverlap(first, second topology.BatchSnapshot) bool {
	type identity struct {
		kind topology.ResourceKind
		key  string
	}
	resources := make(map[identity]struct{}, len(first.Resources))
	for _, resource := range first.Resources {
		resources[identity{kind: resource.Kind, key: resource.Key}] = struct{}{}
	}
	for _, resource := range second.Resources {
		if _, shared := resources[identity{kind: resource.Kind, key: resource.Key}]; shared {
			return true
		}
	}
	return false
}

func hasUnresolvedMutation(batch *api.NamespaceNetworkTransition) bool {
	if batch == nil {
		return false
	}
	status := batch.Status
	if status != nil {
		transitionStatus := &status.TransitionStatus
		currentCheckpoint := transitionStatus.ActionGeneration == batch.Generation && transitionStatus.ObservedGeneration == batch.Generation
		if currentCheckpoint && transitionStatus.LastAction == api.ActionCommit && transitionStatus.ActionState == api.ActionStateSucceeded && transitionStatus.Phase == api.PhaseCommitted && transitionStatus.IrreversibleAction == string(api.ActionCommit) {
			return false
		}
		if transitionStatus.LastAction == api.ActionRollback && transitionStatus.ActionState == api.ActionStateSucceeded && transitionStatus.IrreversibleAction == "" && transitionStatus.ActionGeneration <= batch.Generation {
			return batch.Spec.Action == api.ActionExecute || batch.Spec.Action == api.ActionCommit
		}
		if transitionStatus.LastAction == api.ActionExecute || transitionStatus.LastAction == api.ActionRollback || transitionStatus.LastAction == api.ActionCommit {
			return true
		}
	}
	return batch.Spec.Action == api.ActionExecute || batch.Spec.Action == api.ActionRollback || batch.Spec.Action == api.ActionCommit
}
