/* Copyright © 2024 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package ipaddressallocation

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	Scheme        *apimachineryruntime.Scheme
	Service       *ipaddressallocation.IPAddressAllocationService
	VPCService    servicecommon.VPCServiceProvider
	Recorder      record.EventRecorder
	StatusUpdater common.StatusUpdater
}

func setReadyStatusFalse(client client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, err error, _ ...interface{}) {
	ipaddressallocation := obj.(*v1alpha1.IPAddressAllocation)
	conditions := []v1alpha1.Condition{
		{
			Type:   v1alpha1.Ready,
			Status: v1.ConditionFalse,
			Message: fmt.Sprintf(
				"error occurred while processing the IPAddressAllocation CR. Error: %v",
				err,
			),
			Reason:             "IPAddressAllocationNotReady",
			LastTransitionTime: transitionTime,
		},
	}
	ipaddressallocation.Status.Conditions = conditions
	e := client.Status().Update(ctx, ipaddressallocation)
	if e != nil {
		log.Error(e, "unable to update IPAddressAllocation status", "IPAddressAllocation", ipaddressallocation)
	}
}

func setReadyStatusTrue(client client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, _ ...interface{}) {
	ipaddressallocation := obj.(*v1alpha1.IPAddressAllocation)
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
	e := client.Status().Update(ctx, ipaddressallocation)
	if e != nil {
		log.Error(e, "unable to update IPAddressAllocation status", "IPAddressAllocation", ipaddressallocation)
	}
}

func (r *IPAddressAllocationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &v1alpha1.IPAddressAllocation{}
	log.Info("reconciling IPAddressAllocation CR", "IPAddressAllocation", req.NamespacedName)
	r.StatusUpdater.IncreaseSyncTotal()
	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		if apierrors.IsNotFound(err) {
			err = r.Service.DeleteIPAddressAllocationByNamespacedName(req.Namespace, req.Name)
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
	return r.handleDeletion(req, obj)
}

func (r *IPAddressAllocationReconciler) handleUpdate(ctx context.Context, obj *v1alpha1.IPAddressAllocation) (ctrl.Result, error) {
	r.StatusUpdater.IncreaseUpdateTotal()
	updated, err := r.Service.CreateOrUpdateIPAddressAllocation(obj)
	if err != nil {
		r.StatusUpdater.UpdateFail(ctx, obj, err, "", setReadyStatusFalse)
		return resultRequeue, err
	}
	if updated {
		r.StatusUpdater.UpdateSuccess(ctx, obj, setReadyStatusTrue)
	}
	return resultNormal, nil
}

func (r *IPAddressAllocationReconciler) handleDeletion(req ctrl.Request, obj *v1alpha1.IPAddressAllocation) (ctrl.Result, error) {
	r.StatusUpdater.IncreaseDeleteTotal()
	if err := r.Service.DeleteIPAddressAllocation(obj); err != nil {
		r.StatusUpdater.DeleteFail(req.NamespacedName, nil, err)
		return resultRequeue, err
	}
	r.StatusUpdater.DeleteSuccess(req.NamespacedName, nil)
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

func (r *IPAddressAllocationReconciler) CollectGarbage(ctx context.Context) error {
	log.Info("IPAddressAllocation garbage collector started")
	ipAddressAllocationSet := r.Service.ListIPAddressAllocationID()
	if len(ipAddressAllocationSet) == 0 {
		return nil
	}

	ipAddressAllocationList := &v1alpha1.IPAddressAllocationList{}
	if err := r.Client.List(ctx, ipAddressAllocationList); err != nil {
		log.Error(err, "Failed to list IPAddressAllocation CR")
		return err
	}
	CRIPAddressAllocationSet := sets.New[string]()
	for _, ipa := range ipAddressAllocationList.Items {
		CRIPAddressAllocationSet.Insert(string(ipa.UID))
	}

	log.V(2).Info("IPAddressAllocation garbage collector", "nsxIPAddressAllocationSet", ipAddressAllocationSet, "CRIPAddressAllocationSet", CRIPAddressAllocationSet)

	diffSet := ipAddressAllocationSet.Difference(CRIPAddressAllocationSet)
	var errList []error
	for elem := range diffSet {
		log.Info("GC collected nsx IPAddressAllocation", "UID", elem)
		if err := r.Service.DeleteIPAddressAllocation(types.UID(elem)); err != nil {
			log.Error(err, "Failed to delete nsx IPAddressAllocation", "UID", elem)
			errList = append(errList, err)
		}
	}
	if len(errList) > 0 {
		return fmt.Errorf("errors found in IPAddressAllocation garbage collection: %s", errList)
	}
	return nil
}

func (r *IPAddressAllocationReconciler) RestoreReconcile() error {
	return nil
}

func (r *IPAddressAllocationReconciler) StartController(mgr ctrl.Manager, _ webhook.Server) error {
	if err := r.SetupWithManager(mgr); err != nil {
		log.Error(err, "Failed to create ipaddressallocation controller")
		return err
	}
	go common.GenericGarbageCollector(make(chan bool), servicecommon.GCInterval, r.CollectGarbage)
	return nil
}

func NewIPAddressAllocationReconciler(mgr ctrl.Manager, ipAddressAllocationService *ipaddressallocation.IPAddressAllocationService, vpcService servicecommon.VPCServiceProvider) *IPAddressAllocationReconciler {
	ipAddressAllocationReconciler := &IPAddressAllocationReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		Service:    ipAddressAllocationService,
		VPCService: vpcService,
		Recorder:   mgr.GetEventRecorderFor("ipaddressallocation-controller"),
	}
	ipAddressAllocationReconciler.StatusUpdater = common.NewStatusUpdater(ipAddressAllocationReconciler.Client, ipAddressAllocationReconciler.Service.NSXConfig, ipAddressAllocationReconciler.Recorder, common.MetricResTypeNetworkInfo, "IPAddressAllocation", "IPAddressAllocation")
	return ipAddressAllocationReconciler
}
