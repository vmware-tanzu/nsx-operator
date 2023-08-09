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

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		nsxSubnetPath, err := r.GetSubnetPathForSubnetPort(obj)
		if err != nil {
			log.Error(err, "failed to get NSX resource path from subnet", "subnetport", obj)
			return common.ResultRequeue, err
		}
		err = r.Service.CreateOrUpdateSubnetPort(obj, nsxSubnetPath)
		if err != nil {
			log.Error(err, "failed to create or update NSX subnet port, would retry exponentially", "subnetport", req.NamespacedName)
			updateFail(r, &ctx, obj, &err)
			return common.ResultRequeue, err
		}
		err = r.updateSubnetStatusOnSubnetPort(obj, nsxSubnetPath)
		if err != nil {
			log.Error(err, "failed to retrieve subnet status for subnetport", "subnetport", obj, "nsxSubnetPath", nsxSubnetPath)
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
	subnetPortReconciler := SubnetPortReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
	if subnetPortService, err := subnetport.InitializeSubnetPort(commonService); err != nil {
		log.Error(err, "failed to initialize subnetport commonService", "controller", "SubnetPort")
		os.Exit(1)
	} else {
		subnetPortReconciler.Service = subnetPortService
		common.ServiceMediator.SubnetPortService = subnetPortReconciler.Service
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

func (r *SubnetPortReconciler) GetSubnetPathForSubnetPort(obj *v1alpha1.SubnetPort) (string, error) {
	subnetPath := ""
	if len(obj.Spec.Subnet) > 0 {
		subnet := &v1alpha1.Subnet{}
		namespacedName := types.NamespacedName{
			Name:      obj.Spec.Subnet,
			Namespace: obj.Namespace,
		}
		if err := r.Client.Get(context.Background(), namespacedName, subnet); err != nil {
			log.Error(err, "subnet CR not found", "subnet CR", namespacedName)
			return subnetPath, err
		}
		subnetPath = subnet.Status.NSXResourcePath
		if len(subnetPath) == 0 {
			err := fmt.Errorf("empty NSX resource path from subnet %s", subnet.Name)
			return subnetPath, err
		}
	} else if len(obj.Spec.SubnetSet) > 0 {
		subnetSet := &v1alpha1.SubnetSet{}
		namespacedName := types.NamespacedName{
			Name:      obj.Spec.SubnetSet,
			Namespace: obj.Namespace,
		}
		if err := r.Client.Get(context.Background(), namespacedName, subnetSet); err != nil {
			log.Error(err, "subnetSet CR not found", "subnet CR", namespacedName)
			return subnetPath, err
		}
		subnetPath, err := r.AllocateSubnetFromSubnetSet(obj, subnetSet)
		if err != nil {
			return subnetPath, err
		}
		return subnetPath, nil
	} else {
		subnetSet, err := r.GetDefaultSubnetSet(obj)
		if err != nil {
			return "", err
		}
		subnetPath, err := r.AllocateSubnetFromSubnetSet(obj, subnetSet)
		if err != nil {
			return subnetPath, err
		}
		return subnetPath, nil
	}
	return subnetPath, nil
}

func (r *SubnetPortReconciler) AllocateSubnetFromSubnetSet(subnetPort *v1alpha1.SubnetPort, subnetSet *v1alpha1.SubnetSet) (string, error) {
	log.Info("allocating Subnet for SubnetPort", "subnetPort", subnetPort.Name, "subnetSet", subnetSet.Name)
	subnetPath, err := common.ServiceMediator.GetAvailableSubnet(subnetSet)
	if err != nil {
		log.Error(err, "failed to allocate Subnet")
	}
	log.Info("allocated Subnet for SubnetPort", "subnetPath", subnetPath)
	return subnetPath, nil
}

func (r *SubnetPortReconciler) GetDefaultSubnetSet(subnetPort *v1alpha1.SubnetPort) (*v1alpha1.SubnetSet, error) {
	targetNamespace, _, err := r.getSharedNamespaceAndVpcForNamespace(subnetPort.Namespace)
	if err != nil {
		return nil, err
	}
	if targetNamespace == "" {
		log.Info("subnetport's namespace doesn't have shared VPC, searching the default subnetset in the current namespace", "subnetPort.Name", subnetPort.Name, "subnetPort.Namespace", subnetPort.Namespace)
		targetNamespace = subnetPort.Namespace
	}
	subnetSet, err := r.getDefaultSubnetSetByNamespace(subnetPort, targetNamespace)
	if err != nil {
		return nil, err
	}
	return subnetSet, err
}

func (r *SubnetPortReconciler) getSharedNamespaceAndVpcForNamespace(namespaceName string) (string, string, error) {
	sharedNamespaceName := ""
	sharedVpcName := ""
	namespace := &v1.Namespace{}
	namespacedName := types.NamespacedName{Name: namespaceName}
	if err := r.Client.Get(context.Background(), namespacedName, namespace); err != nil {
		log.Error(err, "failed to get target namespace during getting VPC for namespace")
		return "", "", err
	}
	// TODO: import "nsx.vmware.com/vpc_name" as the constant from types afer VPC patch is merged.
	vpcAnnotation, exists := namespace.Annotations["nsx.vmware.com/vpc_name"]
	if !exists {
		return "", "", nil
	}
	array := strings.Split(vpcAnnotation, "/")
	if len(array) != 2 {
		err := fmt.Errorf("invalid annotation value of 'nsx.vmware.com/vpc_name': %s", vpcAnnotation)
		return "", "", err
	}
	sharedNamespaceName, sharedVpcName = array[0], array[1]
	log.Info("got shared VPC for namespace", "current namespace", namespaceName, "shared VPC", sharedVpcName, "shared namespace", sharedNamespaceName)
	return sharedNamespaceName, sharedVpcName, nil
}

func (r *SubnetPortReconciler) getDefaultSubnetSetByNamespace(subnetPort *v1alpha1.SubnetPort, namespace string) (*v1alpha1.SubnetSet, error) {
	subnetSetList := &v1alpha1.SubnetSetList{}
	subnetSetSelector := &metav1.LabelSelector{
		MatchLabels: map[string]string{
			servicecommon.LabelDefaultSubnetSet: servicecommon.ResourceTypeVirtualMachine,
		},
	}
	labelSelector, _ := metav1.LabelSelectorAsSelector(subnetSetSelector)
	opts := &client.ListOptions{
		LabelSelector: labelSelector,
		Namespace:     namespace,
	}
	if err := r.Service.Client.List(context.Background(), subnetSetList, opts); err != nil {
		log.Error(err, "failed to list default subnetset CR", "namespace", subnetPort.Namespace)
		return nil, err
	}
	if len(subnetSetList.Items) == 0 {
		return nil, errors.New("default subnetset not found")
	} else if len(subnetSetList.Items) > 1 {
		return nil, errors.New("multiple default subnetsets found")
	}
	subnetSet := subnetSetList.Items[0]
	log.Info("got default subnetset", "subnetset.Name", subnetSet.Name, "subnetset.uid", subnetSet.UID)
	return &subnetSet, nil

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
