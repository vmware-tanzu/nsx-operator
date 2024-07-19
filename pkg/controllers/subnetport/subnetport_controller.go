/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnetport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"

	vmv1alpha1 "github.com/vmware-tanzu/vm-operator/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
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

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/crd.nsx.vmware.com/v1alpha1"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

var (
	log                     = &logger.Log
	MetricResTypeSubnetPort = common.MetricResTypeSubnetPort
	once                    sync.Once
)

// SubnetPortReconciler reconciles a SubnetPort object
type SubnetPortReconciler struct {
	client.Client
	Scheme            *apimachineryruntime.Scheme
	SubnetPortService *subnetport.SubnetPortService
	SubnetService     servicecommon.SubnetServiceProvider
	VPCService        servicecommon.VPCServiceProvider
	Recorder          record.EventRecorder
}

// +kubebuilder:rbac:groups=nsx.vmware.com,resources=subnetports,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=nsx.vmware.com,resources=subnetports/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=nsx.vmware.com,resources=subnetports/finalizers,verbs=update
func (r *SubnetPortReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Use once.Do to ensure gc is called only once
	common.GcOnce(r, &once)

	subnetPort := &v1alpha1.SubnetPort{}
	log.Info("reconciling subnetport CR", "subnetport", req.NamespacedName)

	metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerSyncTotal, MetricResTypeSubnetPort)

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
		metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerUpdateTotal, MetricResTypeSubnetPort)
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
			log.Error(err, "failed to get labels from virtualmachine", "subnetPort.Name", subnetPort.Name, "subnetPort.UID", subnetPort.UID)
			return common.ResultRequeue, err
		}
		nsxSubnet, err := r.SubnetService.GetSubnetByPath(nsxSubnetPath)
		if err != nil {
			return common.ResultRequeue, err
		}
		nsxSubnetPortState, err := r.SubnetPortService.CreateOrUpdateSubnetPort(subnetPort, nsxSubnet, "", labels)
		if err != nil {
			log.Error(err, "failed to create or update NSX subnet port, would retry exponentially", "subnetport", req.NamespacedName)
			updateFail(r, &ctx, subnetPort, &err)
			return common.ResultRequeue, err
		}
		subnetPort.Status.Attachment = v1alpha1.SegmentPortAttachmentState{ID: *nsxSubnetPortState.Attachment.Id}
		subnetPort.Status.NetworkInterfaceConfig = v1alpha1.NetworkInterfaceConfig{
			IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
				{
					Gateway: "",
				},
			},
		}
		if len(nsxSubnetPortState.RealizedBindings) > 0 {
			subnetPort.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress = *nsxSubnetPortState.RealizedBindings[0].Binding.IpAddress
			subnetPort.Status.NetworkInterfaceConfig.MACAddress = strings.Trim(*nsxSubnetPortState.RealizedBindings[0].Binding.MacAddress, "\"")
		}
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
			metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerDeleteTotal, MetricResTypeSubnetPort)
			subnetPortID := r.SubnetPortService.BuildSubnetPortId(&subnetPort.ObjectMeta)
			if err := r.SubnetPortService.DeleteSubnetPort(subnetPortID); err != nil {
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
				MaxConcurrentReconciles: common.NumReconcile(),
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
		port := subnetPort
		vmName, err := common.GetVirtualMachineNameForSubnetPort(&port)
		if err != nil {
			// not block the subnetport visiting because of invalid annotations
			log.Error(err, "failed to get virtualmachine name from subnetport", "subnetPort.UID", subnetPort.UID)
		}
		if vmName == vm.GetName() && subnetPort.Namespace == vm.GetNamespace() {
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

func StartSubnetPortController(mgr ctrl.Manager, subnetPortService *subnetport.SubnetPortService, subnetService *subnet.SubnetService, vpcService *vpc.VPCService) {
	subnetPortReconciler := SubnetPortReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		SubnetService:     subnetService,
		SubnetPortService: subnetPortService,
		VPCService:        vpcService,
		Recorder:          mgr.GetEventRecorderFor("subnetport-controller"),
	}
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
	return nil
}

// CollectGarbage collect SubnetPort which has been removed from crd.
// it implements the interface GarbageCollector method.
func (r *SubnetPortReconciler) CollectGarbage(ctx context.Context) {
	log.Info("subnetport garbage collector started")
	nsxSubnetPortSet := r.SubnetPortService.ListNSXSubnetPortIDForCR()
	if len(nsxSubnetPortSet) == 0 {
		return
	}
	subnetPortList := &v1alpha1.SubnetPortList{}
	err := r.Client.List(ctx, subnetPortList)
	if err != nil {
		log.Error(err, "failed to list SubnetPort CR")
		return
	}

	CRSubnetPortSet := sets.New[string]()
	for _, subnetPort := range subnetPortList.Items {
		subnetPortID := r.SubnetPortService.BuildSubnetPortId(&subnetPort.ObjectMeta)
		CRSubnetPortSet.Insert(subnetPortID)
	}

	diffSet := nsxSubnetPortSet.Difference(CRSubnetPortSet)
	for elem := range diffSet {
		log.V(1).Info("GC collected SubnetPort CR", "UID", elem)
		metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerDeleteTotal, MetricResTypeSubnetPort)
		err = r.SubnetPortService.DeleteSubnetPort(elem)
		if err != nil {
			metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResTypeSubnetPort)
		} else {
			metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResTypeSubnetPort)
		}
	}
}

func (r *SubnetPortReconciler) setSubnetPortReadyStatusTrue(ctx *context.Context, subnetPort *v1alpha1.SubnetPort, transitionTime metav1.Time) {
	newConditions := []v1alpha1.Condition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionTrue,
			Message:            "NSX subnet port has been successfully created/updated",
			Reason:             "NSX API returned 200 response code for PATCH",
			LastTransitionTime: transitionTime,
		},
	}
	r.UpdateSubnetPortStatusConditions(ctx, subnetPort, newConditions)
}

func (r *SubnetPortReconciler) setSubnetPortReadyStatusFalse(ctx *context.Context, subnetPort *v1alpha1.SubnetPort, transitionTime metav1.Time, err *error) {
	newConditions := []v1alpha1.Condition{
		{
			Type:    v1alpha1.Ready,
			Status:  v1.ConditionFalse,
			Message: "NSX subnet port could not be created/updated",
			Reason: fmt.Sprintf(
				"error occurred while processing the SubnetPort CR. Error: %v",
				*err,
			),
			LastTransitionTime: transitionTime,
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
	r.setSubnetPortReadyStatusFalse(c, o, metav1.Now(), e)
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailUpdate, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerUpdateFailTotal, MetricResTypeSubnetPort)
}

func deleteFail(r *SubnetPortReconciler, c *context.Context, o *v1alpha1.SubnetPort, e *error) {
	r.setSubnetPortReadyStatusFalse(c, o, metav1.Now(), e)
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailDelete, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResTypeSubnetPort)
}

func updateSuccess(r *SubnetPortReconciler, c *context.Context, o *v1alpha1.SubnetPort) {
	r.setSubnetPortReadyStatusTrue(c, o, metav1.Now())
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulUpdate, "SubnetPort CR has been successfully updated")
	metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerUpdateSuccessTotal, MetricResTypeSubnetPort)
}

func deleteSuccess(r *SubnetPortReconciler, _ *context.Context, o *v1alpha1.SubnetPort) {
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulDelete, "SubnetPort CR has been successfully deleted")
	metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResTypeSubnetPort)
}

func (r *SubnetPortReconciler) GetSubnetPathForSubnetPort(ctx context.Context, subnetPort *v1alpha1.SubnetPort) (string, error) {
	subnetPortID := r.SubnetPortService.BuildSubnetPortId(&subnetPort.ObjectMeta)
	subnetPath := r.SubnetPortService.GetSubnetPathForSubnetPortFromStore(subnetPortID)
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
		subnetPath, err := common.AllocateSubnetFromSubnetSet(subnetSet, r.VPCService, r.SubnetService, r.SubnetPortService)
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
		subnetPath, err := common.AllocateSubnetFromSubnetSet(subnetSet, r.VPCService, r.SubnetService, r.SubnetPortService)
		log.Info("allocated Subnet for SubnetPort", "subnetPath", subnetPath, "subnetPort.Name", subnetPort.Name, "subnetPort.UID", subnetPort.UID)
		if err != nil {
			return subnetPath, err
		}
		return subnetPath, nil
	}
	return subnetPath, nil
}

func (r *SubnetPortReconciler) updateSubnetStatusOnSubnetPort(subnetPort *v1alpha1.SubnetPort, nsxSubnetPath string) error {
	gateway, prefix, err := r.SubnetPortService.GetGatewayPrefixForSubnetPort(subnetPort, nsxSubnetPath)
	if err != nil {
		return err
	}
	// For now, we have an assumption that one subnetport only have one IP address
	if len(subnetPort.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress) > 0 {
		subnetPort.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress += fmt.Sprintf("/%d", prefix)
	}
	subnetPort.Status.NetworkInterfaceConfig.IPAddresses[0].Gateway = gateway
	nsxSubnet, err := r.SubnetService.GetSubnetByPath(nsxSubnetPath)
	if err != nil {
		return err
	}
	subnetPort.Status.NetworkInterfaceConfig.LogicalSwitchUUID = *nsxSubnet.RealizationId
	return nil
}

func (r *SubnetPortReconciler) getLabelsFromVirtualMachine(ctx context.Context, subnetPort *v1alpha1.SubnetPort) (*map[string]string, error) {
	vmName, err := common.GetVirtualMachineNameForSubnetPort(subnetPort)
	if vmName == "" {
		return nil, err
	}
	vm := &vmv1alpha1.VirtualMachine{}
	namespacedName := types.NamespacedName{
		Name:      vmName,
		Namespace: subnetPort.Namespace,
	}
	if err := r.Client.Get(ctx, namespacedName, vm); err != nil {
		return nil, err
	}
	log.Info("got labels from virtualmachine for subnetport", "subnetPort.UID", subnetPort.UID, "vmName", vmName, "labels", vm.ObjectMeta.Labels)
	return &vm.ObjectMeta.Labels, nil
}
