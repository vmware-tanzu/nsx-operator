package subnetset

import (
	"context"
	"reflect"
	"runtime"
	"time"

	v1 "k8s.io/api/core/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
)

var (
	log                     = logger.Log
	ResultNormal            = common.ResultNormal
	ResultRequeue           = common.ResultRequeue
	ResultRequeueAfter5mins = common.ResultRequeueAfter5mins
	MetricResTypeSubnetSet  = common.MetricResTypeSubnetSet
	//TODO rename this
	defaultSubnet = "default-subnet"
)

// SubnetSetReconciler reconciles a SubnetSet object
type SubnetSetReconciler struct {
	Client  client.Client
	Scheme  *apimachineryruntime.Scheme
	Service *subnet.SubnetService
}

func (r *SubnetSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &v1alpha1.SubnetSet{}
	log.Info("reconciling subnetset CR", "subnetset", req.NamespacedName)
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerSyncTotal, MetricResTypeSubnetSet)

	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		log.Error(err, "unable to fetch subnetset CR", "req", req.NamespacedName)
		return ResultNormal, client.IgnoreNotFound(err)
	}

	if obj.ObjectMeta.DeletionTimestamp.IsZero() {
		metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateTotal, MetricResTypeSubnetSet)
		if !controllerutil.ContainsFinalizer(obj, servicecommon.FinalizerName) {
			controllerutil.AddFinalizer(obj, servicecommon.FinalizerName)
			if err := r.Client.Update(ctx, obj); err != nil {
				log.Error(err, "add finalizer", "subnetset", req.NamespacedName)
				updateFail(r, &ctx, obj)
				return ResultRequeue, err
			}
			log.V(1).Info("added finalizer on subnetset CR", "subnetset", req.NamespacedName)
		}
		updateSuccess(r, &ctx, obj)
	} else {
		if controllerutil.ContainsFinalizer(obj, servicecommon.FinalizerName) {
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, MetricResTypeSubnetSet)
			if err := r.Service.DeleteSubnetForSubnetSet(*obj, false); err != nil {
				log.Error(err, "deletion failed, would retry exponentially", "subnetset", req.NamespacedName)
				deleteFail(r, &ctx, obj)
				return ResultRequeue, err
			}
			controllerutil.RemoveFinalizer(obj, servicecommon.FinalizerName)
			if err := r.Client.Update(ctx, obj); err != nil {
				log.Error(err, "deletion failed, would retry exponentially", "subnetset", req.NamespacedName)
				deleteFail(r, &ctx, obj)
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

func updateFail(r *SubnetSetReconciler, c *context.Context, o *v1alpha1.SubnetSet) {
	r.setSubnetSetReadyStatusFalse(c, o)
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateFailTotal, MetricResTypeSubnetSet)
}

func deleteFail(r *SubnetSetReconciler, c *context.Context, o *v1alpha1.SubnetSet) {
	r.setSubnetSetReadyStatusFalse(c, o)
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResTypeSubnetSet)
}

func updateSuccess(r *SubnetSetReconciler, c *context.Context, o *v1alpha1.SubnetSet) {
	r.setSubnetSetReadyStatusTrue(c, o)
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateSuccessTotal, MetricResTypeSubnetSet)
}

func deleteSuccess(r *SubnetSetReconciler, _ *context.Context, _ *v1alpha1.SubnetSet) {
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResTypeSubnetSet)
}

func (r *SubnetSetReconciler) setSubnetSetReadyStatusTrue(ctx *context.Context, subnetset *v1alpha1.SubnetSet) {
	newConditions := []v1alpha1.Condition{
		{
			Type:    v1alpha1.Ready,
			Status:  v1.ConditionTrue,
			Message: "NSX SubnetSet has been successfully created/updated",
			Reason:  "SubnetsReady",
		},
	}
	r.updateSubnetSetStatusConditions(ctx, subnetset, newConditions)
}

func (r *SubnetSetReconciler) setSubnetSetReadyStatusFalse(ctx *context.Context, subnetset *v1alpha1.SubnetSet) {
	newConditions := []v1alpha1.Condition{
		{
			Type:    v1alpha1.Ready,
			Status:  v1.ConditionFalse,
			Message: "NSX SubnetSet could not be created/updated",
			Reason:  "SubnetNotReady",
		},
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
		r.Client.Status().Update(*ctx, subnetset)
		log.V(1).Info("updated Subnet", "Name", subnetset.Name, "Namespace", subnetset.Namespace,
			"New Conditions", newConditions)
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
			MaxConcurrentReconciles: runtime.NumCPU(),
		}).
		Watches(&source.Kind{Type: &v1alpha1.VPC{}},
			&VPCHandler{Client: mgr.GetClient()},
			builder.WithPredicates(VPCPredicate)).
		Watches(
			&source.Kind{Type: &v1alpha1.SubnetPort{}},
			&SubnetPortHandler{Reconciler: r},
			builder.WithPredicates(SubnetPortPredicate)).
		Complete(r)
}

// GarbageCollector collect Subnet which there is no port attached on it.
// cancel is used to break the loop during UT
func (r *SubnetSetReconciler) GarbageCollector(cancel chan bool, timeout time.Duration) {
	ctx := context.Background()
	log.Info("subnetset garbage collector started")
	for {
		select {
		case <-cancel:
			return
		case <-time.After(timeout):
		}
		subnetSetList := &v1alpha1.SubnetSetList{}
		err := r.Client.List(ctx, subnetSetList)
		if err != nil {
			log.Error(err, "failed to list SubnetSet CR")
			continue
		}
		for _, subnetSet := range subnetSetList.Items {
			if err := r.Service.DeleteSubnetForSubnetSet(subnetSet, true); err != nil {
				metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResTypeSubnetSet)
			} else {
				metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResTypeSubnetSet)
			}
		}
	}
}

func StartSubnetSetController(mgr ctrl.Manager, commonService servicecommon.Service) error {
	subnetsetReconciler := &SubnetSetReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
	subnetsetReconciler.Service = subnet.GetSubnetService(commonService)
	if err := subnetsetReconciler.Start(mgr); err != nil {
		log.Error(err, "failed to create controller", "controller", "Subnet")
		return err
	}
	return nil
}

// Start setup manager
func (r *SubnetSetReconciler) Start(mgr ctrl.Manager) error {
	err := r.setupWithManager(mgr)
	if err != nil {
		return err
	}
	go r.GarbageCollector(make(chan bool), servicecommon.GCInterval)
	return nil
}
