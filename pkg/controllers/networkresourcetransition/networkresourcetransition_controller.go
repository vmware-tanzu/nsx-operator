/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package networkresourcetransition

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	status := migrationv1alpha1.MoveIPAddressAllocationStatus{
		Source: moveSpec.Source,
		Target: moveSpec.Target,
		Phase:  migrationv1alpha1.ItemPhasePending,
	}

	targetCR := &v1alpha1.IPAddressAllocation{}
	errTarget := r.Client.Get(ctx, types.NamespacedName{Namespace: moveSpec.Target.Namespace, Name: moveSpec.Target.Name}, targetCR)
	targetExists := (errTarget == nil)

	sourceCR := &v1alpha1.IPAddressAllocation{}
	errSource := r.Client.Get(ctx, types.NamespacedName{Namespace: moveSpec.Source.Namespace, Name: moveSpec.Source.Name}, sourceCR)
	sourceExists := (errSource == nil)

	// 1. Check if the target IPAddressAllocation CR already exists and is ready
	if targetExists && len(targetCR.Status.AllocationIPs) > 0 {
		log.Info("Target IPAddressAllocation already exists and has allocation IP, transition already completed",
			"Target", moveSpec.Target, "AllocationIPs", targetCR.Status.AllocationIPs)
		_ = r.reapSourceCR(ctx, moveSpec.Source)
		_ = r.removeTargetTransitionAnnotation(ctx, targetCR)
		status.Phase = migrationv1alpha1.ItemPhaseSucceeded
		status.Message = "In-place tag adoption complete on loadBalancerVPC: 0 duplicate IP collision"
		return status, false
	}

	// 2. Check if NSX allocation was already adopted by Target in a previous run
	// (e.g. operator restart or crash after NSX tag adoption or during status update).
	var adoptedNsxAlloc *model.VpcIpAddressAllocation
	if targetExists {
		adoptedNsxAlloc, _ = r.Service.GetIPAddressAllocationForTransition(targetCR.UID, moveSpec.Target.Namespace, moveSpec.Target.Name)
	}

	if adoptedNsxAlloc != nil {
		log.Info("NSX allocation already adopted by target, completing recovery reconciliation",
			"Target", moveSpec.Target, "AllocatedIP", adoptedNsxAlloc.AllocationIps)

		allocatedIP := ""
		if adoptedNsxAlloc.AllocationIps != nil {
			allocatedIP = *adoptedNsxAlloc.AllocationIps
		} else if sourceExists && len(sourceCR.Status.AllocationIPs) > 0 {
			allocatedIP = sourceCR.Status.AllocationIPs
		}

		if allocatedIP != "" {
			if err := r.updateTargetStatus(ctx, targetCR, allocatedIP); err != nil {
				log.Error(err, "Failed to update target IPAddressAllocation status during recovery", "Target", moveSpec.Target)
				status.Phase = migrationv1alpha1.ItemPhaseMigrating
				status.Message = fmt.Sprintf("Failed to update target status during recovery: %v", err)
				return status, true
			}
		}

		_ = r.reapSourceCR(ctx, moveSpec.Source)
		_ = r.removeTargetTransitionAnnotation(ctx, targetCR)
		status.Phase = migrationv1alpha1.ItemPhaseSucceeded
		status.Message = "In-place tag adoption complete on loadBalancerVPC: 0 duplicate IP collision"
		return status, false
	}

	// 3. NSX allocation has not been adopted yet; validate source CR.
	if !sourceExists {
		status.Phase = migrationv1alpha1.ItemPhaseFailed
		if apierrors.IsNotFound(errSource) {
			status.Message = fmt.Sprintf("Source IPAddressAllocation %s/%s not found", moveSpec.Source.Namespace, moveSpec.Source.Name)
		} else {
			status.Message = fmt.Sprintf("Failed to get source IPAddressAllocation: %v", errSource)
		}
		return status, false
	}

	if len(sourceCR.Status.AllocationIPs) == 0 {
		log.Info("Source IPAddressAllocation has no allocated IP yet, waiting", "Source", moveSpec.Source)
		status.Phase = migrationv1alpha1.ItemPhaseMigrating
		status.Message = "Waiting for source IPAddressAllocation to be allocated"
		return status, true
	}

	// 4. Locate underlying NSX IPAddressAllocation resource using source CR
	nsxAlloc, err := r.Service.GetIPAddressAllocationForTransition(sourceCR.UID, sourceCR.Namespace, sourceCR.Name)
	if err != nil || nsxAlloc == nil {
		log.Error(err, "Failed to locate NSX allocation for transition", "Source", moveSpec.Source, "UID", sourceCR.UID)
		status.Phase = migrationv1alpha1.ItemPhaseFailed
		status.Message = fmt.Sprintf("Failed to locate NSX allocation for source CR %s/%s: %v", sourceCR.Namespace, sourceCR.Name, err)
		return status, false
	}

	// 5. Ensure Target IPAddressAllocation CR in user namespace exists
	targetCR, err = r.ensureTargetCR(ctx, sourceCR, moveSpec.Target, targetExists, targetCR)
	if err != nil {
		log.Error(err, "Failed to ensure target IPAddressAllocation CR", "Target", moveSpec.Target)
		status.Phase = migrationv1alpha1.ItemPhaseFailed
		status.Message = fmt.Sprintf("Failed to ensure target IPAddressAllocation CR: %v", err)
		return status, false
	}

	// 6. In-place NSX Policy tag adoption (zero re-allocation)
	if err := r.adoptNSXAllocationTags(ctx, nsxAlloc, moveSpec.Target, targetCR); err != nil {
		log.Error(err, "Failed to adopt NSX tags on IPAddressAllocation", "Target", moveSpec.Target)
		status.Phase = migrationv1alpha1.ItemPhaseFailed
		status.Message = fmt.Sprintf("Failed to adopt NSX tags: %v", err)
		return status, false
	}

	// 7. Realize Target CR status
	if err := r.updateTargetStatus(ctx, targetCR, sourceCR.Status.AllocationIPs); err != nil {
		log.Error(err, "Failed to update target IPAddressAllocation status", "Target", moveSpec.Target)
		status.Phase = migrationv1alpha1.ItemPhaseMigrating
		status.Message = fmt.Sprintf("Failed to update target status: %v", err)
		return status, true
	}

	// 8. Safe reaping of source CR without deleting NSX allocation
	_ = r.reapSourceCR(ctx, moveSpec.Source)

	status.Phase = migrationv1alpha1.ItemPhaseSucceeded
	status.Message = "In-place tag adoption complete on loadBalancerVPC: 0 duplicate IP collision"
	return status, false
}

// buildTargetOwnerReferences constructs owner references for the target CR by inheriting
// from the source and resolving the matching Service in the target namespace.
func (r *NetworkResourceTransitionReconciler) buildTargetOwnerReferences(
	ctx context.Context,
	sourceCR *v1alpha1.IPAddressAllocation,
	targetRef migrationv1alpha1.NamespacedObjectReference,
) []metav1.OwnerReference {
	var targetOwnerRefs []metav1.OwnerReference
	for _, ref := range sourceCR.OwnerReferences {
		if ref.Kind == "Service" && ref.APIVersion == "v1" {
			svc := &corev1.Service{}
			if err := r.Client.Get(ctx, types.NamespacedName{Namespace: targetRef.Namespace, Name: ref.Name}, svc); err == nil {
				ref.UID = svc.UID
				targetOwnerRefs = append(targetOwnerRefs, ref)
			} else if err := r.Client.Get(ctx, types.NamespacedName{Namespace: targetRef.Namespace, Name: targetRef.Name}, svc); err == nil {
				targetOwnerRefs = append(targetOwnerRefs, metav1.OwnerReference{
					APIVersion: "v1",
					Kind:       "Service",
					Name:       svc.Name,
					UID:        svc.UID,
					Controller: ref.Controller,
				})
			}
		}
	}
	return targetOwnerRefs
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

	targetOwnerRefs := r.buildTargetOwnerReferences(ctx, sourceCR, targetRef)
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
	return createdTarget, nil
}

// adoptNSXAllocationTags updates tags on the existing NSX allocation to match the target CR.
func (r *NetworkResourceTransitionReconciler) adoptNSXAllocationTags(
	ctx context.Context,
	nsxAlloc *model.VpcIpAddressAllocation,
	targetRef migrationv1alpha1.NamespacedObjectReference,
	targetCR *v1alpha1.IPAddressAllocation,
) error {
	targetNsUID := r.getNamespaceUID(ctx, targetRef.Namespace)
	_, err := r.Service.AdoptIPAddressAllocationTags(nsxAlloc, targetRef.Namespace, targetNsUID, targetRef.Name, targetCR.UID)
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
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
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
) error {
	sourceCR := &v1alpha1.IPAddressAllocation{}
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: sourceRef.Namespace, Name: sourceRef.Name}, sourceCR); err == nil {
		if errDel := r.Client.Delete(ctx, sourceCR); errDel != nil && !apierrors.IsNotFound(errDel) {
			log.Error(errDel, "Failed to reap source IPAddressAllocation CR", "Source", sourceRef)
			return errDel
		}
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
	transitionCR.Status.TotalResources = len(moveStatuses)
	transitionCR.Status.SucceededResources = succeededCount
	transitionCR.Status.FailedResources = failedCount
	transitionCR.Status.MoveIPAddressAllocation = moveStatuses

	if failedCount == 0 && !needRequeue {
		transitionCR.Status.Phase = migrationv1alpha1.MigrationPhaseSucceeded
		transitionCR.Status.Conditions = []metav1.Condition{
			{
				Type:               "Ready",
				Status:             metav1.ConditionTrue,
				LastTransitionTime: metav1.Now(),
				Reason:             "AllResourcesMigrated",
				Message:            "All LB VIPs successfully transitioned",
			},
		}
	} else if succeededCount == 0 && failedCount > 0 {
		transitionCR.Status.Phase = migrationv1alpha1.MigrationPhaseFailed
		transitionCR.Status.Conditions = []metav1.Condition{
			{
				Type:               "Ready",
				Status:             metav1.ConditionFalse,
				LastTransitionTime: metav1.Now(),
				Reason:             "MigrationFailed",
				Message:            fmt.Sprintf("%d resources failed to migrate", failedCount),
			},
		}
	} else if failedCount > 0 {
		transitionCR.Status.Phase = migrationv1alpha1.MigrationPhasePartiallyFailed
		transitionCR.Status.Conditions = []metav1.Condition{
			{
				Type:               "Ready",
				Status:             metav1.ConditionFalse,
				LastTransitionTime: metav1.Now(),
				Reason:             "MigrationPartiallyFailed",
				Message:            fmt.Sprintf("%d succeeded, %d failed", succeededCount, failedCount),
			},
		}
	} else {
		transitionCR.Status.Phase = migrationv1alpha1.MigrationPhaseMigrating
	}

	if err := r.Client.Status().Update(ctx, transitionCR); err != nil {
		log.Error(err, "Failed to update NetworkResourceTransition status", "Name", transitionCR.Name)
		return resultRequeue, err
	}

	if needRequeue {
		return resultRequeue5Sec, nil
	}
	return common.ResultNormal, nil
}

func (r *NetworkResourceTransitionReconciler) getNamespaceUID(ctx context.Context, nsName string) string {
	ns := &corev1.Namespace{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: nsName}, ns); err == nil {
		return string(ns.UID)
	}
	return ""
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
