/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package staticroute

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	_ "github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	commonservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/staticroute"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

var (
	log                      = &logger.Log
	ResultNormal             = common.ResultNormal
	ResultRequeue            = common.ResultRequeue
	ResultRequeueAfter5mins  = common.ResultRequeueAfter5mins
	MetricResTypeStaticRoute = common.MetricResTypeStaticRoute
)

// StaticRouteReconciler StaticRouteReconcile reconciles a StaticRoute object
type StaticRouteReconciler struct {
	Client        client.Client
	Scheme        *apimachineryruntime.Scheme
	Service       *staticroute.StaticRouteService
	Recorder      record.EventRecorder
	StatusUpdater common.StatusUpdater
}

func (r *StaticRouteReconciler) deleteStaticRouteByName(ns, name string) error {
	nsxStaticRoutes := r.Service.ListStaticRouteByName(ns, name)
	for _, item := range nsxStaticRoutes {
		log.Info("Deleting StaticRoute", "Namespace", ns, "Name", name, "nsxStaticRouteId", *item.Id)
		vpcInfo, err := commonservice.ParseVPCResourcePath(*item.Path)
		if err != nil {
			log.Error(err, "Failed to parse NSX VPC path for StaticRoute", "path", *item.Path)
			return err
		}
		if err := r.Service.DeleteStaticRouteByPath(vpcInfo.OrgID, vpcInfo.ProjectID, vpcInfo.VPCID, *item.Id); err != nil {
			log.Error(err, "Failed to delete StaticRoute", "nsxStaticRouteId", *item.Id)
			return err
		}
		log.Info("Successfully deleted StaticRoute", "Namespace", ns, "Name", name, "nsxStaticRouteId", *item.Id)
	}
	return nil
}

func (r *StaticRouteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &v1alpha1.StaticRoute{}
	log.Info("reconciling staticroute CR", "staticroute", req.NamespacedName)
	r.StatusUpdater.IncreaseSyncTotal()

	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		if apierrors.IsNotFound(err) {
			if err := r.deleteStaticRouteByName(req.Namespace, req.Name); err != nil {
				r.StatusUpdater.DeleteFail(req.NamespacedName, nil, err)
				return ResultRequeue, err
			}
			r.StatusUpdater.DeleteSuccess(req.NamespacedName, nil)
			return ResultNormal, nil
		}
		log.Error(err, "unable to fetch static route CR", "req", req.NamespacedName)
		return ResultRequeue, err
	}

	if obj.ObjectMeta.DeletionTimestamp.IsZero() {
		r.StatusUpdater.IncreaseUpdateTotal()
		if err := r.Service.CreateOrUpdateStaticRoute(req.Namespace, obj); err != nil {
			r.StatusUpdater.UpdateFail(ctx, obj, err, "", setStaticRouteReadyStatusFalse)
			// TODO: if error is not retriable, not requeue
			apierror, errortype := util.DumpAPIError(err)
			if apierror != nil {
				log.Info("create or update static route failed", "error", apierror, "error type", errortype)
			}
			return ResultRequeue, err
		}
		r.StatusUpdater.UpdateSuccess(ctx, obj, setStaticRouteReadyStatusTrue)
	} else {
		r.StatusUpdater.IncreaseDeleteTotal()
		if err := r.Service.DeleteStaticRoute(obj); err != nil {
			r.StatusUpdater.DeleteFail(req.NamespacedName, nil, err)
			return ResultRequeue, err
		}
		r.StatusUpdater.DeleteSuccess(req.NamespacedName, nil)
	}
	return ResultNormal, nil
}

func setStaticRouteReadyStatusTrue(client client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, _ ...interface{}) {
	staticRoute := obj.(*v1alpha1.StaticRoute)
	newConditions := []v1alpha1.StaticRouteCondition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionTrue,
			Message:            "NSX Static Route has been successfully created/updated",
			Reason:             "StaticRouteReady",
			LastTransitionTime: transitionTime,
		},
	}
	updateStaticRouteStatusConditions(client, ctx, staticRoute, newConditions)
}

func setStaticRouteReadyStatusFalse(client client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, err error, _ ...interface{}) {
	staticRoute := obj.(*v1alpha1.StaticRoute)
	newConditions := []v1alpha1.StaticRouteCondition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionFalse,
			Message:            fmt.Sprintf("Error occurred while processing the Static Route CR. Please check the config and try again. Error: %v", err),
			Reason:             "StaticRouteNotReady",
			LastTransitionTime: transitionTime,
		},
	}
	updateStaticRouteStatusConditions(client, ctx, staticRoute, newConditions)
}

func updateStaticRouteStatusConditions(client client.Client, ctx context.Context, staticRoute *v1alpha1.StaticRoute, newConditions []v1alpha1.StaticRouteCondition) {
	conditionsUpdated := false
	for i := range newConditions {
		if mergeStaticRouteStatusCondition(staticRoute, &newConditions[i]) {
			conditionsUpdated = true
		}
	}
	if conditionsUpdated {
		client.Status().Update(ctx, staticRoute)
		log.V(1).Info("Updated Static Route CRD", "Name", staticRoute.Name, "Namespace", staticRoute.Namespace, "New Conditions", newConditions)
	}
}

func mergeStaticRouteStatusCondition(staticRoute *v1alpha1.StaticRoute, newCondition *v1alpha1.StaticRouteCondition) bool {
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
		r.StatusUpdater.IncreaseDeleteTotal()
		// get orgId, projectId, staticrouteId from path  "/orgs/<orgId>/projects/<projectId>/vpcs/<vpcId>/static-routes/<srId>"
		path := strings.Split(*elem.Path, "/")
		err = r.Service.DeleteStaticRouteByPath(path[2], path[4], path[6], *elem.Id)
		if err != nil {
			r.StatusUpdater.IncreaseDeleteFailTotal()
		} else {
			r.StatusUpdater.IncreaseDeleteSuccessTotal()
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
	staticRouteReconcile.StatusUpdater = common.NewStatusUpdater(staticRouteReconcile.Client, staticRouteReconcile.Service.NSXConfig, staticRouteReconcile.Recorder, MetricResTypeStaticRoute, "StaticRoute", "StaticRoute")
	if err := staticRouteReconcile.Start(mgr); err != nil {
		log.Error(err, "failed to create controller", "controller", "StaticRoute")
		os.Exit(1)
	}
	go common.GenericGarbageCollector(make(chan bool), commonservice.GCInterval, staticRouteReconcile.CollectGarbage)
}
