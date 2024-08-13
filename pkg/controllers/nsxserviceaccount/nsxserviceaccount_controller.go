/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsxserviceaccount

import (
	"context"
	"errors"
	"fmt"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	nsxvmwarecomv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/nsxserviceaccount"
)

var (
	log                     = &logger.Log
	ResultNormal            = common.ResultNormal
	ResultRequeue           = common.ResultRequeue
	ResultRequeueAfter5mins = common.ResultRequeueAfter5mins
	MetricResType           = common.MetricResTypeNSXServiceAccount
	count                   = uint16(0)
	ca                      []byte
)

// NSXServiceAccountReconciler reconciles a NSXServiceAccount object.
// Requires NSXT 4.0.1
//
// create/delete event will be processed by Reconcile
//
// update event with realized resource missing NSX resources will be processed by Reconcile since NSXT 4.1.2
//
// # GarbageCollector will clean up stale NSX resources and Secret on every GCInterval
//
// GarbageCollector will check and make all Secrets' CA up-to-date on first GC run
//
// GarbageCollector will check and rotate client cert if needed on every GCValidationInterval*GCInterval since NSXT 4.1.3
type NSXServiceAccountReconciler struct {
	client.Client
	Scheme   *apimachineryruntime.Scheme
	Service  *nsxserviceaccount.NSXServiceAccountService
	Recorder record.EventRecorder
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.13.0/pkg/reconcile
func (r *NSXServiceAccountReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &nsxvmwarecomv1alpha1.NSXServiceAccount{}
	log.Info("reconciling CR", "nsxserviceaccount", req.NamespacedName)
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerSyncTotal, MetricResType)

	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		log.Error(err, "unable to fetch NSXServiceAccount CR", "req", req.NamespacedName)
		return ResultNormal, client.IgnoreNotFound(err)
	}

	// Since NSXServiceAccount service can only be activated from NSX 4.1.0 onwards,
	// So need to check NSX version before starting NSXServiceAccount reconcile
	if !r.Service.NSXClient.NSXCheckVersion(nsx.ServiceAccount) {
		err := errors.New("NSX version check failed, NSXServiceAccount feature is not supported")
		updateFail(r, &ctx, obj, &err)
		// if NSX version check fails, it will be put back to reconcile queue and be reconciled after 5 minutes
		return ResultRequeueAfter5mins, nil
	}

	if obj.ObjectMeta.DeletionTimestamp.IsZero() {
		metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateTotal, MetricResType)
		if !controllerutil.ContainsFinalizer(obj, servicecommon.NSXServiceAccountFinalizerName) {
			controllerutil.AddFinalizer(obj, servicecommon.NSXServiceAccountFinalizerName)
			if err := r.Client.Update(ctx, obj); err != nil {
				log.Error(err, "add finalizer", "nsxserviceaccount", req.NamespacedName)
				updateFail(r, &ctx, obj, &err)
				return ResultRequeue, err
			}
			log.V(1).Info("added finalizer on CR", "nsxserviceaccount", req.NamespacedName)
		}

		if nsxserviceaccount.IsNSXServiceAccountRealized(&obj.Status) {
			if r.Service.NSXClient.NSXCheckVersion(nsx.ServiceAccountRestore) {
				if err := r.Service.RestoreRealizedNSXServiceAccount(ctx, obj); err != nil {
					log.Error(err, "update realized failed, would retry exponentially", "nsxserviceaccount", req.NamespacedName)
					metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateFailTotal, MetricResType)
					return ResultRequeue, err
				}
			}
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateSuccessTotal, MetricResType)
			return ResultNormal, nil
		}
		if err := r.Service.CreateOrUpdateNSXServiceAccount(ctx, obj); err != nil {
			log.Error(err, "operate failed, would retry exponentially", "nsxserviceaccount", req.NamespacedName)
			updateFail(r, &ctx, obj, &err)
			return ResultRequeue, err
		}
		updateSuccess(r, &ctx, obj)
	} else {
		if controllerutil.ContainsFinalizer(obj, servicecommon.NSXServiceAccountFinalizerName) {
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, MetricResType)
			if err := r.Service.DeleteNSXServiceAccount(ctx, types.NamespacedName{
				Namespace: obj.Namespace,
				Name:      obj.Name,
			}, obj.UID); err != nil {
				log.Error(err, "deleting failed, would retry exponentially", "nsxserviceaccount", req.NamespacedName)
				deleteFail(r, &ctx, obj, &err)
				return ResultRequeue, err
			}
			controllerutil.RemoveFinalizer(obj, servicecommon.NSXServiceAccountFinalizerName)
			if err := r.Client.Update(ctx, obj); err != nil {
				log.Error(err, "removing finalizer failed, would retry exponentially", "nsxserviceaccount", req.NamespacedName)
				deleteFail(r, &ctx, obj, &err)
				return ResultRequeue, err
			}
			log.V(1).Info("removed finalizer", "nsxserviceaccount", req.NamespacedName)
			deleteSuccess(r, &ctx, obj)
		} else {
			// only print a message because it's not a normal case
			log.Info("finalizers cannot be recognized", "nsxserviceaccount", req.NamespacedName)
		}
	}

	return ResultNormal, nil
}

// setupWithManager sets up the controller with the Manager.
func (r *NSXServiceAccountReconciler) setupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&nsxvmwarecomv1alpha1.NSXServiceAccount{}).
		WithEventFilter(predicate.Funcs{
			DeleteFunc: func(e event.DeleteEvent) bool {
				// Suppress Delete events to avoid filtering them out in the Reconcile function
				return false
			},
		}).
		WithOptions(
			controller.Options{
				MaxConcurrentReconciles: common.NumReconcile(),
			}).
		Complete(r)
}

// Start setup manager and launch GC
func (r *NSXServiceAccountReconciler) Start(mgr ctrl.Manager) error {
	err := r.setupWithManager(mgr)
	if err != nil {
		return err
	}
	return nil
}

// CollectGarbage collect NSXServiceAccount which has been removed from crd.
// it implements the interface GarbageCollector method.
func (r *NSXServiceAccountReconciler) CollectGarbage(ctx context.Context) {
	log.Info("nsx service account garbage collector started")
	ca = r.Service.NSXConfig.GetCACert()
	nsxServiceAccountList := &nsxvmwarecomv1alpha1.NSXServiceAccountList{}
	var gcSuccessCount, gcErrorCount uint32
	var err error
	nsxServiceAccountUIDSet := r.Service.ListNSXServiceAccountRealization()
	if len(nsxServiceAccountUIDSet) == 0 {
		return
	}
	err = r.Client.List(ctx, nsxServiceAccountList)
	if err != nil {
		log.Error(err, "failed to list NSXServiceAccount CR")
		return
	}
	gcSuccessCount, gcErrorCount = r.garbageCollector(nsxServiceAccountUIDSet, nsxServiceAccountList)
	log.V(1).Info("gc collects NSXServiceAccount CR", "success", gcSuccessCount, "error", gcErrorCount)
	count, ca = r.validateRealized(count, ca, nsxServiceAccountList)
}

func (r *NSXServiceAccountReconciler) validateRealized(count uint16, ca []byte, nsxServiceAccountList *nsxvmwarecomv1alpha1.NSXServiceAccountList) (uint16, []byte) {
	// Validate ca at first time
	// Validate client cert every GCValidationInterval
	if count == 0 {
		for _, account := range nsxServiceAccountList.Items {
			nsxServiceAccount := account
			if nsxserviceaccount.IsNSXServiceAccountRealized(&nsxServiceAccount.Status) {
				if err := r.Service.ValidateAndUpdateRealizedNSXServiceAccount(context.TODO(), &nsxServiceAccount, ca); err != nil {
					log.Error(err, "Failed to update realized NSXServiceAccount", "namespace", nsxServiceAccount.Namespace, "name", nsxServiceAccount.Name)
				}
			}
		}
		ca = nil
	}
	count++
	if count == servicecommon.GCValidationInterval {
		count = 0
	}
	return count, ca
}

func (r *NSXServiceAccountReconciler) garbageCollector(nsxServiceAccountUIDSet sets.Set[string], nsxServiceAccountList *nsxvmwarecomv1alpha1.NSXServiceAccountList) (gcSuccessCount, gcErrorCount uint32) {
	nsxServiceAccountCRUIDMap := map[string]types.NamespacedName{}
	for _, nsxServiceAccount := range nsxServiceAccountList.Items {
		nsxServiceAccountCRUIDMap[string(nsxServiceAccount.UID)] = types.NamespacedName{
			Namespace: nsxServiceAccount.Namespace,
			Name:      nsxServiceAccount.Name,
		}
	}

	for nsxServiceAccountUID := range nsxServiceAccountUIDSet {
		if _, ok := nsxServiceAccountCRUIDMap[nsxServiceAccountUID]; ok {
			continue
		}
		log.V(1).Info("gc collects NSXServiceAccount CR", "UID", nsxServiceAccountUID)
		namespacedName := r.Service.GetNSXServiceAccountNameByUID(nsxServiceAccountUID)
		if namespacedName.Namespace == "" || namespacedName.Name == "" {
			log.Info("gc cannot get namespace/name, skip", "namespace", namespacedName.Namespace, "name", namespacedName.Name, "uid", nsxServiceAccountUID)
			continue
		}
		metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, MetricResType)
		err := r.Service.DeleteNSXServiceAccount(context.TODO(), namespacedName, types.UID(nsxServiceAccountUID))
		if err != nil {
			gcErrorCount++
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResType)
		} else {
			gcSuccessCount++
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResType)
		}
	}
	return
}

func (r *NSXServiceAccountReconciler) updateNSXServiceAccountStatus(ctx *context.Context, o *nsxvmwarecomv1alpha1.NSXServiceAccount, e *error) {
	obj := o
	if e != nil && *e != nil {
		obj = o.DeepCopy()
		obj.Status.Phase = nsxvmwarecomv1alpha1.NSXServiceAccountPhaseFailed
		obj.Status.Reason = fmt.Sprintf("Error: %v", *e)
		obj.Status.Conditions = nsxserviceaccount.GenerateNSXServiceAccountConditions(obj.Status.Conditions, obj.Generation, metav1.ConditionFalse, nsxvmwarecomv1alpha1.ConditionReasonRealizationError, fmt.Sprintf("Error: %v", *e))
	}
	err := r.Client.Status().Update(*ctx, obj)
	if err != nil {
		log.Error(err, "update NSXServiceAccount failed", "Namespace", obj.Namespace, "Name", obj.Name, "Status", obj.Status)
	} else {
		log.V(1).Info("updated NSXServiceAccount", "Namespace", obj.Namespace, "Name", obj.Name, "Status", obj.Status)
	}
}

func updateFail(r *NSXServiceAccountReconciler, c *context.Context, o *nsxvmwarecomv1alpha1.NSXServiceAccount, e *error) {
	r.updateNSXServiceAccountStatus(c, o, e)
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailUpdate, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateFailTotal, MetricResType)
}

func deleteFail(r *NSXServiceAccountReconciler, c *context.Context, o *nsxvmwarecomv1alpha1.NSXServiceAccount, e *error) {
	r.updateNSXServiceAccountStatus(c, o, e)
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailDelete, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResType)
}

func updateSuccess(r *NSXServiceAccountReconciler, c *context.Context, o *nsxvmwarecomv1alpha1.NSXServiceAccount) {
	r.updateNSXServiceAccountStatus(c, o, nil)
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulUpdate, "ServiceAccount CR has been successfully updated")
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateSuccessTotal, MetricResType)
}

func deleteSuccess(r *NSXServiceAccountReconciler, _ *context.Context, o *nsxvmwarecomv1alpha1.NSXServiceAccount) {
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulDelete, "ServiceAccount CR has been successfully deleted")
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResType)
}
