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
	"strings"
	"time"

	vmv1alpha1 "github.com/vmware-tanzu/vm-operator/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
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
	subnetPort := &v1alpha1.SubnetPort{}
	log.Info("reconciling subnetport CR", "subnetport", req.NamespacedName)

	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerSyncTotal, MetricResTypeSubnetPort)

	if err := r.Client.Get(ctx, req.NamespacedName, subnetPort); err != nil {
		log.Error(err, "unable to fetch subnetport CR", "req", req.NamespacedName)
		return common.ResultNormal, client.IgnoreNotFound(err)
	}

	if len(subnetPort.Spec.SubnetSet) > 0 && len(subnetPort.Spec.Subnet) > 0 {
		err := errors.New("subnet and subnetset should not be configured at the same time")
		log.Error(err, "failed to get subnet/subnetset of the subnetport", "subnetport", req.NamespacedName)
		return common.ResultNormal, err
	}

	if subnetPort.ObjectMeta.DeletionTimestamp.IsZero() {
		metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateTotal, MetricResTypeSubnetPort)
		if !controllerutil.ContainsFinalizer(subnetPort, servicecommon.SubnetPortFinalizerName) {
			controllerutil.AddFinalizer(subnetPort, servicecommon.SubnetPortFinalizerName)
			if err := r.Client.Update(ctx, subnetPort); err != nil {
				log.Error(err, "add finalizer", "subnetport", req.NamespacedName)
				updateFail(r, &ctx, subnetPort, &err)
				return common.ResultRequeue, err
			}
			log.Info("added finalizer on subnetport CR", "subnetport", req.NamespacedName)
		}

		old_status := subnetPort.Status.DeepCopy()
		nsxSubnetPath, err := r.GetSubnetPathForSubnetPort(ctx, subnetPort)
		if err != nil {
			log.Error(err, "failed to get NSX resource path from subnet", "subnetport", subnetPort)
			return common.ResultRequeue, err
		}
		labels, err := r.getLabelsFromVirtualMachine(ctx, subnetPort)
		if err != nil {
			log.Error(err, "failed to get labels from virtualmachine", "subnetPort.Name", subnetPort.Name, "subnetPort.UID", subnetPort.UID, "subnetPort.Spec.AttachmentRef", subnetPort.Spec.AttachmentRef)
			return common.ResultRequeue, err
		}
		log.Info("got labels from virtualmachine for subnetport", "subnetPort.UID", subnetPort.UID, "virtualmachine name", subnetPort.Spec.AttachmentRef.Name, "labels", labels)
		nsxSubnetPortState, err := r.Service.CreateOrUpdateSubnetPort(subnetPort, nsxSubnetPath, "", labels)
		if err != nil {
			log.Error(err, "failed to create or update NSX subnet port, would retry exponentially", "subnetport", req.NamespacedName)
			updateFail(r, &ctx, subnetPort, &err)
			return common.ResultRequeue, err
		}
		ipAddress := v1alpha1.SubnetPortIPAddress{
			IP: *nsxSubnetPortState.RealizedBindings[0].Binding.IpAddress,
		}
		subnetPort.Status.IPAddresses = []v1alpha1.SubnetPortIPAddress{ipAddress}
		subnetPort.Status.MACAddress = strings.Trim(*nsxSubnetPortState.RealizedBindings[0].Binding.MacAddress, "\"")
		subnetPort.Status.VIFID = *nsxSubnetPortState.Attachment.Id
		err = r.updateSubnetStatusOnSubnetPort(subnetPort, nsxSubnetPath)
		if err != nil {
			log.Error(err, "failed to retrieve subnet status for subnetport", "subnetport", subnetPort, "nsxSubnetPath", nsxSubnetPath)
		}
		if reflect.DeepEqual(old_status, subnetPort.Status) {
			log.Info("status (without conditions) already matched", "new status", subnetPort.Status, "existing status", old_status)
		} else {
			// If the SubnetPort CR's status changed, let's clean the conditions, to ensure the r.Client.Status().Update in the following updateSuccess will be invoked at any time.
			subnetPort.Status.Conditions = nil
		}
		updateSuccess(r, &ctx, subnetPort)
	} else {
		if controllerutil.ContainsFinalizer(subnetPort, servicecommon.SubnetPortFinalizerName) {
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, MetricResTypeSubnetPort)
			if err := r.Service.DeleteSubnetPort(subnetPort.UID); err != nil {
				log.Error(err, "deletion failed, would retry exponentially", "subnetport", req.NamespacedName)
				deleteFail(r, &ctx, subnetPort, &err)
				return common.ResultRequeue, err
			}
			controllerutil.RemoveFinalizer(subnetPort, servicecommon.SubnetPortFinalizerName)
			if err := r.Client.Update(ctx, subnetPort); err != nil {
				log.Error(err, "deletion failed, would retry exponentially", "subnetport", req.NamespacedName)
				deleteFail(r, &ctx, subnetPort, &err)
				return common.ResultRequeue, err
			}
			log.Info("removed finalizer", "subnetport", req.NamespacedName)
			deleteSuccess(r, &ctx, subnetPort)
		} else {
			log.Info("finalizers cannot be recognized", "subnetport", req.NamespacedName)
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
		Watches(&vmv1alpha1.VirtualMachine{},
				handler.EnqueueRequestsFromMapFunc(r.vmMapFunc),
				builder.WithPredicates(predicate.LabelChangedPredicate{})).
		Complete(r) // TODO: watch the virtualmachine event and update the labels on NSX subnet port.
}

func (r *SubnetPortReconciler) vmMapFunc(_ context.Context, vm client.Object) []reconcile.Request {
	subnetPortList := &v1alpha1.SubnetPortList{}
	var requests []reconcile.Request
	err := retry.OnError(retry.DefaultRetry, func(err error) bool {
		return err != nil
	}, func() error {
		err := r.Client.List(context.TODO(), subnetPortList)
		return err
	})
	if err != nil {
		log.Error(err, "failed to list subnetport in VM handler")
		return requests
	}
	for _, subnetPort := range subnetPortList.Items {
		if subnetPort.Spec.AttachmentRef.Name == vm.GetName() && (subnetPort.Spec.AttachmentRef.Namespace == vm.GetNamespace() ||
			(subnetPort.Spec.AttachmentRef.Namespace == "" && subnetPort.Namespace == vm.GetNamespace())) {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      subnetPort.Name,
					Namespace: subnetPort.Namespace,
				},
			})
		}
	}
	return requests
}

func StartSubnetPortController(mgr ctrl.Manager, commonService servicecommon.Service) {
	subnetPortReconciler := SubnetPortReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
	subnetPortService, err := subnetport.InitializeSubnetPort(commonService)
	if err != nil {
		log.Error(err, "failed to initialize subnetport commonService", "controller", "SubnetPort")
		os.Exit(1)
	}
	subnetPortReconciler.Service = subnetPortService
	common.ServiceMediator.SubnetPortService = subnetPortReconciler.Service
	if err := subnetPortReconciler.Start(mgr); err != nil {
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

func (r *SubnetPortReconciler) GetSubnetPathForSubnetPort(ctx context.Context, subnetPort *v1alpha1.SubnetPort) (string, error) {
	subnetPath := r.Service.GetSubnetPathForSubnetPortFromStore(string(subnetPort.UID))
	if len(subnetPath) > 0 {
		log.V(1).Info("NSX subnet port had been created, returning the existing NSX subnet path", "subnetPort.UID", subnetPort.UID, "subnetPath", subnetPath)
		return subnetPath, nil
	}
	if len(subnetPort.Spec.Subnet) > 0 {
		subnet := &v1alpha1.Subnet{}
		namespacedName := types.NamespacedName{
			Name:      subnetPort.Spec.Subnet,
			Namespace: subnetPort.Namespace,
		}
		if err := r.Client.Get(ctx, namespacedName, subnet); err != nil {
			log.Error(err, "subnet CR not found", "subnet CR", namespacedName)
			return subnetPath, err
		}
		subnetPath = subnet.Status.NSXResourcePath
		if len(subnetPath) == 0 {
			err := fmt.Errorf("empty NSX resource path from subnet %s", subnet.Name)
			return subnetPath, err
		}
	} else if len(subnetPort.Spec.SubnetSet) > 0 {
		subnetSet := &v1alpha1.SubnetSet{}
		namespacedName := types.NamespacedName{
			Name:      subnetPort.Spec.SubnetSet,
			Namespace: subnetPort.Namespace,
		}
		if err := r.Client.Get(context.Background(), namespacedName, subnetSet); err != nil {
			log.Error(err, "subnetSet CR not found", "subnet CR", namespacedName)
			return subnetPath, err
		}
		log.Info("got subnetset for subnetport CR, allocating the NSX subnet", "subnetSet.Name", subnetSet.Name, "subnetSet.UID", subnetSet.UID, "subnetPort.Name", subnetPort.Name, "subnetPort.UID", subnetPort.UID)
		subnetPath, err := common.AllocateSubnetFromSubnetSet(subnetSet)
		log.Info("allocated Subnet for SubnetPort", "subnetPath", subnetPath, "subnetPort.Name", subnetPort.Name, "subnetPort.UID", subnetPort.UID)
		if err != nil {
			return subnetPath, err
		}
		return subnetPath, nil
	} else {
		subnetSet, err := common.GetDefaultSubnetSet(r.Client, ctx, subnetPort.Namespace, servicecommon.LabelDefaultVMSubnetSet)
		if err != nil {
			return "", err
		}
		log.Info("got default subnetset for subnetport CR, allocating the NSX subnet", "subnetSet.Name", subnetSet.Name, "subnetSet.UID", subnetSet.UID, "subnetPort.Name", subnetPort.Name, "subnetPort.UID", subnetPort.UID)
		subnetPath, err := common.AllocateSubnetFromSubnetSet(subnetSet)
		log.Info("allocated Subnet for SubnetPort", "subnetPath", subnetPath, "subnetPort.Name", subnetPort.Name, "subnetPort.UID", subnetPort.UID)
		if err != nil {
			return subnetPath, err
		}
		return subnetPath, nil
	}
	return subnetPath, nil
}

func (r *SubnetPortReconciler) updateSubnetStatusOnSubnetPort(subnetPort *v1alpha1.SubnetPort, nsxSubnetPath string) error {
	gateway, netmask, err := r.Service.GetGatewayNetmaskForSubnetPort(subnetPort, nsxSubnetPath)
	if err != nil {
		return err
	}
	subnetInfo, err := servicecommon.ParseVPCResourcePath(nsxSubnetPath)
	if err != nil {
		return err
	}
	// For now, we have an asumption that one subnetport only have one IP address
	subnetPort.Status.IPAddresses[0].Gateway = gateway
	subnetPort.Status.IPAddresses[0].Netmask = netmask
	nsxSubnet := common.ServiceMediator.SubnetStore.GetByKey(subnetInfo.ID)
	if nsxSubnet == nil {
		return errors.New("NSX subnet not found in store")
	}
	subnetPort.Status.LogicalSwitchID = *nsxSubnet.RealizationId
	return nil
}

func (r *SubnetPortReconciler) getLabelsFromVirtualMachine(ctx context.Context, subnetPort *v1alpha1.SubnetPort) (*map[string]string, error) {
	if subnetPort.Spec.AttachmentRef.Name == "" {
		return nil, nil
	}
	vm := &vmv1alpha1.VirtualMachine{}
	namespace := subnetPort.Spec.AttachmentRef.Namespace
	if len(namespace) == 0 {
		namespace = subnetPort.Namespace
	}
	namespacedName := types.NamespacedName{
		Name:      subnetPort.Spec.AttachmentRef.Name,
		Namespace: namespace,
	}
	if err := r.Client.Get(ctx, namespacedName, vm); err != nil {
		return nil, err
	}
	return &vm.ObjectMeta.Labels, nil
}
