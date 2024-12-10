package subnetbinding

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"time"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetbinding"
)

var (
	log = &logger.Log
)

type errorWithRetry struct {
	error
	retry   bool
	message string
}

// Reconciler reconciles a SubnetConnectionBindingMap object
type Reconciler struct {
	Client               client.Client
	Scheme               *runtime.Scheme
	SubnetService        *subnet.SubnetService
	SubnetBindingService *subnetbinding.BindingService
	StatusUpdater        common.StatusUpdater
}

func StartSubnetBindingController(mgr ctrl.Manager, subnetService *subnet.SubnetService, subnetBindingService *subnetbinding.BindingService) {
	reconciler := newReconciler(mgr, subnetService, subnetBindingService)
	// Start the controller
	if err := reconciler.setupWithManager(mgr); err != nil {
		log.Error(err, "Failed to create controller", "controller", "SubnetConnectionBindingMap")
		os.Exit(1)
	}
	// Start garbage collector in a separate goroutine
	go common.GenericGarbageCollector(make(chan bool), servicecommon.GCInterval, reconciler.CollectGarbage)
}

func newReconciler(mgr ctrl.Manager, subnetService *subnet.SubnetService, subnetBindingService *subnetbinding.BindingService) *Reconciler {
	recorder := mgr.GetEventRecorderFor("subnetconnectionbindingmap-controller")
	// Create the SubnetConnectionBindingMap Reconciler with the necessary services and configuration
	return &Reconciler{
		Client:               mgr.GetClient(),
		Scheme:               mgr.GetScheme(),
		SubnetService:        subnetService,
		SubnetBindingService: subnetBindingService,
		StatusUpdater:        common.NewStatusUpdater(mgr.GetClient(), subnetBindingService.NSXConfig, recorder, common.MetricResTypeSubnetConnectionBindingMap, "SubnetConnectionBindingMap", "SubnetConnectionBindingMap"),
	}
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	startTime := time.Now()
	defer func() {
		log.Info("Finished reconciling SubnetConnectionBindingMap", "SubnetConnectionBindingMap", req.NamespacedName, "duration(ms)", time.Since(startTime).Milliseconds())
	}()

	r.StatusUpdater.IncreaseSyncTotal()

	bindingMapCR := &v1alpha1.SubnetConnectionBindingMap{}
	if err := r.Client.Get(ctx, req.NamespacedName, bindingMapCR); err != nil {
		if apierrors.IsNotFound(err) {
			r.StatusUpdater.IncreaseDeleteTotal()
			// Try to delete NSX SubnetConnectionBindingMaps if exists
			if err := r.SubnetBindingService.DeleteSubnetConnectionBindingMapsByCRName(req.Name, req.Namespace); err != nil {
				log.Error(err, "Failed to delete NSX SubnetConnectionBindingMap", "SubnetConnectionBindingMap", req.NamespacedName)
				r.StatusUpdater.DeleteFail(req.NamespacedName, nil, err)
				return common.ResultRequeue, nil
			}
			r.StatusUpdater.DeleteSuccess(req.NamespacedName, nil)
			return common.ResultNormal, nil
		}
		log.Error(err, "Unable to fetch SubnetConnectionBindingMap CR", "SubnetConnectionBindingMap", req.NamespacedName)
		return common.ResultRequeue, nil
	}

	// Create or update SubnetConnectionBindingMap
	r.StatusUpdater.IncreaseUpdateTotal()
	childSubnet, parentSubnets, err := r.validateDependency(ctx, bindingMapCR)
	if err != nil {
		// Update SubnetConnectionBindingMap with not-ready condition
		r.StatusUpdater.UpdateFail(ctx, bindingMapCR, err, "dependent Subnets are not ready", updateBindingMapStatusWithUnreadyCondition, "DependencyNotReady", err.message)
		if !err.retry {
			return common.ResultNormal, nil
		}
		// Requeue after 60s to support the case that the dependent Subnet is not nested.
		return common.ResultRequeueAfter60sec, nil
	}

	if err := r.SubnetBindingService.CreateOrUpdateSubnetConnectionBindingMap(bindingMapCR, childSubnet, parentSubnets); err != nil {
		// Update SubnetConnectionBindingMap with not-ready condition
		r.StatusUpdater.UpdateFail(ctx, bindingMapCR, err, "failure to configure SubnetConnectionBindingMaps on NSX", updateBindingMapStatusWithUnreadyCondition, "ConfigureFailed", fmt.Sprintf("Failed to realize SubnetConnectionBindingMap %s on NSX", req.Name))
		return common.ResultRequeue, nil
	}
	// Update SubnetConnectionBindingMap with ready condition
	r.StatusUpdater.UpdateSuccess(ctx, bindingMapCR, updateBindingMapStatusWithReadyCondition)
	return common.ResultNormal, nil
}

// CollectGarbage collects the stale SubnetConnectionBindingMaps and deletes them on NSX which have been removed from K8s.
// It implements the interface GarbageCollector method.
func (r *Reconciler) CollectGarbage(ctx context.Context) {
	startTime := time.Now()
	defer func() {
		log.Info("SubnetConnectionBindingMap garbage collection completed", "duration(ms)", time.Since(startTime).Milliseconds())
	}()

	bindingMapIdSetByCRs, err := r.listBindingMapIDsFromCRs(ctx)
	if err != nil {
		log.Error(err, "Failed to list SubnetConnectionBindingMap CRs")
		return
	}
	bindingMapIdSetInStore := r.SubnetBindingService.ListSubnetConnectionBindingMapCRUIDsInStore()

	if err = r.SubnetBindingService.DeleteMultiSubnetConnectionBindingMapsByCRs(bindingMapIdSetInStore.Difference(bindingMapIdSetByCRs)); err != nil {
		log.Error(err, "Failed to delete stale SubnetConnectionBindingMaps")
	}
}

var PredicateFuncsForBindingMaps = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldBindingMap := e.ObjectOld.(*v1alpha1.SubnetConnectionBindingMap)
		newBindingMap := e.ObjectNew.(*v1alpha1.SubnetConnectionBindingMap)
		if !reflect.DeepEqual(oldBindingMap.Spec, newBindingMap.Spec) {
			return true
		}
		return false
	},
	CreateFunc: func(e event.CreateEvent) bool {
		return true
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return true
	},
	GenericFunc: func(e event.GenericEvent) bool {
		return false
	},
}

func (r *Reconciler) setupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.SubnetConnectionBindingMap{}, builder.WithPredicates(PredicateFuncsForBindingMaps)).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: common.NumReconcile(),
		}).
		Watches(
			&v1alpha1.Subnet{},
			&common.EnqueueRequestForDependency{
				Client:          r.Client,
				RequeueByUpdate: requeueBindingMapsBySubnetUpdate,
				ResourceType:    "Subnet"},
			builder.WithPredicates(PredicateFuncsForSubnets),
		).
		Watches(
			&v1alpha1.SubnetSet{},
			&common.EnqueueRequestForDependency{
				Client:          r.Client,
				RequeueByUpdate: requeueBindingMapsBySubnetSetUpdate,
				ResourceType:    "SubnetSet"},
			builder.WithPredicates(PredicateFuncsForSubnetSets),
		).
		Complete(r)
}

func (r *Reconciler) listBindingMapIDsFromCRs(ctx context.Context) (sets.Set[string], error) {
	bmIDs := sets.New[string]()
	connectionBindingMapList := &v1alpha1.SubnetConnectionBindingMapList{}
	err := r.Client.List(ctx, connectionBindingMapList)
	if err != nil {
		return nil, err
	}
	for _, bm := range connectionBindingMapList.Items {
		bmIDs.Insert(string(bm.UID))
	}
	return bmIDs, nil
}

// validateDependency validates the following conditions:
//  1. the dependent Subnet/SubnetSet is not realized. In this case, a not-retry error is returned, and the
//     Subnet/SubnetSet readiness update will actively trigger a requeue event
//  2. the associated Subnet is already used as a target Subnet in another SubnetConnectionBindingMap CR, or the target
//     Subnet already has associated SubnetConnectionBindingMap CR. In this case, a retry error is returned.
func (r *Reconciler) validateDependency(ctx context.Context, bindingMap *v1alpha1.SubnetConnectionBindingMap) (*model.VpcSubnet, []*model.VpcSubnet, *errorWithRetry) {
	childSubnets, err := r.validateVpcSubnetsBySubnetCR(ctx, bindingMap.Namespace, bindingMap.Spec.SubnetName, false)
	if err != nil {
		return nil, nil, err
	}
	childSubnet := childSubnets[0]

	if bindingMap.Spec.TargetSubnetName != "" {
		parentSubnets, err := r.validateVpcSubnetsBySubnetCR(ctx, bindingMap.Namespace, bindingMap.Spec.TargetSubnetName, true)
		if err != nil {
			return nil, nil, err
		}
		return childSubnet, parentSubnets, nil
	}

	parentSubnets, err := r.validateVpcSubnetsBySubnetSetCR(ctx, bindingMap.Namespace, bindingMap.Spec.TargetSubnetSetName)
	if err != nil {
		return nil, nil, err
	}
	return childSubnet, parentSubnets, nil
}

func (r *Reconciler) validateVpcSubnetsBySubnetCR(ctx context.Context, namespace, name string, isTarget bool) ([]*model.VpcSubnet, *errorWithRetry) {
	subnetCR := &v1alpha1.Subnet{}
	subnetKey := types.NamespacedName{Namespace: namespace, Name: name}
	// Check the Subnet CR existence.
	err := r.Client.Get(ctx, subnetKey, subnetCR)
	if err != nil {
		log.Error(err, "Failed to get Subnet CR", "Subnet", subnetKey.String())
		return nil, &errorWithRetry{
			message: fmt.Sprintf("Unable to get Subnet CR %s", name),
			retry:   false,
			error:   fmt.Errorf("failed to get Subnet %s in Namespace %s with error: %v", name, namespace, err),
		}
	}

	// Check the Subnet CR realization.
	subnets := r.SubnetService.ListSubnetCreatedBySubnet(string(subnetCR.UID))
	if len(subnets) == 0 {
		log.Info("NSX VpcSubnets by subnet CR do not exist", "Subnet", subnetKey.String())
		return nil, &errorWithRetry{
			message: fmt.Sprintf("Subnet CR %s is not realized on NSX", name),
			retry:   false,
			error:   fmt.Errorf("not found NSX VpcSubnets created by Subnet CR '%s/%s'", namespace, name),
		}
	}

	// Check if the Subnet CR is nested.
	if !isTarget {
		bms := r.SubnetBindingService.GetSubnetConnectionBindingMapsByParentSubnet(subnets[0])
		if len(bms) > 0 {
			dependency := r.SubnetBindingService.GetSubnetConnectionBindingMapCRName(bms[0])
			return nil, &errorWithRetry{
				message: fmt.Sprintf("Subnet CR %s is working as target by %s", name, dependency),
				error:   fmt.Errorf("Subnet %s already works as target in SubnetConnectionBindingMap %s", name, dependency),
				retry:   true,
			}
		}
	} else {
		bms := r.SubnetBindingService.GetSubnetConnectionBindingMapsByChildSubnet(subnets[0])
		if len(bms) > 0 {
			dependency := r.SubnetBindingService.GetSubnetConnectionBindingMapCRName(bms[0])
			return nil, &errorWithRetry{
				message: fmt.Sprintf("Target Subnet CR %s is associated by %s", name, dependency),
				error:   fmt.Errorf("target Subnet %s is already associated by SubnetConnectionBindingMap %s", name, dependency),
				retry:   true,
			}
		}
	}

	return subnets, nil
}

func (r *Reconciler) validateVpcSubnetsBySubnetSetCR(ctx context.Context, namespace, name string) ([]*model.VpcSubnet, *errorWithRetry) {
	subnetSetCR := &v1alpha1.SubnetSet{}
	subnetSetKey := types.NamespacedName{Namespace: namespace, Name: name}
	err := r.Client.Get(ctx, subnetSetKey, subnetSetCR)
	if err != nil {
		log.Error(err, "Failed to get SubnetSet CR", "SubnetSet", subnetSetKey.String())
		return nil, &errorWithRetry{
			message: fmt.Sprintf("Unable to get SubnetSet CR %s", name),
			error:   fmt.Errorf("failed to get SubnetSet %s in Namespace %s with error: %v", name, namespace, err),
			retry:   false,
		}
	}

	subnets := r.SubnetService.ListSubnetCreatedBySubnetSet(string(subnetSetCR.UID))
	if len(subnets) == 0 {
		log.Info("NSX VpcSubnets by SubnetSet CR do not exist", "SubnetSet", subnetSetKey.String())
		return nil, &errorWithRetry{
			message: fmt.Sprintf("SubnetSet CR %s is not realized on NSX", name),
			error:   fmt.Errorf("no existing NSX VpcSubnet created by SubnetSet CR '%s/%s'", namespace, name),
			retry:   false,
		}
	}
	return subnets, nil
}

func updateBindingMapStatusWithUnreadyCondition(c client.Client, ctx context.Context, obj client.Object, _ metav1.Time, _ error, args ...interface{}) {
	bindingMap := obj.(*v1alpha1.SubnetConnectionBindingMap)
	reason := args[0].(string)
	msg := args[1].(string)
	condition := v1alpha1.Condition{
		Type:    v1alpha1.Ready,
		Status:  corev1.ConditionFalse,
		Reason:  reason,
		Message: msg,
	}
	updateBindingMapCondition(c, ctx, bindingMap, condition)
}

func updateBindingMapStatusWithReadyCondition(c client.Client, ctx context.Context, obj client.Object, _ metav1.Time, _ ...interface{}) {
	bindingMap := obj.(*v1alpha1.SubnetConnectionBindingMap)
	condition := v1alpha1.Condition{
		Type:   v1alpha1.Ready,
		Status: corev1.ConditionTrue,
	}
	updateBindingMapCondition(c, ctx, bindingMap, condition)
}

func updateBindingMapCondition(c client.Client, ctx context.Context, bindingMap *v1alpha1.SubnetConnectionBindingMap, condition v1alpha1.Condition) {
	condition.LastTransitionTime = metav1.Now()
	newConditions := []v1alpha1.Condition{condition}
	for _, cond := range bindingMap.Status.Conditions {
		if cond.Type == condition.Type {
			if cond.Status == condition.Status && cond.Reason == condition.Reason && cond.Message == condition.Message {
				return
			}
			continue
		}
		newConditions = append(newConditions, cond)
	}
	bindingMap.Status.Conditions = newConditions
	err := c.Status().Update(ctx, bindingMap)
	if err != nil {
		log.Error(err, "Failed to update SubnetConnectionBindingMap status", "Namespace", bindingMap.Namespace, "Name", bindingMap.Name)
	}
	log.V(1).Info("Updated SubnetConnectionBindingMap status", "Namespace", bindingMap.Namespace, "Name", bindingMap.Name)
}
