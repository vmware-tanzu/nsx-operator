/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsxserviceaccount

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	nsxvmwarecomv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/nsxserviceaccount"
)

var (
	log                     = logger.Log
	ResultNormal            = common.ResultNormal
	ResultRequeue           = common.ResultRequeue
	ResultRequeueAfter5mins = common.ResultRequeueAfter5mins
	MetricResType           = common.MetricResTypeNSXServiceAccount
)

// NSXServiceAccountReconciler reconciles a NSXServiceAccount object
type NSXServiceAccountReconciler struct {
	client.Client
	Scheme  *apimachineryruntime.Scheme
	Service *nsxserviceaccount.NSXServiceAccountService
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

		if nsxserviceaccount.IsNSXServiceAccountRealized(obj.Status) {
			if r.Service.NSXClient.NSXCheckVersion(nsx.ServiceAccountRestore) {
				if err := r.Service.UpdateRealizedNSXServiceAccount(ctx, obj); err != nil {
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
				MaxConcurrentReconciles: runtime.NumCPU(),
			}).
		Complete(r)
}

// Start setup manager and launch GC
func (r *NSXServiceAccountReconciler) Start(mgr ctrl.Manager) error {
	err := r.setupWithManager(mgr)
	if err != nil {
		return err
	}

	go r.GarbageCollector(make(chan bool), servicecommon.GCInterval)
	return nil
}

// GarbageCollector collect NSXServiceAccount which has been removed from crd.
// cancel is used to break the loop during UT
func (r *NSXServiceAccountReconciler) GarbageCollector(cancel chan bool, timeout time.Duration) {
	ctx := context.Background()
	log.Info("garbage collector started")
	for {
		select {
		case <-cancel:
			return
		case <-time.After(timeout):
		}
		nsxServiceAccountUIDSet := r.Service.ListNSXServiceAccountRealization()
		if len(nsxServiceAccountUIDSet) == 0 {
			continue
		}
		nsxServiceAccountList := &nsxvmwarecomv1alpha1.NSXServiceAccountList{}
		err := r.Client.List(ctx, nsxServiceAccountList)
		if err != nil {
			log.Error(err, "failed to list NSXServiceAccount CR")
			continue
		}
		gcSuccessCount, gcErrorCount := r.garbageCollector(nsxServiceAccountUIDSet, nsxServiceAccountList)
		log.V(1).Info("gc collects NSXServiceAccount CR", "success", gcSuccessCount, "error", gcErrorCount)
	}
}

func (r *NSXServiceAccountReconciler) garbageCollector(nsxServiceAccountUIDSet sets.String, nsxServiceAccountList *nsxvmwarecomv1alpha1.NSXServiceAccountList) (gcSuccessCount, gcErrorCount uint32) {
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
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateFailTotal, MetricResType)
}

func deleteFail(r *NSXServiceAccountReconciler, c *context.Context, o *nsxvmwarecomv1alpha1.NSXServiceAccount, e *error) {
	r.updateNSXServiceAccountStatus(c, o, e)
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResType)
}

func updateSuccess(r *NSXServiceAccountReconciler, c *context.Context, o *nsxvmwarecomv1alpha1.NSXServiceAccount) {
	r.updateNSXServiceAccountStatus(c, o, nil)
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateSuccessTotal, MetricResType)
}

func deleteSuccess(r *NSXServiceAccountReconciler, _ *context.Context, _ *nsxvmwarecomv1alpha1.NSXServiceAccount) {
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResType)
}
