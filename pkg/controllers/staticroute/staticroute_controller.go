/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package staticroute

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	_ "github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	commonservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/staticroute"
)

var (
	log                     = logger.Log
	ResultNormal            = common.ResultNormal
	ResultRequeue           = common.ResultRequeue
	ResultRequeueAfter5mins = common.ResultRequeueAfter5mins
	MetricResType           = common.MetricResTypeStaticRoute
)

// StaticRouteReconciler StaticRouteReconcile reconciles a StaticRoute object
type StaticRouteReconciler struct {
	Client  client.Client
	Scheme  *apimachineryruntime.Scheme
	Service *staticroute.StaticRouteService
}

func deleteFail(r *StaticRouteReconciler, c *context.Context, o *v1alpha1.StaticRoute, e *error) {
	r.setStaticRouteReadyStatusFalse(c, o, e)
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, common.MetricResTypeStaticRoute)
}

func updateFail(r *StaticRouteReconciler, c *context.Context, o *v1alpha1.StaticRoute, e *error) {
	r.setStaticRouteReadyStatusFalse(c, o, e)
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateFailTotal, MetricResType)
}

func updateSuccess(r *StaticRouteReconciler, c *context.Context, o *v1alpha1.StaticRoute) {
	r.setStaticRouteReadyStatusTrue(c, o)
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateSuccessTotal, common.MetricResTypeStaticRoute)
}

func deleteSuccess(r *StaticRouteReconciler, _ *context.Context, _ *v1alpha1.StaticRoute) {
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, common.MetricResTypeStaticRoute)
}

func (r *StaticRouteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
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
			return ResultRequeue, err
		}
		updateSuccess(r, &ctx, obj)
	} else {
		if controllerutil.ContainsFinalizer(obj, commonservice.StaticRouteFinalizerName) {
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, common.MetricResTypeStaticRoute)
			// TODO, update the value from 'default' to actual value， get OrgID, ProjectID, VPCID depending on obj.Namespace from vpc store
			if err := r.Service.DeleteStaticRoute(req.Namespace, string(obj.UID)); err != nil {
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

func (r *StaticRouteReconciler) setStaticRouteReadyStatusTrue(ctx *context.Context, static_route *v1alpha1.StaticRoute) {
	newConditions := []v1alpha1.StaticRouteCondition{
		{
			Type:    v1alpha1.Ready,
			Status:  v1.ConditionTrue,
			Message: "NSX Static Route has been successfully created/updated",
			Reason:  "NSX API returned 200 response code for PATCH",
		},
	}
	r.updateStaticRouteStatusConditions(ctx, static_route, newConditions)
}

func (r *StaticRouteReconciler) setStaticRouteReadyStatusFalse(ctx *context.Context, static_route *v1alpha1.StaticRoute, err *error) {
	newConditions := []v1alpha1.StaticRouteCondition{
		{
			Type:    v1alpha1.Ready,
			Status:  v1.ConditionFalse,
			Message: "NSX Static Route could not be created/updated/deleted",
			Reason:  fmt.Sprintf("Error occurred while processing the Static Route CR. Please check the config and try again. Error: %v", *err),
		},
	}
	r.updateStaticRouteStatusConditions(ctx, static_route, newConditions)
}

func (r *StaticRouteReconciler) updateStaticRouteStatusConditions(ctx *context.Context, static_route *v1alpha1.StaticRoute, newConditions []v1alpha1.StaticRouteCondition) {
	conditionsUpdated := false
	for i := range newConditions {
		if r.mergeStaticRouteStatusCondition(static_route, &newConditions[i]) {
			conditionsUpdated = true
		}
	}
	if conditionsUpdated {
		r.Client.Status().Update(*ctx, static_route)
		log.V(1).Info("Updated Static Route CRD", "Name", static_route.Name, "Namespace", static_route.Namespace, "New Conditions", newConditions)
	}
}

func (r *StaticRouteReconciler) mergeStaticRouteStatusCondition(static_route *v1alpha1.StaticRoute, newCondition *v1alpha1.StaticRouteCondition) bool {
	matchedCondition := getExistingConditionOfType(v1alpha1.StaticRouteStatusCondition(newCondition.Type), static_route.Status.Conditions)

	if reflect.DeepEqual(matchedCondition, newCondition) {
		log.V(2).Info("Conditions already match", "New Condition", newCondition, "Existing Condition", matchedCondition)
		return false
	}

	if matchedCondition != nil {
		matchedCondition.Reason = newCondition.Reason
		matchedCondition.Message = newCondition.Message
		matchedCondition.Status = newCondition.Status
	} else {
		static_route.Status.Conditions = append(static_route.Status.Conditions, *newCondition)
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
				MaxConcurrentReconciles: runtime.NumCPU(),
			}).
		Complete(r)
}

// Start setup manager and launch GC
func (r *StaticRouteReconciler) Start(mgr ctrl.Manager) error {
	err := r.setupWithManager(mgr)
	if err != nil {
		return err
	}

	go r.GarbageCollector(make(chan bool), commonservice.GCInterval)
	return nil
}

// GarbageCollector collect staticroute which has been removed from crd.
// cancel is used to break the loop during UT
func (r *StaticRouteReconciler) GarbageCollector(cancel chan bool, timeout time.Duration) {
	ctx := context.Background()
	log.Info("garbage collector started")
	for {
		select {
		case <-cancel:
			return
		case <-time.After(timeout):
		}
		nsxStaticRouteList := r.Service.ListStaticRoute()
		if len(nsxStaticRouteList) == 0 {
			continue
		}

		crdStaticRouteList := &v1alpha1.StaticRouteList{}
		err := r.Client.List(ctx, crdStaticRouteList)
		if err != nil {
			log.Error(err, "failed to list static route CR")
			continue
		}

		crdStaticRouteSet := sets.NewString()
		for _, sr := range crdStaticRouteList.Items {
			crdStaticRouteSet.Insert(string(sr.UID))
		}

		for _, elem := range nsxStaticRouteList {
			if crdStaticRouteSet.Has(*elem.Id) {
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
}

func StartStaticRouteController(mgr ctrl.Manager, commonService commonservice.Service) {
	staticRouteReconcile := StaticRouteReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
	if staticRouteService, err := staticroute.InitializeStaticRoute(commonService); err != nil {
		log.Error(err, "failed to initialize staticroute commonService", "controller", "StaticRoute")
		os.Exit(1)
	} else {
		staticRouteReconcile.Service = staticRouteService
	}
	if err := staticRouteReconcile.Start(mgr); err != nil {
		log.Error(err, "failed to create controller", "controller", "StaticRoute")
		os.Exit(1)
	}
}
