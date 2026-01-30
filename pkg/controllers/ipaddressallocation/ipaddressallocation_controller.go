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
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

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
	restoreMode   bool
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
		log.Error(e, "Unable to update IPAddressAllocation status", "IPAddressAllocation", ipaddressallocation)
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
		log.Error(e, "Unable to update IPAddressAllocation status", "IPAddressAllocation", ipaddressallocation)
	}
}

func (r *IPAddressAllocationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &v1alpha1.IPAddressAllocation{}
	log.Info("Reconciling IPAddressAllocation CR", "IPAddressAllocation", req.NamespacedName)
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

func (r *IPAddressAllocationReconciler) setIPAddressBlockVisibilityDefaultValue(ctx context.Context, obj *v1alpha1.IPAddressAllocation) error {
	log.Debug("setIPAddressBlockVisibilityDefaultValue called", "obj", obj.Name, "namespace", obj.Namespace, "currentValue", obj.Spec.IPAddressBlockVisibility)
	if obj.Spec.IPAddressBlockVisibility == "" {
		accessMode, vpcNetworkConfig, err := common.GetDefaultAccessMode(r.VPCService, obj.Namespace)
		if err != nil {
			return err
		}
		if vpcNetworkConfig == nil {
			err := fmt.Errorf("failed to find VPCNetworkConfig for Namespace %s", obj.Namespace)
			r.StatusUpdater.UpdateFail(ctx, obj, err, "Failed to find VPCNetworkConfig", setReadyStatusFalse)
			return err
		}
		if accessMode == v1alpha1.AccessMode(v1alpha1.AccessModePrivate) {
			obj.Spec.IPAddressBlockVisibility = v1alpha1.IPAddressVisibilityPrivate
		} else {
			obj.Spec.IPAddressBlockVisibility = v1alpha1.IPAddressVisibilityExternal
		}
		// Update the object to persist the IPAddressBlockVisibility default value
		if err := r.Client.Update(ctx, obj); err != nil {
			r.StatusUpdater.UpdateFail(ctx, obj, err, "Failed to update IPAddressAllocation", setReadyStatusFalse)
			return err
		}
	}
	log.Debug("setIPAddressBlockVisibilityDefaultValue called", "obj", obj.Name, "namespace", obj.Namespace, "currentValue", obj.Spec.IPAddressBlockVisibility)
	return nil
}

func (r *IPAddressAllocationReconciler) handleUpdate(ctx context.Context, obj *v1alpha1.IPAddressAllocation) (ctrl.Result, error) {
	err := r.setIPAddressBlockVisibilityDefaultValue(ctx, obj)
	if err != nil {
		return resultNormal, err
	}
	r.StatusUpdater.IncreaseUpdateTotal()
	updated, err := r.Service.CreateOrUpdateIPAddressAllocation(obj, r.restoreMode)
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

func (r *IPAddressAllocationReconciler) setupWithManager(mgr ctrl.Manager) error {
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

	ipAddressAllocationCRList := &v1alpha1.IPAddressAllocationList{}
	if err := r.Client.List(ctx, ipAddressAllocationCRList); err != nil {
		log.Error(err, "Failed to list IPAddressAllocation CR")
		return err
	}
	CRIPAddressAllocationSet := sets.New[string]()
	for _, ipa := range ipAddressAllocationCRList.Items {
		CRIPAddressAllocationSet.Insert(string(ipa.UID))
	}

	log.Trace("IPAddressAllocation garbage collector", "nsxIPAddressAllocationSet", ipAddressAllocationSet, "CRIPAddressAllocationSet", CRIPAddressAllocationSet)

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
	restoreList, err := r.getRestoreList()
	if err != nil {
		err = fmt.Errorf("failed to get IPAddressAllocation restore list: %w", err)
		return err
	}
	var errorList []error
	r.restoreMode = true
	for _, key := range restoreList {
		result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
		if result.Requeue || err != nil {
			errorList = append(errorList, fmt.Errorf("failed to restore IPAddressAllocation %s, error: %w", key, err))
		}
	}
	if len(errorList) > 0 {
		return fmt.Errorf("errors found in IPAddressAllocation restore: %v", errorList)
	}
	return nil
}

func (r *IPAddressAllocationReconciler) getRestoreList() ([]types.NamespacedName, error) {
	ipAddressAllocationCRIDs := r.Service.ListIPAddressAllocationID()

	restoreList := []types.NamespacedName{}
	ipAddressAllocationCRList := &v1alpha1.IPAddressAllocationList{}
	if err := r.Client.List(context.TODO(), ipAddressAllocationCRList); err != nil {
		return restoreList, err
	}
	for _, ipAddressAllocationCR := range ipAddressAllocationCRList.Items {
		// Restore an IPAddressAllocation if IPAddressAllocation CR has status updated but no corresponding NSX IPAddressAllocation in cache
		if len(ipAddressAllocationCR.Status.AllocationIPs) > 0 && !ipAddressAllocationCRIDs.Has(string(ipAddressAllocationCR.GetUID())) {
			restoreList = append(restoreList, types.NamespacedName{Namespace: ipAddressAllocationCR.Namespace, Name: ipAddressAllocationCR.Name})
		}
	}
	return restoreList, nil
}

func (r *IPAddressAllocationReconciler) StartController(mgr ctrl.Manager, hookServer webhook.Server) error {
	if err := r.setupWithManager(mgr); err != nil {
		log.Error(err, "Failed to create ipaddressallocation controller")
		return err
	}
	if hookServer != nil {
		hookServer.Register("/validate-crd-nsx-vmware-com-v1alpha1-ipaddressallocation",
			&webhook.Admission{
				Handler: &IPAddressAllocationValidator{
					Client:  mgr.GetClient(),
					decoder: admission.NewDecoder(mgr.GetScheme()),
				},
			})
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
