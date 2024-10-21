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
	"time"

	vmv1alpha1 "github.com/vmware-tanzu/vm-operator/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	log                     = &logger.Log
	MetricResTypeSubnetPort = common.MetricResTypeSubnetPort
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
func (r *SubnetPortReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log.Info("reconciling subnetport CR", "subnetport", req.NamespacedName)
	startTime := time.Now()
	defer func() {
		log.Info("finished reconciling SubnetPort", "SubnetPort", req.NamespacedName, "duration", time.Since(startTime))
	}()

	metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerSyncTotal, MetricResTypeSubnetPort)

	subnetPort := &v1alpha1.SubnetPort{}
	if err := r.Client.Get(ctx, req.NamespacedName, subnetPort); err != nil {
		if apierrors.IsNotFound(err) {
			if err := r.deleteSubnetPortByName(ctx, req.Namespace, req.Name); err != nil {
				log.Error(err, "failed to delete NSX SubnetPort", "SubnetPort", req.NamespacedName)
				return common.ResultRequeue, err
			}
			return common.ResultNormal, nil
		}
		log.Error(err, "unable to fetch SubnetPort CR", "SubnetPort", req.NamespacedName)
		return common.ResultRequeue, err
	}
	if len(subnetPort.Spec.SubnetSet) > 0 && len(subnetPort.Spec.Subnet) > 0 {
		err := errors.New("subnet and subnetset should not be configured at the same time")
		log.Error(err, "failed to get subnet/subnetset of the subnetport", "subnetport", req.NamespacedName)
		updateFail(r, ctx, subnetPort, &err)
		return common.ResultNormal, err
	}

	if subnetPort.ObjectMeta.DeletionTimestamp.IsZero() {
		metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerUpdateTotal, MetricResTypeSubnetPort)

		old_status := subnetPort.Status.DeepCopy()
		isParentResourceTerminating, nsxSubnetPath, err := r.CheckAndGetSubnetPathForSubnetPort(ctx, subnetPort)
		if isParentResourceTerminating {
			updateFail(r, ctx, subnetPort, &err)
			return common.ResultNormal, err
		}
		if err != nil {
			log.Error(err, "failed to get NSX resource path from subnet", "subnetport", subnetPort)
			updateFail(r, ctx, subnetPort, &err)
			return common.ResultRequeue, err
		}
		labels, err := r.getLabelsFromVirtualMachine(ctx, subnetPort)
		if err != nil {
			log.Error(err, "failed to get labels from virtualmachine", "subnetPort.Name", subnetPort.Name, "subnetPort.UID", subnetPort.UID)
			updateFail(r, ctx, subnetPort, &err)
			return common.ResultRequeue, err
		}
		// There is a race condition that the subnetset controller may delete the
		// subnet during CollectGarbage. So check the subnet under lock.
		r.SubnetService.LockSubnet(&nsxSubnetPath)
		defer r.SubnetService.UnlockSubnet(&nsxSubnetPath)

		nsxSubnet, err := r.SubnetService.GetSubnetByPath(nsxSubnetPath)
		if err != nil {
			updateFail(r, ctx, subnetPort, &err)
			return common.ResultRequeue, err
		}
		nsxSubnetPortState, err := r.SubnetPortService.CreateOrUpdateSubnetPort(subnetPort, nsxSubnet, "", labels)
		if err != nil {
			log.Error(err, "failed to create or update NSX subnet port, would retry exponentially", "subnetport", req.NamespacedName)
			updateFail(r, ctx, subnetPort, &err)
			return common.ResultRequeue, err
		}
		subnetPort.Status.Attachment = v1alpha1.PortAttachment{ID: *nsxSubnetPortState.Attachment.Id}
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
		updateSuccess(r, ctx, subnetPort)
	} else {
		metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerDeleteTotal, MetricResTypeSubnetPort)
		subnetPortID := r.SubnetPortService.BuildSubnetPortId(&subnetPort.ObjectMeta)
		if err := r.SubnetPortService.DeleteSubnetPortById(subnetPortID); err != nil {
			log.Error(err, "deletion failed, would retry exponentially", "SubnetPort", req.NamespacedName)
			deleteFail(r, ctx, subnetPort, &err)
			return common.ResultRequeue, err
		}
		deleteSuccess(r, ctx, subnetPort)
	}
	return common.ResultNormal, nil
}

func subnetPortNamespaceVMIndexFunc(obj client.Object) []string {
	if sp, ok := obj.(*v1alpha1.SubnetPort); !ok {
		log.Info("Invalid object", "type", reflect.TypeOf(obj))
		return []string{}
	} else {
		vm, _, err := common.GetVirtualMachineNameForSubnetPort(sp)
		if vm == "" || err != nil {
			log.Info("No proper annotation found", "annotations", sp.Annotations)
			return []string{}
		}
		return []string{fmt.Sprintf("%s/%s", sp.Namespace, vm)}
	}
}

func addressBindingNamespaceVMIndexFunc(obj client.Object) []string {
	if ab, ok := obj.(*v1alpha1.AddressBinding); !ok {
		log.Info("Invalid object", "type", reflect.TypeOf(obj))
		return []string{}
	} else {
		return []string{fmt.Sprintf("%s/%s", ab.Namespace, ab.Spec.VMName)}
	}
}

func (r *SubnetPortReconciler) deleteSubnetPortByName(ctx context.Context, ns string, name string) error {
	// When deleting SubnetPort by Name and Namespace, skip the SubnetPort belonging to the existed SubnetPort CR
	nsxSubnetPorts := r.SubnetPortService.ListSubnetPortByName(ns, name)

	crSubnetPortIDsSet, err := r.SubnetPortService.ListSubnetPortIDsFromCRs(ctx)
	if err != nil {
		log.Error(err, "failed to list SubnetPort CRs")
		return err
	}

	for _, nsxSubnetPort := range nsxSubnetPorts {
		if crSubnetPortIDsSet.Has(*nsxSubnetPort.Id) {
			log.Info("skipping deletion, SubnetPort CR still exists in K8s", "ID", *nsxSubnetPort.Id)
			continue
		}
		if err := r.SubnetPortService.DeleteSubnetPort(nsxSubnetPort); err != nil {
			return err
		}
	}
	log.Info("successfully deleted nsxSubnetPort", "namespace", ns, "name", name)
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SubnetPortReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.TODO(), &v1alpha1.SubnetPort{}, util.SubnetPortNamespaceVMIndexKey, subnetPortNamespaceVMIndexFunc); err != nil {
		return err
	}
	if err := mgr.GetFieldIndexer().IndexField(context.TODO(), &v1alpha1.AddressBinding{}, util.AddressBindingNamespaceVMIndexKey, addressBindingNamespaceVMIndexFunc); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.SubnetPort{}).
		WithOptions(
			controller.Options{
				MaxConcurrentReconciles: common.NumReconcile(),
			}).
		Watches(&vmv1alpha1.VirtualMachine{},
			handler.EnqueueRequestsFromMapFunc(r.vmMapFunc),
			builder.WithPredicates(predicate.LabelChangedPredicate{})).
		Watches(&v1alpha1.AddressBinding{},
				handler.EnqueueRequestsFromMapFunc(r.addressBindingMapFunc)).
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
		vmName, _, err := common.GetVirtualMachineNameForSubnetPort(&port)
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

func StartSubnetPortController(mgr ctrl.Manager, subnetPortService *subnetport.SubnetPortService, subnetService *subnet.SubnetService, vpcService *vpc.VPCService, hookServer webhook.Server) {
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
	if hookServer != nil {
		hookServer.Register("/validate-crd-nsx-vmware-com-v1alpha1-addressbinding",
			&webhook.Admission{
				Handler: &AddressBindingValidator{
					Client:  mgr.GetClient(),
					decoder: admission.NewDecoder(mgr.GetScheme()),
				},
			})
	}
	go common.GenericGarbageCollector(make(chan bool), servicecommon.GCInterval, subnetPortReconciler.CollectGarbage)
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

	crSubnetPortIDsSet, err := r.SubnetPortService.ListSubnetPortIDsFromCRs(ctx)
	if err != nil {
		return
	}

	diffSet := nsxSubnetPortSet.Difference(crSubnetPortIDsSet)
	for elem := range diffSet {
		log.V(1).Info("GC collected SubnetPort CR", "UID", elem)
		metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerDeleteTotal, MetricResTypeSubnetPort)
		err = r.SubnetPortService.DeleteSubnetPortById(elem)
		if err != nil {
			metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResTypeSubnetPort)
		} else {
			metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResTypeSubnetPort)
		}
	}
}

func (r *SubnetPortReconciler) setSubnetPortReadyStatusTrue(ctx context.Context, subnetPort *v1alpha1.SubnetPort, transitionTime metav1.Time) {
	newConditions := []v1alpha1.Condition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionTrue,
			Message:            "NSX subnet port has been successfully created/updated",
			Reason:             "SubnetPortReady",
			LastTransitionTime: transitionTime,
		},
	}
	r.UpdateSubnetPortStatusConditions(ctx, subnetPort, newConditions)
}

func (r *SubnetPortReconciler) setSubnetPortReadyStatusFalse(ctx context.Context, subnetPort *v1alpha1.SubnetPort, transitionTime metav1.Time, err *error) {
	newConditions := []v1alpha1.Condition{
		{
			Type:   v1alpha1.Ready,
			Status: v1.ConditionFalse,
			Message: fmt.Sprintf(
				"error occurred while processing the SubnetPort CR. Error: %v",
				*err,
			),
			Reason:             "SubnetPortNotReady",
			LastTransitionTime: transitionTime,
		},
	}
	r.UpdateSubnetPortStatusConditions(ctx, subnetPort, newConditions)
}

func (r *SubnetPortReconciler) UpdateSubnetPortStatusConditions(ctx context.Context, subnetPort *v1alpha1.SubnetPort, newConditions []v1alpha1.Condition) {
	conditionsUpdated := false
	for i := range newConditions {
		if r.mergeSubnetPortStatusCondition(ctx, subnetPort, &newConditions[i]) {
			conditionsUpdated = true
		}
	}
	if conditionsUpdated {
		r.Client.Status().Update(ctx, subnetPort)
		log.V(1).Info("updated subnet port CR", "Name", subnetPort.Name, "Namespace", subnetPort.Namespace,
			"New Conditions", newConditions)
	}
}

func (r *SubnetPortReconciler) mergeSubnetPortStatusCondition(_ context.Context, subnetPort *v1alpha1.SubnetPort, newCondition *v1alpha1.Condition) bool {
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

func updateFail(r *SubnetPortReconciler, c context.Context, o *v1alpha1.SubnetPort, e *error) {
	r.setSubnetPortReadyStatusFalse(c, o, metav1.Now(), e)
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailUpdate, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerUpdateFailTotal, MetricResTypeSubnetPort)
}

func deleteFail(r *SubnetPortReconciler, c context.Context, o *v1alpha1.SubnetPort, e *error) {
	r.setSubnetPortReadyStatusFalse(c, o, metav1.Now(), e)
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailDelete, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResTypeSubnetPort)
}

func updateSuccess(r *SubnetPortReconciler, c context.Context, o *v1alpha1.SubnetPort) {
	r.setSubnetPortReadyStatusTrue(c, o, metav1.Now())
	r.setAddressBindingStatus(c, o)
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulUpdate, "SubnetPort CR has been successfully updated")
	metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerUpdateSuccessTotal, MetricResTypeSubnetPort)
}

func deleteSuccess(r *SubnetPortReconciler, _ context.Context, o *v1alpha1.SubnetPort) {
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulDelete, "SubnetPort CR has been successfully deleted")
	metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResTypeSubnetPort)
}

func (r *SubnetPortReconciler) CheckAndGetSubnetPathForSubnetPort(ctx context.Context, subnetPort *v1alpha1.SubnetPort) (isStale bool, subnetPath string, err error) {
	subnetPortID := r.SubnetPortService.BuildSubnetPortId(&subnetPort.ObjectMeta)
	subnetPath = r.SubnetPortService.GetSubnetPathForSubnetPortFromStore(subnetPortID)
	if len(subnetPath) > 0 {
		// If there is a SubnetPath in store, there is a subnetport in NSX, the subnetport is not created first time.
		// Check if the Subnet is deleted due to race condition, in which case the stale
		// subnetport is needed to be deleted.
		_, err = r.SubnetService.GetSubnetByPath(subnetPath)
		if err != nil {
			log.Info("previous NSX subnet is deleted, deleting the stale subnet port", "subnetPort.UID", subnetPort.UID, "subnetPath", subnetPath)
			if err = r.SubnetPortService.DeleteSubnetPortById(subnetPortID); err != nil {
				log.Error(err, "failed to delete the stale subnetport", "subnetport.UID", subnetPort.UID)
				return
			}
		} else {
			log.V(1).Info("NSX subnet port had been created, returning the existing NSX subnet path", "subnetPort.UID", subnetPort.UID, "subnetPath", subnetPath)
			return
		}
	}
	if len(subnetPort.Spec.Subnet) > 0 {
		subnet := &v1alpha1.Subnet{}
		namespacedName := types.NamespacedName{
			Name:      subnetPort.Spec.Subnet,
			Namespace: subnetPort.Namespace,
		}
		if err = r.Client.Get(ctx, namespacedName, subnet); err != nil {
			log.Error(err, "subnet CR not found", "subnet CR", namespacedName)
			return
		}
		if !subnet.DeletionTimestamp.IsZero() {
			isStale = true
			err = fmt.Errorf("subnet %s is being deleted, cannot operate subnetport %s", namespacedName, subnetPort.Name)
			return
		}
		subnetList := r.SubnetService.GetSubnetsByIndex(servicecommon.TagScopeSubnetCRUID, string(subnet.GetUID()))
		if len(subnetList) == 0 {
			err = fmt.Errorf("empty NSX resource path for subnet CR %s(%s)", subnet.Name, subnet.GetUID())
			return
		} else if len(subnetList) > 1 {
			err = fmt.Errorf("multiple NSX subnets found for subnet CR %s(%s)", subnet.Name, subnet.GetUID())
			log.Error(err, "failed to get NSX subnet by subnet CR UID", "subnetList", subnetList)
			return
		}
		subnetPath = *subnetList[0].Path
	} else if len(subnetPort.Spec.SubnetSet) > 0 {
		subnetSet := &v1alpha1.SubnetSet{}
		namespacedName := types.NamespacedName{
			Name:      subnetPort.Spec.SubnetSet,
			Namespace: subnetPort.Namespace,
		}
		if err = r.Client.Get(context.Background(), namespacedName, subnetSet); err != nil {
			log.Error(err, "subnetSet CR not found", "subnetSet CR", namespacedName)
			return
		}
		if !subnetSet.DeletionTimestamp.IsZero() {
			isStale = true
			err = fmt.Errorf("subnetset %s is being deleted, cannot operate subnetport %s", namespacedName, subnetPort.Name)
			return
		}
		log.Info("got subnetset for subnetport CR, allocating the NSX subnet", "subnetSet.Name", subnetSet.Name, "subnetSet.UID", subnetSet.UID, "subnetPort.Name", subnetPort.Name, "subnetPort.UID", subnetPort.UID)
		subnetPath, err = common.AllocateSubnetFromSubnetSet(subnetSet, r.VPCService, r.SubnetService, r.SubnetPortService)
		log.Info("allocated Subnet for SubnetPort", "subnetPath", subnetPath, "subnetPort.Name", subnetPort.Name, "subnetPort.UID", subnetPort.UID)
		if err != nil {
			return
		}
	} else {
		subnetSet := &v1alpha1.SubnetSet{}
		subnetSet, err = common.GetDefaultSubnetSet(r.Client, ctx, subnetPort.Namespace, servicecommon.LabelDefaultVMSubnetSet)
		if err != nil {
			return
		}
		if subnetSet != nil && !subnetSet.DeletionTimestamp.IsZero() {
			isStale = true
			err = fmt.Errorf("default subnetset %s is being deleted, cannot operate subnetport %s", subnetSet.Name, subnetPort.Name)
			return
		}
		log.Info("got default subnetset for subnetport CR, allocating the NSX subnet", "subnetSet.Name", subnetSet.Name, "subnetSet.UID", subnetSet.UID, "subnetPort.Name", subnetPort.Name, "subnetPort.UID", subnetPort.UID)
		subnetPath, err = common.AllocateSubnetFromSubnetSet(subnetSet, r.VPCService, r.SubnetService, r.SubnetPortService)
		log.Info("allocated Subnet for SubnetPort", "subnetPath", subnetPath, "subnetPort.Name", subnetPort.Name, "subnetPort.UID", subnetPort.UID)
		if err != nil {
			return
		}
	}
	return
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
	vmName, _, err := common.GetVirtualMachineNameForSubnetPort(subnetPort)
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

func (r *SubnetPortReconciler) addressBindingMapFunc(ctx context.Context, obj client.Object) []reconcile.Request {
	ab, ok := obj.(*v1alpha1.AddressBinding)
	if !ok {
		log.Info("Invalid object", "type", reflect.TypeOf(obj))
		return nil
	}
	// skip reconcile if AddressBinding exists and is realized
	if ab.Status.IPAddress != "" {
		namespacedName := types.NamespacedName{
			Name:      ab.Name,
			Namespace: ab.Namespace,
		}
		existingAddressBinding := &v1alpha1.AddressBinding{}
		if err := r.Client.Get(context.TODO(), namespacedName, existingAddressBinding); err == nil {
			return nil
		}
	}
	spList := &v1alpha1.SubnetPortList{}
	spIndexValue := fmt.Sprintf("%s/%s", ab.Namespace, ab.Spec.VMName)
	err := r.Client.List(context.TODO(), spList, client.MatchingFields{util.SubnetPortNamespaceVMIndexKey: spIndexValue})
	if err != nil || len(spList.Items) == 0 {
		log.Error(err, "Failed to list SubnetPort from cache", "indexValue", spIndexValue)
		return nil
	}
	if ab.Spec.InterfaceName == "" {
		if len(spList.Items) == 1 {
			log.V(1).Info("Enqueue SubnetPort for default AddressBinding", "namespace", ab.Namespace, "name", ab.Name, "SubnetPortName", spList.Items[0].Name, "VM", ab.Spec.VMName)
			return []reconcile.Request{{
				NamespacedName: types.NamespacedName{
					Name:      spList.Items[0].Name,
					Namespace: spList.Items[0].Namespace,
				},
			}}
		} else {
			log.Info("Found multiple SubnetPorts for a VM, ignore default AddressBinding for SubnetPort", "namespace", ab.Namespace, "name", ab.Name, "subnetPortCount", len(spList.Items), "VM", ab.Spec.VMName)
			return nil
		}
	}
	for i, sp := range spList.Items {
		vm, port, err := common.GetVirtualMachineNameForSubnetPort(&spList.Items[i])
		if err != nil || vm == "" {
			log.Error(err, "Failed to get VM name from SubnetPort", "namespace", sp.Namespace, "name", sp.Name, "annotations", sp.Annotations)
			continue
		}
		if ab.Spec.InterfaceName == port {
			log.V(1).Info("Enqueue SubnetPort for AddressBinding", "namespace", ab.Namespace, "name", ab.Name, "SubnetPortName", spList.Items[0].Name, "VM", ab.Spec.VMName, "port", port)

			return []reconcile.Request{{
				NamespacedName: types.NamespacedName{
					Name:      sp.Name,
					Namespace: sp.Namespace,
				},
			}}
		}
	}
	log.Info("No SubnetPort found for AddressBinding", "namespace", ab.Namespace, "name", ab.Name, "VM", ab.Spec.VMName)
	return nil
}

func (r *SubnetPortReconciler) setAddressBindingStatus(ctx context.Context, subnetPort *v1alpha1.SubnetPort) {
	subnetPortID := r.SubnetPortService.BuildSubnetPortId(&subnetPort.ObjectMeta)
	nsxSubnetPort := r.SubnetPortService.SubnetPortStore.GetByKey(subnetPortID)
	if nsxSubnetPort == nil {
		log.Info("Missing SubnetPort", "id", subnetPort.UID)
		return
	}
	if nsxSubnetPort.ExternalAddressBinding == nil || nsxSubnetPort.ExternalAddressBinding.ExternalIpAddress == nil {
		return
	}
	ab := r.SubnetPortService.GetAddressBindingBySubnetPort(subnetPort)
	if ab == nil {
		log.Info("Missing AddressBinding for SubnetPort", "namespace", subnetPort.Namespace, "name", subnetPort.Name)
		return
	}
	if ab.Status.IPAddress != *nsxSubnetPort.ExternalAddressBinding.ExternalIpAddress {
		ab = ab.DeepCopy()
		ab.Status.IPAddress = *nsxSubnetPort.ExternalAddressBinding.ExternalIpAddress
		r.Client.Status().Update(ctx, ab)
		log.V(1).Info("Updated AddressBinding CR status", "namespace", ab.Namespace, "name", ab.Name, "status", ab.Status)
	}
}
