package subnetset

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
)

var (
	log                     = &logger.Log
	ResultNormal            = common.ResultNormal
	ResultRequeue           = common.ResultRequeue
	ResultRequeueAfter5mins = common.ResultRequeueAfter5mins
	MetricResTypeSubnetSet  = common.MetricResTypeSubnetSet
	once                    sync.Once
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
	// Use once.Do to ensure gc is called only once
	common.GcOnce(r, &once)

	obj := &v1alpha1.SubnetSet{}
	log.Info("reconciling subnetset CR", "subnetset", req.NamespacedName)
	metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerSyncTotal, MetricResTypeSubnetSet)

	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		log.Error(err, "unable to fetch subnetset CR", "req", req.NamespacedName)
		return ResultNormal, client.IgnoreNotFound(err)
	}

	if obj.ObjectMeta.DeletionTimestamp.IsZero() {
		metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerUpdateTotal, MetricResTypeSubnetSet)
		if !controllerutil.ContainsFinalizer(obj, servicecommon.SubnetSetFinalizerName) {
			controllerutil.AddFinalizer(obj, servicecommon.SubnetSetFinalizerName)
			if obj.Spec.AccessMode == "" || obj.Spec.IPv4SubnetSize == 0 {
				vpcNetworkConfig := r.VPCService.GetVPCNetworkConfigByNamespace(obj.Namespace)
				if vpcNetworkConfig == nil {
					err := fmt.Errorf("failed to find VPCNetworkConfig for namespace %s", obj.Namespace)
					log.Error(err, "operate failed, would retry exponentially", "subnet", req.NamespacedName)
					updateFail(r, &ctx, obj, "")
					return ResultRequeue, err
				}
				if obj.Spec.AccessMode == "" {
					obj.Spec.AccessMode = v1alpha1.AccessMode(vpcNetworkConfig.DefaultSubnetAccessMode)
				}
				if obj.Spec.IPv4SubnetSize == 0 {
					obj.Spec.IPv4SubnetSize = vpcNetworkConfig.DefaultIPv4SubnetSize
				}
			}
			if err := r.Client.Update(ctx, obj); err != nil {
				log.Error(err, "add finalizer", "subnetset", req.NamespacedName)
				updateFail(r, &ctx, obj, "")
				return ResultRequeue, err
			}
			log.V(1).Info("added finalizer on subnetset CR", "subnetset", req.NamespacedName)
		}

		// update subnetset tags if labels of namespace changed
		nsxSubnets := r.SubnetService.SubnetStore.GetByIndex(servicecommon.TagScopeSubnetSetCRUID, string(obj.UID))
		if len(nsxSubnets) > 0 {
			tags := r.SubnetService.GenerateSubnetNSTags(obj, obj.Namespace)
			if tags == nil {
				return ResultRequeue, errors.New("failed to generate subnet tags")
			}
			// tags cannot exceed maximum size 26
			if len(tags) > servicecommon.TagsCountMax {
				errorMsg := fmt.Sprintf("tags cannot exceed maximum size 26, tags length: %d", len(tags))
				log.Error(nil, "exceed tags limit, would not retry", "subnet", req.NamespacedName)
				updateFail(r, &ctx, obj, errorMsg)
				return ResultNormal, nil
			}
			if err := r.SubnetService.UpdateSubnetSetTags(obj.Namespace, nsxSubnets, tags); err != nil {
				log.Error(err, "failed to update subnetset tags")
			}
		}
		updateSuccess(r, &ctx, obj)
	} else {
		if controllerutil.ContainsFinalizer(obj, servicecommon.SubnetSetFinalizerName) {
			metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerDeleteTotal, MetricResTypeSubnetSet)
			if err := r.DeleteSubnetForSubnetSet(*obj, false); err != nil {
				log.Error(err, "deletion failed, would retry exponentially", "subnetset", req.NamespacedName)
				deleteFail(r, &ctx, obj, "")
				return ResultRequeue, err
			}
			controllerutil.RemoveFinalizer(obj, servicecommon.SubnetSetFinalizerName)
			if err := r.Client.Update(ctx, obj); err != nil {
				log.Error(err, "deletion failed, would retry exponentially", "subnetset", req.NamespacedName)
				deleteFail(r, &ctx, obj, "")
				return ResultRequeue, err
			}
			log.V(1).Info("removed finalizer", "subnetset", req.NamespacedName)
			deleteSuccess(r, &ctx, obj)
		} else {
			log.Info("finalizers cannot be recognized", "subnetset", req.NamespacedName)
		}
	}
	return ctrl.Result{}, nil
}

func updateFail(r *SubnetSetReconciler, c *context.Context, o *v1alpha1.SubnetSet, m string) {
	r.setSubnetSetReadyStatusFalse(c, o, metav1.Now(), m)
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailUpdate, m)
	metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerUpdateFailTotal, MetricResTypeSubnetSet)
}

func deleteFail(r *SubnetSetReconciler, c *context.Context, o *v1alpha1.SubnetSet, m string) {
	r.setSubnetSetReadyStatusFalse(c, o, metav1.Now(), m)
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailDelete, m)
	metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResTypeSubnetSet)
}

func updateSuccess(r *SubnetSetReconciler, c *context.Context, o *v1alpha1.SubnetSet) {
	r.setSubnetSetReadyStatusTrue(c, o, metav1.Now())
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulUpdate, "SubnetSet CR has been successfully updated")
	metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerUpdateSuccessTotal, MetricResTypeSubnetSet)
}

func deleteSuccess(r *SubnetSetReconciler, _ *context.Context, o *v1alpha1.SubnetSet) {
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulDelete, "SubnetSet CR has been successfully deleted")
	metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResTypeSubnetSet)
}

func (r *SubnetSetReconciler) setSubnetSetReadyStatusTrue(ctx *context.Context, subnetset *v1alpha1.SubnetSet, transitionTime metav1.Time) {
	newConditions := []v1alpha1.Condition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionTrue,
			Message:            "NSX SubnetSet has been successfully created/updated",
			Reason:             "SubnetsReady",
			LastTransitionTime: transitionTime,
		},
	}
	r.updateSubnetSetStatusConditions(ctx, subnetset, newConditions)
}

func (r *SubnetSetReconciler) setSubnetSetReadyStatusFalse(ctx *context.Context, subnetset *v1alpha1.SubnetSet, transitionTime metav1.Time, m string) {
	newConditions := []v1alpha1.Condition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionFalse,
			Message:            "NSX SubnetSet could not be created/updated",
			Reason:             "SubnetNotReady",
			LastTransitionTime: transitionTime,
		},
	}
	if m != "" {
		newConditions[0].Message = m
	}
	r.updateSubnetSetStatusConditions(ctx, subnetset, newConditions)
}

func (r *SubnetSetReconciler) updateSubnetSetStatusConditions(ctx *context.Context, subnetset *v1alpha1.SubnetSet, newConditions []v1alpha1.Condition) {
	conditionsUpdated := false
	for i := range newConditions {
		if r.mergeSubnetSetStatusCondition(ctx, subnetset, &newConditions[i]) {
			conditionsUpdated = true
		}
	}
	if conditionsUpdated {
		if err := r.Client.Status().Update(*ctx, subnetset); err != nil {
			log.Error(err, "failed to update status", "Name", subnetset.Name, "Namespace", subnetset.Namespace)
		} else {
			log.Info("updated SubnetSet", "Name", subnetset.Name, "Namespace", subnetset.Namespace, "New Conditions", newConditions)
		}
	}
}

func (r *SubnetSetReconciler) mergeSubnetSetStatusCondition(ctx *context.Context, subnetset *v1alpha1.SubnetSet, newCondition *v1alpha1.Condition) bool {
	matchedCondition := getExistingConditionOfType(newCondition.Type, subnetset.Status.Conditions)

	if reflect.DeepEqual(matchedCondition, newCondition) {
		log.V(2).Info("conditions already match", "New Condition", newCondition, "Existing Condition", matchedCondition)
		return false
	}

	if matchedCondition != nil {
		matchedCondition.Reason = newCondition.Reason
		matchedCondition.Message = newCondition.Message
		matchedCondition.Status = newCondition.Status
	} else {
		subnetset.Status.Conditions = append(subnetset.Status.Conditions, *newCondition)
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
	log.Info("subnetset garbage collector started")
	subnetSetList := &v1alpha1.SubnetSetList{}
	err := r.Client.List(ctx, subnetSetList)
	if err != nil {
		log.Error(err, "failed to list SubnetSet CR")
		return
	}
	var nsxSubnetList []*model.VpcSubnet
	for _, subnetSet := range subnetSetList.Items {
		nsxSubnetList = append(nsxSubnetList, r.SubnetService.ListSubnetCreatedBySubnetSet(string(subnetSet.UID))...)
	}
	if len(nsxSubnetList) == 0 {
		return
	}

	subnetSetIDs := sets.New[string]()
	for _, subnetSet := range subnetSetList.Items {
		if err := r.DeleteSubnetForSubnetSet(subnetSet, true); err != nil {
			metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResTypeSubnetSet)
		} else {
			metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResTypeSubnetSet)
		}
		subnetSetIDs.Insert(string(subnetSet.UID))
	}
	for _, subnet := range nsxSubnetList {
		if !r.SubnetService.IsOrphanSubnet(*subnet, subnetSetIDs) {
			continue
		}
		if err := r.SubnetService.DeleteSubnet(*subnet); err != nil {
			metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResTypeSubnetSet)
		} else {
			metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResTypeSubnetSet)
		}
	}
}

func (r *SubnetSetReconciler) DeleteSubnetForSubnetSet(obj v1alpha1.SubnetSet, updataStatus bool) error {
	nsxSubnets := r.SubnetService.SubnetStore.GetByIndex(servicecommon.TagScopeSubnetSetCRUID, string(obj.GetUID()))
	hitError := false
	for _, subnet := range nsxSubnets {
		portNums := len(r.SubnetPortService.GetPortsOfSubnet(*subnet.Id))
		if portNums > 0 {
			continue
		}
		if err := r.SubnetService.DeleteSubnet(*subnet); err != nil {
			log.Error(err, "fail to delete subnet from subnetset cr", "ID", *subnet.Id)
			hitError = true
		}

	}
	if updataStatus {
		if err := r.SubnetService.UpdateSubnetSetStatus(&obj); err != nil {
			return err
		}
	}
	if hitError {
		return errors.New("error occurs when deleting subnet")
	}
	return nil
}

func StartSubnetSetController(mgr ctrl.Manager, subnetService *subnet.SubnetService,
	subnetPortService servicecommon.SubnetPortServiceProvider, vpcService servicecommon.VPCServiceProvider,
	enableWebhook bool) error {
	subnetsetReconciler := &SubnetSetReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		SubnetService:     subnetService,
		SubnetPortService: subnetPortService,
		VPCService:        vpcService,
		Recorder:          mgr.GetEventRecorderFor("subnetset-controller"),
	}
	if err := subnetsetReconciler.Start(mgr, enableWebhook); err != nil {
		log.Error(err, "failed to create controller", "controller", "Subnet")
		return err
	}
	return nil
}

// Start setup manager
func (r *SubnetSetReconciler) Start(mgr ctrl.Manager, enableWebhook bool) error {
	err := r.setupWithManager(mgr)
	if err != nil {
		return err
	}
	if enableWebhook {
		hookServer := mgr.GetWebhookServer()
		hookServer.Register("/validate-nsx-vmware-com-v1alpha1-subnetset",
			&webhook.Admission{
				Handler: &SubnetSetValidator{
					Client:  mgr.GetClient(),
					decoder: admission.NewDecoder(mgr.GetScheme()),
				},
			})
	}
	return nil
}
