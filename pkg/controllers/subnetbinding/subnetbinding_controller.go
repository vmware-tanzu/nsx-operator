package subnetbinding

import (
	"context"
	"fmt"
	"reflect"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetbinding"
)

var (
	log = logger.Log
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

func (r *Reconciler) RestoreReconcile() error {
	return nil
}

func (r *Reconciler) StartController(mgr ctrl.Manager, _ webhook.Server) error {
	// Start the controller
	if err := r.setupWithManager(mgr); err != nil {
		log.Error(err, "Failed to create controller", "controller", "SubnetConnectionBindingMap")
		return err
	}
	// Setup field indexers
	if err := r.SetupFieldIndexers(mgr); err != nil {
		log.Error(err, "Failed to setup field indexers", "controller", "SubnetConnectionBindingMap")
		return err
	}
	// Start garbage collector in a separate goroutine
	go common.GenericGarbageCollector(make(chan bool), servicecommon.GCInterval, r.CollectGarbage)
	return nil
}

func NewReconciler(mgr ctrl.Manager, subnetService *subnet.SubnetService, subnetBindingService *subnetbinding.BindingService) *Reconciler {
	recorder := mgr.GetEventRecorderFor("subnetconnectionbindingmap-controller") //nolint:staticcheck // record.EventRecorder; StatusUpdater not on events.EventRecorder yet
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
	hostSubnetPath, peerSubnetPaths, err := r.validateDependency(ctx, bindingMapCR)
	if err != nil {
		// Update SubnetConnectionBindingMap with not-ready condition
		r.StatusUpdater.UpdateFail(ctx, bindingMapCR, err, "dependent Subnets are not ready", updateBindingMapStatusWithUnreadyCondition, "DependencyNotReady", err.message)
		if !err.retry {
			return common.ResultNormal, nil
		}
		// Requeue after 60s to support the case that the dependent Subnet is not nested.
		return common.ResultRequeueAfter60sec, nil
	}

	if err := r.SubnetBindingService.CreateOrUpdateSubnetConnectionBindingMap(bindingMapCR, hostSubnetPath, peerSubnetPaths); err != nil {
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
func (r *Reconciler) CollectGarbage(ctx context.Context) error {
	startTime := time.Now()
	defer func() {
		log.Info("SubnetConnectionBindingMap garbage collection completed", "duration(ms)", time.Since(startTime).Milliseconds())
	}()

	bindingMapIdSetByCRs, err := r.listBindingMapIDsFromCRs(ctx)
	if err != nil {
		log.Error(err, "Failed to list SubnetConnectionBindingMap CRs")
		return err
	}
	bindingMapIdSetInStore := r.SubnetBindingService.ListSubnetConnectionBindingMapCRUIDsInStore()

	if err = r.SubnetBindingService.DeleteMultiSubnetConnectionBindingMapsByCRs(bindingMapIdSetInStore.Difference(bindingMapIdSetByCRs)); err != nil {
		log.Error(err, "Failed to delete stale SubnetConnectionBindingMaps")
		return err
	}
	return nil
}

var PredicateFuncsForBindingMaps = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldBindingMap := e.ObjectOld.(*v1alpha1.SubnetConnectionBindingMap)
		newBindingMap := e.ObjectNew.(*v1alpha1.SubnetConnectionBindingMap)
		return !reflect.DeepEqual(oldBindingMap.Spec, newBindingMap.Spec)
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

func getVpcPath(subnetPath string) (string, *errorWithRetry) {
	info, err := servicecommon.ParseVPCResourcePath(subnetPath)
	if err != nil {
		return "", &errorWithRetry{
			message: fmt.Sprintf("Invalid Subnet path %s", subnetPath),
			retry:   false,
			error:   fmt.Errorf("failed to parse Subnet path %s", subnetPath),
		}
	}
	return info.GetVPCPath(), nil
}

// validateDependency validates dependent Subnets/SubnetSets and returns the NSX path of the host Subnet
// (spec.subnetName, where the binding map is created) and peer Subnet path(s) (target Subnet or SubnetSet members).
func (r *Reconciler) validateDependency(ctx context.Context, bindingMap *v1alpha1.SubnetConnectionBindingMap) (string, []string, *errorWithRetry) {
	isBranch := bindingMap.Spec.IsBranchAssociation()
	targetNamespace := bindingMap.Namespace

	// Any Subnet cannot be used as a child in another binding (to prevent multiple parents and cascading).
	const checkNotUsedAsChild = true
	var hostCheckNotUsedAsParent, peerCheckNotUsedAsParent bool

	if isBranch {
		// Branch mode: subnetName is the parent (trunk), targetSubnetName is the child (branch).
		// Parent can have multiple children, so it can be a parent in another binding (hostCheckNotUsedAsParent = false).
		// Child cannot be a parent, so it cannot be a parent in another binding (peerCheckNotUsedAsParent = true).
		hostCheckNotUsedAsParent = false
		peerCheckNotUsedAsParent = true
	} else {
		// Legacy Trunk mode: subnetName is the child (branch), targetSubnetName is the parent (trunk).
		// Child cannot be a parent, so it cannot be a parent in another binding (hostCheckNotUsedAsParent = true).
		// Parent can have multiple children, so it can be a parent in another binding (peerCheckNotUsedAsParent = false).
		hostCheckNotUsedAsParent = true
		peerCheckNotUsedAsParent = false
	}

	hostSubnetPaths, hostSubnetCR, err := r.validateVpcSubnetsBySubnetCR(ctx, bindingMap.Namespace, bindingMap.Spec.SubnetName, checkNotUsedAsChild, hostCheckNotUsedAsParent, bindingMap.Name)
	if err != nil {
		return "", nil, err
	}
	hostSubnetPath := hostSubnetPaths[0]

	var peerSubnetPaths []string
	if bindingMap.Spec.TargetSubnetName != "" {
		var peerSubnetCR *v1alpha1.Subnet
		peerSubnetPaths, peerSubnetCR, err = r.validateVpcSubnetsBySubnetCR(ctx, targetNamespace, bindingMap.Spec.TargetSubnetName, checkNotUsedAsChild, peerCheckNotUsedAsParent, bindingMap.Name)
		if err != nil {
			return "", nil, err
		}
		if !isBranch {
			if _, ok := peerSubnetCR.GetAnnotations()[servicecommon.AnnotationAssociatedResource]; ok {
				return "", nil, &errorWithRetry{
					message: fmt.Sprintf("Target Subnet %s/%s is a pre-created Subnet", targetNamespace, bindingMap.Spec.TargetSubnetName),
					error:   fmt.Errorf("pre-created Subnet %s/%s cannot be a target Subnet", targetNamespace, bindingMap.Spec.TargetSubnetName),
					retry:   false,
				}
			}
		}
	} else {
		if isBranch {
			return "", nil, &errorWithRetry{
				message: "subnetAssociation Branch requires targetSubnetName",
				error:   fmt.Errorf("targetSubnetSetName is not supported with subnetAssociation Branch"),
				retry:   false,
			}
		}
		peerSubnetPaths, err = r.validateVpcSubnetsBySubnetSetCR(ctx, bindingMap.Namespace, bindingMap.Spec.TargetSubnetSetName)
		if err != nil {
			return "", nil, err
		}
	}

	// Legacy Trunk workflow: a shared (pre-created) child must stay in the same VPC as the parent target.
	if !isBranch {
		if _, ok := hostSubnetCR.GetAnnotations()[servicecommon.AnnotationAssociatedResource]; ok {
			hostVpcPath, vpcErr := getVpcPath(hostSubnetPath)
			if vpcErr != nil {
				return "", nil, vpcErr
			}
			peerVpcPath, vpcErr := getVpcPath(peerSubnetPaths[0])
			if vpcErr != nil {
				return "", nil, vpcErr
			}
			if hostVpcPath != peerVpcPath {
				return "", nil, &errorWithRetry{
					message: fmt.Sprintf("Subnet %s and target Subnet %s are in different VPCs", hostSubnetPath, peerSubnetPaths[0]),
					retry:   false,
					error:   fmt.Errorf("Subnet and target Subnet are in different VPCs"),
				}
			}
		}
	}
	return hostSubnetPath, peerSubnetPaths, nil
}

func (r *Reconciler) validateVpcSubnetsBySubnetCR(ctx context.Context, namespace, name string, checkNotUsedAsChild, checkNotUsedAsParent bool, currentBindingMapName string) ([]string, *v1alpha1.Subnet, *errorWithRetry) {
	subnetCR := &v1alpha1.Subnet{}
	subnetKey := types.NamespacedName{Namespace: namespace, Name: name}
	// Check the Subnet CR existence.
	err := r.Client.Get(ctx, subnetKey, subnetCR)
	if err != nil {
		log.Error(err, "Failed to get Subnet CR", "Subnet", subnetKey.String())
		return nil, subnetCR, &errorWithRetry{
			message: fmt.Sprintf("Unable to get Subnet CR %s", name),
			retry:   false,
			error:   fmt.Errorf("failed to get Subnet %s in Namespace %s with error: %v", name, namespace, err),
		}
	}

	if checkNotUsedAsChild || checkNotUsedAsParent {
		bmsAsTarget, err := r.getSubnetConnectionBindingMapsByTargetSubnet(ctx, namespace, name)
		if err != nil {
			log.Error(err, "Failed to get SubnetConnectionBindingMaps with Subnet as targetSubnet", "Subnet", subnetKey.String())
			return nil, subnetCR, &errorWithRetry{
				message: fmt.Sprintf("Failed to get SubnetConnectionBindingMaps with Subnet as targetSubnet %s", name),
				retry:   true,
				error:   err,
			}
		}
		bmsAsHost, err := r.getSubnetConnectionBindingMapsByHostSubnet(ctx, namespace, name)
		if err != nil {
			log.Error(err, "Failed to get SubnetConnectionBindingMaps with Subnet as host Subnet", "Subnet", subnetKey.String())
			return nil, subnetCR, &errorWithRetry{
				message: fmt.Sprintf("Failed to get SubnetConnectionBindingMaps with Subnet as host Subnet %s", name),
				retry:   true,
				error:   err,
			}
		}

		var usedAsChildBms []types.NamespacedName
		var usedAsParentBms []types.NamespacedName

		for _, bm := range bmsAsTarget {
			if bm.Name == currentBindingMapName {
				continue
			}
			nn := types.NamespacedName{Namespace: bm.Namespace, Name: bm.Name}
			if bm.Spec.IsBranchAssociation() {
				usedAsChildBms = append(usedAsChildBms, nn)
			} else {
				usedAsParentBms = append(usedAsParentBms, nn)
			}
		}

		for _, bm := range bmsAsHost {
			if bm.Name == currentBindingMapName {
				continue
			}
			nn := types.NamespacedName{Namespace: bm.Namespace, Name: bm.Name}
			if bm.Spec.IsBranchAssociation() {
				usedAsParentBms = append(usedAsParentBms, nn)
			} else {
				usedAsChildBms = append(usedAsChildBms, nn)
			}
		}

		if checkNotUsedAsChild && len(usedAsChildBms) > 0 {
			return nil, subnetCR, &errorWithRetry{
				message: fmt.Sprintf("Subnet CR %s is already used as a child by %s", name, usedAsChildBms),
				error:   fmt.Errorf("Subnet %s already works as a child in SubnetConnectionBindingMap %s", name, usedAsChildBms),
				retry:   true,
			}
		}

		if checkNotUsedAsParent && len(usedAsParentBms) > 0 {
			return nil, subnetCR, &errorWithRetry{
				message: fmt.Sprintf("Subnet CR %s is already used as a parent by %s", name, usedAsParentBms),
				error:   fmt.Errorf("Subnet %s already works as a parent in SubnetConnectionBindingMap %s", name, usedAsParentBms),
				retry:   true,
			}
		}
	}

	// Check the Subnet CR realization.
	var subnetPaths []string
	if anno, ok := subnetCR.GetAnnotations()[servicecommon.AnnotationAssociatedResource]; ok {
		realized := false
		for _, con := range subnetCR.Status.Conditions {
			if con.Type == v1alpha1.Ready && con.Status == corev1.ConditionTrue {
				realized = true
				break
			}
		}
		if !realized {
			return nil, subnetCR, &errorWithRetry{
				message: fmt.Sprintf("Subnet CR %s is not realized on NSX", name),
				retry:   false,
				error:   err,
			}
		}
		path, err := servicecommon.GetSubnetPathFromAssociatedResource(anno)
		if err != nil {
			// No need to retry as not support associated resource annotation
			// changing after Subnet creation.
			log.Error(err, "Failed to get NSX Subnet path for shared Subnet", "Subnet", subnetKey.String())
			return nil, subnetCR, &errorWithRetry{
				message: fmt.Sprintf("Failed to get NSX Subnet path for shared Subnet %s", name),
				retry:   false,
				error:   err,
			}
		}
		subnetPaths = append(subnetPaths, path)
	} else {
		subnets := r.SubnetService.ListSubnetCreatedBySubnet(string(subnetCR.UID))
		for _, subnet := range subnets {
			subnetPaths = append(subnetPaths, *subnet.Path)
		}
	}

	if len(subnetPaths) == 0 {
		log.Info("NSX VpcSubnets by Subnet CR do not exist", "Subnet", subnetKey.String())
		return nil, subnetCR, &errorWithRetry{
			message: fmt.Sprintf("Subnet CR %s is not realized on NSX", name),
			retry:   false,
			error:   fmt.Errorf("not found NSX VpcSubnets created by Subnet CR '%s/%s'", namespace, name),
		}
	}

	return subnetPaths, subnetCR, nil
}

func (r *Reconciler) validateVpcSubnetsBySubnetSetCR(ctx context.Context, namespace, name string) ([]string, *errorWithRetry) {
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
	if subnetSetCR.Spec.SubnetNames != nil {
		return nil, &errorWithRetry{
			message: fmt.Sprintf("Target SubnetSet %s/%s is a SubnetSet with pre-created Subnets", namespace, name),
			error:   fmt.Errorf("SubnetSet with pre-created Subnets %s/%s cannot be a target SubnetSet", namespace, name),
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
	subnetPaths := make([]string, len(subnets))
	for i := range subnets {
		subnetPaths[i] = *subnets[i].Path
	}
	return subnetPaths, nil
}

// getSubnetConnectionBindingMapsByTargetSubnet lists bindings that reference the Subnet as targetSubnetName
// in the given target Namespace.
func (r *Reconciler) getSubnetConnectionBindingMapsByTargetSubnet(ctx context.Context, targetNs, name string) ([]v1alpha1.SubnetConnectionBindingMap, error) {
	subnetBindingList := &v1alpha1.SubnetConnectionBindingMapList{}
	err := r.Client.List(ctx, subnetBindingList, client.InNamespace(targetNs), client.MatchingFields{"spec.targetSubnetName": name})
	if err != nil {
		return nil, fmt.Errorf("failed to list SubnetConnectionBindingMap CRs: %w", err)
	}
	return subnetBindingList.Items, nil
}

func (r *Reconciler) getSubnetConnectionBindingMapsByHostSubnet(ctx context.Context, ns, name string) ([]v1alpha1.SubnetConnectionBindingMap, error) {
	subnetBindingList := &v1alpha1.SubnetConnectionBindingMapList{}
	err := r.Client.List(ctx, subnetBindingList, client.InNamespace(ns), client.MatchingFields{"spec.subnetName": name})
	if err != nil {
		return nil, fmt.Errorf("failed to list SubnetConnectionBindingMap CRs: %w", err)
	}
	return subnetBindingList.Items, nil
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
	key := types.NamespacedName{Namespace: bindingMap.Namespace, Name: bindingMap.Name}

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Fetch the latest version of the object
		latestBindingMap := &v1alpha1.SubnetConnectionBindingMap{}
		if err := c.Get(ctx, key, latestBindingMap); err != nil {
			return err
		}

		// Check if the update is needed
		newConditions := []v1alpha1.Condition{condition}
		for _, cond := range latestBindingMap.Status.Conditions {
			if cond.Type == condition.Type {
				if cond.Status == condition.Status && cond.Reason == condition.Reason && cond.Message == condition.Message {
					return nil
				}
				continue
			}
			newConditions = append(newConditions, cond)
		}
		latestBindingMap.Status.Conditions = newConditions
		return c.Status().Update(ctx, latestBindingMap)
	})

	if err != nil {
		log.Error(err, "Failed to update SubnetConnectionBindingMap status", "Namespace", bindingMap.Namespace, "Name", bindingMap.Name)
		return
	}
	log.Debug("Updated SubnetConnectionBindingMap status", "Namespace", bindingMap.Namespace, "Name", bindingMap.Name)
}

// subnetConnectionBindingMapSubnetNameIndexFunc is an index function that indexes SubnetConnectionBindingMap by namespace and subnet name
func subnetConnectionBindingMapSubnetNameIndexFunc(obj client.Object) []string {
	if binding, ok := obj.(*v1alpha1.SubnetConnectionBindingMap); !ok {
		log.Info("Invalid object", "type", reflect.TypeOf(obj))
		return []string{}
	} else {
		if binding.Spec.SubnetName == "" {
			return []string{}
		}
		return []string{binding.Spec.SubnetName}
	}
}

// subnetConnectionBindingMapSubnetNameIndexFunc is an index function that indexes SubnetConnectionBindingMap by namespace and subnet name
func subnetConnectionBindingMapTargetSubnetNameIndexFunc(obj client.Object) []string {
	if binding, ok := obj.(*v1alpha1.SubnetConnectionBindingMap); !ok {
		log.Info("Invalid object", "type", reflect.TypeOf(obj))
		return []string{}
	} else {
		if binding.Spec.TargetSubnetName == "" {
			return []string{}
		}
		return []string{binding.Spec.TargetSubnetName}
	}
}

// SetupFieldIndexers sets up the field indexers for SubnetConnectionBindingMap
func (r *Reconciler) SetupFieldIndexers(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.TODO(), &v1alpha1.SubnetConnectionBindingMap{}, "spec.subnetName", subnetConnectionBindingMapSubnetNameIndexFunc); err != nil {
		return err
	}
	if err := mgr.GetFieldIndexer().IndexField(context.TODO(), &v1alpha1.SubnetConnectionBindingMap{}, "spec.targetSubnetName", subnetConnectionBindingMapTargetSubnetNameIndexFunc); err != nil {
		return err
	}
	return nil
}
