/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnetport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
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
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

const resourceTypeAddressBinding = "AddressBinding"

var (
	log                     = &logger.Log
	MetricResTypeSubnetPort = common.MetricResTypeSubnetPort
)

var (
	vmOrInterfaceNotFoundError  = fmt.Errorf("VM or interface not found")
	subnetPortRealizationError  = fmt.Errorf("SubnetPort realization error")
	multipleInterfaceFoundError = fmt.Errorf("multiple interfaces found")
)

// SubnetPortReconciler reconciles a SubnetPort object
type SubnetPortReconciler struct {
	client.Client
	Scheme            *apimachineryruntime.Scheme
	SubnetPortService *subnetport.SubnetPortService
	SubnetService     servicecommon.SubnetServiceProvider
	VPCService        servicecommon.VPCServiceProvider
	Recorder          record.EventRecorder
	StatusUpdater     common.StatusUpdater
}

// +kubebuilder:rbac:groups=nsx.vmware.com,resources=subnetports,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=nsx.vmware.com,resources=subnetports/status,verbs=get;update;patch
func (r *SubnetPortReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log.Info("reconciling subnetport CR", "subnetport", req.NamespacedName)
	startTime := time.Now()
	defer func() {
		log.Info("finished reconciling SubnetPort", "SubnetPort", req.NamespacedName, "duration", time.Since(startTime))
	}()

	r.StatusUpdater.IncreaseSyncTotal()

	subnetPort := &v1alpha1.SubnetPort{}
	if err := r.Client.Get(ctx, req.NamespacedName, subnetPort); err != nil {
		if apierrors.IsNotFound(err) {
			if err := r.deleteSubnetPortByName(ctx, req.Namespace, req.Name); err != nil {
				r.StatusUpdater.DeleteFail(req.NamespacedName, nil, err)
				return common.ResultRequeue, err
			}
			r.StatusUpdater.DeleteSuccess(req.NamespacedName, nil)
			return common.ResultNormal, nil
		}
		log.Error(err, "unable to fetch SubnetPort CR", "SubnetPort", req.NamespacedName)
		return common.ResultRequeue, err
	}

	if subnetPort.ObjectMeta.DeletionTimestamp.IsZero() {
		r.StatusUpdater.IncreaseUpdateTotal()

		old_status := subnetPort.Status.DeepCopy()
		isExisting, isParentResourceTerminating, nsxSubnetPath, err := r.CheckAndGetSubnetPathForSubnetPort(ctx, subnetPort)
		if isParentResourceTerminating {
			err = errors.New("parent resource is terminating, SubnetPort cannot be created")
			r.StatusUpdater.UpdateFail(ctx, subnetPort, err, "", setSubnetPortReadyStatusFalse, r.SubnetPortService)
			return common.ResultNormal, err
		}
		if err != nil {
			r.StatusUpdater.UpdateFail(ctx, subnetPort, err, "Failed to get NSX resource path from Subnet", setSubnetPortReadyStatusFalse, r.SubnetPortService)
			return common.ResultRequeue, err
		}
		if !isExisting {
			defer r.SubnetPortService.ReleasePortInSubnet(nsxSubnetPath)
		}
		labels, err := r.getLabelsFromVirtualMachine(ctx, subnetPort)
		if err != nil {
			r.StatusUpdater.UpdateFail(ctx, subnetPort, err, "Failed to get labels from VirtualMachine", setSubnetPortReadyStatusFalse, r.SubnetPortService)
			return common.ResultRequeue, err
		}

		nsxSubnet, err := r.SubnetService.GetSubnetByPath(nsxSubnetPath)
		if err != nil {
			r.StatusUpdater.UpdateFail(ctx, subnetPort, err, fmt.Sprintf("Failed to get Subnet by path: %s", nsxSubnetPath), setSubnetPortReadyStatusFalse, r.SubnetPortService)
			return common.ResultRequeue, err
		}
		nsxSubnetPortState, err := r.SubnetPortService.CreateOrUpdateSubnetPort(subnetPort, nsxSubnet, "", labels)
		if err != nil {
			r.StatusUpdater.UpdateFail(ctx, subnetPort, err, "", setSubnetPortReadyStatusFalse, r.SubnetPortService)
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
		if reflect.DeepEqual(*old_status, subnetPort.Status) {
			log.Info("status (without conditions) already matched", "new status", subnetPort.Status, "existing status", old_status)
		} else {
			// If the SubnetPort CR's status changed, let's clean the conditions, to ensure the r.Client.Status().Update in the following updateSuccess will be invoked at any time.
			subnetPort.Status.Conditions = nil
		}
		r.StatusUpdater.UpdateSuccess(ctx, subnetPort, setReadyStatusTrue, r.SubnetPortService)
	} else {
		r.StatusUpdater.IncreaseDeleteTotal()
		subnetPortID := r.SubnetPortService.BuildSubnetPortId(&subnetPort.ObjectMeta)
		if err := r.SubnetPortService.DeleteSubnetPortById(subnetPortID); err != nil {
			r.StatusUpdater.DeleteFail(req.NamespacedName, nil, err)
			setAddressBindingStatusBySubnetPort(r.Client, ctx, subnetPort, r.SubnetPortService, metav1.Now(), subnetPortRealizationError)
			return common.ResultRequeue, err
		}
		r.StatusUpdater.DeleteSuccess(req.NamespacedName, nil)
		setAddressBindingStatusBySubnetPort(r.Client, ctx, subnetPort, r.SubnetPortService, metav1.Now(), vmOrInterfaceNotFoundError)
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

	var hasSubnetPort bool
	var externalIpAddress *string
	for _, nsxSubnetPort := range nsxSubnetPorts {
		if crSubnetPortIDsSet.Has(*nsxSubnetPort.Id) {
			log.Info("skipping deletion, SubnetPort CR still exists in K8s", "ID", *nsxSubnetPort.Id)
			hasSubnetPort = true
			continue
		}
		if nsxSubnetPort.ExternalAddressBinding != nil && nsxSubnetPort.ExternalAddressBinding.ExternalIpAddress != nil && *nsxSubnetPort.ExternalAddressBinding.ExternalIpAddress != "" {
			externalIpAddress = nsxSubnetPort.ExternalAddressBinding.ExternalIpAddress
		}
		if err := r.SubnetPortService.DeleteSubnetPort(nsxSubnetPort); err != nil {
			if !hasSubnetPort && externalIpAddress != nil {
				r.collectAddressBindingGarbage(ctx, &ns, externalIpAddress)
			}
			return err
		}
	}
	if !hasSubnetPort && externalIpAddress != nil {
		r.collectAddressBindingGarbage(ctx, &ns, externalIpAddress)
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
	subnetPortReconciler.StatusUpdater = common.NewStatusUpdater(subnetPortReconciler.Client, subnetPortReconciler.SubnetPortService.NSXConfig, subnetPortReconciler.Recorder, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort")
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
		r.StatusUpdater.IncreaseDeleteTotal()
		err = r.SubnetPortService.DeleteSubnetPortById(elem)
		if err != nil {
			r.StatusUpdater.IncreaseDeleteFailTotal()
		} else {
			r.StatusUpdater.IncreaseDeleteSuccessTotal()
		}
	}

	r.collectAddressBindingGarbage(ctx, nil, nil)
}

func setReadyStatusTrue(client client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, args ...interface{}) {
	if len(args) != 1 {
		log.Error(nil, "SubnetPortService are needed when updating SubnetPort status")
		return
	}
	subnetPort := obj.(*v1alpha1.SubnetPort)
	subnetPortService := args[0].(*subnetport.SubnetPortService)
	setSubnetPortReadyStatusTrue(client, ctx, obj, transitionTime)
	setAddressBindingStatusBySubnetPort(client, ctx, subnetPort, subnetPortService, transitionTime, nil)
}

func setSubnetPortReadyStatusTrue(client client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time) {
	subnetPort := obj.(*v1alpha1.SubnetPort)
	newConditions := []v1alpha1.Condition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionTrue,
			Message:            "NSX subnet port has been successfully created/updated",
			Reason:             "SubnetPortReady",
			LastTransitionTime: transitionTime,
		},
	}
	updateSubnetPortStatusConditions(client, ctx, subnetPort, newConditions)
}

func setSubnetPortReadyStatusFalse(client client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, err error, args ...interface{}) {
	if len(args) != 1 {
		log.Error(nil, "SubnetPortService are needed when updating SubnetPort status")
		return
	}
	subnetPort := obj.(*v1alpha1.SubnetPort)
	subnetPortService := args[0].(*subnetport.SubnetPortService)
	newConditions := []v1alpha1.Condition{
		{
			Type:   v1alpha1.Ready,
			Status: v1.ConditionFalse,
			Message: fmt.Sprintf(
				"error occurred while processing the SubnetPort CR. Error: %v",
				err,
			),
			Reason:             "SubnetPortNotReady",
			LastTransitionTime: transitionTime,
		},
	}
	updateSubnetPortStatusConditions(client, ctx, subnetPort, newConditions)
	setAddressBindingStatusBySubnetPort(client, ctx, subnetPort, subnetPortService, transitionTime, subnetPortRealizationError)
}

func updateSubnetPortStatusConditions(client client.Client, ctx context.Context, subnetPort *v1alpha1.SubnetPort, newConditions []v1alpha1.Condition) {
	conditionsUpdated := false
	for i := range newConditions {
		if mergeSubnetPortStatusCondition(subnetPort, &newConditions[i]) {
			conditionsUpdated = true
		}
	}
	if conditionsUpdated {
		client.Status().Update(ctx, subnetPort)
		log.V(1).Info("updated subnet port CR", "Name", subnetPort.Name, "Namespace", subnetPort.Namespace,
			"New Conditions", newConditions)
	}
}

func mergeSubnetPortStatusCondition(subnetPort *v1alpha1.SubnetPort, newCondition *v1alpha1.Condition) bool {
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

func (r *SubnetPortReconciler) CheckAndGetSubnetPathForSubnetPort(ctx context.Context, subnetPort *v1alpha1.SubnetPort) (existing bool, isStale bool, subnetPath string, err error) {
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
			existing = true
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
		nsxSubnet := subnetList[0]
		if !r.SubnetPortService.AllocatePortFromSubnet(nsxSubnet) {
			err = fmt.Errorf("no valid IP in Subnet %s", *nsxSubnet.Path)
			return
		}
		subnetPath = *nsxSubnet.Path
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
	spList := &v1alpha1.SubnetPortList{}
	spIndexValue := fmt.Sprintf("%s/%s", ab.Namespace, ab.Spec.VMName)
	err := r.Client.List(ctx, spList, client.MatchingFields{util.SubnetPortNamespaceVMIndexKey: spIndexValue})
	if err != nil {
		log.Error(err, "Failed to list SubnetPort from cache", "indexValue", spIndexValue)
		return nil
	}
	if len(spList.Items) == 0 {
		setAddressBindingStatus(r.Client, ctx, ab, metav1.Now(), vmOrInterfaceNotFoundError, "")
		return nil
	}
	// sort by CreationTimestamp
	slices.SortFunc(spList.Items, func(a, b v1alpha1.SubnetPort) int {
		return a.CreationTimestamp.UTC().Compare(b.CreationTimestamp.UTC())
	})
	if ab.Spec.InterfaceName == "" {
		if len(spList.Items) == 1 {
			log.V(1).Info("Enqueue SubnetPort for default AddressBinding", "namespace", ab.Namespace, "name", ab.Name, "SubnetPortName", spList.Items[0].Name, "VM", ab.Spec.VMName)
		} else {
			// Reconcile the oldest SubnetPort to check if the ExternalAddress should be removed.
			log.Info("Found multiple SubnetPorts for a VM, enqueue oldest SubnetPort", "namespace", ab.Namespace, "name", ab.Name, "subnetPortCount", len(spList.Items), "VM", ab.Spec.VMName)
			setAddressBindingStatus(r.Client, ctx, ab, metav1.Now(), multipleInterfaceFoundError, "")
		}
		return []reconcile.Request{{
			NamespacedName: types.NamespacedName{
				Name:      spList.Items[0].Name,
				Namespace: spList.Items[0].Namespace,
			},
		}}
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
	setAddressBindingStatus(r.Client, ctx, ab, metav1.Now(), vmOrInterfaceNotFoundError, "")
	return nil
}

func (r *SubnetPortReconciler) collectAddressBindingGarbage(ctx context.Context, namespace, ipAddress *string) {
	abList := &v1alpha1.AddressBindingList{}
	var err error
	listOptions := []client.ListOption{}
	if namespace != nil && *namespace != "" {
		listOptions = append(listOptions, client.InNamespace(*namespace))
	}
	if err = r.Client.List(ctx, abList, listOptions...); err != nil {
		log.Error(err, "Failed to list AddressBindings from cache")
		return
	}
	for i, ab := range abList.Items {
		if ipAddress != nil && ab.Status.IPAddress != *ipAddress {
			continue
		}
		spList := &v1alpha1.SubnetPortList{}
		spIndexValue := fmt.Sprintf("%s/%s", ab.Namespace, ab.Spec.VMName)
		err := r.Client.List(ctx, spList, client.MatchingFields{util.SubnetPortNamespaceVMIndexKey: spIndexValue})
		if err != nil {
			log.Error(err, "Failed to list SubnetPort from cache", "indexValue", spIndexValue)
			continue
		}
		if ab.Spec.InterfaceName == "" && len(spList.Items) > 1 {
			setAddressBindingStatus(r.Client, ctx, &abList.Items[i], metav1.Now(), multipleInterfaceFoundError, "")
			continue
		}
		found := false
		for i, sp := range spList.Items {
			vm, port, err := common.GetVirtualMachineNameForSubnetPort(&spList.Items[i])
			if err != nil || vm == "" {
				log.Error(err, "Failed to get VM name from SubnetPort", "namespace", sp.Namespace, "name", sp.Name, "annotations", sp.Annotations)
				continue
			}
			if ab.Spec.InterfaceName == "" || ab.Spec.InterfaceName == port {
				found = true
				break
			}
		}
		if !found {
			setAddressBindingStatus(r.Client, ctx, &abList.Items[i], metav1.Now(), vmOrInterfaceNotFoundError, "")
		}
	}
}

func setAddressBindingStatusBySubnetPort(client client.Client, ctx context.Context, subnetPort *v1alpha1.SubnetPort, subnetPortService *subnetport.SubnetPortService, transitionTime metav1.Time, e error) {
	ipAddress := ""
	subnetPortID := subnetPortService.BuildSubnetPortId(&subnetPort.ObjectMeta)
	nsxSubnetPort := subnetPortService.SubnetPortStore.GetByKey(subnetPortID)
	if nsxSubnetPort == nil {
		log.Info("Missing SubnetPort", "id", subnetPort.UID)
		if e == nil {
			e = vmOrInterfaceNotFoundError
		}
	} else if nsxSubnetPort.ExternalAddressBinding != nil && nsxSubnetPort.ExternalAddressBinding.ExternalIpAddress != nil {
		ipAddress = *nsxSubnetPort.ExternalAddressBinding.ExternalIpAddress
	}
	ab := subnetPortService.GetAddressBindingBySubnetPort(subnetPort)
	if ab == nil {
		log.Info("No AddressBinding for SubnetPort", "namespace", subnetPort.Namespace, "name", subnetPort.Name)
		return
	}
	setAddressBindingStatus(client, ctx, ab, transitionTime, e, ipAddress)
}

func setAddressBindingStatus(client client.Client, ctx context.Context, ab *v1alpha1.AddressBinding, transitionTime metav1.Time, e error, ipAddress string) {
	newConditions := newReadyCondition(resourceTypeAddressBinding, transitionTime, e)
	isUpdated := false
	for i := range newConditions {
		conditionUpdated := false
		ab.Status.Conditions, conditionUpdated = mergeCondition(ab.Status.Conditions, &newConditions[i])
		isUpdated = isUpdated || conditionUpdated
	}
	if ab.Status.IPAddress != ipAddress {
		isUpdated = true
	}
	if isUpdated {
		ab = ab.DeepCopy()
		ab.Status.IPAddress = ipAddress
		err := client.Status().Update(ctx, ab)
		log.V(1).Info("Updated AddressBinding CR status", "namespace", ab.Namespace, "name", ab.Name, "status", ab.Status, "err", err)
	}
}

func newReadyCondition(resourceType string, transitionTime metav1.Time, e error) []v1alpha1.Condition {
	if e == nil {
		return []v1alpha1.Condition{
			{
				Type:               v1alpha1.Ready,
				Status:             v1.ConditionTrue,
				Message:            fmt.Sprintf("%s has been successfully created/updated", resourceType),
				Reason:             fmt.Sprintf("%sReady", resourceType),
				LastTransitionTime: transitionTime,
			},
		}
	}
	return []v1alpha1.Condition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionFalse,
			Message:            fmt.Sprintf("error occurred while processing the %s CR. Error: %v", resourceType, e),
			Reason:             fmt.Sprintf("%sNotReady", resourceType),
			LastTransitionTime: transitionTime,
		},
	}
}

func mergeCondition(existingConditions []v1alpha1.Condition, newCondition *v1alpha1.Condition) ([]v1alpha1.Condition, bool) {
	matchedCondition := getExistingConditionOfType(newCondition.Type, existingConditions)

	newConditionCopy := newCondition.DeepCopy()
	if matchedCondition != nil {
		// Ignore LastTransitionTime mismatch
		newConditionCopy.LastTransitionTime = matchedCondition.LastTransitionTime
	}
	if reflect.DeepEqual(matchedCondition, newConditionCopy) {
		log.V(2).Info("conditions already match", "New Condition", newCondition, "Existing Condition", matchedCondition)
		return existingConditions, false
	}

	if matchedCondition != nil {
		matchedCondition.Reason = newCondition.Reason
		matchedCondition.Message = newCondition.Message
		matchedCondition.Status = newCondition.Status
	} else {
		existingConditions = append(existingConditions, *newCondition)
	}
	return existingConditions, true
}
