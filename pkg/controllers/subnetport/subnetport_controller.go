/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnetport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"time"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	v1 "k8s.io/api/core/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
)

var (
	log                     = logger.Log
	MetricResTypeSubnetPort = common.MetricResTypeSubnetPort
)

// SubnetPortReconciler reconciles a SubnetPort object
type SubnetPortReconciler struct {
	client.Client
	Scheme  *apimachineryruntime.Scheme
	Service *subnetport.SubnetPortService
}

// +kubebuilder:rbac:groups=nsx.vmware.com,resources=subnetports,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=nsx.vmware.com,resources=subnetports/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=nsx.vmware.com,resources=subnetports/finalizers,verbs=update
func (r *SubnetPortReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &v1alpha1.SubnetPort{}
	log.Info("reconciling subnetport CR", "subnetport", req.NamespacedName)

	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerSyncTotal, MetricResTypeSubnetPort)

	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		log.Error(err, "unable to fetch subnetport CR", "req", req.NamespacedName)
		return common.ResultNormal, client.IgnoreNotFound(err)
	}

	if len(obj.Spec.SubnetSet) > 0 && len(obj.Spec.Subnet) > 0 {
		err := errors.New("subnet and subnetset should not be configured at the same time")
		log.Error(err, "failed to get subnet/subnetset of the subnetport", "subnetport", req.NamespacedName)
		return common.ResultNormal, err
	}

	if obj.ObjectMeta.DeletionTimestamp.IsZero() {
		metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateTotal, MetricResTypeSubnetPort)
		if !controllerutil.ContainsFinalizer(obj, servicecommon.SubnetPortFinalizerName) {
			controllerutil.AddFinalizer(obj, servicecommon.SubnetPortFinalizerName)
			if err := r.Client.Update(ctx, obj); err != nil {
				log.Error(err, "add finalizer", "subnetport", req.NamespacedName)
				updateFail(r, &ctx, obj, &err)
				return common.ResultRequeue, err
			}
			log.Info("added finalizer on subnetport CR", "subnetport", req.NamespacedName)
		}

		// TODO: valid attachmentRef means the port for VM
		// dhcpPort := false
		// defaultVMSubnet := false
		// attachmentRef := obj.Spec.AttachmentRef
		// if attachmentRef.Name == "" {
		// 	defaultVMSubnet = true
		// }
		old_status := obj.Status.DeepCopy()
		err := r.Service.CreateOrUpdateSubnetPort(obj)
		if err != nil {
			log.Error(err, "failed to create or update NSX subnet port, would retry exponentially", "subnetport", req.NamespacedName)
			updateFail(r, &ctx, obj, &err)
			return common.ResultRequeue, err
		}
		if reflect.DeepEqual(old_status, obj.Status) {
			log.Info("status (without conditions) already matched", "new status", obj.Status, "existing status", old_status)
		} else {
			// If the SubnetPort CR's status changed, let's clean the conditions, to ensure the r.Client.Status().Update in the following updateSuccess will be invoked at any time.
			obj.Status.Conditions = nil
		}
		updateSuccess(r, &ctx, obj)
	} else {
		if controllerutil.ContainsFinalizer(obj, servicecommon.SubnetPortFinalizerName) {
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, MetricResTypeSubnetPort)
			if err := r.Service.DeleteSubnetPort(obj.UID); err != nil {
				log.Error(err, "deletion failed, would retry exponentially", "subnetport", req.NamespacedName)
				deleteFail(r, &ctx, obj, &err)
				return common.ResultRequeue, err
			}
			controllerutil.RemoveFinalizer(obj, servicecommon.SubnetPortFinalizerName)
			if err := r.Client.Update(ctx, obj); err != nil {
				log.Error(err, "deletion failed, would retry exponentially", "subnetport", req.NamespacedName)
				deleteFail(r, &ctx, obj, &err)
				return common.ResultRequeue, err
			}
			log.Info("removed finalizer", "subnet", req.NamespacedName)
			deleteSuccess(r, &ctx, obj)
		} else {
			log.Info("finalizers cannot be recognized", "subnet", req.NamespacedName)
		}
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SubnetPortReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.SubnetPort{}).
		WithEventFilter(
			predicate.Funcs{
				DeleteFunc: func(e event.DeleteEvent) bool {
					// Suppress Delete events to avoid filtering them out in the Reconcile function
					return false
				},
			},
		).
		WithOptions(
			controller.Options{
				MaxConcurrentReconciles: runtime.NumCPU(),
				}).
		Complete(r) // TODO: watch the virtualmachine event and update the labels on NSX subnet port.
}

func StartSubnetPortController(mgr ctrl.Manager, commonService servicecommon.Service) {
	subnetPortReconcile := SubnetPortReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
	if subnetPortService, err := subnetport.InitializeSubnetPort(commonService); err != nil {
		log.Error(err, "failed to initialize subnetport commonService", "controller", "SubnetPort")
		os.Exit(1)
	} else {
		subnetPortReconcile.Service = subnetPortService
	}
	if err := subnetPortReconcile.Start(mgr); err != nil {
		log.Error(err, "failed to create controller", "controller", "SubnetPort")
		os.Exit(1)
	}
}

// Start setup manager and launch GC
func (r *SubnetPortReconciler) Start(mgr ctrl.Manager) error {
	err := r.SetupWithManager(mgr)
	if err != nil {
		return err
	}
	go r.GarbageCollector(make(chan bool), servicecommon.GCInterval)
	return nil
}

// GarbageCollector collect SubnetPort which has been removed from crd.
// cancel is used to break the loop during UT
func (r *SubnetPortReconciler) GarbageCollector(cancel chan bool, timeout time.Duration) {
	ctx := context.Background()
	log.Info("subnetport garbage collector started")
	for {
		select {
		case <-cancel:
			return
		case <-time.After(timeout):
		}
		nsxSubnetPortSet := r.Service.ListNSXSubnetPortIDForCR()
		if len(nsxSubnetPortSet) == 0 {
			continue
		}
		subnetPortList := &v1alpha1.SubnetPortList{}
		err := r.Client.List(ctx, subnetPortList)
		if err != nil {
			log.Error(err, "failed to list SubnetPort CR")
			continue
		}

		CRSubnetPortSet := sets.NewString()
		for _, subnetPort := range subnetPortList.Items {
			CRSubnetPortSet.Insert(string(subnetPort.UID))
		}

		for elem := range nsxSubnetPortSet {
			if CRSubnetPortSet.Has(elem) {
				continue
			}
			log.V(1).Info("GC collected SubnetPort CR", "UID", elem)
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, MetricResTypeSubnetPort)
			err = r.Service.DeleteSubnetPort(types.UID(elem))
			if err != nil {
				metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResTypeSubnetPort)
			} else {
				metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResTypeSubnetPort)
			}
		}
	}
}

func (r *SubnetPortReconciler) setSubnetPortReadyStatusTrue(ctx *context.Context, subnetPort *v1alpha1.SubnetPort) {
	newConditions := []v1alpha1.Condition{
		{
			Type:    v1alpha1.Ready,
			Status:  v1.ConditionTrue,
			Message: "NSX subnet port has been successfully created/updated",
			Reason:  "NSX API returned 200 response code for PATCH",
		},
	}
	r.UpdateSubnetPortStatusConditions(ctx, subnetPort, newConditions)
}

func (r *SubnetPortReconciler) setSubnetPortReadyStatusFalse(ctx *context.Context, subnetPort *v1alpha1.SubnetPort, err *error) {
	newConditions := []v1alpha1.Condition{
		{
			Type:    v1alpha1.Ready,
			Status:  v1.ConditionFalse,
			Message: "NSX subnet port could not be created/updated",
			Reason: fmt.Sprintf(
				"error occurred while processing the SubnetPort CR. Error: %v",
				*err,
			),
		},
	}
	r.UpdateSubnetPortStatusConditions(ctx, subnetPort, newConditions)
}

func (r *SubnetPortReconciler) UpdateSubnetPortStatusConditions(ctx *context.Context, subnetPort *v1alpha1.SubnetPort, newConditions []v1alpha1.Condition) {
	conditionsUpdated := false
	for i := range newConditions {
		if r.mergeSubnetPortStatusCondition(ctx, subnetPort, &newConditions[i]) {
			conditionsUpdated = true
		}
	}
	if conditionsUpdated {
		r.Client.Status().Update(*ctx, subnetPort)
		log.V(1).Info("updated subnet port CR", "Name", subnetPort.Name, "Namespace", subnetPort.Namespace,
			"New Conditions", newConditions)
	}
}

func (r *SubnetPortReconciler) mergeSubnetPortStatusCondition(ctx *context.Context, subnetPort *v1alpha1.SubnetPort, newCondition *v1alpha1.Condition) bool {
	matchedCondition := getExistingConditionOfType(newCondition.Type, subnetPort.Status.Conditions)

	if reflect.DeepEqual(matchedCondition, newCondition) {
		log.V(2).Info("conditions already match", "New Condition", newCondition, "Existing Condition", matchedCondition)
		return false
	}

	if matchedCondition != nil {
		matchedCondition.Reason = newCondition.Reason
		matchedCondition.Message = newCondition.Message
		matchedCondition.Status = newCondition.Status
	} else {
		subnetPort.Status.Conditions = append(subnetPort.Status.Conditions, *newCondition)
	}
	return true
}

func getExistingConditionOfType(conditionType v1alpha1.ConditionType, existingConditions []v1alpha1.Condition) *v1alpha1.Condition {
	for i := range existingConditions {
		if existingConditions[i].Type == conditionType {
			return &existingConditions[i]
		}
	}
	return nil
}

func updateFail(r *SubnetPortReconciler, c *context.Context, o *v1alpha1.SubnetPort, e *error) {
	r.setSubnetPortReadyStatusFalse(c, o, e)
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateFailTotal, MetricResTypeSubnetPort)
}

func deleteFail(r *SubnetPortReconciler, c *context.Context, o *v1alpha1.SubnetPort, e *error) {
	r.setSubnetPortReadyStatusFalse(c, o, e)
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResTypeSubnetPort)
}

func updateSuccess(r *SubnetPortReconciler, c *context.Context, o *v1alpha1.SubnetPort) {
	r.setSubnetPortReadyStatusTrue(c, o)
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateSuccessTotal, MetricResTypeSubnetPort)
}

func deleteSuccess(r *SubnetPortReconciler, _ *context.Context, _ *v1alpha1.SubnetPort) {
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResTypeSubnetPort)
}
