/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnetport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	vmv1alpha1 "github.com/vmware-tanzu/vm-operator/api/v1alpha1"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
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
	Scheme                     *apimachineryruntime.Scheme
	SubnetPortService          *subnetport.SubnetPortService
	SubnetService              servicecommon.SubnetServiceProvider
	VPCService                 servicecommon.VPCServiceProvider
	IpAddressAllocationService servicecommon.IPAddressAllocationServiceProvider
	Recorder                   record.EventRecorder
	StatusUpdater              common.StatusUpdater
	restoreMode                bool
}

// +kubebuilder:rbac:groups=nsx.vmware.com,resources=subnetports,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=nsx.vmware.com,resources=subnetports/status,verbs=get;update;patch
func (r *SubnetPortReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log.Info("Reconciling SubnetPort CR", "SubnetPort", req.NamespacedName)
	startTime := time.Now()
	defer func() {
		log.Info("Finished reconciling SubnetPort", "SubnetPort", req.NamespacedName, "duration", time.Since(startTime))
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
		log.Error(err, "Unable to fetch SubnetPort CR", "SubnetPort", req.NamespacedName)
		return common.ResultRequeue, err
	}

	if subnetPort.ObjectMeta.DeletionTimestamp.IsZero() {
		r.StatusUpdater.IncreaseUpdateTotal()

		old_status := subnetPort.Status.DeepCopy()
		subnetCR, isParentResourceTerminating, err := r.getSubnetCR(ctx, subnetPort)
		if isParentResourceTerminating {
			err = errors.New("parent resource is terminating, SubnetPort cannot be created")
			r.StatusUpdater.UpdateFail(ctx, subnetPort, err, "", setSubnetPortReadyStatusFalse, r.SubnetPortService, r.restoreMode)
			return common.ResultNormal, err
		}
		if err != nil {
			r.StatusUpdater.UpdateFail(ctx, subnetPort, err, "Failed to get Subnet CR", setSubnetPortReadyStatusFalse, r.SubnetPortService, r.restoreMode)
			return common.ResultRequeue, err
		}
		isExisting, isParentResourceTerminating, nsxSubnetPath, err := r.CheckAndGetSubnetPathForSubnetPort(ctx, subnetPort, subnetCR)
		if isParentResourceTerminating {
			err = errors.New("parent resource is terminating, SubnetPort cannot be created")
			r.StatusUpdater.UpdateFail(ctx, subnetPort, err, "", setSubnetPortReadyStatusFalse, r.SubnetPortService, r.restoreMode)
			return common.ResultNormal, err
		}
		if err != nil {
			r.StatusUpdater.UpdateFail(ctx, subnetPort, err, "Failed to get NSX resource path from Subnet", setSubnetPortReadyStatusFalse, r.SubnetPortService, r.restoreMode)
			return common.ResultRequeue, err
		}
		if !isExisting {
			defer r.SubnetPortService.ReleasePortInSubnet(nsxSubnetPath)
		}

		var labels *map[string]string
		vm, nicName, err := r.getVirtualMachine(ctx, subnetPort)
		if err != nil {
			r.StatusUpdater.UpdateFail(ctx, subnetPort, err, "Failed to get labels from VirtualMachine", setSubnetPortReadyStatusFalse, r.SubnetPortService, r.restoreMode)
			return common.ResultRequeue, err
		}
		if vm != nil {
			labels = &vm.Labels
		}
		inSharedSubnet := false
		if subnetCR != nil {
			inSharedSubnet = servicecommon.IsSharedSubnet(subnetCR)
		}
		nsxSubnet, err := r.SubnetService.GetSubnetByPath(nsxSubnetPath, inSharedSubnet)
		if err != nil {
			vpcSubnetPort, searchErr := r.SubnetPortService.SubnetPortStore.GetVpcSubnetPortByUID(subnetPort.GetUID())
			if searchErr != nil {
				log.Error(searchErr, "failed to use the SubnetPort CR to search VpcSubnetPort", "CR UID", subnetPort.GetUID())
				err = errors.Join(err, searchErr)
			} else if vpcSubnetPort != nil {
				if e := r.SubnetPortService.DeleteSubnetPort(vpcSubnetPort); e != nil {
					log.Error(e, "Failed to delete the stale SubnetPort", "subnetPort.UID", subnetPort.UID)
					err = errors.Join(err, e)
				}
			}
			r.StatusUpdater.UpdateFail(ctx, subnetPort, err, fmt.Sprintf("Failed to get Subnet by path: %s", nsxSubnetPath), setSubnetPortReadyStatusFalse, r.SubnetPortService, r.restoreMode)
			return common.ResultRequeue, err
		}

		isVmSubnetPort := true
		if value, exists := subnetPort.Labels[servicecommon.LabelImageFetcher]; exists && value == "true" {
			isVmSubnetPort = false
			if labels == nil {
				labels = &map[string]string{}
			}
			(*labels)[servicecommon.LabelImageFetcher] = "true"
		}
		ab := r.SubnetPortService.GetAddressBindingBySubnetPort(subnetPort)
		err = r.IpAddressAllocationService.CreateIPAddressAllocationForAddressBinding(ab, subnetPort, r.restoreMode)
		if err != nil {
			r.StatusUpdater.UpdateFail(ctx, subnetPort, err, "Failed to create NSX IPAddressAllocation for AddressBinding restore", setSubnetPortReadyStatusFalse, r.SubnetPortService, r.restoreMode)
			return common.ResultRequeue, err
		}
		nsxSubnetPortState, enableDHCP, err := r.SubnetPortService.CreateOrUpdateSubnetPort(subnetPort, nsxSubnet, "", labels, isVmSubnetPort, r.restoreMode)
		if err != nil {
			r.StatusUpdater.UpdateFail(ctx, subnetPort, err, "", setSubnetPortReadyStatusFalse, r.SubnetPortService, r.restoreMode)
			return common.ResultRequeue, err
		}
		if nsxSubnetPortState.ExternalAddressBinding == nil && ab == nil {
			err = r.IpAddressAllocationService.DeleteIPAddressAllocationForAddressBinding(subnetPort)
			if err != nil {
				log.Error(err, "Failed to cleanup possible NSX IPAddressAllocation", "SubnetPort", subnetPort)
				r.StatusUpdater.UpdateFail(ctx, subnetPort, err, "", setSubnetPortReadyStatusFalse, r.SubnetPortService, r.restoreMode)
				return common.ResultRequeue, err
			}
		}
		subnetPort.Status.Attachment = v1alpha1.PortAttachment{ID: *nsxSubnetPortState.Attachment.Id}
		subnetPort.Status.NetworkInterfaceConfig = v1alpha1.NetworkInterfaceConfig{
			IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
				{
					Gateway: "",
				},
			},
		}
		if !enableDHCP && len(nsxSubnetPortState.RealizedBindings) > 0 {
			subnetPort.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress = *nsxSubnetPortState.RealizedBindings[0].Binding.IpAddress
			subnetPort.Status.NetworkInterfaceConfig.MACAddress = strings.Trim(*nsxSubnetPortState.RealizedBindings[0].Binding.MacAddress, "\"")
		}
		err = r.updateSubnetStatusOnSubnetPort(subnetPort, nsxSubnetPath, nsxSubnet)
		if err != nil {
			log.Error(err, "Failed to retrieve Subnet status for SubnetPort", "SubnetPort", subnetPort, "nsxSubnetPath", nsxSubnetPath)
		}
		if reflect.DeepEqual(*old_status, subnetPort.Status) {
			log.Info("Status (without conditions) already matched", "new status", subnetPort.Status, "existing status", old_status)
		} else {
			// If the SubnetPort CR's status changed, let's clean the conditions, to ensure the r.Client.Status().Update in the following updateSuccess will be invoked at any time.
			subnetPort.Status.Conditions = nil
		}
		r.StatusUpdater.UpdateSuccess(ctx, subnetPort, setReadyStatusTrue, r.SubnetPortService)
		if r.restoreMode {
			// For restored SubnetPort,
			// add restore annotation on SubnetPort CR for cpVM;
			// add restore annotation on VM for VM service VM
			portLabels := subnetPort.GetLabels()
			cpvmValue, ok := portLabels[servicecommon.LabelCPVM]
			if ok {
				isCPVM, err := strconv.ParseBool(cpvmValue)
				if err != nil {
					log.Error(err, "Failed to parse cpvm label", "label", cpvmValue)
				} else if isCPVM {
					retry.OnError(util.K8sClientRetry, func(err error) bool {
						return err != nil
					}, func() error {
						return common.UpdateRestoreAnnotation(r.Client, ctx, subnetPort, "cpvm")
					})
				}
			}
			if vm != nil {
				retry.OnError(util.K8sClientRetry, func(err error) bool {
					return err != nil
				}, func() error {
					return common.UpdateRestoreAnnotation(r.Client, ctx, vm, nicName)
				})
			}
		}
	} else {
		r.StatusUpdater.IncreaseDeleteTotal()
		vpcSubnetPort, err := r.SubnetPortService.SubnetPortStore.GetVpcSubnetPortByUID(subnetPort.GetUID())
		if err != nil {
			r.StatusUpdater.DeleteFail(req.NamespacedName, nil, err)
			setAddressBindingStatusBySubnetPort(r.Client, ctx, subnetPort, r.SubnetPortService, metav1.Now(), subnetPortRealizationError)
			return common.ResultRequeue, err
		}
		if vpcSubnetPort != nil {
			if err = r.SubnetPortService.DeleteSubnetPort(vpcSubnetPort); err != nil {
				r.StatusUpdater.DeleteFail(req.NamespacedName, nil, err)
				setAddressBindingStatusBySubnetPort(r.Client, ctx, subnetPort, r.SubnetPortService, metav1.Now(), subnetPortRealizationError)
				return common.ResultRequeue, err
			}
		}

		ab := r.SubnetPortService.GetAddressBindingBySubnetPort(subnetPort)
		err = r.IpAddressAllocationService.DeleteIPAddressAllocationForAddressBinding(ab)
		if err != nil {
			log.Error(err, "Failed to delete IPAddressAllocation for AddressBinding", "AddressBinding", ab)
			return common.ResultRequeue, err
		}
		r.StatusUpdater.DeleteSuccess(req.NamespacedName, nil)
		setAddressBindingStatusBySubnetPort(r.Client, ctx, subnetPort, r.SubnetPortService, metav1.Now(), vmOrInterfaceNotFoundError)
	}
	return common.ResultNormal, nil
}

func (r *SubnetPortReconciler) getSubnetCR(ctx context.Context, subnetPort *v1alpha1.SubnetPort) (*v1alpha1.Subnet, bool, error) {
	if len(subnetPort.Spec.Subnet) > 0 {
		subnetCR := &v1alpha1.Subnet{}
		namespacedName := types.NamespacedName{
			Name:      subnetPort.Spec.Subnet,
			Namespace: subnetPort.Namespace,
		}
		if err := r.Client.Get(ctx, namespacedName, subnetCR); err != nil {
			log.Error(err, "Subnet CR not found", "SubnetCR", namespacedName)
			return nil, false, err
		}
		if !subnetCR.DeletionTimestamp.IsZero() {
			return nil, true, fmt.Errorf("subnet %s is being deleted, cannot operate SubnetPort %s", namespacedName, subnetPort.Name)
		}
		return subnetCR, false, nil
	}
	return nil, false, nil
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

func addressBindingIPAddressAllocationNameIndexFunc(obj client.Object) []string {
	if ab, ok := obj.(*v1alpha1.AddressBinding); !ok {
		log.Info("Invalid object", "type", reflect.TypeOf(obj))
		return []string{}
	} else {
		return []string{fmt.Sprintf("%s", ab.Spec.IPAddressAllocationName)}
	}
}

func subnetPortSubnetIndexFunc(obj client.Object) []string {
	if subnetPort, ok := obj.(*v1alpha1.SubnetPort); !ok {
		log.Info("Invalid object", "type", reflect.TypeOf(obj))
		return []string{}
	} else {
		if subnetPort.Spec.Subnet == "" {
			return []string{}
		}
		return []string{subnetPort.Spec.Subnet}
	}
}

func (r *SubnetPortReconciler) deleteSubnetPortByName(ctx context.Context, ns string, name string) error {
	// NamespacedName is a unique identity in store as only one worker can deal with the NamespacedName at a time
	nsxSubnetPorts := r.SubnetPortService.ListSubnetPortByName(ns, name)

	var externalIpAddress *string
	for _, nsxSubnetPort := range nsxSubnetPorts {
		if nsxSubnetPort.ExternalAddressBinding != nil && nsxSubnetPort.ExternalAddressBinding.ExternalIpAddress != nil && *nsxSubnetPort.ExternalAddressBinding.ExternalIpAddress != "" {
			externalIpAddress = nsxSubnetPort.ExternalAddressBinding.ExternalIpAddress
		}
		if err := r.SubnetPortService.DeleteSubnetPort(nsxSubnetPort); err != nil {
			if externalIpAddress != nil {
				r.collectAddressBindingGarbage(ctx, &ns, externalIpAddress)
			}
			return err
		}
	}
	if externalIpAddress != nil {
		r.collectAddressBindingGarbage(ctx, &ns, externalIpAddress)
	}
	log.Info("Successfully deleted nsxSubnetPort", "Namespace", ns, "Name", name)
	return nil
}

// setupWithManager sets up the controller with the Manager.
func (r *SubnetPortReconciler) setupWithManager(mgr ctrl.Manager) error {
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

func (r *SubnetPortReconciler) SetupFieldIndexers(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.TODO(), &v1alpha1.SubnetPort{}, util.SubnetPortNamespaceVMIndexKey, subnetPortNamespaceVMIndexFunc); err != nil {
		return err
	}
	if err := mgr.GetFieldIndexer().IndexField(context.TODO(), &v1alpha1.AddressBinding{}, util.AddressBindingNamespaceVMIndexKey, addressBindingNamespaceVMIndexFunc); err != nil {
		return err
	}
	if err := mgr.GetFieldIndexer().IndexField(context.TODO(), &v1alpha1.AddressBinding{}, util.AddressBindingIPAddressAllocationNameIndexKey, addressBindingIPAddressAllocationNameIndexFunc); err != nil {
		return err
	}
	if err := mgr.GetFieldIndexer().IndexField(context.TODO(), &v1alpha1.SubnetPort{}, "spec.subnet", subnetPortSubnetIndexFunc); err != nil {
		return err
	}

	return nil
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

func (r *SubnetPortReconciler) RestoreReconcile() error {
	restoreList, err := r.getRestoreList()
	if err != nil {
		err = fmt.Errorf("failed to get SubnetPort restore list: %w", err)
		return err
	}
	var errorList []error
	r.restoreMode = true
	for _, key := range restoreList {
		result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
		if result.Requeue || err != nil {
			errorList = append(errorList, fmt.Errorf("failed to restore SubnetPort %s, error: %w", key, err))
		}
	}
	if len(errorList) > 0 {
		return errors.Join(errorList...)
	}
	return nil
}

func (r *SubnetPortReconciler) getRestoreList() ([]types.NamespacedName, error) {
	nsxSubnetPortCRIDs := r.SubnetPortService.SubnetPortStore.ListIndexFuncValues(servicecommon.TagScopeSubnetPortCRUID)
	restoreList := []types.NamespacedName{}
	subnetPortList := &v1alpha1.SubnetPortList{}
	if err := r.Client.List(context.TODO(), subnetPortList); err != nil {
		return restoreList, err
	}
	for _, subnetport := range subnetPortList.Items {
		// Restore a SubnetPort if SubnetPort CR has status updated but no corresponding NSX Subnetport in cache
		if len(subnetport.Status.NetworkInterfaceConfig.IPAddresses) > 0 && !nsxSubnetPortCRIDs.Has(string(subnetport.GetUID())) {
			restoreList = append(restoreList, types.NamespacedName{Namespace: subnetport.Namespace, Name: subnetport.Name})
			continue
		}

		ab := r.SubnetPortService.GetAddressBindingBySubnetPort(&subnetport)
		if ab == nil {
			// The SubnetPort doesn't have related external AddressBinding:
			// create SubnetPort -> backup -> restore
			continue
		}
		if len(ab.Status.IPAddress) == 0 {
			continue
		}
		// The SubnetPort does have related external AddressBinding. We need to check when the AddressBinding was realized, i.e. before the backup or after the backup.
		nsxSubnetPort, err := r.SubnetPortService.SubnetPortStore.GetVpcSubnetPortByUID(subnetport.GetUID())
		if err != nil {
			return restoreList, err
		}
		if nsxSubnetPort == nil {
			return restoreList, fmt.Errorf("NSX subnet port for SubnetPort CR %s not found", subnetport.UID)
		}
		if nsxSubnetPort.ExternalAddressBinding == nil {
			// The port's external address binding was created after the backup, should be restored:
			// create SubnetPort -> backup -> create external AddressBinding CR -> restore.
			restoreList = append(restoreList, types.NamespacedName{Namespace: subnetport.Namespace, Name: subnetport.Name})
			continue
		}
		if ab.Status.IPAddress != *nsxSubnetPort.ExternalAddressBinding.ExternalIpAddress {
			// The case is as follows:
			// create SubnetPort -> create external AddressBinding CR A (1.1.1.1) -> backup -> delete external AddressBinding CR A -> create external AddressBinding CR B (2.2.2.2) -> restore.
			restoreList = append(restoreList, types.NamespacedName{Namespace: subnetport.Namespace, Name: subnetport.Name})
		}
	}
	return restoreList, nil
}

func (r *SubnetPortReconciler) StartController(mgr ctrl.Manager, hookServer webhook.Server) error {
	if err := r.Start(mgr); err != nil {
		log.Error(err, "failed to create controller", "controller", "SubnetPort")
		return err
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
	go common.GenericGarbageCollector(make(chan bool), servicecommon.GCInterval, r.CollectGarbage)
	return nil
}

func NewSubnetPortReconciler(mgr ctrl.Manager, subnetPortService *subnetport.SubnetPortService, subnetService *subnet.SubnetService, vpcService *vpc.VPCService, ipAddressAllocationService servicecommon.IPAddressAllocationServiceProvider) *SubnetPortReconciler {
	subnetPortReconciler := &SubnetPortReconciler{
		Client:                     mgr.GetClient(),
		Scheme:                     mgr.GetScheme(),
		SubnetService:              subnetService,
		SubnetPortService:          subnetPortService,
		VPCService:                 vpcService,
		IpAddressAllocationService: ipAddressAllocationService,
		Recorder:                   mgr.GetEventRecorderFor("subnetport-controller"),
	}
	err := subnetPortReconciler.SetupFieldIndexers(mgr)
	if err != nil {
		log.Error(err, "Failed to setup field indexers for the SubnetPort controller")
		os.Exit(1)
	}
	subnetPortReconciler.StatusUpdater = common.NewStatusUpdater(subnetPortReconciler.Client, subnetPortReconciler.SubnetPortService.NSXConfig, subnetPortReconciler.Recorder, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort")
	return subnetPortReconciler
}

// Start setup manager and launch GC
func (r *SubnetPortReconciler) Start(mgr ctrl.Manager) error {
	err := r.setupWithManager(mgr)
	if err != nil {
		return err
	}
	return nil
}

// CollectGarbage collect SubnetPort which has been removed from crd.
// it implements the interface GarbageCollector method.
func (r *SubnetPortReconciler) CollectGarbage(ctx context.Context) error {
	log.Info("subnetport garbage collector started")
	nsxSubnetPortSet := r.SubnetPortService.ListNSXSubnetPortIDForCR()
	if len(nsxSubnetPortSet) == 0 {
		return nil
	}

	crSubnetPortIDsSet, err := r.SubnetPortService.ListSubnetPortIDsFromCRs(ctx)
	if err != nil {
		return err
	}

	var errList []error
	diffSet := nsxSubnetPortSet.Difference(crSubnetPortIDsSet)
	for elem := range diffSet {
		log.V(1).Info("GC collected SubnetPort CR", "UID", elem)
		r.StatusUpdater.IncreaseDeleteTotal()
		err = r.SubnetPortService.DeleteSubnetPortById(elem)
		if err != nil {
			errList = append(errList, err)
			r.StatusUpdater.IncreaseDeleteFailTotal()
		} else {
			r.StatusUpdater.IncreaseDeleteSuccessTotal()
		}
	}
	addressBindingUIDSet, err := r.getAddressBindingCRUIDSet(ctx)
	if err != nil {
		return err
	}
	subnetPortUIDSet, err := r.getSubnetPortCRUIDSet(ctx)
	if err != nil {
		return err
	}

	nsxIPAddressAllocationList := r.IpAddressAllocationService.ListIPAddressAllocationWithAddressBinding()
	for _, nsxIPAddressAllocation := range nsxIPAddressAllocationList {
		abUID := nsxutil.FindTag(nsxIPAddressAllocation.Tags, servicecommon.TagScopeAddressBindingCRUID)
		spUID := nsxutil.FindTag(nsxIPAddressAllocation.Tags, servicecommon.TagScopeSubnetPortCRUID)
		if addressBindingUIDSet.Has(abUID) && subnetPortUIDSet.Has(spUID) {
			continue
		}
		// Reclaim the NSX IPAddressAllocation if its AddressBinding CR or SubnetPort CR is removed.
		log.Info("GC collected NSX IPAddressAllocation", "nsxIPAddressAllocation", nsxIPAddressAllocation, "AddressBinding.UID", abUID, "SubnetPort.UID", spUID)
		err := r.IpAddressAllocationService.DeleteIPAddressAllocationByNSXResource(nsxIPAddressAllocation)
		if err != nil {
			errList = append(errList, err)
		}
	}

	r.collectAddressBindingGarbage(ctx, nil, nil)
	if len(errList) > 0 {
		return fmt.Errorf("errors found in SubnetPort garbage collection: %s", errList)
	}
	return nil
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
	if len(args) != 2 {
		log.Error(nil, "restoreMode and SubnetPortService are needed when updating SubnetPort status")
		return
	}
	subnetPort := obj.(*v1alpha1.SubnetPort)
	subnetPortService := args[0].(*subnetport.SubnetPortService)
	restoreMode := args[1].(bool)
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
	if restoreMode {
		// We need to reserve the Status info on the AddressBinding for the restore.
		return
	}
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
		log.V(1).Info("Updated SubnetPort CR", "Name", subnetPort.Name, "Namespace", subnetPort.Namespace,
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

func (r *SubnetPortReconciler) getSubnetBySubnetPort(subnetPort *v1alpha1.SubnetPort) (string, error) {
	var subnets []*model.VpcSubnet
	if len(subnetPort.Spec.Subnet) > 0 {
		subnets = r.SubnetService.ListSubnetByName(subnetPort.Namespace, subnetPort.Spec.Subnet)
	} else if len(subnetPort.Spec.SubnetSet) > 0 {
		subnets = r.SubnetService.ListSubnetBySubnetSetName(subnetPort.Namespace, subnetPort.Spec.SubnetSet)
	} else {
		subnetSet, err := common.GetDefaultSubnetSetByNamespace(r.Client, subnetPort.Namespace, servicecommon.LabelDefaultVMSubnetSet)
		if err != nil {
			return "", err
		}
		subnets = r.SubnetService.GetSubnetsByIndex(servicecommon.TagScopeSubnetSetCRUID, string(subnetSet.UID))
	}
	gatewayIP := net.ParseIP(subnetPort.Status.NetworkInterfaceConfig.IPAddresses[0].Gateway)
	return common.GetSubnetByIP(subnets, gatewayIP)
}

func (r *SubnetPortReconciler) CheckAndGetSubnetPathForSubnetPort(ctx context.Context, subnetPort *v1alpha1.SubnetPort, subnetCR *v1alpha1.Subnet) (existing bool, isStale bool, subnetPath string, err error) {
	existingSubnetPort, err := r.SubnetPortService.SubnetPortStore.GetVpcSubnetPortByUID(subnetPort.GetUID())
	if err != nil {
		log.Error(err, "failed to use the SubnetPort CR to search VpcSubnetPort", "CR UID", subnetPort.GetUID())
		return false, false, "", err
	}
	if existingSubnetPort != nil && existingSubnetPort.ParentPath != nil && len(*existingSubnetPort.ParentPath) > 0 {
		subnetPath = *existingSubnetPort.ParentPath
		// If there is a SubnetPath in store, there is a subnetport in NSX, the subnetport is not created first time.
		log.V(1).Info("NSX SubnetPort had been created, returning the existing NSX Subnet path", "subnetPort.UID", subnetPort.UID, "subnetPath", subnetPath)
		existing = true
		return
	}
	if r.restoreMode {
		// For restore case, SubnetPort will be created on the Subnet with matching CIDR
		if subnetPort.Status.NetworkInterfaceConfig.IPAddresses[0].Gateway != "" {
			subnetPath, err = r.getSubnetBySubnetPort(subnetPort)
			if err != nil {
				log.Error(err, "Failed to find Subnet for restored SubnetPort", "SubnetPort", subnetPort)
				return
			}
			existing = true
			log.V(1).Info("NSX SubnetPort will be restored on the existing NSX Subnet", "subnetPort.UID", subnetPort.UID, "subnetPath", subnetPath)
			return
		}
	}
	if len(subnetPort.Spec.Subnet) > 0 {
		if subnetCR == nil {
			err = fmt.Errorf("failed to get Subnet CR %s/%s", subnetPort.Namespace, subnetPort.Spec.Subnet)
			return
		}
		var nsxSubnet *model.VpcSubnet
		nsxSubnet, err = r.SubnetService.GetSubnetByCR(subnetCR)
		if err != nil {
			return
		}
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
		subnetSet, err = common.GetDefaultSubnetSetByNamespace(r.Client, subnetPort.Namespace, servicecommon.LabelDefaultVMSubnetSet)
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

func (r *SubnetPortReconciler) updateSubnetStatusOnSubnetPort(subnetPort *v1alpha1.SubnetPort, nsxSubnetPath string, nsxSubnet *model.VpcSubnet) error {
	gateway, prefix, err := r.SubnetPortService.GetGatewayPrefixForSubnetPort(subnetPort, nsxSubnetPath)
	if err != nil {
		return err
	}
	// For now, we have an assumption that one subnetport only have one IP address
	if len(subnetPort.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress) > 0 {
		subnetPort.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress += fmt.Sprintf("/%d", prefix)
	}
	subnetPort.Status.NetworkInterfaceConfig.IPAddresses[0].Gateway = gateway
	subnetPort.Status.NetworkInterfaceConfig.LogicalSwitchUUID = *nsxSubnet.RealizationId
	return nil
}

func (r *SubnetPortReconciler) getVirtualMachine(ctx context.Context, subnetPort *v1alpha1.SubnetPort) (*vmv1alpha1.VirtualMachine, string, error) {
	vmName, nicName, err := common.GetVirtualMachineNameForSubnetPort(subnetPort)
	if vmName == "" || err != nil {
		return nil, "", err
	}
	vm := &vmv1alpha1.VirtualMachine{}
	namespacedName := types.NamespacedName{
		Name:      vmName,
		Namespace: subnetPort.Namespace,
	}
	if err := r.Client.Get(ctx, namespacedName, vm); err != nil {
		return nil, "", err
	}
	log.Info("Got VirtualMachine for SubnetPort", "subnetPort.UID", subnetPort.UID, "vmName", vmName, "nicName", nicName, "labels", vm.ObjectMeta.Labels)
	return vm, nicName, nil
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

func (r *SubnetPortReconciler) getAddressBindingList(ctx context.Context, namespace *string) (*v1alpha1.AddressBindingList, error) {
	abList := &v1alpha1.AddressBindingList{}
	var err error
	listOptions := []client.ListOption{}
	if namespace != nil && *namespace != "" {
		listOptions = append(listOptions, client.InNamespace(*namespace))
	}
	if err = r.Client.List(ctx, abList, listOptions...); err != nil {
		return nil, err
	}
	return abList, nil
}

func (r *SubnetPortReconciler) getAddressBindingCRUIDSet(ctx context.Context) (sets.Set[string], error) {
	addressBindingCRUIDSet := sets.New[string]()
	abList, err := r.getAddressBindingList(ctx, nil)
	if err != nil {
		return addressBindingCRUIDSet, err
	}
	for _, ab := range abList.Items {
		addressBindingCRUIDSet.Insert(string(ab.UID))
	}
	return addressBindingCRUIDSet, nil
}

func (r *SubnetPortReconciler) getSubnetPortCRUIDSet(ctx context.Context) (sets.Set[string], error) {
	subnetPortCRUIDSet := sets.New[string]()
	subnetPortList := &v1alpha1.SubnetPortList{}
	if err := r.Client.List(ctx, subnetPortList); err != nil {
		return nil, err
	}
	for _, sp := range subnetPortList.Items {
		subnetPortCRUIDSet.Insert(string(sp.UID))
	}
	return subnetPortCRUIDSet, nil
}

func (r *SubnetPortReconciler) collectAddressBindingGarbage(ctx context.Context, namespace, ipAddress *string) {
	abList, err := r.getAddressBindingList(ctx, namespace)
	if err != nil {
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
	nsxSubnetPort, err := subnetPortService.SubnetPortStore.GetVpcSubnetPortByUID(subnetPort.GetUID())
	if err != nil {
		log.Error(err, "Failed to get VpcSubnetPort from cache using SubnetPort CR", "CR UID", subnetPort.UID)
		e = err
	} else if nsxSubnetPort == nil {
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
