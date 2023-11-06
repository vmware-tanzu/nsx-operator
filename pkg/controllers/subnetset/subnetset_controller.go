package subnetset

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"time"

	v1 "k8s.io/api/core/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	commonctl "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
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
		if !controllerutil.ContainsFinalizer(obj, servicecommon.SubnetSetFinalizerName) {
			controllerutil.AddFinalizer(obj, servicecommon.SubnetSetFinalizerName)
			if obj.Spec.AccessMode == "" || obj.Spec.IPv4SubnetSize == 0 {
				vpcNetworkConfig := commonctl.ServiceMediator.GetVPCNetworkConfigByNamespace(obj.Namespace)
				if vpcNetworkConfig == nil {
					err := fmt.Errorf("failed to find VPCNetworkConfig for namespace %s", obj.Namespace)
					log.Error(err, "operate failed, would retry exponentially", "subnet", req.NamespacedName)
					updateFail(r, &ctx, obj)
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
				updateFail(r, &ctx, obj)
				return ResultRequeue, err
			}
			log.V(1).Info("added finalizer on subnetset CR", "subnetset", req.NamespacedName)
		}

		// update subnetset tags if labels of namespace changed
		nsxSubnets := r.Service.SubnetStore.GetByIndex(servicecommon.TagScopeSubnetSetCRUID, string(obj.UID))
		if len(nsxSubnets) > 0 {
			nsObj := &v1.Namespace{}
			if err := r.Client.Get(ctx, client.ObjectKey{Name: obj.Namespace}, nsObj); err != nil {
				err = fmt.Errorf("unable to fetch namespace %s", obj.Namespace)
				log.Error(err, "")
				return ResultRequeue, err
			}
			tags := r.Service.GenerateSubnetNSTags(obj, string(nsObj.UID))
			for k, v := range nsObj.Labels {
				tags = append(tags, model.Tag{Scope: servicecommon.String(k), Tag: servicecommon.String(v)})
			}
			if err := r.Service.UpdateSubnetSetTags(obj.Namespace, nsxSubnets, tags); err != nil {
				log.Error(err, "failed to update subnetset tags")
			}
		}
		updateSuccess(r, &ctx, obj)
	} else {
		if controllerutil.ContainsFinalizer(obj, servicecommon.SubnetSetFinalizerName) {
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, MetricResTypeSubnetSet)
			if err := r.DeleteSubnetForSubnetSet(*obj, false); err != nil {
				log.Error(err, "deletion failed, would retry exponentially", "subnetset", req.NamespacedName)
				deleteFail(r, &ctx, obj)
				return ResultRequeue, err
			}
			controllerutil.RemoveFinalizer(obj, servicecommon.SubnetSetFinalizerName)
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
		Watches(&v1alpha1.VPC{},
			&VPCHandler{Client: mgr.GetClient()},
			builder.WithPredicates(VPCPredicate)).
		Watches(
			&v1.Namespace{},
			&EnqueueRequestForNamespace{Client: mgr.GetClient()},
			builder.WithPredicates(PredicateFuncsNs),
		).
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

		nsxSubnetList := r.Service.ListSubnetCreatedBySubnetSet()
		if len(nsxSubnetList) == 0 {
			continue
		}

		subnetSetIDs := sets.NewString()
		for _, subnetSet := range subnetSetList.Items {
			if err := r.DeleteSubnetForSubnetSet(subnetSet, true); err != nil {
				metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResTypeSubnetSet)
			} else {
				metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResTypeSubnetSet)
			}
			subnetSetIDs.Insert(string(subnetSet.UID))
		}
		for _, subnet := range nsxSubnetList {
			if !r.Service.IsOrphanSubnet(subnet, subnetSetIDs) {
				continue
			}
			if err := r.Service.DeleteSubnet(subnet); err != nil {
				metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResTypeSubnetSet)
			} else {
				metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResTypeSubnetSet)
			}
		}
	}
}

func (r *SubnetSetReconciler) DeleteSubnetForSubnetSet(obj v1alpha1.SubnetSet, updataStatus bool) error {
	nsxSubnets := r.Service.SubnetStore.GetByIndex(servicecommon.TagScopeSubnetSetCRUID, string(obj.GetUID()))
	hitError := false
	for _, subnet := range nsxSubnets {
		portNums := len(common.ServiceMediator.GetPortsOfSubnet(*subnet.Id))
		if portNums > 0 {
			continue
		}
		if err := r.Service.DeleteSubnet(subnet); err != nil {
			log.Error(err, "fail to delete subnet from subnetset cr", "ID", *subnet.Id)
			hitError = true
		}

	}
	if updataStatus {
		if err := r.Service.UpdateSubnetSetStatus(&obj); err != nil {
			return err
		}
	}
	if hitError {
		return errors.New("error occurs when deleting subnet")
	}
	return nil
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
	hookServer := mgr.GetWebhookServer()
	hookServer.Register("/validate-nsx-vmware-com-v1alpha1-subnetset",
		&webhook.Admission{
			Handler: &SubnetSetValidator{Client: mgr.GetClient()},
		})
	go r.GarbageCollector(make(chan bool), servicecommon.GCInterval)
	return nil
}
