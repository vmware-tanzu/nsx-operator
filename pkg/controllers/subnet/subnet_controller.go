package subnet

import (
	"context"
	"errors"
	"fmt"
	"reflect"

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
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

var (
	log                     = &logger.Log
	ResultNormal            = common.ResultNormal
	ResultRequeue           = common.ResultRequeue
	ResultRequeueAfter5mins = common.ResultRequeueAfter5mins
	ResultRequeueAfter10sec = common.ResultRequeueAfter10sec
	MetricResTypeSubnet     = common.MetricResTypeSubnet
)

// SubnetReconciler reconciles a SubnetSet object
type SubnetReconciler struct {
	Client            client.Client
	Scheme            *apimachineryruntime.Scheme
	SubnetService     *subnet.SubnetService
	SubnetPortService servicecommon.SubnetPortServiceProvider
	VPCService        servicecommon.VPCServiceProvider
	Recorder          record.EventRecorder
}

func (r *SubnetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &v1alpha1.Subnet{}
	log.Info("reconciling subnet CR", "subnet", req.NamespacedName)
	metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerSyncTotal, MetricResTypeSubnet)

	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		log.Error(err, "unable to fetch Subnet CR", "req", req.NamespacedName)
		return ResultNormal, client.IgnoreNotFound(err)
	}

	if obj.ObjectMeta.DeletionTimestamp.IsZero() {
		metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerUpdateTotal, MetricResTypeSubnet)
		if !controllerutil.ContainsFinalizer(obj, servicecommon.SubnetFinalizerName) {
			controllerutil.AddFinalizer(obj, servicecommon.SubnetFinalizerName)

			if obj.Spec.AccessMode == "" || obj.Spec.IPv4SubnetSize == 0 {
				vpcNetworkConfig := r.VPCService.GetVPCNetworkConfigByNamespace(obj.Namespace)
				if vpcNetworkConfig == nil {
					err := fmt.Errorf("operate failed: cannot get configuration for Subnet CR")
					log.Error(nil, "failed to find VPCNetworkConfig for Subnet CR", "subnet", req.NamespacedName, "namespace %s", obj.Namespace)
					updateFail(r, &ctx, obj, "")
					return ResultRequeue, err
				}

				if obj.Spec.AccessMode == "" {
					log.Info("aobj.Spec.AccessMode set", "subnet", req.NamespacedName)
					obj.Spec.AccessMode = v1alpha1.AccessMode(v1alpha1.AccessModePrivate)
				}
				if obj.Spec.IPv4SubnetSize == 0 {
					obj.Spec.IPv4SubnetSize = vpcNetworkConfig.DefaultSubnetSize
				}
			}
			if err := r.Client.Update(ctx, obj); err != nil {
				log.Error(err, "add finalizer", "subnet", req.NamespacedName)
				updateFail(r, &ctx, obj, "")
				return ResultRequeue, err
			}
			log.V(1).Info("added finalizer on subnet CR", "subnet", req.NamespacedName)
		}
		tags := r.SubnetService.GenerateSubnetNSTags(obj, obj.Namespace)
		if tags == nil {
			return ResultRequeue, errors.New("failed to generate subnet tags")
		}
		vpcInfoList := r.VPCService.ListVPCInfo(req.Namespace)
		if len(vpcInfoList) == 0 {
			return ResultRequeueAfter10sec, nil
		}
		if _, err := r.SubnetService.CreateOrUpdateSubnet(obj, vpcInfoList[0], tags); err != nil {
			if errors.As(err, &util.ExceedTagsError{}) {
				log.Error(err, "exceed tags limit, would not retry", "subnet", req.NamespacedName)
				updateFail(r, &ctx, obj, err.Error())
				return ResultNormal, nil
			}
			log.Error(err, "operate failed, would retry exponentially", "subnet", req.NamespacedName)
			updateFail(r, &ctx, obj, "")
			return ResultRequeue, err
		}
		if err := r.updateSubnetStatus(obj); err != nil {
			log.Error(err, "update subnet status failed, would retry exponentially", "subnet", req.NamespacedName)
			updateFail(r, &ctx, obj, "")
			return ResultRequeue, err
		}
		updateSuccess(r, &ctx, obj)
	} else {
		if controllerutil.ContainsFinalizer(obj, servicecommon.SubnetFinalizerName) {
			metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerDeleteTotal, MetricResTypeSubnet)
			if err := r.DeleteSubnet(*obj); err != nil {
				log.Error(err, "deletion failed, would retry exponentially", "subnet", req.NamespacedName)
				deleteFail(r, &ctx, obj, "")
				return ResultRequeue, err
			}
			controllerutil.RemoveFinalizer(obj, servicecommon.SubnetFinalizerName)
			if err := r.Client.Update(ctx, obj); err != nil {
				log.Error(err, "deletion failed, would retry exponentially", "subnet", req.NamespacedName)
				deleteFail(r, &ctx, obj, "")
				return ResultRequeue, err
			}
			log.V(1).Info("removed finalizer", "subnet", req.NamespacedName)
			deleteSuccess(r, &ctx, obj)
		} else {
			log.Info("finalizers cannot be recognized", "subnet", req.NamespacedName)
		}
	}
	return ctrl.Result{}, nil
}

func (r *SubnetReconciler) DeleteSubnet(obj v1alpha1.Subnet) error {
	nsxSubnets := r.SubnetService.SubnetStore.GetByIndex(servicecommon.TagScopeSubnetCRUID, string(obj.GetUID()))
	if len(nsxSubnets) == 0 {
		log.Info("no subnet found for subnet CR", "uid", string(obj.GetUID()))
		return nil
	}
	portNums := len(r.SubnetPortService.GetPortsOfSubnet(*nsxSubnets[0].Id))
	if portNums > 0 {
		err := errors.New("subnet still attached by port")
		log.Error(err, "", "ID", *nsxSubnets[0].Id)
		return err
	}
	return r.SubnetService.DeleteSubnet(*nsxSubnets[0])
}

func (r *SubnetReconciler) updateSubnetStatus(obj *v1alpha1.Subnet) error {
	nsxSubnet := r.SubnetService.SubnetStore.GetByKey(r.SubnetService.BuildSubnetID(obj))
	if nsxSubnet == nil {
		return errors.New("failed to get NSX Subnet from store")
	}
	obj.Status.NetworkAddresses = obj.Status.NetworkAddresses[:0]
	obj.Status.GatewayAddresses = obj.Status.GatewayAddresses[:0]
	obj.Status.DHCPServerAddresses = obj.Status.DHCPServerAddresses[:0]
	statusList, err := r.SubnetService.GetSubnetStatus(nsxSubnet)
	if err != nil {
		return err
	}
	for _, status := range statusList {
		obj.Status.NetworkAddresses = append(obj.Status.NetworkAddresses, *status.NetworkAddress)
		obj.Status.GatewayAddresses = append(obj.Status.GatewayAddresses, *status.GatewayAddress)
		// DHCPServerAddress is only for the subnet with DHCP enabled
		if status.DhcpServerAddress != nil {
			obj.Status.DHCPServerAddresses = append(obj.Status.DHCPServerAddresses, *status.DhcpServerAddress)
		}
	}
	obj.Status.NSXResourcePath = *nsxSubnet.Path
	return nil
}

func (r *SubnetReconciler) setSubnetReadyStatusTrue(ctx *context.Context, subnet *v1alpha1.Subnet, transitionTime metav1.Time) {
	newConditions := []v1alpha1.Condition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionTrue,
			Message:            "NSX Subnet has been successfully created/updated",
			Reason:             "SubnetCreated",
			LastTransitionTime: transitionTime,
		},
	}
	r.updateSubnetStatusConditions(ctx, subnet, newConditions)
}

func (r *SubnetReconciler) setSubnetReadyStatusFalse(ctx *context.Context, subnet *v1alpha1.Subnet, transitionTime metav1.Time, msg string) {
	newConditions := []v1alpha1.Condition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionFalse,
			Message:            "NSX Subnet could not be created/updated",
			Reason:             "SubnetNotReady",
			LastTransitionTime: transitionTime,
		},
	}
	if msg != "" {
		newConditions[0].Message = msg
	}
	r.updateSubnetStatusConditions(ctx, subnet, newConditions)
}

func (r *SubnetReconciler) updateSubnetStatusConditions(ctx *context.Context, subnet *v1alpha1.Subnet, newConditions []v1alpha1.Condition) {
	conditionsUpdated := false
	for i := range newConditions {
		if r.mergeSubnetStatusCondition(ctx, subnet, &newConditions[i]) {
			conditionsUpdated = true
		}
	}
	if conditionsUpdated {
		if err := r.Client.Status().Update(*ctx, subnet); err != nil {
			log.Error(err, "failed to update subnet status", "Name", subnet.Name, "Namespace", subnet.Namespace)
		} else {
			log.Info("updated Subnet", "Name", subnet.Name, "Namespace", subnet.Namespace, "New Conditions", newConditions)
		}
	}
}

func (r *SubnetReconciler) mergeSubnetStatusCondition(ctx *context.Context, subnet *v1alpha1.Subnet, newCondition *v1alpha1.Condition) bool {
	matchedCondition := getExistingConditionOfType(newCondition.Type, subnet.Status.Conditions)

	if reflect.DeepEqual(matchedCondition, newCondition) {
		log.V(2).Info("conditions already match", "New Condition", newCondition, "Existing Condition", matchedCondition)
		return false
	}

	if matchedCondition != nil {
		matchedCondition.Reason = newCondition.Reason
		matchedCondition.Message = newCondition.Message
		matchedCondition.Status = newCondition.Status
	} else {
		subnet.Status.Conditions = append(subnet.Status.Conditions, *newCondition)
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

func updateFail(r *SubnetReconciler, c *context.Context, o *v1alpha1.Subnet, m string) {
	r.setSubnetReadyStatusFalse(c, o, metav1.Now(), m)
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailUpdate, m)
	metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerUpdateFailTotal, MetricResTypeSubnet)
}

func deleteFail(r *SubnetReconciler, c *context.Context, o *v1alpha1.Subnet, m string) {
	r.setSubnetReadyStatusFalse(c, o, metav1.Now(), m)
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailDelete, m)
	metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResTypeSubnet)
}

func updateSuccess(r *SubnetReconciler, c *context.Context, o *v1alpha1.Subnet) {
	r.setSubnetReadyStatusTrue(c, o, metav1.Now())
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulUpdate, "Subnet CR has been successfully updated")
	metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerUpdateSuccessTotal, MetricResTypeSubnet)
}

func deleteSuccess(r *SubnetReconciler, _ *context.Context, o *v1alpha1.Subnet) {
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulDelete, "Subnet CR has been successfully deleted")
	metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResTypeSubnet)
}

func (r *SubnetReconciler) setupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Subnet{}).
		WithEventFilter(predicate.Funcs{
			DeleteFunc: func(e event.DeleteEvent) bool {
				// Suppress Delete events to avoid filtering them out in the Reconcile function
				return false
			},
		}).
		Watches(
			&v1.Namespace{},
			&EnqueueRequestForNamespace{Client: mgr.GetClient()},
			builder.WithPredicates(PredicateFuncsNs),
		).
		WithOptions(
			controller.Options{
				MaxConcurrentReconciles: common.NumReconcile(),
			}).
		Complete(r)
}

func StartSubnetController(mgr ctrl.Manager, subnetService *subnet.SubnetService, subnetPortService servicecommon.SubnetPortServiceProvider, vpcService servicecommon.VPCServiceProvider) error {
	subnetReconciler := &SubnetReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		SubnetService:     subnetService,
		SubnetPortService: subnetPortService,
		VPCService:        vpcService,
		Recorder:          mgr.GetEventRecorderFor("subnet-controller"),
	}
	if err := subnetReconciler.Start(mgr); err != nil {
		log.Error(err, "failed to create controller", "controller", "Subnet")
		return err
	}
	go common.GenericGarbageCollector(make(chan bool), servicecommon.GCInterval, subnetReconciler.CollectGarbage)
	return nil
}

// Start setup manager
func (r *SubnetReconciler) Start(mgr ctrl.Manager) error {
	err := r.setupWithManager(mgr)
	if err != nil {
		return err
	}
	return nil
}

// CollectGarbage implements the interface GarbageCollector method.
func (r *SubnetReconciler) CollectGarbage(ctx context.Context) {
	log.Info("subnet garbage collector started")
	crdSubnetList := &v1alpha1.SubnetList{}
	err := r.Client.List(ctx, crdSubnetList)
	if err != nil {
		log.Error(err, "failed to list subnet CR")
		return
	}
	var nsxSubnetList []*model.VpcSubnet
	for _, subnet := range crdSubnetList.Items {
		nsxSubnetList = append(nsxSubnetList, r.SubnetService.ListSubnetCreatedBySubnet(string(subnet.UID))...)
	}
	if len(nsxSubnetList) == 0 {
		return
	}

	crdSubnetIDs := sets.NewString()
	for _, sr := range crdSubnetList.Items {
		crdSubnetIDs.Insert(string(sr.UID))
	}

	for _, elem := range nsxSubnetList {
		uid := util.FindTag(elem.Tags, servicecommon.TagScopeSubnetCRUID)
		if crdSubnetIDs.Has(uid) {
			continue
		}

		log.Info("GC collected Subnet CR", "UID", elem)
		metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerDeleteTotal, common.MetricResTypeSubnet)
		err = r.SubnetService.DeleteSubnet(*elem)
		if err != nil {
			metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerDeleteFailTotal, common.MetricResTypeSubnet)
		} else {
			metrics.CounterInc(r.SubnetService.NSXConfig, metrics.ControllerDeleteSuccessTotal, common.MetricResTypeSubnet)
		}
	}
}
