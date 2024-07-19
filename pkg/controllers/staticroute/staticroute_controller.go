/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package staticroute

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/crd.nsx.vmware.com/v1alpha1"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	_ "github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	commonservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/staticroute"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

var (
	log                     = &logger.Log
	ResultNormal            = common.ResultNormal
	ResultRequeue           = common.ResultRequeue
	ResultRequeueAfter5mins = common.ResultRequeueAfter5mins
	MetricResType           = common.MetricResTypeStaticRoute
	once                    sync.Once
)

// StaticRouteReconciler StaticRouteReconcile reconciles a StaticRoute object
type StaticRouteReconciler struct {
	Client   client.Client
	Scheme   *apimachineryruntime.Scheme
	Service  *staticroute.StaticRouteService
	Recorder record.EventRecorder
}

func deleteFail(r *StaticRouteReconciler, c *context.Context, o *v1alpha1.StaticRoute, e *error) {
	r.setStaticRouteReadyStatusFalse(c, o, metav1.Now(), e)
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailDelete, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, common.MetricResTypeStaticRoute)
}

func updateFail(r *StaticRouteReconciler, c *context.Context, o *v1alpha1.StaticRoute, e *error) {
	r.setStaticRouteReadyStatusFalse(c, o, metav1.Now(), e)
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailUpdate, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateFailTotal, MetricResType)
}

func updateSuccess(r *StaticRouteReconciler, c *context.Context, o *v1alpha1.StaticRoute) {
	r.setStaticRouteReadyStatusTrue(c, o, metav1.Now())
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulUpdate, "StaticRoute CR has been successfully updated")
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateSuccessTotal, common.MetricResTypeStaticRoute)
}

func deleteSuccess(r *StaticRouteReconciler, _ *context.Context, o *v1alpha1.StaticRoute) {
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulDelete, "StaticRoute CR has been successfully deleted")
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, common.MetricResTypeStaticRoute)
}

func (r *StaticRouteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Use once.Do to ensure gc is called only once
	common.GcOnce(r, &once)

	obj := &v1alpha1.StaticRoute{}
	log.Info("reconciling staticroute CR", "staticroute", req.NamespacedName)
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerSyncTotal, common.MetricResTypeStaticRoute)

	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		log.Error(err, "unable to fetch static route CR", "req", req.NamespacedName)
		return ResultNormal, client.IgnoreNotFound(err)
	}

	if obj.ObjectMeta.DeletionTimestamp.IsZero() {
		metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateTotal, common.MetricResTypeStaticRoute)
		if !controllerutil.ContainsFinalizer(obj, commonservice.StaticRouteFinalizerName) {
			controllerutil.AddFinalizer(obj, commonservice.StaticRouteFinalizerName)
			if err := r.Client.Update(ctx, obj); err != nil {
				log.Error(err, "add finalizer", "staticroute", req.NamespacedName)
				updateFail(r, &ctx, obj, &err)
				return ResultRequeue, err
			}
			log.V(1).Info("added finalizer on staticroute CR", "staticroute", req.NamespacedName)
		}

		if err := r.Service.CreateOrUpdateStaticRoute(req.Namespace, obj); err != nil {
			updateFail(r, &ctx, obj, &err)
			// TODO: if error is not retriable, not requeue
			apierror, errortype := util.DumpAPIError(err)
			if apierror != nil {
				log.Info("create or update static route failed", "error", apierror, "error type", errortype)
			}
			return ResultRequeue, err
		}
		updateSuccess(r, &ctx, obj)
	} else {
		if controllerutil.ContainsFinalizer(obj, commonservice.StaticRouteFinalizerName) {
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, common.MetricResTypeStaticRoute)
			// TODO, update the value from 'default' to actual value， get OrgID, ProjectID, VPCID depending on obj.Namespace from vpc store
			if err := r.Service.DeleteStaticRoute(obj); err != nil {
				log.Error(err, "delete failed, would retry exponentially", "staticroute", req.NamespacedName)
				deleteFail(r, &ctx, obj, &err)
				return ResultRequeue, err
			}
			controllerutil.RemoveFinalizer(obj, commonservice.StaticRouteFinalizerName)
			if err := r.Client.Update(ctx, obj); err != nil {
				deleteFail(r, &ctx, obj, &err)
				return ResultRequeue, err
			}
			log.V(1).Info("removed finalizer", "staticroute", req.NamespacedName)
			deleteSuccess(r, &ctx, obj)
		} else {
			// only print a message because it's not a normal case
			log.Info("finalizers cannot be recognized", "staticroute", req.NamespacedName)
		}
	}

	return ResultNormal, nil
}

func (r *StaticRouteReconciler) setStaticRouteReadyStatusTrue(ctx *context.Context, staticRoute *v1alpha1.StaticRoute, transitionTime metav1.Time) {
	newConditions := []v1alpha1.StaticRouteCondition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionTrue,
			Message:            "NSX Static Route has been successfully created/updated",
			Reason:             "NSX API returned 200 response code for PATCH",
			LastTransitionTime: transitionTime,
		},
	}
	r.updateStaticRouteStatusConditions(ctx, staticRoute, newConditions)
}

func (r *StaticRouteReconciler) setStaticRouteReadyStatusFalse(ctx *context.Context, staticRoute *v1alpha1.StaticRoute, transitionTime metav1.Time, err *error) {
	newConditions := []v1alpha1.StaticRouteCondition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionFalse,
			Message:            "NSX Static Route could not be created/updated/deleted",
			Reason:             fmt.Sprintf("Error occurred while processing the Static Route CR. Please check the config and try again. Error: %v", *err),
			LastTransitionTime: transitionTime,
		},
	}
	r.updateStaticRouteStatusConditions(ctx, staticRoute, newConditions)
}

func (r *StaticRouteReconciler) updateStaticRouteStatusConditions(ctx *context.Context, staticRoute *v1alpha1.StaticRoute, newConditions []v1alpha1.StaticRouteCondition) {
	conditionsUpdated := false
	for i := range newConditions {
		if r.mergeStaticRouteStatusCondition(staticRoute, &newConditions[i]) {
			conditionsUpdated = true
		}
	}
	if conditionsUpdated {
		r.Client.Status().Update(*ctx, staticRoute)
		log.V(1).Info("Updated Static Route CRD", "Name", staticRoute.Name, "Namespace", staticRoute.Namespace, "New Conditions", newConditions)
	}
}

func (r *StaticRouteReconciler) mergeStaticRouteStatusCondition(staticRoute *v1alpha1.StaticRoute, newCondition *v1alpha1.StaticRouteCondition) bool {
	matchedCondition := getExistingConditionOfType(v1alpha1.StaticRouteStatusCondition(newCondition.Type), staticRoute.Status.Conditions)

	if reflect.DeepEqual(matchedCondition, newCondition) {
		log.V(2).Info("Conditions already match", "New Condition", newCondition, "Existing Condition", matchedCondition)
		return false
	}

	if matchedCondition != nil {
		matchedCondition.Reason = newCondition.Reason
		matchedCondition.Message = newCondition.Message
		matchedCondition.Status = newCondition.Status
	} else {
		staticRoute.Status.Conditions = append(staticRoute.Status.Conditions, *newCondition)
	}
	return true
}

func getExistingConditionOfType(conditionType v1alpha1.StaticRouteStatusCondition, existingConditions []v1alpha1.StaticRouteCondition) *v1alpha1.StaticRouteCondition {
	for i := range existingConditions {
		if existingConditions[i].Type == v1alpha1.ConditionType(conditionType) {
			return &existingConditions[i]
		}
	}
	return nil
}

func (r *StaticRouteReconciler) setupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.StaticRoute{}).
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
func (r *StaticRouteReconciler) Start(mgr ctrl.Manager) error {
	err := r.setupWithManager(mgr)
	if err != nil {
		return err
	}
	return nil
}

// CollectGarbage collect staticroute which has been removed from crd.
// it implements the interface GarbageCollector method.
func (r *StaticRouteReconciler) CollectGarbage(ctx context.Context) {
	log.Info("static route garbage collector started")
	nsxStaticRouteList := r.Service.ListStaticRoute()
	if len(nsxStaticRouteList) == 0 {
		return
	}

	crdStaticRouteList := &v1alpha1.StaticRouteList{}
	err := r.Client.List(ctx, crdStaticRouteList)
	if err != nil {
		log.Error(err, "failed to list static route CR")
		return
	}

	crdStaticRouteSet := sets.NewString()
	for _, sr := range crdStaticRouteList.Items {
		crdStaticRouteSet.Insert(string(sr.UID))
	}

	for _, e := range nsxStaticRouteList {
		elem := e
		UID := r.Service.GetUID(elem)
		if UID == nil {
			continue
		}
		if crdStaticRouteSet.Has(*UID) {
			continue
		}

		log.V(1).Info("GC collected StaticRoute CR", "UID", elem)
		metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, common.MetricResTypeStaticRoute)
		// get orgId, projectId, staticrouteId from path  "/orgs/<orgId>/projects/<projectId>/vpcs/<vpcId>/static-routes/<srId>"
		path := strings.Split(*elem.Path, "/")
		err = r.Service.DeleteStaticRouteByPath(path[2], path[4], path[6], *elem.Id)
		if err != nil {
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, common.MetricResTypeStaticRoute)
		} else {
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, common.MetricResTypeStaticRoute)
		}
	}
}

func StartStaticRouteController(mgr ctrl.Manager, staticRouteService *staticroute.StaticRouteService) {
	staticRouteReconcile := StaticRouteReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("staticroute-controller"),
	}
	staticRouteReconcile.Service = staticRouteService
	if err := staticRouteReconcile.Start(mgr); err != nil {
		log.Error(err, "failed to create controller", "controller", "StaticRoute")
		os.Exit(1)
	}
}
