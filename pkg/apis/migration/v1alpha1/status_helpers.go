/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

// Shared phase names keep status consumers independent of action dispatch.
// Phase fields remain open strings so future milestones retain their identity.
// RollingBack and RolledBack describe namespace recovery; infrastructure uses forward retry.
const (
	PhasePending                 = "Pending"
	PhaseBlocked                 = "Blocked"
	PhaseNeedsInput              = "NeedsInput"
	PhaseEvaluating              = "Evaluating"
	PhaseEvaluated               = "Evaluated"
	PhaseEvaluationPassed        = "EvaluationPassed"
	PhaseEvaluationFailed        = "EvaluationFailed"
	PhasePlanning                = "Planning"
	PhasePlanReady               = "PlanReady"
	PhaseDeriving                = "Deriving"
	PhaseDerived                 = "Derived"
	PhaseExecuting               = "Executing"
	PhaseInfraTransitionComplete = "InfraTransitionComplete"
	PhaseCompleted               = "Completed"
	PhaseFailed                  = "Failed"
	PhaseRollingBack             = "RollingBack"
	PhaseRolledBack              = "RolledBack"
	PhaseCommitting              = "Committing"
	PhaseCommitted               = "Committed"
	PhaseFinalizing              = "Finalizing"
	PhaseFinalized               = "Finalized"
)

// Shared condition types identify the CR-level readiness and design milestones.
const (
	ConditionReady                          = "Ready"
	ConditionInfraTransitionComplete        = "InfraTransitionComplete"
	ConditionWorkloadCompatibilityEvaluated = "WorkloadCompatibilityEvaluated"
	ConditionMigrationPlanSynthesized       = "MigrationPlanSynthesized"
	ConditionSubnetsDerived                 = "SubnetsDerived"
	ConditionNetworkProviderTransitioned    = "NetworkProviderTransitioned"
	ConditionWorkloadsMigrated              = "WorkloadsMigrated"
	ConditionLoadBalancerMigrated           = "LoadBalancerMigrated"
	ConditionNamespaceMigrationCommitted    = "NamespaceMigrationCommitted"
)

// NamespacePhaseCounts derives batch progress without storing redundant counters.
// It counts each spec.namespaces member once. Missing records and empty phases
// count as Pending; unknown phases retain their own bucket. Counts reflect the
// recorded namespace progress, not successful execution of the current action.
// Records outside the batch are ignored defensively (admission rejects them).
// For duplicate records in an unvalidated object, the last record wins.
func (t *NamespaceNetworkTransition) NamespacePhaseCounts() map[string]int32 {
	counts := make(map[string]int32)
	if t == nil {
		return counts
	}
	phases := make(map[string]string)
	if t.Status != nil {
		for _, entry := range t.Status.NamespaceStatuses {
			phases[entry.Name] = entry.Phase
		}
	}
	seen := make(map[string]bool)
	for _, name := range t.Spec.Namespaces {
		if seen[name] {
			continue
		}
		seen[name] = true
		phase := phases[name]
		if phase == "" {
			phase = PhasePending
		}
		counts[phase]++
	}
	return counts
}
