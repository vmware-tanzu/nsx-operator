package subnetset

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	log                     = &logger.Log
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
	Recorder          record.EventRecorder
}

func (r *SubnetSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	startTime := time.Now()
	defer func() {
		log.Info("Finished reconciling SubnetSet", "SubnetSet", req.NamespacedName, "duration(ms)", time.Since(startTime).Milliseconds())
	}()

	subnetsetCR := &v1alpha1.SubnetSet{}
	metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerSyncTotal, MetricResTypeSubnetSet)

	if err := r.Client.Get(ctx, req.NamespacedName, subnetsetCR); err != nil {
		if apierrors.IsNotFound(err) {
			if err := r.deleteSubnetBySubnetSetName(ctx, req.Name, req.Namespace); err != nil {
				log.Error(err, "Failed to delete NSX Subnet", "SubnetSet", req.NamespacedName)
				return ResultRequeue, err
			}
			return ResultNormal, nil
		}
		log.Error(err, "Unable to fetch SubnetSet CR", "SubnetSet", req.NamespacedName)
		return ResultRequeue, err
	}
	if !subnetsetCR.ObjectMeta.DeletionTimestamp.IsZero() {
		metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerDeleteTotal, MetricResTypeSubnetSet)
		err := r.deleteSubnetForSubnetSet(*subnetsetCR, false, false)
		if err != nil {
			log.Error(err, "Failed to delete NSX Subnet, retrying", "SubnetSet", req.NamespacedName)
			deleteFail(r, ctx, subnetsetCR, err.Error())
			return ResultRequeue, err
		}
		if err := r.Client.Delete(ctx, subnetsetCR); err != nil {
			log.Error(err, "Failed to delete SubnetSet CR, retrying", "SubnetSet", req.NamespacedName)
			deleteFail(r, ctx, subnetsetCR, err.Error())
			return ResultRequeue, err
		}
		deleteSuccess(r, ctx, subnetsetCR)
		return ResultNormal, nil
	}

	metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerUpdateTotal, MetricResTypeSubnetSet)

	specChanged := false
	if subnetsetCR.Spec.AccessMode == "" {
		subnetsetCR.Spec.AccessMode = v1alpha1.AccessMode(v1alpha1.AccessModePrivate)
		specChanged = true
	}
	if subnetsetCR.Spec.IPv4SubnetSize == 0 {
		vpcNetworkConfig := r.VPCService.GetVPCNetworkConfigByNamespace(subnetsetCR.Namespace)
		if vpcNetworkConfig == nil {
			err := fmt.Errorf("failed to find VPCNetworkConfig for Namespace %s", subnetsetCR.Namespace)
			log.Error(err, "Operate failed, would retry exponentially", "SubnetSet", req.NamespacedName)
			updateFail(r, ctx, subnetsetCR, err.Error())
			return ResultRequeue, err
		}
		subnetsetCR.Spec.IPv4SubnetSize = vpcNetworkConfig.DefaultSubnetSize
		specChanged = true
	}
	if !util.IsPowerOfTwo(subnetsetCR.Spec.IPv4SubnetSize) {
		errorMsg := fmt.Sprintf("ipv4SubnetSize has invalid size %d, which needs to be >= 16 and power of 2", subnetsetCR.Spec.IPv4SubnetSize)
		log.Error(nil, errorMsg, "SubnetSet", req.NamespacedName)
		updateFail(r, ctx, subnetsetCR, errorMsg)
		return ResultNormal, nil
	}

	if specChanged {
		err := r.Client.Update(ctx, subnetsetCR)
		if err != nil {
			log.Error(err, "Update SubnetSet failed", "SubnetSet", req.NamespacedName)
			updateFail(r, ctx, subnetsetCR, err.Error())
			return ResultRequeue, err
		}
	}

	// update SubnetSet tags if labels of namespace changed
	nsxSubnets := r.SubnetService.SubnetStore.GetByIndex(servicecommon.TagScopeSubnetSetCRUID, string(subnetsetCR.UID))
	if len(nsxSubnets) > 0 {
		tags := r.SubnetService.GenerateSubnetNSTags(subnetsetCR)
		if tags == nil {
			log.Error(nil, "Failed to generate SubnetSet tags", "SubnetSet", req.NamespacedName)
			return ResultRequeue, errors.New("failed to generate SubnetSet tags")
		}
		// tags cannot exceed maximum size 26
		if len(tags) > servicecommon.TagsCountMax {
			errorMsg := fmt.Sprintf("tags cannot exceed maximum size 26, tags length: %d", len(tags))
			log.Error(nil, "Exceed tags limit, would not retry", "SubnetSet", req.NamespacedName)
			updateFail(r, ctx, subnetsetCR, errorMsg)
			return ResultNormal, nil
		}
		if err := r.SubnetService.UpdateSubnetSetTags(subnetsetCR.Namespace, nsxSubnets, tags); err != nil {
			log.Error(err, "Failed to update SubnetSet tags", "SubnetSet", req.NamespacedName)
		}
	}
	updateSuccess(r, ctx, subnetsetCR)

	return ctrl.Result{}, nil
}

func updateFail(r *SubnetSetReconciler, c context.Context, o *v1alpha1.SubnetSet, m string) {
	r.setSubnetSetReadyStatusFalse(c, o, metav1.Now(), m)
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailUpdate, m)
	metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerUpdateFailTotal, MetricResTypeSubnetSet)
}

func deleteFail(r *SubnetSetReconciler, c context.Context, o *v1alpha1.SubnetSet, m string) {
	r.setSubnetSetReadyStatusFalse(c, o, metav1.Now(), m)
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailDelete, m)
	metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResTypeSubnetSet)
}

func updateSuccess(r *SubnetSetReconciler, c context.Context, o *v1alpha1.SubnetSet) {
	r.setSubnetSetReadyStatusTrue(c, o, metav1.Now())
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulUpdate, "SubnetSet CR has been successfully updated")
	metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerUpdateSuccessTotal, MetricResTypeSubnetSet)
}

func deleteSuccess(r *SubnetSetReconciler, _ context.Context, o *v1alpha1.SubnetSet) {
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulDelete, "SubnetSet CR has been successfully deleted")
	metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResTypeSubnetSet)
}

func (r *SubnetSetReconciler) setSubnetSetReadyStatusTrue(ctx context.Context, subnetSet *v1alpha1.SubnetSet, transitionTime metav1.Time) {
	newConditions := []v1alpha1.Condition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionTrue,
			Message:            "SubnetSet CR has been successfully created/updated",
			Reason:             "SubnetSetReady",
			LastTransitionTime: transitionTime,
		},
	}
	r.updateSubnetSetStatusConditions(ctx, subnetSet, newConditions)
}

func (r *SubnetSetReconciler) setSubnetSetReadyStatusFalse(ctx context.Context, subnetSet *v1alpha1.SubnetSet, transitionTime metav1.Time, m string) {
	newConditions := []v1alpha1.Condition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionFalse,
			Message:            "SubnetSet CR could not be created/updated",
			Reason:             "SubnetSetNotReady",
			LastTransitionTime: transitionTime,
		},
	}
	if m != "" {
		newConditions[0].Message = m
	}
	r.updateSubnetSetStatusConditions(ctx, subnetSet, newConditions)
}

func (r *SubnetSetReconciler) updateSubnetSetStatusConditions(ctx context.Context, subnetSet *v1alpha1.SubnetSet, newConditions []v1alpha1.Condition) {
	conditionsUpdated := false
	for i := range newConditions {
		if r.mergeSubnetSetStatusCondition(ctx, subnetSet, &newConditions[i]) {
			conditionsUpdated = true
		}
	}
	if conditionsUpdated {
		if err := r.Client.Status().Update(ctx, subnetSet); err != nil {
			log.Error(err, "Failed to update status", "Name", subnetSet.Name, "Namespace", subnetSet.Namespace)
		} else {
			log.Info("Updated SubnetSet", "Name", subnetSet.Name, "Namespace", subnetSet.Namespace, "New Conditions", newConditions)
		}
	}
}

func (r *SubnetSetReconciler) mergeSubnetSetStatusCondition(ctx context.Context, subnetSet *v1alpha1.SubnetSet, newCondition *v1alpha1.Condition) bool {
	matchedCondition := getExistingConditionOfType(newCondition.Type, subnetSet.Status.Conditions)

	if reflect.DeepEqual(matchedCondition, newCondition) {
		log.V(2).Info("Conditions already match", "New Condition", newCondition, "Existing Condition", matchedCondition)
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
		Complete(r)
}

// CollectGarbage collect Subnet which there is no port attached on it.
// it implements the interface GarbageCollector method.
func (r *SubnetSetReconciler) CollectGarbage(ctx context.Context) {
	startTime := time.Now()
	defer func() {
		log.Info("SubnetSet garbage collection completed", "duration(ms)", time.Since(startTime).Milliseconds())
	}()

	crdSubnetSetList, err := listSubnetSet(r.Client, ctx)
	if err != nil {
		log.Error(err, "Failed to list SubnetSet CRs")
		return
	}

	crdSubnetSetIDsSet := sets.New[string]()
	for _, subnetSet := range crdSubnetSetList.Items {
		crdSubnetSetIDsSet.Insert(string(subnetSet.UID))
		if err := r.deleteSubnetForSubnetSet(subnetSet, true, true); err != nil {
			metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResTypeSubnetSet)
		} else {
			metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResTypeSubnetSet)
		}
	}

	subnetSetIDs := r.SubnetService.ListSubnetSetIDsFromNSXSubnets()
	subnetSetIDsToDelete := subnetSetIDs.Difference(crdSubnetSetIDsSet)
	for subnetSetID := range subnetSetIDsToDelete {
		nsxSubnets := r.SubnetService.ListSubnetCreatedBySubnetSet(subnetSetID)
		log.Info("SubnetSet garbage collection, cleaning stale Subnets for SubnetSet", "Count", len(nsxSubnets))
		if _, err := r.deleteSubnets(nsxSubnets); err != nil {
			log.Error(err, "SubnetSet garbage collection, failed to delete NSX subnet", "SubnetSetUID", subnetSetID)
			metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResTypeSubnetSet)
		} else {
			log.Info("SubnetSet garbage collection, successfully deleted NSX subnet", "SubnetSetUID", subnetSetID)
			metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResTypeSubnetSet)
		}
	}
}

func (r *SubnetSetReconciler) deleteSubnetBySubnetSetName(ctx context.Context, subnetSetName, ns string) error {
	nsxSubnets := r.SubnetService.ListSubnetBySubnetSetName(ns, subnetSetName)
	return r.deleteStaleSubnets(ctx, nsxSubnets)
}

func (r *SubnetSetReconciler) deleteSubnetForSubnetSet(subnetSet v1alpha1.SubnetSet, updateStatus, ignoreStaleSubnetPort bool) error {
	nsxSubnets := r.SubnetService.SubnetStore.GetByIndex(servicecommon.TagScopeSubnetSetCRUID, string(subnetSet.GetUID()))
	hasStaleSubnetPort, deleteErr := r.deleteSubnets(nsxSubnets)
	if updateStatus {
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
func (r *SubnetSetReconciler) deleteSubnets(nsxSubnets []*model.VpcSubnet) (hasStalePort bool, err error) {
	var deleteErrs []error
	for _, nsxSubnet := range nsxSubnets {
		r.SubnetService.LockSubnet(nsxSubnet.Path)
		portNums := len(r.SubnetPortService.GetPortsOfSubnet(*nsxSubnet.Id))
		if portNums > 0 {
			r.SubnetService.UnlockSubnet(nsxSubnet.Path)
			hasStalePort = true
			log.Info("Skipped deleting NSX Subnet due to stale ports", "nsxSubnet", *nsxSubnet.Id)
			continue
		}
		if err := r.SubnetService.DeleteSubnet(*nsxSubnet); err != nil {
			r.SubnetService.UnlockSubnet(nsxSubnet.Path)
			deleteErr := fmt.Errorf("failed to delete NSX Subnet/%s: %+v", *nsxSubnet.Id, err)
			deleteErrs = append(deleteErrs, deleteErr)
			log.Error(deleteErr, "Skipping to next Subnet")
			continue
		}
		r.SubnetService.UnlockSubnet(nsxSubnet.Path)
	}
	if len(deleteErrs) > 0 {
		err = fmt.Errorf("multiple errors occurred while deleting Subnets: %v", deleteErrs)
		return
	}
	log.Info("Successfully deleted all specified NSX Subnets", "subnetCount", len(nsxSubnets))
	return
}

func (r *SubnetSetReconciler) deleteStaleSubnets(ctx context.Context, nsxSubnets []*model.VpcSubnet) error {
	crdSubnetSetIDsSet, err := r.SubnetService.ListSubnetSetID(ctx)
	if err != nil {
		log.Error(err, "Failed to list SubnetSet CRs")
		return err
	}
	nsxSubnetsToDelete := make([]*model.VpcSubnet, 0, len(nsxSubnets))
	for _, nsxSubnet := range nsxSubnets {
		uid := nsxutil.FindTag(nsxSubnet.Tags, servicecommon.TagScopeSubnetSetCRUID)
		if crdSubnetSetIDsSet.Has(uid) {
			log.Info("Skipping deletion, SubnetSet CR still exists in K8s", "ID", *nsxSubnet.Id)
			continue
		}
		nsxSubnetsToDelete = append(nsxSubnetsToDelete, nsxSubnet)
	}
	log.Info("Cleaning stale Subnets for SubnetSet", "Count", len(nsxSubnetsToDelete))
	hasStaleSubnetPort, err := r.deleteSubnets(nsxSubnetsToDelete)
	if err != nil || hasStaleSubnetPort {
		return fmt.Errorf("failed to delete stale Subnets, error: %v, hasStaleSubnetPort: %t", err, hasStaleSubnetPort)
	}
	return nil
}

func StartSubnetSetController(mgr ctrl.Manager, subnetService *subnet.SubnetService,
	subnetPortService servicecommon.SubnetPortServiceProvider, vpcService servicecommon.VPCServiceProvider,
	hookServer webhook.Server,
) error {
	subnetsetReconciler := &SubnetSetReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		SubnetService:     subnetService,
		SubnetPortService: subnetPortService,
		VPCService:        vpcService,
		Recorder:          mgr.GetEventRecorderFor("subnetset-controller"),
	}
	if err := subnetsetReconciler.Start(mgr, hookServer); err != nil {
		log.Error(err, "Failed to create controller", "controller", "SubnetSet")
		return err
	}
	go common.GenericGarbageCollector(make(chan bool), servicecommon.GCInterval, subnetsetReconciler.CollectGarbage)
	return nil
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
					Client:  mgr.GetClient(),
					decoder: admission.NewDecoder(mgr.GetScheme()),
				},
			})
	}
	return nil
}
