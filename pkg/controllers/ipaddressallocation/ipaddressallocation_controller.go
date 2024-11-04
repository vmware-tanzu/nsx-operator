/* Copyright © 2024 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package ipaddressallocation

import (
	"context"
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

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/ipaddressallocation"
)

var (
	log           = logger.Log
	resultNormal  = common.ResultNormal
	resultRequeue = common.ResultRequeue
	MetricResType = common.MetricResTypeIPAddressAllocation
)

// IPAddressAllocationReconciler reconciles a IPAddressAllocation object
type IPAddressAllocationReconciler struct {
	client.Client
	Scheme     *apimachineryruntime.Scheme
	Service    *ipaddressallocation.IPAddressAllocationService
	VPCService servicecommon.VPCServiceProvider
	Recorder   record.EventRecorder
}

func deleteSuccess(r *IPAddressAllocationReconciler, _ context.Context, o *v1alpha1.IPAddressAllocation) {
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulDelete, "IPAddressAllocation CR has been successfully deleted")
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResType)
}

func deleteFail(r *IPAddressAllocationReconciler, c context.Context, o *v1alpha1.IPAddressAllocation, e *error) {
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailDelete, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResType)
}

func updateSuccess(r *IPAddressAllocationReconciler, c context.Context, o *v1alpha1.IPAddressAllocation) {
	r.setReadyStatusTrue(c, o, metav1.Now())
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulUpdate, "IPAddressAllocation CR has been successfully updated")
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateSuccessTotal, MetricResType)
}

func updateFail(r *IPAddressAllocationReconciler, c context.Context, o *v1alpha1.IPAddressAllocation, e *error) {
	r.setReadyStatusFalse(c, o, metav1.Now(), e)
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailUpdate, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateFailTotal, MetricResType)
}

func (r *IPAddressAllocationReconciler) setReadyStatusFalse(ctx context.Context, ipaddressallocation *v1alpha1.IPAddressAllocation, transitionTime metav1.Time, err *error) {
	conditions := []v1alpha1.Condition{
		{
			Type:   v1alpha1.Ready,
			Status: v1.ConditionFalse,
			Message: fmt.Sprintf(
				"error occurred while processing the IPAddressAllocation CR. Error: %v",
				*err,
			),
			Reason:             "IPAddressAllocationNotReady",
			LastTransitionTime: transitionTime,
		},
	}
	ipaddressallocation.Status.Conditions = conditions
	e := r.Client.Status().Update(ctx, ipaddressallocation)
	if e != nil {
		log.Error(e, "unable to update IPAddressAllocation status", "IPAddressAllocation", ipaddressallocation)
	}
}

func (r *IPAddressAllocationReconciler) setReadyStatusTrue(ctx context.Context, ipaddressallocation *v1alpha1.IPAddressAllocation, transitionTime metav1.Time) {
	conditions := []v1alpha1.Condition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionTrue,
			Message:            "NSX IPAddressAllocation has been successfully created/updated",
			Reason:             "IPAddressAllocationReady",
			LastTransitionTime: transitionTime,
		},
	}
	ipaddressallocation.Status.Conditions = conditions
	e := r.Client.Status().Update(ctx, ipaddressallocation)
	if e != nil {
		log.Error(e, "unable to update IPAddressAllocation status", "IPAddressAllocation", ipaddressallocation)
	}
}

func (r *IPAddressAllocationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &v1alpha1.IPAddressAllocation{}
	log.Info("reconciling IPAddressAllocation CR", "IPAddressAllocation", req.NamespacedName)
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerSyncTotal, MetricResType)
	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		if client.IgnoreNotFound(err) == nil {
			err = r.Service.DeleteIPAddressAllocationByNamespacedName(req.Namespace, req.Name)
			if err != nil {
				log.Error(err, "failed to delete IPAddressAllocation", "IPAddressAllocation", req.NamespacedName)
				return resultRequeue, err
			}
		}
		return resultRequeue, err
	}
	if obj.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.handleUpdate(ctx, req, obj)
	}
	return r.handleDeletion(ctx, req, obj)
}

func (r *IPAddressAllocationReconciler) handleUpdate(ctx context.Context, req ctrl.Request, obj *v1alpha1.IPAddressAllocation) (ctrl.Result, error) {
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateTotal, MetricResType)
	updated, err := r.Service.CreateOrUpdateIPAddressAllocation(obj)
	if err != nil {
		updateFail(r, ctx, obj, &err)
		return resultRequeue, err
	}
	if updated {
		updateSuccess(r, ctx, obj)
	}
	return resultNormal, nil
}

func (r *IPAddressAllocationReconciler) handleDeletion(ctx context.Context, req ctrl.Request, obj *v1alpha1.IPAddressAllocation) (ctrl.Result, error) {
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, MetricResType)
	if err := r.Service.DeleteIPAddressAllocation(obj); err != nil {
		log.Error(err, "deletion failed, would retry exponentially", "IPAddressAllocation", req.NamespacedName)
		deleteFail(r, ctx, obj, &err)
		return resultRequeue, err
	}
	deleteSuccess(r, ctx, obj)
	log.Info("successfully deleted IPAddressAllocation CR and all subnets", "IPAddressAllocation", obj)
	return resultNormal, nil
}

func (r *IPAddressAllocationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.IPAddressAllocation{}).
		WithOptions(
			controller.Options{
				MaxConcurrentReconciles: common.NumReconcile(),
			}).
		Complete(r)
}

func (r *IPAddressAllocationReconciler) CollectGarbage(ctx context.Context) {
	log.Info("IPAddressAllocation garbage collector started")
	ipAddressAllocationSet := r.Service.ListIPAddressAllocationID()
	if len(ipAddressAllocationSet) == 0 {
		return
	}

	ipAddressAllocationList := &v1alpha1.IPAddressAllocationList{}
	if err := r.Client.List(ctx, ipAddressAllocationList); err != nil {
		log.Error(err, "failed to list IPAddressAllocation CR")
		return
	}
	CRIPAddressAllocationSet := sets.New[string]()
	for _, ipa := range ipAddressAllocationList.Items {
		CRIPAddressAllocationSet.Insert(string(ipa.UID))
	}

	log.V(2).Info("IPAddressAllocation garbage collector", "nsxIPAddressAllocationSet", ipAddressAllocationSet, "CRIPAddressAllocationSet", CRIPAddressAllocationSet)

	diffSet := ipAddressAllocationSet.Difference(CRIPAddressAllocationSet)
	for elem := range diffSet {
		log.Info("GC collected nsx IPAddressAllocation", "UID", elem)
		if err := r.Service.DeleteIPAddressAllocation(types.UID(elem)); err != nil {
			log.Error(err, "failed to delete nsx IPAddressAllocation", "UID", elem)
		}
	}
}
