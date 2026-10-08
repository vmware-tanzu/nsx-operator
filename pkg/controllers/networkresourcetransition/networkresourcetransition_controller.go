/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package networkresourcetransition

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
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
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	migrationv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

var (
	log               = logger.Log
	resultRequeue     = reconcile.Result{Requeue: true}
	resultRequeue5Sec = reconcile.Result{RequeueAfter: 5 * time.Second}
)

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

	if !transitionCR.ObjectMeta.DeletionTimestamp.IsZero() {
		log.Info("NetworkResourceTransition CR marked for deletion, nothing to clean up", "Name", req.Name)
		return common.ResultNormal, nil
	}

	totalItems := len(transitionCR.Spec.MoveIPAddressAllocation)
	if totalItems == 0 {
		log.Debug("No MoveIPAddressAllocation specified in NetworkResourceTransition, ignoring", "Name", req.Name)
		return common.ResultNormal, nil
	}

	// 1. Check LB Provider up-front. If querying fails, requeue immediately.
	// If LB provider is not NSXLB (e.g. AVI or None), ignore this CR completely without modifying its status,
	// as other LB providers will manage their own resources.
	lbProvider, err := r.VPCService.GetLBProvider()
	if err != nil {
		log.Error(err, "Failed to get LB provider for NetworkResourceTransition, will requeue", "Name", req.Name)
		return resultRequeue5Sec, err
	}
	if lbProvider != servicecommon.NSXLB {
		log.Info("LB provider is not nsxlb, ignoring NetworkResourceTransition CR without updating status",
			"Name", req.Name, "lbProvider", lbProvider)
		return common.ResultNormal, nil
	}

	succeededCount := 0
	failedCount := 0
	needRequeue := false
	moveStatuses := make([]migrationv1alpha1.MoveIPAddressAllocationStatus, totalItems)

	for i, moveSpec := range transitionCR.Spec.MoveIPAddressAllocation {
		status, itemNeedRequeue := r.reconcileMoveItem(ctx, moveSpec)
		moveStatuses[i] = status
		switch status.Phase {
		case migrationv1alpha1.ItemPhaseSucceeded:
			succeededCount++
		case migrationv1alpha1.ItemPhaseFailed:
			failedCount++
		}
		if itemNeedRequeue {
			needRequeue = true
		}
	}

	return r.updateTransitionStatus(ctx, transitionCR, moveStatuses, succeededCount, failedCount, needRequeue)
}

// reconcileMoveItem reconciles a single MoveIPAddressAllocation specification.
func (r *NetworkResourceTransitionReconciler) reconcileMoveItem(
	ctx context.Context,
	moveSpec migrationv1alpha1.MoveIPAddressAllocationSpec,
) (migrationv1alpha1.MoveIPAddressAllocationStatus, bool) {
	status := migrationv1alpha1.MoveIPAddressAllocationStatus{Source: moveSpec.Source, Target: moveSpec.Target}
	fail := func(message string, requeue bool) (migrationv1alpha1.MoveIPAddressAllocationStatus, bool) {
		status.Phase = migrationv1alpha1.ItemPhaseFailed
		if requeue {
			status.Phase = migrationv1alpha1.ItemPhaseMigrating
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
		return fail(fmt.Sprintf("Failed to get target IPAddressAllocation: %v", errTarget), true)
	}
	targetExists := errTarget == nil
	if targetExists && !targetCR.DeletionTimestamp.IsZero() {
		return fail("Target IPAddressAllocation is being deleted", false)
	}
	sourceCR := &v1alpha1.IPAddressAllocation{}
	errSource := r.Client.Get(ctx, types.NamespacedName{Namespace: moveSpec.Source.Namespace, Name: moveSpec.Source.Name}, sourceCR)
	if errSource != nil && !apierrors.IsNotFound(errSource) {
		return fail(fmt.Sprintf("Failed to get source IPAddressAllocation: %v", errSource), true)
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
		if targetExists && sourceCR.Spec != targetCR.Spec {
			return fail("Target IPAddressAllocation spec conflicts with source", false)
		}
	}

	complete := func(allocatedIP string) (migrationv1alpha1.MoveIPAddressAllocationStatus, bool) {
		if err := r.updateTargetStatus(ctx, targetCR, allocatedIP); err != nil {
			return fail(fmt.Sprintf("Failed to update target status: %v", err), true)
		}
		if err := r.reapSourceCR(ctx, moveSpec.Source, sourceUID); err != nil {
			return fail(fmt.Sprintf("Failed to reap source IPAddressAllocation: %v", err), true)
		}
		status.Phase = migrationv1alpha1.ItemPhaseSucceeded
		status.Message = "In-place tag adoption complete on loadBalancerVPC: 0 duplicate IP collision"
		return status, false
	}

	// A ready status alone does not prove this request adopted the source VIP.
	// The target UID must own a confirmed allocation, and any remaining source
	// must identify that same VIP and backend object.
	if targetExists {
		adopted, err := r.Service.GetIPAddressAllocationForTransition(targetCR.UID, targetCR.Namespace, targetCR.Name)
		if err != nil {
			return fail(fmt.Sprintf("Failed to get target allocation: %v", err), true)
		}
		if adopted != nil {
			if adopted.AllocationIps == nil || *adopted.AllocationIps == "" || adopted.Path == nil ||
				nsxutil.FindTag(adopted.Tags, servicecommon.TagScopeIPAddressAllocationCRUID) != string(targetCR.UID) ||
				nsxutil.FindTag(adopted.Tags, servicecommon.TagScopeNamespace) != targetCR.Namespace ||
				nsxutil.FindTag(adopted.Tags, servicecommon.TagScopeIPAddressAllocationCRName) != targetCR.Name {
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
					return fail(fmt.Sprintf("Failed to verify source allocation: %v", err), true)
				}
				if sourceAlloc != nil && (sourceAlloc.Path == nil || *sourceAlloc.Path != *adopted.Path) {
					return fail("Target NSX allocation conflicts with source allocation", false)
				}
			}
			return complete(allocatedIP)
		}
		if targetCR.Status.AllocationIPs != "" {
			return fail("Ready target has no confirmed allocation for its current UID", true)
		}
	}
	if !sourceExists {
		return fail(fmt.Sprintf("Source IPAddressAllocation %s/%s not found", moveSpec.Source.Namespace, moveSpec.Source.Name), false)
	}
	nsxAlloc, err := r.Service.GetIPAddressAllocationForTransition(sourceCR.UID, sourceCR.Namespace, sourceCR.Name)
	if err != nil {
		return fail(fmt.Sprintf("Failed to locate source allocation: %v", err), true)
	}
	if nsxAlloc == nil || nsxAlloc.AllocationIps == nil || *nsxAlloc.AllocationIps != sourceCR.Status.AllocationIPs {
		return fail("Source NSX allocation does not match the saved VIP", true)
	}
	targetCR, err = r.ensureTargetCR(ctx, sourceCR, moveSpec.Target, targetExists, targetCR)
	if err != nil {
		return fail(fmt.Sprintf("Failed to ensure target IPAddressAllocation CR: %v", err), true)
	}
	if err := r.adoptNSXAllocationTags(ctx, nsxAlloc, moveSpec.Target, targetCR); err != nil {
		return fail(fmt.Sprintf("Failed to adopt NSX tags: %v", err), true)
	}
	return complete(sourceCR.Status.AllocationIPs)
}

// buildTargetOwnerReferences constructs owner references for the target CR by inheriting
// from the source and resolving the matching Service in the target namespace.
func (r *NetworkResourceTransitionReconciler) buildTargetOwnerReferences(
	ctx context.Context,
	sourceCR *v1alpha1.IPAddressAllocation,
	targetRef migrationv1alpha1.NamespacedObjectReference,
) ([]metav1.OwnerReference, error) {
	var targetOwnerRefs []metav1.OwnerReference
	for _, ref := range sourceCR.OwnerReferences {
		if ref.Kind == "Service" && ref.APIVersion == "v1" {
			svc := &corev1.Service{}
			err := r.Client.Get(ctx, types.NamespacedName{Namespace: targetRef.Namespace, Name: ref.Name}, svc)
			if err == nil {
				ref.UID = svc.UID
				targetOwnerRefs = append(targetOwnerRefs, ref)
				continue
			}
			if !apierrors.IsNotFound(err) {
				return nil, fmt.Errorf("resolve target Service owner: %w", err)
			}
			if err := r.Client.Get(ctx, types.NamespacedName{Namespace: targetRef.Namespace, Name: targetRef.Name}, svc); err == nil {
				targetOwnerRefs = append(targetOwnerRefs, metav1.OwnerReference{
					APIVersion:         "v1",
					Kind:               "Service",
					Name:               svc.Name,
					UID:                svc.UID,
					Controller:         ref.Controller,
					BlockOwnerDeletion: ref.BlockOwnerDeletion,
				})
			} else {
				return nil, fmt.Errorf("resolve target Service owner: %w", err)
			}
		}
	}
	return targetOwnerRefs, nil
}

// ensureTargetCR ensures the target IPAddressAllocation CR exists in the target namespace.
func (r *NetworkResourceTransitionReconciler) ensureTargetCR(
	ctx context.Context,
	sourceCR *v1alpha1.IPAddressAllocation,
	targetRef migrationv1alpha1.NamespacedObjectReference,
	targetExists bool,
	existingTarget *v1alpha1.IPAddressAllocation,
) (*v1alpha1.IPAddressAllocation, error) {
	if targetExists && existingTarget != nil {
		if !existingTarget.DeletionTimestamp.IsZero() {
			return nil, fmt.Errorf("target IPAddressAllocation is being deleted")
		}
		if existingTarget.Spec != sourceCR.Spec {
			return nil, fmt.Errorf("target IPAddressAllocation spec conflicts with source")
		}
		if existingTarget.Annotations[servicecommon.AnnotationTransitionTarget] != "true" {
			return nil, fmt.Errorf("existing target is not awaiting transition adoption")
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
	return r.ensureTargetCR(ctx, sourceCR, targetRef, true, createdTarget)
}

// adoptNSXAllocationTags updates tags on the existing NSX allocation to match the target CR.
func (r *NetworkResourceTransitionReconciler) adoptNSXAllocationTags(
	ctx context.Context,
	nsxAlloc *model.VpcIpAddressAllocation,
	targetRef migrationv1alpha1.NamespacedObjectReference,
	targetCR *v1alpha1.IPAddressAllocation,
) error {
	targetNsUID, err := r.getNamespaceUID(ctx, targetRef.Namespace)
	if err != nil {
		return err
	}
	_, err = r.Service.AdoptIPAddressAllocationTags(nsxAlloc, targetRef.Namespace, targetNsUID, targetRef.Name, targetCR.UID)
	return err
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
			return fmt.Errorf("target IPAddressAllocation UID changed during transition")
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
			return fmt.Errorf("target IPAddressAllocation UID changed during transition")
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
	sourceRef migrationv1alpha1.NamespacedObjectReference,
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
		return fmt.Errorf("source IPAddressAllocation UID changed during transition")
	}
	if err := r.Client.Delete(ctx, sourceCR, client.Preconditions{UID: &expectedUID}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// updateTransitionStatus updates the overall status of NetworkResourceTransition CR.
func (r *NetworkResourceTransitionReconciler) updateTransitionStatus(
	ctx context.Context,
	transitionCR *migrationv1alpha1.NetworkResourceTransition,
	moveStatuses []migrationv1alpha1.MoveIPAddressAllocationStatus,
	succeededCount, failedCount int,
	needRequeue bool,
) (ctrl.Result, error) {
	oldStatus := transitionCR.Status.DeepCopy()
	transitionCR.Status.TotalResources = len(moveStatuses)
	transitionCR.Status.SucceededResources = succeededCount
	transitionCR.Status.FailedResources = failedCount
	transitionCR.Status.MoveIPAddressAllocation = moveStatuses

	condition := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		ObservedGeneration: transitionCR.Generation,
		Reason:             "MigrationInProgress",
		Message:            fmt.Sprintf("%d of %d resources migrated", succeededCount, len(moveStatuses)),
	}
	if failedCount == 0 && !needRequeue {
		transitionCR.Status.Phase = migrationv1alpha1.MigrationPhaseSucceeded
		condition.Status = metav1.ConditionTrue
		condition.Reason = "AllResourcesMigrated"
		condition.Message = "All LB VIPs successfully transitioned"
	} else if succeededCount == 0 && failedCount > 0 {
		transitionCR.Status.Phase = migrationv1alpha1.MigrationPhaseFailed
		condition.Reason = "MigrationFailed"
		condition.Message = fmt.Sprintf("%d resources failed to migrate", failedCount)
	} else if failedCount > 0 {
		transitionCR.Status.Phase = migrationv1alpha1.MigrationPhasePartiallyFailed
		condition.Reason = "MigrationPartiallyFailed"
		condition.Message = fmt.Sprintf("%d succeeded, %d failed", succeededCount, failedCount)
	} else {
		transitionCR.Status.Phase = migrationv1alpha1.MigrationPhaseMigrating
	}
	meta.SetStatusCondition(&transitionCR.Status.Conditions, condition)

	// SetStatusCondition preserves LastTransitionTime while Ready is unchanged.
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
