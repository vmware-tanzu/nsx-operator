/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package serviceendpoint

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
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/serviceendpoint"
)

var (
	log           = logger.Log
	resultNormal  = common.ResultNormal
	resultRequeue = common.ResultRequeue
	MetricResType = common.MetricResTypeServiceEndpoint
)

const readyConditionType = "Ready"

// ServiceEndpointReconciler reconciles a ServiceEndpoint object
type ServiceEndpointReconciler struct {
	client.Client
	Scheme        *apimachineryruntime.Scheme
	Service       *serviceendpoint.ServiceEndpointService
	Recorder      record.EventRecorder
	StatusUpdater common.StatusUpdater
}

func setReadyStatusFalse(k8sClient client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, err error, _ ...interface{}) {
	se := obj.(*v1alpha1.ServiceEndpoint)
	cond := metav1.Condition{
		Type:               readyConditionType,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: se.Generation,
		Reason:             "ServiceEndpointNotReady",
		Message:            fmt.Sprintf("error occurred while processing the ServiceEndpoint CR. Error: %v", err),
		LastTransitionTime: transitionTime,
	}
	if apimeta.SetStatusCondition(&se.Status.Conditions, cond) {
		if updateErr := k8sClient.Status().Update(ctx, se); updateErr != nil {
			log.Error(updateErr, "Failed to update status", "Name", se.Name, "Namespace", se.Namespace)
		}
	}
}

func setDeleteFailedStatus(k8sClient client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, err error) {
	se := obj.(*v1alpha1.ServiceEndpoint)
	cond := metav1.Condition{
		Type:               "DeleteFailure",
		Status:             metav1.ConditionTrue,
		ObservedGeneration: se.Generation,
		Reason:             "ServiceEndpointInUse",
		Message:            fmt.Sprintf("NSX rejected the delete: %v", err),
		LastTransitionTime: transitionTime,
	}
	if apimeta.SetStatusCondition(&se.Status.Conditions, cond) {
		if updateErr := k8sClient.Status().Update(ctx, se); updateErr != nil {
			log.Error(updateErr, "Failed to update status", "Name", se.Name, "Namespace", se.Namespace)
		}
	}
}

func setReadyStatusTrue(k8sClient client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, _ ...interface{}) {
	se := obj.(*v1alpha1.ServiceEndpoint)
	cond := metav1.Condition{
		Type:               readyConditionType,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: se.Generation,
		Reason:             "ServiceEndpointReady",
		Message:            "NSX ServiceEndpoint has been successfully created/updated",
		LastTransitionTime: transitionTime,
	}
	if apimeta.SetStatusCondition(&se.Status.Conditions, cond) {
		if updateErr := k8sClient.Status().Update(ctx, se); updateErr != nil {
			log.Error(updateErr, "Failed to update status", "Name", se.Name, "Namespace", se.Namespace)
		}
	}
}

func (r *ServiceEndpointReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &v1alpha1.ServiceEndpoint{}
	log.Info("Reconciling ServiceEndpoint CR", "ServiceEndpoint", req.NamespacedName)
	r.StatusUpdater.IncreaseSyncTotal()
	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		if apierrors.IsNotFound(err) {
			err = r.Service.DeleteServiceEndpointByNamespacedName(req.Namespace, req.Name)
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

func (r *ServiceEndpointReconciler) handleUpdate(ctx context.Context, obj *v1alpha1.ServiceEndpoint) (ctrl.Result, error) {
	r.StatusUpdater.IncreaseUpdateTotal()
	if err := r.Service.CreateOrUpdateServiceEndpoint(obj); err != nil {
		r.StatusUpdater.UpdateFail(ctx, obj, err, "", setReadyStatusFalse)
		return resultRequeue, err
	}
	r.StatusUpdater.UpdateSuccess(ctx, obj, setReadyStatusTrue)
	return resultNormal, nil
}

func (r *ServiceEndpointReconciler) handleDeletion(ctx context.Context, req ctrl.Request, obj *v1alpha1.ServiceEndpoint) (ctrl.Result, error) {
	r.StatusUpdater.IncreaseDeleteTotal()
	if err := r.Service.DeleteServiceEndpoint(obj); err != nil {
		setDeleteFailedStatus(r.Client, ctx, obj, metav1.Now(), err)
		r.StatusUpdater.DeleteFail(req.NamespacedName, obj, err)
		return resultRequeue, err
	}
	r.StatusUpdater.DeleteSuccess(req.NamespacedName, obj)
	return resultNormal, nil
}

func (r *ServiceEndpointReconciler) setupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ServiceEndpoint{}).
		WithEventFilter(common.VPCNamespacePredicate(r.Client)).
		WithOptions(
			controller.Options{
				MaxConcurrentReconciles: common.NumReconcile(),
			}).
		Complete(r)
}

func (r *ServiceEndpointReconciler) CollectGarbage(ctx context.Context) error {
	log.Info("ServiceEndpoint garbage collector started")
	serviceEndpointSet := r.Service.ListServiceEndpointID()
	if len(serviceEndpointSet) == 0 {
		return nil
	}

	serviceEndpointCRList := &v1alpha1.ServiceEndpointList{}
	if err := r.Client.List(ctx, serviceEndpointCRList); err != nil {
		log.Error(err, "Failed to list ServiceEndpoint CR")
		return err
	}
	crServiceEndpointSet := sets.New[string]()
	for _, se := range serviceEndpointCRList.Items {
		crServiceEndpointSet.Insert(string(se.UID))
	}

	diffSet := serviceEndpointSet.Difference(crServiceEndpointSet)
	var errList []error
	for elem := range diffSet {
		log.Info("GC collected nsx ServiceEndpoint", "UID", elem)
		if err := r.Service.DeleteServiceEndpoint(types.UID(elem)); err != nil {
			log.Error(err, "Failed to delete nsx ServiceEndpoint", "UID", elem)
			errList = append(errList, err)
		}
	}
	if len(errList) > 0 {
		return fmt.Errorf("errors found in ServiceEndpoint garbage collection: %s", errList)
	}
	return nil
}

// RestoreReconcile is a no-op for ServiceEndpoint: every field is sourced
// from the CR spec, nothing needs to be recovered from status, and GC
// already recreates any NSX object missing for an existing CR.
func (r *ServiceEndpointReconciler) RestoreReconcile() error {
	return nil
}

func (r *ServiceEndpointReconciler) StartController(mgr ctrl.Manager, _ webhook.Server) error {
	if err := r.setupWithManager(mgr); err != nil {
		log.Error(err, "Failed to create serviceendpoint controller")
		return err
	}
	go common.GenericGarbageCollector(make(chan bool), servicecommon.GCInterval, r.CollectGarbage)
	return nil
}

func NewServiceEndpointReconciler(mgr ctrl.Manager, serviceEndpointService *serviceendpoint.ServiceEndpointService) *ServiceEndpointReconciler {
	reconciler := &ServiceEndpointReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Service:  serviceEndpointService,
		Recorder: mgr.GetEventRecorderFor("serviceendpoint-controller"), //nolint:staticcheck // record.EventRecorder; StatusUpdater not on events.EventRecorder yet
	}
	reconciler.StatusUpdater = common.NewStatusUpdater(reconciler.Client, reconciler.Service.NSXConfig, reconciler.Recorder, common.MetricResTypeServiceEndpoint, "ServiceEndpoint", "ServiceEndpoint")
	return reconciler
}
