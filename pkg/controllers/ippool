/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package ippool

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/pkg/errors"
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

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha2"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/ippool"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	util2 "github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	log           = logger.Log
	resultNormal  = common.ResultNormal
	resultRequeue = common.ResultRequeue
	MetricResType = common.MetricResTypeIPPool
)

// IPPoolReconciler reconciles a IPPool object
type IPPoolReconciler struct {
	client.Client
	Scheme     *apimachineryruntime.Scheme
	Service    *ippool.IPPoolService
	VPCService servicecommon.VPCServiceProvider
	Recorder   record.EventRecorder
}

func deleteSuccess(r *IPPoolReconciler, _ *context.Context, o *v1alpha2.IPPool) {
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulDelete, "IPPool CR has been successfully deleted")
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResType)
}

func deleteFail(r *IPPoolReconciler, c *context.Context, o *v1alpha2.IPPool, e *error) {
	r.setReadyStatusFalse(c, o, metav1.Now(), e)
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailDelete, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResType)
}

func updateSuccess(r *IPPoolReconciler, c *context.Context, o *v1alpha2.IPPool) {
	r.setReadyStatusTrue(c, o, metav1.Now())
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulUpdate, "IPPool CR has been successfully updated")
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateSuccessTotal, MetricResType)
}

func updateFail(r *IPPoolReconciler, c *context.Context, o *v1alpha2.IPPool, e *error) {
	r.setReadyStatusFalse(c, o, metav1.Now(), e)
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailUpdate, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateFailTotal, MetricResType)
}

func (r *IPPoolReconciler) setReadyStatusFalse(ctx *context.Context, ippool *v1alpha2.IPPool, transitionTime metav1.Time, err *error) {
	conditions := []v1alpha1.Condition{
		{
			Type:    v1alpha1.Ready,
			Status:  v1.ConditionFalse,
			Message: "NSX IPPool could not be created/updated/deleted",
			Reason: fmt.Sprintf(
				"error occurred while processing the IPPool CR. Error: %v",
				*err,
			),
			LastTransitionTime: transitionTime,
		},
	}
	ippool.Status.Conditions = conditions
	if ippool.Status.Subnets == nil {
		ippool.Status.Subnets = make([]v1alpha2.SubnetResult, 0)
	}
	e := r.Client.Status().Update(*ctx, ippool)
	if e != nil {
		log.Error(e, "unable to update IPPool status", "ippool", ippool)
	}
}

func (r *IPPoolReconciler) setReadyStatusTrue(ctx *context.Context, ippool *v1alpha2.IPPool, transitionTime metav1.Time) {
	conditions := []v1alpha1.Condition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionTrue,
			Message:            "NSX IPPool has been successfully created/updated",
			Reason:             "",
			LastTransitionTime: transitionTime,
		},
	}
	ippool.Status.Conditions = conditions
	e := r.Client.Status().Update(*ctx, ippool)
	if e != nil {
		log.Error(e, "unable to update IPPool status", "ippool", ippool)
	}
}

func (r *IPPoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &v1alpha2.IPPool{}
	log.Info("reconciling ippool CR", "ippool", req.NamespacedName)
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerSyncTotal, MetricResType)
	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		log.Error(err, "unable to fetch ippool CR", "req", req.NamespacedName)
		return resultNormal, client.IgnoreNotFound(err)
	}

	// TODO: As we do not have base controller in Go, we need to take care of NSX exceptions in each controller separately.
	//I agree we should not do infinite retry for all errors, but it's ok to add error handling in a following patch

	// TODO: Since only the cloud provider creates it, we can take all the validation logic into consideration later.

	// TODO: add webhook to disallow user update prefixLength

	// TODO: Tao's suggestions: Should we consider some Orphan subnets may exist？

	// TODO: Xiaopei's suggestions: is there possibility that IPPool was deleted from nsx store but NSX block subnet was not deleted?

	if obj.Spec.Type == "" {
		vpcNetworkConfig := r.VPCService.GetVPCNetworkConfigByNamespace(obj.Namespace)
		if vpcNetworkConfig == nil {
			err := fmt.Errorf("operate failed: cannot get configuration for IPPool CR")
			log.Error(err, "failed to find VPCNetworkConfig for IPPool CR", "ippool", req.NamespacedName, "namespace %s", obj.Namespace)
			updateFail(r, &ctx, obj, &err)
			return resultRequeue, err
		}
		obj.Spec.Type = "Private"
	}

	if obj.ObjectMeta.DeletionTimestamp.IsZero() {
		metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateTotal, MetricResType)
		if !controllerutil.ContainsFinalizer(obj, servicecommon.IPPoolFinalizerName) {
			controllerutil.AddFinalizer(obj, servicecommon.IPPoolFinalizerName)
			if err := r.Client.Update(ctx, obj); err != nil {
				log.Error(err, "add finalizer", "ippool", req.NamespacedName)
				updateFail(r, &ctx, obj, &err)
				return resultRequeue, err
			}
			log.V(1).Info("added finalizer on ippool CR", "ippool", req.NamespacedName)
		}

		subnetCidrUpdated, ipPoolSubnetsUpdated, err := r.Service.CreateOrUpdateIPPool(obj)
		// check if ipblock is exhausted
		apiErr, _ := util.DumpAPIError(err)
		if apiErr != nil {
			for _, apiErrItem := range apiErr.RelatedErrors {
				// 520012=IpAddressBlock with max size does not have spare capacity to satisfy new block subnet of size
				if *apiErrItem.ErrorCode == 520012 {
					pathPattern := `path=\[([^\]]+)\]`
					pathRegex := regexp.MustCompile(pathPattern)
					pathMatch := pathRegex.FindStringSubmatch(*apiErrItem.ErrorMessage)
					if len(pathMatch) > 1 {
						path := pathMatch[1]
						if !util2.Contains(r.Service.ExhaustedIPBlock, path) {
							r.Service.ExhaustedIPBlock = append(r.Service.ExhaustedIPBlock, path)
							log.Info("ExhaustedIPBlock: ", "ExhaustedIPBlock", r.Service.ExhaustedIPBlock)
						}
					}
				}
			}
		}

		if err != nil {
			updateFail(r, &ctx, obj, &err)
			// if all ip blocks are exhausted, we should not retry
			if errors.As(err, &util.IPBlockAllExhaustedError{}) {
				log.Error(err, "ip blocks are all exhausted, would retry exponentially", "ippool", req.NamespacedName)
				r.Service.ExhaustedIPBlock = []string{}
				log.Info("Clear ExhaustedIPBlock: ", "ExhaustedIPBlock", r.Service.ExhaustedIPBlock)
				return common.ResultRequeueAfter10sec, err
			}
			log.Error(err, "operate failed, would retry exponentially", "ippool", req.NamespacedName)
			return resultRequeue, err
		}
		if !r.Service.FullyRealized(obj) {
			if len(obj.Spec.Subnets) == 0 {
				updateSuccess(r, &ctx, obj)
				return resultNormal, nil
			}
			if subnetCidrUpdated || ipPoolSubnetsUpdated {
				err := fmt.Errorf("partial subnets are unrealized, would retry exponentially")
				updateFail(r, &ctx, obj, &err)
				log.Info("successfully reconcile ippool CR, but put back ippool again, since partial subnets are unrealized", "subnets",
					r.Service.GetUnrealizedSubnetNames(obj))
				return resultRequeue, nil
			}
		} else {
			if subnetCidrUpdated || ipPoolSubnetsUpdated || len(obj.Spec.Subnets) == 0 {
				updateSuccess(r, &ctx, obj)
				log.Info("successfully reconcile ippool CR and all subnets are fully realized", "ippool", obj)
			} else {
				log.Info("full realized already, and resources are not changed, skip updating them", "obj", obj)
			}
		}
	} else {
		if controllerutil.ContainsFinalizer(obj, servicecommon.IPPoolFinalizerName) {
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, MetricResType)
			if err := r.Service.DeleteIPPool(obj); err != nil {
				log.Error(err, "deletion failed, would retry exponentially", "ippool", req.NamespacedName)
				deleteFail(r, &ctx, obj, &err)
				return resultRequeue, err
			}
			controllerutil.RemoveFinalizer(obj, servicecommon.IPPoolFinalizerName)
			if err := r.Client.Update(ctx, obj); err != nil {
				log.Error(err, "deletion failed, would retry exponentially", "ippool", req.NamespacedName)
				deleteFail(r, &ctx, obj, &err)
				return resultRequeue, err
			}
			log.V(1).Info("removed finalizer on ippool CR", "ippool", req.NamespacedName)
			deleteSuccess(r, &ctx, obj)
			log.Info("successfully deleted ippool CR and all subnets", "ippool", obj)
		} else {
			// only print a message because it's not a normal case
			log.Info("ippool CR is being deleted but its finalizers cannot be recognized", "ippool", req.NamespacedName)
		}
	}
	return resultNormal, nil
}

func (r *IPPoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha2.IPPool{}).
		WithEventFilter(predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				// Ignore updates to CR status in which case metadata.Generation does not change
				return e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration()
			},
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
func (r *IPPoolReconciler) Start(mgr ctrl.Manager) error {
	err := r.SetupWithManager(mgr)
	if err != nil {
		return err
	}
	go r.IPPoolGarbageCollector(make(chan bool), servicecommon.GCInterval)
	return nil
}

// IPPoolGarbageCollector collect ippool which has been removed from crd.
// cancel is used to break the loop during UT
func (r *IPPoolReconciler) IPPoolGarbageCollector(cancel chan bool, timeout time.Duration) {
	ctx := context.Background()
	log.Info("ippool garbage collector started")
	for {
		select {
		case <-cancel:
			return
		case <-time.After(timeout):
		}
		nsxIPPoolSet := r.Service.ListIPPoolID()
		if len(nsxIPPoolSet) == 0 {
			continue
		}
		ipPoolList := &v1alpha2.IPPoolList{}
		err := r.Client.List(ctx, ipPoolList)
		if err != nil {
			log.Error(err, "failed to list ip pool CR")
			continue
		}

		CRIPPoolSet := sets.NewString()
		for _, ipp := range ipPoolList.Items {
			CRIPPoolSet.Insert(string(ipp.UID))
		}

		log.V(2).Info("ippool garbage collector", "nsxIPPoolSet", nsxIPPoolSet, "CRIPPoolSet", CRIPPoolSet)

		for elem := range nsxIPPoolSet {
			if CRIPPoolSet.Has(elem) {
				continue
			}
			log.Info("GC collected ip pool CR", "UID", elem)
			err = r.Service.DeleteIPPool(types.UID(elem))
			if err != nil {
				log.Error(err, "failed to delete ip pool CR", "UID", elem)
			}
		}
	}
}
