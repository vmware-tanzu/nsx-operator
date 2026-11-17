/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package networkresourcetransition

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	migrationv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
)

type transitionProgress struct {
	total, succeeded, failed int
	inProgress               bool
}

// summarizeTransition counts IPAA results and the port operations requested in
// the spec. A port operation without status is still in progress.
func summarizeTransition(transitionCR *migrationv1alpha1.NetworkResourceTransition, moveStatuses []migrationv1alpha1.IPAddressAllocationMoveStatus) transitionProgress {
	progress := transitionProgress{}
	// The shared API also supports port operations. Keep their status intact
	// and do not report overall success while those operations are unfinished.
	countPhase := func(phase migrationv1alpha1.TransitionPhase) {
		progress.total++
		switch phase {
		case migrationv1alpha1.TransitionPhaseSucceeded:
			progress.succeeded++
		case migrationv1alpha1.TransitionPhaseFailed:
			progress.failed++
		default:
			progress.inProgress = true
		}
	}
	for _, move := range moveStatuses {
		countPhase(move.Phase)
	}
	for _, cutover := range transitionCR.Spec.SubnetPortCutovers {
		var phase migrationv1alpha1.TransitionPhase
		for _, status := range transitionCR.Status.SubnetPortCutovers {
			if status.SubnetPort == cutover.SubnetPort && status.SubnetIPReservation == cutover.SubnetIPReservation {
				phase = status.Phase
				break
			}
		}
		countPhase(phase)
	}
	for _, revert := range transitionCR.Spec.SubnetPortReverts {
		var phase migrationv1alpha1.TransitionPhase
		for _, status := range transitionCR.Status.SubnetPortReverts {
			if status.SubnetPort == revert.SubnetPort {
				phase = status.Phase
				break
			}
		}
		countPhase(phase)
	}
	return progress
}

// updateTransitionStatus updates the overall status of NetworkResourceTransition CR.
func (r *NetworkResourceTransitionReconciler) updateTransitionStatus(
	ctx context.Context,
	transitionCR *migrationv1alpha1.NetworkResourceTransition,
	moveStatuses []migrationv1alpha1.IPAddressAllocationMoveStatus,
	needRequeue bool,
) (ctrl.Result, error) {
	return r.updateTransitionStatusWithCondition(ctx, transitionCR, moveStatuses, needRequeue, "", "")
}

// updateStatusWaiting records an in-progress status for every IPAddressAllocation
// move while the LB provider is unavailable, then requeues the transition.
func (r *NetworkResourceTransitionReconciler) updateStatusWaiting(
	ctx context.Context,
	transitionCR *migrationv1alpha1.NetworkResourceTransition,
	message string,
	conditionReason string,
) (ctrl.Result, error) {
	moveStatuses := make([]migrationv1alpha1.IPAddressAllocationMoveStatus, len(transitionCR.Spec.IPAddressAllocationMoves))
	for i, moveSpec := range transitionCR.Spec.IPAddressAllocationMoves {
		moveStatuses[i] = migrationv1alpha1.IPAddressAllocationMoveStatus{
			Source:  moveSpec.Source,
			Target:  moveSpec.Target,
			Phase:   migrationv1alpha1.TransitionPhaseInProgress,
			Message: message,
		}
	}
	result, err := r.updateTransitionStatusWithCondition(ctx, transitionCR, moveStatuses, true, conditionReason, message)
	if err != nil {
		return resultRequeue5Sec, err
	}
	return result, nil
}

func (r *NetworkResourceTransitionReconciler) updateTransitionStatusWithCondition(
	ctx context.Context,
	transitionCR *migrationv1alpha1.NetworkResourceTransition,
	moveStatuses []migrationv1alpha1.IPAddressAllocationMoveStatus,
	needRequeue bool,
	conditionReason string,
	conditionMessage string,
) (ctrl.Result, error) {
	oldStatus := transitionCR.Status.DeepCopy()
	progress := summarizeTransition(transitionCR, moveStatuses)
	needRequeue = needRequeue || progress.inProgress
	transitionCR.Status.ObservedGeneration = transitionCR.Generation
	transitionCR.Status.TotalResources = progress.total
	transitionCR.Status.SucceededResources = progress.succeeded
	transitionCR.Status.FailedResources = progress.failed
	transitionCR.Status.IPAddressAllocationMoves = moveStatuses

	condition := v1alpha1.Condition{
		Type:               v1alpha1.Ready,
		Status:             corev1.ConditionFalse,
		LastTransitionTime: metav1.Now(),
		Reason:             "MigrationInProgress",
		Message:            fmt.Sprintf("%d of %d resources migrated", progress.succeeded, progress.total),
	}
	if progress.failed == 0 && !needRequeue {
		transitionCR.Status.Phase = migrationv1alpha1.TransitionPhaseSucceeded
		condition.Status = corev1.ConditionTrue
		condition.Reason = "AllResourcesMigrated"
		condition.Message = "All network resources successfully transitioned"
	} else if progress.succeeded == 0 && progress.failed > 0 {
		transitionCR.Status.Phase = migrationv1alpha1.TransitionPhaseFailed
		condition.Reason = "MigrationFailed"
		condition.Message = fmt.Sprintf("%d resources failed to migrate", progress.failed)
	} else if progress.failed > 0 {
		transitionCR.Status.Phase = migrationv1alpha1.TransitionPhaseFailed
		condition.Reason = "MigrationPartiallyFailed"
		condition.Message = fmt.Sprintf("%d succeeded, %d failed", progress.succeeded, progress.failed)
	} else {
		transitionCR.Status.Phase = migrationv1alpha1.TransitionPhaseInProgress
	}
	if needRequeue {
		transitionCR.Status.Phase = migrationv1alpha1.TransitionPhaseInProgress
		if conditionReason == "" {
			condition.Reason = "MigrationInProgress"
			condition.Message = fmt.Sprintf(
				"Migration is still in progress; %d succeeded, %d failed out of %d resources",
				progress.succeeded, progress.failed, progress.total,
			)
		}
	}
	if conditionReason != "" {
		condition.Reason = conditionReason
		condition.Message = conditionMessage
	}
	updatedCondition := false
	for i := range transitionCR.Status.Conditions {
		previous := &transitionCR.Status.Conditions[i]
		if previous.Type != condition.Type {
			continue
		}
		if previous.Status == condition.Status {
			condition.LastTransitionTime = previous.LastTransitionTime
		}
		*previous = condition
		updatedCondition = true
		break
	}
	if !updatedCondition {
		transitionCR.Status.Conditions = append(transitionCR.Status.Conditions, condition)
	}

	// Preserve LastTransitionTime while Ready is unchanged.
	// Skip unchanged status to avoid triggering another reconcile through our own update.
	if !apiequality.Semantic.DeepEqual(*oldStatus, transitionCR.Status) {
		if err := r.Client.Status().Update(ctx, transitionCR); err != nil {
			log.Error(err, "Failed to update NetworkResourceTransition status", "Name", transitionCR.Name)
			return resultRequeue, err
		}
	}

	if needRequeue {
		return resultRequeue5Sec, nil
	}
	return common.ResultNormal, nil
}
