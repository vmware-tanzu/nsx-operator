/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package networkresourcetransition

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	migrationv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	log               = logger.Log
	resultRequeue     = reconcile.Result{Requeue: true}
	resultRequeue5Sec = reconcile.Result{RequeueAfter: 5 * time.Second}
)

// permanentTransitionValidationError marks input/state conflicts that cannot
// be resolved by retrying the same transition request.
type permanentTransitionValidationError struct {
	err error
}

func (e *permanentTransitionValidationError) Error() string { return e.err.Error() }
func (e *permanentTransitionValidationError) Unwrap() error { return e.err }

func isPermanentTransitionValidationError(err error) bool {
	var validationErr *permanentTransitionValidationError
	return errors.As(err, &validationErr)
}

// shouldRetryTransitionCompletionError keeps the transition active until the
// target status and metadata are published and the source CR is reaped. API
// failures in these cleanup steps may be resolved externally (for example, by
// fixing RBAC), so only explicit transition validation conflicts are terminal.
func shouldRetryTransitionCompletionError(err error) bool {
	return err != nil && !isPermanentTransitionValidationError(err)
}

// shouldRetryTransitionError handles Kubernetes API request errors here.
// NSX operation errors follow IPAddressAllocationReconciler behavior and are
// requeued; NSX transport handles its own retry and credential-refresh cases.
// TODO: If IPAddressAllocation transition handling identifies non-retryable NSX
// error codes, classify them by structured error code in shared NSX error
// handling and apply that classification here; do not inspect error messages.
func shouldRetryTransitionError(err error) bool {
	if err == nil || isPermanentTransitionValidationError(err) {
		return false
	}
	if apierrors.IsBadRequest(err) || apierrors.IsInvalid(err) ||
		apierrors.IsForbidden(err) {
		return false
	}
	return true
}

// IPAddressAllocationTransitionService defines the narrow interface needed specifically by
// NetworkResourceTransitionReconciler for migrating IPAddressAllocation resources without
// polluting the general IPAddressAllocationServiceProvider interface.
type IPAddressAllocationTransitionService interface {
	GetIPAddressAllocationForTransition(uid types.UID, ns, name string) (*model.VpcIpAddressAllocation, error)
	AdoptIPAddressAllocationTags(existingAlloc *model.VpcIpAddressAllocation, targetNs, targetNsUID, targetName string, targetUID types.UID) (*model.VpcIpAddressAllocation, error)
}

type NetworkResourceTransitionReconciler struct {
	Client     client.Client
	Scheme     *runtime.Scheme
	Service    IPAddressAllocationTransitionService
	VPCService servicecommon.VPCServiceProvider
	Recorder   record.EventRecorder
}

func (r *NetworkResourceTransitionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log.Info("Reconciling NetworkResourceTransition CR", "Name", req.Name)
	transitionCR := &migrationv1alpha1.NetworkResourceTransition{}
	if err := r.Client.Get(ctx, req.NamespacedName, transitionCR); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("NetworkResourceTransition CR deleted", "Name", req.Name)
			return common.ResultNormal, nil
		}
		log.Error(err, "Failed to get NetworkResourceTransition CR", "Name", req.Name)
		return resultRequeue, err
	}

	totalItems := len(transitionCR.Spec.IPAddressAllocationMoves)
	if totalItems == 0 {
		log.Debug("No IPAddressAllocationMoves specified in NetworkResourceTransition, ignoring", "Name", req.Name)
		return common.ResultNormal, nil
	}
	deleting := !transitionCR.DeletionTimestamp.IsZero()
	hasFinalizer := controllerutil.ContainsFinalizer(transitionCR, servicecommon.NetworkResourceTransitionFinalizerName)
	if deleting && !hasFinalizer {
		// This CR was deleted before the controller persisted its finalizer. No
		// migration side effect is allowed to start without deletion protection.
		log.Info("NetworkResourceTransition was deleted before migration started", "Name", req.Name)
		return common.ResultNormal, nil
	}
	if deleting && (transitionCR.Status.Phase == migrationv1alpha1.TransitionPhaseSucceeded ||
		transitionCR.Status.Phase == migrationv1alpha1.TransitionPhaseFailed) {
		if err := r.removeFinalizer(ctx, transitionCR); err != nil {
			log.Error(err, "Failed to remove finalizer from terminal NetworkResourceTransition", "Name", req.Name)
			return resultRequeue, err
		}
		return common.ResultNormal, nil
	}
	if transitionCR.Status.Phase == migrationv1alpha1.TransitionPhaseSucceeded ||
		transitionCR.Status.Phase == migrationv1alpha1.TransitionPhaseFailed {
		return common.ResultNormal, nil
	}

	// Check the LB provider up-front. Retry any query error; a successfully queried
	// but unsupported provider is a terminal failure for this transition.
	lbProvider, err := r.VPCService.GetLBProvider()
	if err != nil {
		log.Error(err, "Failed to get LB provider for NetworkResourceTransition, will requeue", "Name", req.Name)
		return r.updateStatusWaiting(ctx, transitionCR,
			fmt.Sprintf("Failed to get LB provider: %v", err), "WaitingForLBProvider")
	}
	if lbProvider == "" || lbProvider == servicecommon.NoneLB {
		message := "Waiting for LB provider to be ready"
		log.Info(message, "Name", req.Name, "lbProvider", lbProvider)
		return r.updateStatusWaiting(ctx, transitionCR, message, "WaitingForLBProvider")
	}
	if lbProvider != servicecommon.NSXLB {
		message := fmt.Sprintf("unsupported LB provider %q for IPAddressAllocation transition; only %q is supported", lbProvider, servicecommon.NSXLB)
		log.Info("LB provider does not support NetworkResourceTransition, marking it failed",
			"Name", req.Name, "lbProvider", lbProvider)
		moveStatuses := make([]migrationv1alpha1.IPAddressAllocationMoveStatus, totalItems)
		for i, moveSpec := range transitionCR.Spec.IPAddressAllocationMoves {
			moveStatuses[i] = migrationv1alpha1.IPAddressAllocationMoveStatus{
				Source:  moveSpec.Source,
				Target:  moveSpec.Target,
				Phase:   migrationv1alpha1.TransitionPhaseFailed,
				Message: message,
			}
		}
		return r.updateTransitionStatus(ctx, transitionCR, moveStatuses, false)
	}
	if !hasFinalizer {
		controllerutil.AddFinalizer(transitionCR, servicecommon.NetworkResourceTransitionFinalizerName)
		if err := r.Client.Update(ctx, transitionCR); err != nil {
			log.Error(err, "Failed to add finalizer to NetworkResourceTransition", "Name", req.Name)
			return resultRequeue, err
		}
	}

	needRequeue := false
	moveStatuses := make([]migrationv1alpha1.IPAddressAllocationMoveStatus, totalItems)

	for i, moveSpec := range transitionCR.Spec.IPAddressAllocationMoves {
		status, itemNeedRequeue := r.reconcileMoveItem(ctx, moveSpec)
		moveStatuses[i] = status
		if itemNeedRequeue {
			needRequeue = true
		}
	}

	result, err := r.updateTransitionStatus(ctx, transitionCR, moveStatuses, needRequeue)
	if err != nil || transitionCR.Status.Phase != migrationv1alpha1.TransitionPhaseSucceeded || !deleting {
		return result, err
	}
	if err := r.removeFinalizer(ctx, transitionCR); err != nil {
		log.Error(err, "Failed to remove finalizer from completed NetworkResourceTransition", "Name", req.Name)
		return resultRequeue, err
	}
	return common.ResultNormal, nil
}

func (r *NetworkResourceTransitionReconciler) removeFinalizer(
	ctx context.Context,
	transitionCR *migrationv1alpha1.NetworkResourceTransition,
) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &migrationv1alpha1.NetworkResourceTransition{}
		if err := r.Client.Get(ctx, types.NamespacedName{Name: transitionCR.Name}, latest); err != nil {
			return err
		}
		if latest.UID != transitionCR.UID {
			return fmt.Errorf("NetworkResourceTransition UID changed while removing finalizer")
		}
		if !controllerutil.RemoveFinalizer(latest, servicecommon.NetworkResourceTransitionFinalizerName) {
			return nil
		}
		return r.Client.Update(ctx, latest)
	})
}

// reconcileMoveItem reconciles a single IPAddressAllocationMoves specification.
func (r *NetworkResourceTransitionReconciler) reconcileMoveItem(
	ctx context.Context,
	moveSpec migrationv1alpha1.IPAddressAllocationMoveSpec,
) (migrationv1alpha1.IPAddressAllocationMoveStatus, bool) {
	status := migrationv1alpha1.IPAddressAllocationMoveStatus{Source: moveSpec.Source, Target: moveSpec.Target}
	fail := func(message string, requeue bool) (migrationv1alpha1.IPAddressAllocationMoveStatus, bool) {
		status.Phase = migrationv1alpha1.TransitionPhaseFailed
		if requeue {
			status.Phase = migrationv1alpha1.TransitionPhaseInProgress
		}
		status.Message = message
		return status, requeue
	}
	if moveSpec.Source == moveSpec.Target {
		return fail("Source and target IPAddressAllocation must be different objects", false)
	}

	targetCR := &v1alpha1.IPAddressAllocation{}
	errTarget := r.Client.Get(ctx, types.NamespacedName{Namespace: moveSpec.Target.Namespace, Name: moveSpec.Target.Name}, targetCR)
	if errTarget != nil && !apierrors.IsNotFound(errTarget) {
		return fail(fmt.Sprintf("Failed to get target IPAddressAllocation: %v", errTarget), shouldRetryTransitionError(errTarget))
	}
	targetExists := errTarget == nil
	if targetExists {
		if err := validateTargetCR(nil, targetCR); err != nil {
			return fail(fmt.Sprintf("Invalid target IPAddressAllocation: %v", err), !isPermanentTransitionValidationError(err))
		}
	}
	sourceCR := &v1alpha1.IPAddressAllocation{}
	errSource := r.Client.Get(ctx, types.NamespacedName{Namespace: moveSpec.Source.Namespace, Name: moveSpec.Source.Name}, sourceCR)
	if errSource != nil && !apierrors.IsNotFound(errSource) {
		return fail(fmt.Sprintf("Failed to get source IPAddressAllocation: %v", errSource), shouldRetryTransitionError(errSource))
	}
	sourceExists := errSource == nil
	var sourceUID types.UID
	if sourceExists {
		sourceUID = sourceCR.UID
		if sourceCR.Spec.UsedFor != v1alpha1.UsedForLBFrontend {
			return fail("Source IPAddressAllocation is not LB_FRONTEND", false)
		}
		if sourceCR.Status.AllocationIPs == "" {
			return fail("Waiting for source IPAddressAllocation to be allocated", true)
		}
		if targetExists {
			if err := validateTargetCR(sourceCR, targetCR); err != nil {
				return fail(fmt.Sprintf("Invalid target IPAddressAllocation: %v", err), !isPermanentTransitionValidationError(err))
			}
		}
	}
	failForMissingSource := func() (migrationv1alpha1.IPAddressAllocationMoveStatus, bool) {
		message := fmt.Sprintf("Source IPAddressAllocation %s/%s not found", moveSpec.Source.Namespace, moveSpec.Source.Name)
		if targetExists && targetCR.Annotations[servicecommon.AnnotationTransitionTarget] == "true" {
			if err := r.markTargetTransitionFailed(ctx, targetCR, message+"; target was not adopted and remains quarantined"); err != nil {
				return fail(fmt.Sprintf("%s; failed to record target failure: %v", message, err), shouldRetryTransitionCompletionError(err))
			}
		}
		return fail(message, false)
	}

	complete := func(allocatedIP string) (migrationv1alpha1.IPAddressAllocationMoveStatus, bool) {
		if err := r.updateTargetStatus(ctx, targetCR, allocatedIP); err != nil {
			return fail(fmt.Sprintf("Failed to update target status: %v", err), shouldRetryTransitionCompletionError(err))
		}
		if err := r.reapSourceCR(ctx, moveSpec.Source, sourceUID); err != nil {
			return fail(fmt.Sprintf("Failed to reap source IPAddressAllocation: %v", err), shouldRetryTransitionCompletionError(err))
		}
		status.Phase = migrationv1alpha1.TransitionPhaseSucceeded
		status.Message = "In-place tag adoption complete on loadBalancerVPC: 0 duplicate IP collision"
		return status, false
	}

	// A ready status alone does not prove this request adopted the source VIP.
	// The target UID must own a confirmed allocation, and any remaining source
	// must identify that same VIP and backend object.
	if targetExists {
		adopted, err := r.Service.GetIPAddressAllocationForTransition(targetCR.UID, targetCR.Namespace, targetCR.Name)
		if err != nil {
			return fail(fmt.Sprintf("Failed to get target allocation: %v", err), shouldRetryTransitionError(err))
		}
		if adopted != nil {
			if adopted.AllocationIps == nil || *adopted.AllocationIps == "" || adopted.Path == nil ||
				!util.IPAddressAllocationHasOwner(adopted, targetCR.UID, targetCR.Namespace, targetCR.Name) {
				return fail("Target allocation ownership is not confirmed", true)
			}
			allocatedIP := *adopted.AllocationIps
			if targetCR.Status.AllocationIPs != "" && targetCR.Status.AllocationIPs != allocatedIP {
				return fail("Target status conflicts with its NSX allocation", false)
			}
			if sourceExists {
				if sourceCR.Status.AllocationIPs != allocatedIP {
					return fail("Target VIP conflicts with source VIP", false)
				}
				sourceAlloc, err := r.Service.GetIPAddressAllocationForTransition(sourceCR.UID, sourceCR.Namespace, sourceCR.Name)
				if err != nil {
					return fail(fmt.Sprintf("Failed to verify source allocation: %v", err), shouldRetryTransitionError(err))
				}
				if sourceAlloc != nil && (sourceAlloc.Path == nil || *sourceAlloc.Path != *adopted.Path) {
					return fail("Target NSX allocation conflicts with source allocation", false)
				}
			}
			return complete(allocatedIP)
		}
		if targetCR.Status.AllocationIPs != "" {
			if !sourceExists {
				return failForMissingSource()
			}
			return fail("Ready target has no confirmed allocation for its current UID", true)
		}
	}
	if !sourceExists {
		return failForMissingSource()
	}
	nsxAlloc, err := r.Service.GetIPAddressAllocationForTransition(sourceCR.UID, sourceCR.Namespace, sourceCR.Name)
	if err != nil {
		return fail(fmt.Sprintf("Failed to locate source allocation: %v", err), shouldRetryTransitionError(err))
	}
	if nsxAlloc == nil || nsxAlloc.AllocationIps == nil || *nsxAlloc.AllocationIps != sourceCR.Status.AllocationIPs {
		return fail("Source NSX allocation does not match the saved VIP", true)
	}
	if targetExists {
		if err := validateTargetForAdoption(sourceCR, targetCR); err != nil {
			return fail(fmt.Sprintf("Invalid target IPAddressAllocation: %v", err), !isPermanentTransitionValidationError(err))
		}
	}
	targetCR, err = r.ensureTargetCR(ctx, sourceCR, moveSpec.Target, targetExists, targetCR)
	if err != nil {
		return fail(fmt.Sprintf("Failed to ensure target IPAddressAllocation CR: %v", err), !isPermanentTransitionValidationError(err))
	}
	if err := r.adoptNSXAllocationTags(ctx, nsxAlloc, moveSpec.Target, targetCR); err != nil {
		return fail(fmt.Sprintf("Failed to adopt NSX tags: %v", err), shouldRetryTransitionError(err))
	}
	return complete(sourceCR.Status.AllocationIPs)
}

// buildTargetOwnerReferences constructs owner references for the target CR by inheriting
// from the source and resolving the matching Service in the target namespace.
func (r *NetworkResourceTransitionReconciler) buildTargetOwnerReferences(
	ctx context.Context,
	sourceCR *v1alpha1.IPAddressAllocation,
	targetRef migrationv1alpha1.NamespacedName,
) ([]metav1.OwnerReference, error) {
	var targetOwnerRefs []metav1.OwnerReference
	for _, ref := range sourceCR.OwnerReferences {
		if ref.Kind != "Service" || ref.APIVersion != "v1" {
			continue
		}
		svc, err := r.resolveTargetService(ctx, targetRef, ref.Name)
		if err != nil {
			return nil, fmt.Errorf("resolve target Service owner: %w", err)
		}
		ref.Name = svc.Name
		ref.UID = svc.UID
		targetOwnerRefs = append(targetOwnerRefs, ref)
	}
	return targetOwnerRefs, nil
}

func (r *NetworkResourceTransitionReconciler) resolveTargetService(ctx context.Context, targetRef migrationv1alpha1.NamespacedName, preferredName string) (*corev1.Service, error) {
	svc := &corev1.Service{}
	err := r.Client.Get(ctx, types.NamespacedName{Namespace: targetRef.Namespace, Name: preferredName}, svc)
	if err == nil {
		return svc, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: targetRef.Namespace, Name: targetRef.Name}, svc); err != nil {
		return nil, err
	}
	return svc, nil
}

// validateTargetCR also applies to recovery after adoption, when the source may
// already be gone and the transient annotation has been removed.
func validateTargetCR(source, target *v1alpha1.IPAddressAllocation) error {
	if !target.DeletionTimestamp.IsZero() {
		return fmt.Errorf("target IPAddressAllocation is being deleted")
	}
	if source != nil && target.Spec != source.Spec {
		return &permanentTransitionValidationError{err: fmt.Errorf("target IPAddressAllocation spec conflicts with source")}
	}
	return nil
}

func validateTargetForAdoption(source, target *v1alpha1.IPAddressAllocation) error {
	if err := validateTargetCR(source, target); err != nil {
		return err
	}
	if target.Annotations[servicecommon.AnnotationTransitionTarget] != "true" {
		return &permanentTransitionValidationError{err: fmt.Errorf("existing target is not awaiting transition adoption")}
	}
	return nil
}

// ensureTargetCR ensures the target IPAddressAllocation CR exists in the target namespace.
func (r *NetworkResourceTransitionReconciler) ensureTargetCR(
	ctx context.Context,
	sourceCR *v1alpha1.IPAddressAllocation,
	targetRef migrationv1alpha1.NamespacedName,
	targetExists bool,
	existingTarget *v1alpha1.IPAddressAllocation,
) (*v1alpha1.IPAddressAllocation, error) {
	if targetExists && existingTarget != nil {
		if err := validateTargetForAdoption(sourceCR, existingTarget); err != nil {
			return nil, err
		}
		return existingTarget, nil
	}

	targetAnnotations := make(map[string]string)
	for k, v := range sourceCR.Annotations {
		targetAnnotations[k] = v
	}
	// Mark the target CR with a transient handshake annotation.
	// This instructs IPAddressAllocationReconciler to skip reconciliation on the newly created CR
	// until NetworkResourceTransition controller finishes adopting the existing NSX allocation tags
	// and populating Status.AllocationIPs. This prevents a race condition where the IPAA controller
	// allocates a new, redundant VIP from NSX before tag adoption completes.
	targetAnnotations[servicecommon.AnnotationTransitionTarget] = "true"

	targetOwnerRefs, err := r.buildTargetOwnerReferences(ctx, sourceCR, targetRef)
	if err != nil {
		return nil, err
	}
	targetCR := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Name:            targetRef.Name,
			Namespace:       targetRef.Namespace,
			OwnerReferences: targetOwnerRefs,
			Labels:          sourceCR.Labels,
			Annotations:     targetAnnotations,
		},
		Spec: sourceCR.Spec,
	}

	if err := r.Client.Create(ctx, targetCR); err != nil && !apierrors.IsAlreadyExists(err) {
		return nil, fmt.Errorf("create target IPAddressAllocation CR: %w", err)
	}

	createdTarget := &v1alpha1.IPAddressAllocation{}
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: targetRef.Namespace, Name: targetRef.Name}, createdTarget); err != nil {
		return nil, fmt.Errorf("get target IPAddressAllocation CR after create: %w", err)
	}
	// Create may race with another writer. Validate the persisted target too.
	if err := validateTargetForAdoption(sourceCR, createdTarget); err != nil {
		return nil, err
	}
	return createdTarget, nil
}

// adoptNSXAllocationTags updates tags on the existing NSX allocation to match the target CR.
func (r *NetworkResourceTransitionReconciler) adoptNSXAllocationTags(
	ctx context.Context,
	nsxAlloc *model.VpcIpAddressAllocation,
	targetRef migrationv1alpha1.NamespacedName,
	targetCR *v1alpha1.IPAddressAllocation,
) error {
	targetNsUID, err := r.getNamespaceUID(ctx, targetRef.Namespace)
	if err != nil {
		return err
	}
	_, err = r.Service.AdoptIPAddressAllocationTags(nsxAlloc, targetRef.Namespace, targetNsUID, targetRef.Name, targetCR.UID)
	return err
}

// markTargetTransitionFailed records that a pending target could not be adopted
// because its source disappeared. Keep the transition annotation so the normal
// IPAddressAllocation reconciler cannot allocate a new VIP for this target.
func (r *NetworkResourceTransitionReconciler) markTargetTransitionFailed(
	ctx context.Context,
	targetCR *v1alpha1.IPAddressAllocation,
	message string,
) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.IPAddressAllocation{}
		if err := r.Client.Get(ctx, types.NamespacedName{Namespace: targetCR.Namespace, Name: targetCR.Name}, latest); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		if latest.UID != targetCR.UID {
			return &permanentTransitionValidationError{err: fmt.Errorf("target IPAddressAllocation UID changed during transition")}
		}
		if latest.Annotations == nil || latest.Annotations[servicecommon.AnnotationTransitionTarget] != "true" {
			return nil
		}

		condition := v1alpha1.Condition{
			Type:               v1alpha1.Ready,
			Status:             corev1.ConditionFalse,
			LastTransitionTime: metav1.Now(),
			Reason:             "TransitionSourceMissing",
			Message:            message,
		}
		for i := range latest.Status.Conditions {
			if latest.Status.Conditions[i].Type != v1alpha1.Ready {
				continue
			}
			previous := &latest.Status.Conditions[i]
			if previous.Status == condition.Status && previous.Reason == condition.Reason && previous.Message == condition.Message {
				condition.LastTransitionTime = previous.LastTransitionTime
				return nil
			}
			*previous = condition
			latest.Status.AllocationIPs = ""
			return r.Client.Status().Update(ctx, latest)
		}
		latest.Status.Conditions = append(latest.Status.Conditions, condition)
		latest.Status.AllocationIPs = ""
		return r.Client.Status().Update(ctx, latest)
	})
}

// updateTargetStatus updates the target CR's status and ready condition,
// and removes the transient AnnotationTransitionTarget handshake annotation so the target CR metadata remains clean.
func (r *NetworkResourceTransitionReconciler) updateTargetStatus(
	ctx context.Context,
	targetCR *v1alpha1.IPAddressAllocation,
	allocatedIP string,
) error {
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.IPAddressAllocation{}
		if err := r.Client.Get(ctx, types.NamespacedName{Namespace: targetCR.Namespace, Name: targetCR.Name}, latest); err != nil {
			return err
		}
		if latest.UID != targetCR.UID {
			return &permanentTransitionValidationError{err: fmt.Errorf("target IPAddressAllocation UID changed during transition")}
		}
		for _, condition := range latest.Status.Conditions {
			if latest.Status.AllocationIPs == allocatedIP && condition.Type == v1alpha1.Ready && condition.Status == corev1.ConditionTrue {
				return nil
			}
		}
		latest.Status.AllocationIPs = allocatedIP
		latest.Status.Conditions = []v1alpha1.Condition{
			{
				Type:               v1alpha1.Ready,
				Status:             corev1.ConditionTrue,
				LastTransitionTime: metav1.Now(),
				Reason:             "IPAddressAllocationReady",
				Message:            "NSX IPAddressAllocation has been successfully transitioned",
			},
		}
		return r.Client.Status().Update(ctx, latest)
	}); err != nil {
		return err
	}

	// Remove transient handshake annotation once status is populated so that target CR is left clean
	// and regular IPAddressAllocation controller lifecycle can resume.
	return r.removeTargetTransitionAnnotation(ctx, targetCR)
}

// removeTargetTransitionAnnotation removes AnnotationTransitionTarget from target CR metadata if present.
func (r *NetworkResourceTransitionReconciler) removeTargetTransitionAnnotation(
	ctx context.Context,
	targetCR *v1alpha1.IPAddressAllocation,
) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.IPAddressAllocation{}
		if err := r.Client.Get(ctx, types.NamespacedName{Namespace: targetCR.Namespace, Name: targetCR.Name}, latest); err != nil {
			return err
		}
		if latest.UID != targetCR.UID {
			return &permanentTransitionValidationError{err: fmt.Errorf("target IPAddressAllocation UID changed during transition")}
		}
		if latest.Annotations == nil || latest.Annotations[servicecommon.AnnotationTransitionTarget] == "" {
			return nil
		}
		delete(latest.Annotations, servicecommon.AnnotationTransitionTarget)
		return r.Client.Update(ctx, latest)
	})
}

// reapSourceCR safely deletes the source IPAddressAllocation CR from K8s if it exists.
func (r *NetworkResourceTransitionReconciler) reapSourceCR(
	ctx context.Context,
	sourceRef migrationv1alpha1.NamespacedName,
	expectedUID types.UID,
) error {
	sourceCR := &v1alpha1.IPAddressAllocation{}
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: sourceRef.Namespace, Name: sourceRef.Name}, sourceCR); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if expectedUID == "" || sourceCR.UID != expectedUID {
		return &permanentTransitionValidationError{err: fmt.Errorf("source IPAddressAllocation UID changed during transition")}
	}
	if err := r.Client.Delete(ctx, sourceCR, client.Preconditions{UID: &expectedUID}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (r *NetworkResourceTransitionReconciler) getNamespaceUID(ctx context.Context, nsName string) (string, error) {
	ns := &corev1.Namespace{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: nsName}, ns); err != nil {
		return "", err
	}
	return string(ns.UID), nil
}

func (r *NetworkResourceTransitionReconciler) RestoreReconcile() error {
	return nil
}

func (r *NetworkResourceTransitionReconciler) CollectGarbage(ctx context.Context) error {
	return nil
}

func (r *NetworkResourceTransitionReconciler) StartController(mgr ctrl.Manager, _ webhook.Server) error {
	//TODO: remove those code when CRD ready
	kind := migrationv1alpha1.GroupVersion.WithKind("NetworkResourceTransition")
	if _, err := mgr.GetRESTMapper().RESTMapping(kind.GroupKind(), kind.Version); err != nil {
		if meta.IsNoMatchError(err) {
			log.Warn("NetworkResourceTransition CRD is not installed; skipping its controller until operator restart",
				"apiVersion", kind.GroupVersion().String(), "kind", kind.Kind)
			return nil
		}
		return fmt.Errorf("failed to discover NetworkResourceTransition API: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&migrationv1alpha1.NetworkResourceTransition{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: common.NumReconcile(),
		}).
		Complete(r)
}

func NewNetworkResourceTransitionReconciler(
	mgr ctrl.Manager,
	ipAddressAllocationService IPAddressAllocationTransitionService,
	vpcService servicecommon.VPCServiceProvider,
) *NetworkResourceTransitionReconciler {
	return &NetworkResourceTransitionReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		Service:    ipAddressAllocationService,
		VPCService: vpcService,
		Recorder:   mgr.GetEventRecorderFor("networkresourcetransition-controller"), //nolint:staticcheck
	}
}
