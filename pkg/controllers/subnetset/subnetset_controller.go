package subnetset

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetbinding"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	log                     = logger.Log
	ResultNormal            = common.ResultNormal
	ResultRequeue           = common.ResultRequeue
	ResultRequeueAfter5mins = common.ResultRequeueAfter5mins
	MetricResTypeSubnetSet  = common.MetricResTypeSubnetSet
)

// SubnetSetReconciler reconciles a SubnetSet object
type SubnetSetReconciler struct {
	Client            client.Client
	Scheme            *apimachineryruntime.Scheme
	SubnetService     *subnet.SubnetService
	SubnetPortService servicecommon.SubnetPortServiceProvider
	VPCService        servicecommon.VPCServiceProvider
	BindingService    *subnetbinding.BindingService
	Recorder          record.EventRecorder
	StatusUpdater     common.StatusUpdater
	restoreMode       bool
}

func (r *SubnetSetReconciler) UpdateSubnetSetForSubnetNames(ctx context.Context, subnetsetCR *v1alpha1.SubnetSet) error {
	// Deduplicate the SubnetNames
	dedupSubnetNames := []string{}
	existedSubnetNames := sets.New[string]()
	specChanged := false
	for _, subnetName := range *subnetsetCR.Spec.SubnetNames {
		if !existedSubnetNames.Has(subnetName) {
			existedSubnetNames.Insert(subnetName)
			dedupSubnetNames = append(dedupSubnetNames, subnetName)
		} else {
			specChanged = true
		}
	}
	if specChanged {
		subnetsetCR.Spec.SubnetNames = &dedupSubnetNames
		err := r.Client.Update(ctx, subnetsetCR)
		if err != nil {
			r.StatusUpdater.UpdateFail(ctx, subnetsetCR, err, "Failed to update SubnetSet", setSubnetSetReadyStatusFalse)
			return err
		}
	}
	// Update Subnet Info on SubnetSet
	var subnetInfoList []v1alpha1.SubnetInfo
	for _, subnetName := range *subnetsetCR.Spec.SubnetNames {
		subnet := &v1alpha1.Subnet{}
		if err := r.Client.Get(ctx, types.NamespacedName{Namespace: subnetsetCR.Namespace, Name: subnetName}, subnet); err != nil {
			return err
		}
		if !common.IsObjectReady(subnet.Status.Conditions) {
			return fmt.Errorf("Subnet %s/%s is not realized", subnet.Namespace, subnet.Name)
		}
		if len(subnet.Status.GatewayAddresses) == 0 {
			return fmt.Errorf("Subnet %s/%s gateway is not updated", subnet.Namespace, subnet.Name)
		}
		subnetInfo := v1alpha1.SubnetInfo{}
		subnetInfo.NetworkAddresses = subnet.Status.NetworkAddresses
		subnetInfo.GatewayAddresses = subnet.Status.GatewayAddresses
		subnetInfo.DHCPServerAddresses = subnet.Status.DHCPServerAddresses
		subnetInfoList = append(subnetInfoList, subnetInfo)
	}
	subnetsetCR.Status.Subnets = subnetInfoList
	if err := r.Client.Status().Update(ctx, subnetsetCR); err != nil {
		log.Error(err, "Failed to update SubnetSet status for SubnetNames")
		return err
	}
	log.Info("Successfully updated SubnetSet with Subnet Status", "Subnets", subnetInfoList)
	return nil
}

func (r *SubnetSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	startTime := time.Now()
	defer func() {
		log.Info("Finished reconciling SubnetSet", "SubnetSet", req.NamespacedName, "duration(ms)", time.Since(startTime).Milliseconds())
	}()

	subnetsetCR := &v1alpha1.SubnetSet{}
	r.StatusUpdater.IncreaseSyncTotal()

	if err := r.Client.Get(ctx, req.NamespacedName, subnetsetCR); err != nil {
		if apierrors.IsNotFound(err) {
			if err := r.deleteSubnetBySubnetSetName(ctx, req.Name, req.Namespace); err != nil {
				r.StatusUpdater.DeleteFail(req.NamespacedName, nil, err)
				return ResultRequeue, err
			}
			r.StatusUpdater.DeleteSuccess(req.NamespacedName, nil)
			return ResultNormal, nil
		}
		log.Error(err, "Unable to fetch SubnetSet CR", "SubnetSet", req.NamespacedName)
		return ResultRequeue, err
	}

	if subnetsetCR.Spec.SubnetNames != nil {
		// For SubnetSet with pre-created Subnet, update the Status from Subnet CR
		if err := r.UpdateSubnetSetForSubnetNames(ctx, subnetsetCR); err != nil {
			r.StatusUpdater.UpdateFail(ctx, subnetsetCR, err, "Failed to update SubnetSet", setSubnetSetReadyStatusFalse)
			return ResultNormal, err
		}
		r.StatusUpdater.UpdateSuccess(ctx, subnetsetCR, setSubnetSetReadyStatusTrue)
		return ResultNormal, nil
	}

	bindingCRs := r.getSubnetBindingCRsBySubnetSet(ctx, subnetsetCR)
	if len(bindingCRs) > 0 {
		if !controllerutil.ContainsFinalizer(subnetsetCR, servicecommon.SubnetSetFinalizerName) {
			controllerutil.AddFinalizer(subnetsetCR, servicecommon.SubnetSetFinalizerName)
			if err := r.Client.Update(ctx, subnetsetCR); err != nil {
				log.Error(err, "Failed to add the finalizer", "SubnetSet", req.NamespacedName)
				msgFailAddFinalizer := fmt.Sprintf("Failed to add the finalizer on SubnetSet for the dependency by SubnetConnectionBindingMap %s", bindingCRs[0].Name)
				r.StatusUpdater.UpdateFail(ctx, subnetsetCR, err, "Unable to add the finalizer on SubnetSet used by SubnetConnectionBindingMap",
					setSubnetSetReadyStatusFalse, msgFailAddFinalizer)
				return ResultRequeue, err
			}
		}
	} else {
		if controllerutil.ContainsFinalizer(subnetsetCR, servicecommon.SubnetSetFinalizerName) {
			controllerutil.RemoveFinalizer(subnetsetCR, servicecommon.SubnetSetFinalizerName)
			if err := r.Client.Update(ctx, subnetsetCR); err != nil {
				log.Error(err, "Failed to delete the finalizer", "SubnetSet", req.NamespacedName)
				msgFailDelFinalizer := "Failed to remove the finalizer on SubnetSet when there is no reference by SubnetConnectionBindingMaps"
				r.StatusUpdater.UpdateFail(ctx, subnetsetCR, err, "Unable to remove the finalizer from SubnetSet",
					setSubnetSetReadyStatusFalse, fmt.Sprint(msgFailDelFinalizer))
				return ResultRequeue, err
			}
		}
	}

	if !subnetsetCR.ObjectMeta.DeletionTimestamp.IsZero() {
		r.StatusUpdater.IncreaseDeleteTotal()
		bindingsOnNSX := r.getNSXSubnetBindingsBySubnetSet(string(subnetsetCR.UID))
		if len(bindingsOnNSX) > 0 {
			err := fmt.Errorf("failed to delete SubnetSet CR %s", req.String())
			log.Error(err, "The SubnetSet CR is used by SubnetConnectionBindingMaps, retrying", "SubnetConnectionBindingMap", bindingsOnNSX[0].GetName())
			r.StatusUpdater.DeleteFail(req.NamespacedName, nil, err)
			msgDeleteInUse := fmt.Sprintf("SubnetSet is used by SubnetConnectionBindingMap %s and not able to delete", bindingsOnNSX[0].GetName())
			r.setSubnetDeletionFailedStatus(ctx, subnetsetCR, metav1.Now(), msgDeleteInUse, "SubnetSetInUse")
			return ResultRequeue, err
		}

		err := r.deleteSubnetForSubnetSet(*subnetsetCR, false, false)
		if err != nil {
			r.StatusUpdater.DeleteFail(req.NamespacedName, nil, err)
			return ResultRequeue, err
		}
		r.StatusUpdater.DeleteSuccess(req.NamespacedName, nil)
		return ResultNormal, nil
	}
	r.StatusUpdater.IncreaseUpdateTotal()

	specChanged := false
	var vpcNetworkConfig *v1alpha1.VPCNetworkConfiguration
	var err error
	if subnetsetCR.Spec.AccessMode == "" {
		subnetsetCR.Spec.AccessMode, vpcNetworkConfig, err = common.GetDefaultAccessMode(r.VPCService, subnetsetCR.Namespace)
		if err != nil {
			return ResultNormal, err
		}
		if vpcNetworkConfig == nil {
			r.StatusUpdater.UpdateFail(ctx, subnetsetCR, err, "Failed to find VPCNetworkConfig", setSubnetSetReadyStatusFalse)
			return ResultNormal, fmt.Errorf("vpcNeworkConfig is nil")
		}
		specChanged = true
	}
	if subnetsetCR.Spec.IPv4SubnetSize == 0 {
		if vpcNetworkConfig == nil {
			vpcNetworkConfig, err = common.GetVpcNetworkConfig(r.VPCService, subnetsetCR.Namespace)
			if err != nil {
				return ResultNormal, err
			}
			if vpcNetworkConfig == nil {
				r.StatusUpdater.UpdateFail(ctx, subnetsetCR, err, "Failed to find VPCNetworkConfig", setSubnetSetReadyStatusFalse)
				return ResultNormal, err
			}
		}
		subnetsetCR.Spec.IPv4SubnetSize = vpcNetworkConfig.Spec.DefaultSubnetSize
		specChanged = true
	}
	isSystemNs, err := util.IsVPCSystemNamespace(r.Client, subnetsetCR.Namespace, nil)
	if err != nil {
		r.StatusUpdater.UpdateFail(ctx, subnetsetCR, err, "Failed to update SubnetSet", setSubnetSetReadyStatusFalse)
		return ResultNormal, err
	}
	metadataChanged := updateLabels(subnetsetCR, isSystemNs)
	if specChanged || metadataChanged {
		err := r.Client.Update(ctx, subnetsetCR)
		if err != nil {
			r.StatusUpdater.UpdateFail(ctx, subnetsetCR, err, "Failed to update SubnetSet", setSubnetSetReadyStatusFalse)
			return ResultRequeue, err
		}
	}

	nsxSubnets := r.SubnetService.SubnetStore.GetByIndex(servicecommon.TagScopeSubnetSetCRUID, string(subnetsetCR.UID))
	if len(nsxSubnets) > 0 || r.restoreMode {
		// update SubnetSet tags if labels of namespace changed
		tags := r.SubnetService.GenerateSubnetNSTags(subnetsetCR)
		if tags == nil {
			log.Error(nil, "Failed to generate SubnetSet tags", "SubnetSet", req.NamespacedName)
			return ResultRequeue, errors.New("failed to generate SubnetSet tags")
		}
		// tags cannot exceed maximum size 26
		if len(tags) > servicecommon.MaxTagsCount {
			err := fmt.Errorf("tags cannot exceed maximum size 26, tags length: %d", len(tags))
			r.StatusUpdater.UpdateFail(ctx, subnetsetCR, err, "Exceed tags limit", setSubnetSetReadyStatusFalse)
			return ResultNormal, nil
		}
		if r.restoreMode {
			log.Debug("Restore SubnetSet", "SubnetSet", req.NamespacedName)
			vpcInfoList := r.VPCService.ListVPCInfo(req.Namespace)
			if len(vpcInfoList) == 0 {
				return ResultNormal, fmt.Errorf("failed to find VPC for Namespace %s", req.Namespace)
			}
			if err := r.SubnetService.RestoreSubnetSet(subnetsetCR, vpcInfoList[0], tags); err != nil {
				r.StatusUpdater.UpdateFail(ctx, subnetsetCR, err, "Failed to restore SubnetSet", setSubnetSetReadyStatusFalse)
				return ResultNormal, err
			}
			r.StatusUpdater.UpdateSuccess(ctx, subnetsetCR, setSubnetSetReadyStatusTrue)
		}
		if err := r.SubnetService.UpdateSubnetSet(subnetsetCR.Namespace, nsxSubnets, tags, string(subnetsetCR.Spec.SubnetDHCPConfig.Mode)); err != nil {
			r.StatusUpdater.UpdateFail(ctx, subnetsetCR, err, "Failed to update SubnetSet", setSubnetSetReadyStatusFalse)
			return ResultNormal, err
		}
	}
	r.StatusUpdater.UpdateSuccess(ctx, subnetsetCR, setSubnetSetReadyStatusTrue)

	return ResultNormal, nil
}

func updateLabels(subnetSet *v1alpha1.SubnetSet, systemNs bool) bool {
	metadataChanged := false
	if subnetSet.Labels == nil {
		return metadataChanged
	}
	if value, exists := subnetSet.Labels[servicecommon.LabelDefaultSubnetSet]; exists {
		log.Debug("Adding default-network label", "SubnetSet", subnetSet.Name)
		// reserve the old label for other controllers compatibility
		// TODO: using systemNs to determine if the old label will be kept after other controllers compatibility are resolved
		switch value {
		case servicecommon.LabelDefaultPodSubnetSet:
			value = servicecommon.DefaultPodNetwork
		case servicecommon.LabelDefaultVMSubnetSet:
			value = servicecommon.DefaultVMNetwork
		default:
			log.Error(errors.New("Unknown value"), "Update labels", "Label values", value)
		}
		// No need to update the label if it already existed
		if existingValue, exists := subnetSet.Labels[servicecommon.LabelDefaultNetwork]; exists {
			if existingValue == value {
				return metadataChanged
			}
		}
		subnetSet.Labels[servicecommon.LabelDefaultNetwork] = value
		metadataChanged = true
	}
	return metadataChanged
}

func setSubnetSetReadyStatusTrue(client client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, _ ...interface{}) {
	subnetSet := obj.(*v1alpha1.SubnetSet)
	newConditions := []v1alpha1.Condition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionTrue,
			Message:            "SubnetSet CR has been successfully created/updated",
			Reason:             "SubnetSetReady",
			LastTransitionTime: transitionTime,
		},
	}
	updateSubnetSetStatusConditions(client, ctx, subnetSet, newConditions)
}

func setSubnetSetReadyStatusFalse(client client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, err error, args ...interface{}) {
	subnetSet := obj.(*v1alpha1.SubnetSet)
	newConditions := []v1alpha1.Condition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionFalse,
			Message:            "SubnetSet CR could not be created/updated",
			Reason:             "SubnetSetNotReady",
			LastTransitionTime: transitionTime,
		},
	}

	if len(args) > 0 {
		newConditions[0].Message = args[0].(string)
	} else {
		if err != nil {
			newConditions[0].Message = fmt.Sprintf("Error occurred while processing the SubnetSet CR. Please check the config and try again. Error: %v", err)
		}
	}
	updateSubnetSetStatusConditions(client, ctx, subnetSet, newConditions)
}

func (r *SubnetSetReconciler) setSubnetDeletionFailedStatus(ctx context.Context, subnetSet *v1alpha1.SubnetSet, transitionTime metav1.Time, msg string, reason string) {
	newConditions := []v1alpha1.Condition{
		{
			Type:               v1alpha1.DeleteFailure,
			Status:             v1.ConditionTrue,
			Message:            "SubnetSet could not be deleted",
			Reason:             "NSXOperationFailed",
			LastTransitionTime: transitionTime,
		},
	}
	if msg != "" {
		newConditions[0].Message = msg
	}
	if reason != "" {
		newConditions[0].Reason = reason
	}
	updateSubnetSetStatusConditions(r.Client, ctx, subnetSet, newConditions)
}

func updateSubnetSetStatusConditions(client client.Client, ctx context.Context, subnetSet *v1alpha1.SubnetSet, newConditions []v1alpha1.Condition) {
	conditionsUpdated := false
	for i := range newConditions {
		if mergeSubnetSetStatusCondition(subnetSet, &newConditions[i]) {
			conditionsUpdated = true
		}
	}
	if conditionsUpdated {
		if err := client.Status().Update(ctx, subnetSet); err != nil {
			log.Error(err, "Failed to update status", "Name", subnetSet.Name, "Namespace", subnetSet.Namespace)
		} else {
			log.Info("Updated SubnetSet", "Name", subnetSet.Name, "Namespace", subnetSet.Namespace, "New Conditions", newConditions)
		}
	}
}

func mergeSubnetSetStatusCondition(subnetSet *v1alpha1.SubnetSet, newCondition *v1alpha1.Condition) bool {
	matchedCondition := getExistingConditionOfType(newCondition.Type, subnetSet.Status.Conditions)

	if reflect.DeepEqual(matchedCondition, newCondition) {
		log.Trace("Conditions already match", "New Condition", newCondition, "Existing Condition", matchedCondition)
		return false
	}

	if matchedCondition != nil {
		matchedCondition.Reason = newCondition.Reason
		matchedCondition.Message = newCondition.Message
		matchedCondition.Status = newCondition.Status
	} else {
		subnetSet.Status.Conditions = append(subnetSet.Status.Conditions, *newCondition)
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

func (r *SubnetSetReconciler) setupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.SubnetSet{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: common.NumReconcile(),
		}).
		Watches(
			&v1.Namespace{},
			&EnqueueRequestForNamespace{Client: mgr.GetClient()},
			builder.WithPredicates(PredicateFuncsNs),
		).
		Watches(
			&v1alpha1.SubnetConnectionBindingMap{},
			&common.EnqueueRequestForDependency{
				Client:          r.Client,
				ResourceType:    "SubnetConnectionBindingMap",
				RequeueByCreate: requeueSubnetSetBySubnetBindingCreate,
				RequeueByUpdate: requeueSubnetSetBySubnetBindingUpdate,
				RequeueByDelete: requeueSubnetSetBySubnetBindingDelete,
			},
			builder.WithPredicates(common.PredicateFuncsWithSubnetBindings),
		).
		Complete(r)
}

func (r *SubnetSetReconciler) EnableRestoreMode() {
	r.restoreMode = true
}

// CollectGarbage collect Subnet which there is no port attached on it.
// it implements the interface GarbageCollector method.
func (r *SubnetSetReconciler) CollectGarbage(ctx context.Context) error {
	startTime := time.Now()
	defer func() {
		log.Info("SubnetSet garbage collection completed", "duration(ms)", time.Since(startTime).Milliseconds())
	}()

	crdSubnetSetList, err := listSubnetSet(r.Client, ctx)
	if err != nil {
		log.Error(err, "Failed to list SubnetSet CRs")
		return err
	}

	var errList []error
	crdSubnetSetIDsSet := sets.New[string]()
	for _, subnetSet := range crdSubnetSetList.Items {
		// No need to gc SubnetSet with pre-created Subnet
		if subnetSet.Spec.SubnetNames != nil {
			continue
		}
		crdSubnetSetIDsSet.Insert(string(subnetSet.UID))
		if err := r.deleteSubnetForSubnetSet(subnetSet, true, true); err != nil {
			errList = append(errList, err)
			r.StatusUpdater.IncreaseDeleteFailTotal()
		} else {
			r.StatusUpdater.IncreaseDeleteSuccessTotal()
		}
	}

	subnetSetIDs := r.SubnetService.ListSubnetSetIDsFromNSXSubnets()
	subnetSetIDsToDelete := subnetSetIDs.Difference(crdSubnetSetIDsSet)
	for subnetSetID := range subnetSetIDsToDelete {
		nsxSubnets := r.SubnetService.ListSubnetCreatedBySubnetSet(subnetSetID)
		log.Info("SubnetSet garbage collection, cleaning stale Subnets for SubnetSet", "Count", len(nsxSubnets))
		if _, err := r.deleteSubnets(nsxSubnets, true); err != nil {
			errList = append(errList, err)
			log.Error(err, "SubnetSet garbage collection, failed to delete NSX subnet", "SubnetSetUID", subnetSetID)
			r.StatusUpdater.IncreaseDeleteFailTotal()
		} else {
			log.Info("SubnetSet garbage collection, successfully deleted NSX subnet", "SubnetSetUID", subnetSetID)
			r.StatusUpdater.IncreaseDeleteSuccessTotal()
		}
	}

	// clean the SubnetSet lock used to create Subnet
	common.SubnetSetLocks.Range(func(key, value interface{}) bool {
		uuid := key.(types.UID)
		if !crdSubnetSetIDsSet.Has(string(uuid)) {
			common.SubnetSetLocks.Delete(key)
		}
		return true
	})
	if len(errList) > 0 {
		return fmt.Errorf("errors found in SubnetSet garbage collection: %s", errList)
	}
	return nil
}

func (r *SubnetSetReconciler) deleteSubnetBySubnetSetName(ctx context.Context, subnetSetName, ns string) error {
	nsxSubnets := r.SubnetService.ListSubnetBySubnetSetName(ns, subnetSetName)
	// We also actively delete the SubnetConnectionBindingMaps associated with the empty NSX Subnet that has no SubnetPort.
	hasStaleSubnetPort, err := r.deleteSubnets(nsxSubnets, true)
	if err != nil || hasStaleSubnetPort {
		return fmt.Errorf("failed to delete stale Subnets, error: %v, hasStaleSubnetPort: %t", err, hasStaleSubnetPort)
	}
	return nil
}

func (r *SubnetSetReconciler) deleteSubnetForSubnetSet(subnetSet v1alpha1.SubnetSet, updateStatus, ignoreStaleSubnetPort bool) error {
	subnetSetLock := common.WLockSubnetSet(subnetSet.GetUID())
	nsxSubnets := r.SubnetService.SubnetStore.GetByIndex(servicecommon.TagScopeSubnetSetCRUID, string(subnetSet.GetUID()))

	// For restore mode, we use SubnetSet CR status as source of the truth to sync the NSX Subnet
	// For non-restore mode, we scale down the SubnetSet by deleting NSX Subnet without ports
	if r.restoreMode {
		subnetCIDRSet := sets.New[string]()
		for _, subnet := range subnetSet.Status.Subnets {
			subnetCIDRSet.Insert(strings.Join(subnet.NetworkAddresses, ","))
		}
		var revisedNSXSubnet []*model.VpcSubnet
		for _, nsxSubnet := range nsxSubnets {
			if !subnetCIDRSet.Has(strings.Join(nsxSubnet.IpAddresses, ",")) {
				revisedNSXSubnet = append(revisedNSXSubnet, nsxSubnet)
			}
		}
		nsxSubnets = revisedNSXSubnet
		// NSX SubnetPorts under the NSX Subnet not in CR status should be deleted before SubnetSet GC
		ignoreStaleSubnetPort = false
	}
	// If ignoreStaleSubnetPort is true, we will actively delete the existing SubnetConnectionBindingMaps connected to the
	// corresponding NSX Subnet. This happens in the GC case to scale-in the NSX Subnet if no SubnetPort exists.
	// For SubnetSet CR deletion event, we don't delete the existing SubnetConnectionBindingMaps but let the
	// SubnetConnectionBindingMap controller do it after the binding CR is removed.
	hasStaleSubnetPort, deleteErr := r.deleteSubnets(nsxSubnets, ignoreStaleSubnetPort)
	common.WUnlockSubnetSet(subnetSet.GetUID(), subnetSetLock)
	// Skip SubnetSet status update for restore case, as we need the stale status to restore the NSX Subnet
	if updateStatus && !r.restoreMode {
		if err := r.SubnetService.UpdateSubnetSetStatus(&subnetSet); err != nil {
			return err
		}
	}
	if deleteErr != nil {
		return deleteErr
	}
	if hasStaleSubnetPort && !ignoreStaleSubnetPort {
		return fmt.Errorf("stale Subnet ports found while deleting Subnet for SubnetSet %s/%s", subnetSet.Name, subnetSet.Namespace)
	}
	return nil
}

// deleteSubnets deletes all the specified NSX Subnets.
// If any of the Subnets have stale SubnetPorts, they are skipped. The final result returns true.
// If there is an error while deleting any NSX Subnet, it is skipped, and the final result returns an error.
func (r *SubnetSetReconciler) deleteSubnets(nsxSubnets []*model.VpcSubnet, deleteBindingMaps bool) (hasStalePort bool, err error) {
	if len(nsxSubnets) == 0 {
		return
	}
	var deleteErrs []error
	for _, nsxSubnet := range nsxSubnets {

		if !r.SubnetPortService.IsEmptySubnet(*nsxSubnet.Id, *nsxSubnet.Path) {
			hasStalePort = true
			log.Info("Skipped deleting NSX Subnet due to stale ports", "nsxSubnet", *nsxSubnet.Id)
			continue
		}

		if deleteBindingMaps {
			if err = r.BindingService.DeleteSubnetConnectionBindingMapsByParentSubnet(nsxSubnet); err != nil {
				deleteErr := fmt.Errorf("failed to delete NSX SubnetConnectionBindingMaps connected to NSX Subnet/%s: %+v", *nsxSubnet.Id, err)
				deleteErrs = append(deleteErrs, deleteErr)
				log.Error(deleteErr, "Skipping to next Subnet")
				continue
			}
		}

		if err := r.SubnetService.DeleteSubnet(*nsxSubnet); err != nil {
			deleteErr := fmt.Errorf("failed to delete NSX Subnet/%s: %+v", *nsxSubnet.Id, err)
			deleteErrs = append(deleteErrs, deleteErr)
			log.Error(deleteErr, "Skipping to next Subnet")
		} else {
			log.Debug("Delete Subnet successfully", "Subnet", *nsxSubnet.Id)
			r.SubnetPortService.DeletePortCount(*nsxSubnet.Path)
		}

	}
	if len(deleteErrs) > 0 {
		err = fmt.Errorf("multiple errors occurred while deleting Subnets: %v", deleteErrs)
		return
	}
	log.Info("Successfully deleted all specified NSX Subnets", "subnetCount", len(nsxSubnets))
	return
}

func (r *SubnetSetReconciler) RestoreReconcile() error {
	restoreList, err := r.getRestoreList()
	if err != nil {
		err = fmt.Errorf("failed to get SubnetSet restore list: %w", err)
		return err
	}
	var errorList []error
	for _, key := range restoreList {
		result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
		if result.Requeue || err != nil {
			errorList = append(errorList, fmt.Errorf("failed to restore SubnetSet %s, error: %w", key, err))
		}
	}
	if len(errorList) > 0 {
		return fmt.Errorf("errors found in SubnetSet restore: %v", errorList)
	}
	return nil
}

func (r *SubnetSetReconciler) getRestoreList() ([]types.NamespacedName, error) {
	restoreList := []types.NamespacedName{}
	subnetSetList := &v1alpha1.SubnetSetList{}
	if err := r.Client.List(context.TODO(), subnetSetList); err != nil {
		return restoreList, err
	}
	for _, subnetSet := range subnetSetList.Items {
		// Restore a SubnetSet if it has Subnets in status
		// No need to restore pre-created SubnetSet
		if subnetSet.Spec.SubnetNames == nil && len(subnetSet.Status.Subnets) > 0 {
			restoreList = append(restoreList, types.NamespacedName{Namespace: subnetSet.Namespace, Name: subnetSet.Name})
		}
	}
	return restoreList, nil
}

func (r *SubnetSetReconciler) StartController(mgr ctrl.Manager, hookServer webhook.Server) error {
	if err := r.Start(mgr, hookServer); err != nil {
		log.Error(err, "Failed to create controller", "controller", "SubnetSet")
		return err
	}
	go common.GenericGarbageCollector(make(chan bool), servicecommon.SubnetGCInterval, r.CollectGarbage)
	return nil
}

func NewSubnetSetReconciler(mgr ctrl.Manager, subnetService *subnet.SubnetService,
	subnetPortService servicecommon.SubnetPortServiceProvider, vpcService servicecommon.VPCServiceProvider,
	bindingService *subnetbinding.BindingService) *SubnetSetReconciler {
	subnetsetReconciler := &SubnetSetReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		SubnetService:     subnetService,
		SubnetPortService: subnetPortService,
		BindingService:    bindingService,

		VPCService: vpcService,
		Recorder:   mgr.GetEventRecorderFor("subnetset-controller"),
	}
	subnetsetReconciler.StatusUpdater = common.NewStatusUpdater(subnetsetReconciler.Client, subnetsetReconciler.SubnetService.NSXConfig, subnetsetReconciler.Recorder, MetricResTypeSubnetSet, "Subnet", "SubnetSet")
	return subnetsetReconciler
}

// Start setup manager
func (r *SubnetSetReconciler) Start(mgr ctrl.Manager, hookServer webhook.Server) error {
	err := r.setupWithManager(mgr)
	if err != nil {
		return err
	}
	if hookServer != nil {
		hookServer.Register("/validate-crd-nsx-vmware-com-v1alpha1-subnetset",
			&webhook.Admission{
				Handler: &SubnetSetValidator{
					Client:            mgr.GetClient(),
					decoder:           admission.NewDecoder(mgr.GetScheme()),
					nsxClient:         r.SubnetService.NSXClient,
					vpcService:        r.VPCService,
					subnetService:     r.SubnetService,
					subnetPortService: r.SubnetPortService,
				},
			})
	}
	return nil
}
