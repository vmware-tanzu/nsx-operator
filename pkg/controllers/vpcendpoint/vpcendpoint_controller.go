/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package vpcendpoint

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpcendpoint"
)

var (
	log           = logger.Log
	resultNormal  = common.ResultNormal
	resultRequeue = common.ResultRequeue
	MetricResType = common.MetricResTypeVPCEndpoint
)

const readyConditionType = "Ready"

// VPCEndpointReconciler reconciles a VPCEndpoint object
type VPCEndpointReconciler struct {
	client.Client
	Scheme        *apimachineryruntime.Scheme
	Service       *vpcendpoint.VPCEndpointService
	Recorder      record.EventRecorder
	StatusUpdater common.StatusUpdater
}

func setReadyStatusFalse(k8sClient client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, err error, _ ...interface{}) {
	ve := obj.(*v1alpha1.VPCEndpoint)
	cond := metav1.Condition{
		Type:               readyConditionType,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: ve.Generation,
		Reason:             "VPCEndpointNotReady",
		Message:            fmt.Sprintf("error occurred while processing the VPCEndpoint CR. Error: %v", err),
		LastTransitionTime: transitionTime,
	}
	if apimeta.SetStatusCondition(&ve.Status.Conditions, cond) {
		if updateErr := k8sClient.Status().Update(ctx, ve); updateErr != nil {
			log.Error(updateErr, "Failed to update status", "Name", ve.Name, "Namespace", ve.Namespace)
		}
	}
}

func setDeleteFailedStatus(k8sClient client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, err error) {
	ve := obj.(*v1alpha1.VPCEndpoint)
	cond := metav1.Condition{
		Type:               "DeleteFailure",
		Status:             metav1.ConditionTrue,
		ObservedGeneration: ve.Generation,
		Reason:             "VPCEndpointInUse",
		Message:            fmt.Sprintf("NSX rejected the delete: %v", err),
		LastTransitionTime: transitionTime,
	}
	if apimeta.SetStatusCondition(&ve.Status.Conditions, cond) {
		if updateErr := k8sClient.Status().Update(ctx, ve); updateErr != nil {
			log.Error(updateErr, "Failed to update status", "Name", ve.Name, "Namespace", ve.Namespace)
		}
	}
}

func setReadyStatusTrue(k8sClient client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, _ ...interface{}) {
	ve := obj.(*v1alpha1.VPCEndpoint)
	cond := metav1.Condition{
		Type:               readyConditionType,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: ve.Generation,
		Reason:             "VPCEndpointReady",
		Message:            "NSX VPCEndpoint has been successfully created/updated",
		LastTransitionTime: transitionTime,
	}
	if apimeta.SetStatusCondition(&ve.Status.Conditions, cond) {
		if updateErr := k8sClient.Status().Update(ctx, ve); updateErr != nil {
			log.Error(updateErr, "Failed to update status", "Name", ve.Name, "Namespace", ve.Namespace)
		}
	}
}

func (r *VPCEndpointReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &v1alpha1.VPCEndpoint{}
	log.Info("Reconciling VPCEndpoint CR", "VPCEndpoint", req.NamespacedName)
	r.StatusUpdater.IncreaseSyncTotal()
	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		if apierrors.IsNotFound(err) {
			err = r.Service.DeleteVPCEndpointByNamespacedName(req.Namespace, req.Name)
			if err != nil {
				r.StatusUpdater.DeleteFail(req.NamespacedName, nil, err)
				return resultRequeue, err
			}
			r.StatusUpdater.DeleteSuccess(req.NamespacedName, nil)
			return common.ResultNormal, nil
		}
		return resultRequeue, err
	}
	if obj.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.handleUpdate(ctx, obj)
	}
	return r.handleDeletion(ctx, req, obj)
}

func (r *VPCEndpointReconciler) handleUpdate(ctx context.Context, obj *v1alpha1.VPCEndpoint) (ctrl.Result, error) {
	r.StatusUpdater.IncreaseUpdateTotal()
	if err := r.Service.CreateOrUpdateVPCEndpoint(ctx, obj); err != nil {
		r.StatusUpdater.UpdateFail(ctx, obj, err, "", setReadyStatusFalse)
		return resultRequeue, err
	}
	r.StatusUpdater.UpdateSuccess(ctx, obj, setReadyStatusTrue)
	return resultNormal, nil
}

func (r *VPCEndpointReconciler) handleDeletion(ctx context.Context, req ctrl.Request, obj *v1alpha1.VPCEndpoint) (ctrl.Result, error) {
	r.StatusUpdater.IncreaseDeleteTotal()
	if err := r.Service.DeleteVPCEndpoint(obj); err != nil {
		setDeleteFailedStatus(r.Client, ctx, obj, metav1.Now(), err)
		r.StatusUpdater.DeleteFail(req.NamespacedName, obj, err)
		return resultRequeue, err
	}
	r.StatusUpdater.DeleteSuccess(req.NamespacedName, obj)
	return resultNormal, nil
}

func (r *VPCEndpointReconciler) setupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.VPCEndpoint{}).
		WithEventFilter(common.VPCNamespacePredicate(r.Client)).
		WithOptions(
			controller.Options{
				MaxConcurrentReconciles: common.NumReconcile(),
			}).
		Complete(r)
}

func (r *VPCEndpointReconciler) CollectGarbage(ctx context.Context) error {
	log.Info("VPCEndpoint garbage collector started")
	vpcEndpointSet := r.Service.ListVPCEndpointID()
	if len(vpcEndpointSet) == 0 {
		return nil
	}

	vpcEndpointCRList := &v1alpha1.VPCEndpointList{}
	if err := r.Client.List(ctx, vpcEndpointCRList); err != nil {
		log.Error(err, "Failed to list VPCEndpoint CR")
		return err
	}
	crVPCEndpointSet := sets.New[string]()
	for _, ve := range vpcEndpointCRList.Items {
		crVPCEndpointSet.Insert(string(ve.UID))
	}

	diffSet := vpcEndpointSet.Difference(crVPCEndpointSet)
	var errList []error
	for elem := range diffSet {
		log.Info("GC collected nsx VPCEndpoint", "UID", elem)
		if err := r.Service.DeleteVPCEndpoint(types.UID(elem)); err != nil {
			log.Error(err, "Failed to delete nsx VPCEndpoint", "UID", elem)
			errList = append(errList, err)
		}
	}
	if len(errList) > 0 {
		return fmt.Errorf("errors found in VPCEndpoint garbage collection: %s", errList)
	}
	return nil
}

// RestoreReconcile is a no-op for VPCEndpoint: every field is sourced from
// the CR spec, nothing needs to be recovered from status, and GC already
// recreates any NSX object missing for an existing CR.
func (r *VPCEndpointReconciler) RestoreReconcile() error {
	return nil
}

func (r *VPCEndpointReconciler) StartController(mgr ctrl.Manager, _ webhook.Server) error {
	if err := r.setupWithManager(mgr); err != nil {
		log.Error(err, "Failed to create vpcendpoint controller")
		return err
	}
	go common.GenericGarbageCollector(make(chan bool), servicecommon.GCInterval, r.CollectGarbage)
	return nil
}

func NewVPCEndpointReconciler(mgr ctrl.Manager, vpcEndpointService *vpcendpoint.VPCEndpointService) *VPCEndpointReconciler {
	reconciler := &VPCEndpointReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Service:  vpcEndpointService,
		Recorder: mgr.GetEventRecorderFor("vpcendpoint-controller"), //nolint:staticcheck // record.EventRecorder; StatusUpdater not on events.EventRecorder yet
	}
	reconciler.StatusUpdater = common.NewStatusUpdater(reconciler.Client, reconciler.Service.NSXConfig, reconciler.Recorder, common.MetricResTypeVPCEndpoint, "VPCEndpoint", "VPCEndpoint")
	return reconciler
}
