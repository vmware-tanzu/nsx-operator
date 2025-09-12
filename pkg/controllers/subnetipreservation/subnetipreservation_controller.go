package subnetipreservation

import (
	"context"
	"fmt"
	"reflect"
	"time"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetipreservation"
)

var (
	log = &logger.Log
)

type errorWithRetry struct {
	error
	retry   bool
	message string
}

// Reconciler reconciles a SubnetIPReservation object
type Reconciler struct {
	Client               client.Client
	Scheme               *runtime.Scheme
	IPReservationService *subnetipreservation.IPReservationService
	SubnetService        servicecommon.SubnetServiceProvider
	StatusUpdater        common.StatusUpdater
}

func (r *Reconciler) RestoreReconcile() error {
	return nil
}

func NewReconciler(mgr ctrl.Manager, ipReservationService *subnetipreservation.IPReservationService, subnetService servicecommon.SubnetServiceProvider) *Reconciler {
	recorder := mgr.GetEventRecorderFor("subnetipreservation-controller")
	// Create the SubnetIPReservation Reconciler with the necessary services and configuration
	return &Reconciler{
		Client:               mgr.GetClient(),
		Scheme:               mgr.GetScheme(),
		IPReservationService: ipReservationService,
		SubnetService:        subnetService,
		StatusUpdater:        common.NewStatusUpdater(mgr.GetClient(), ipReservationService.NSXConfig, recorder, common.MetricResTypeSubnetIPReservation, "SubnetIPReservation", "SubnetIPReservation"),
	}
}

func (r *Reconciler) StartController(mgr ctrl.Manager, _ webhook.Server) error {
	// Start the controller
	if err := r.setupWithManager(mgr); err != nil {
		log.Error(err, "Failed to create controller", "controller", "SubnetIPReservation")
		return err
	}
	// Setup field indexers
	if err := r.SetupFieldIndexers(mgr); err != nil {
		log.Error(err, "Failed to setup field indexers", "controller", "SubnetIPReservation")
		return err
	}
	// Start garbage collector in a separate goroutine
	go common.GenericGarbageCollector(make(chan bool), servicecommon.GCInterval, r.CollectGarbage)
	return nil
}

// SetupFieldIndexers sets up the field indexers for SubnetIPReservation
func (r *Reconciler) SetupFieldIndexers(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.TODO(), &v1alpha1.SubnetIPReservation{}, "spec.subnet", subnetIPReservationSubnetNameIndexFunc); err != nil {
		return err
	}
	return nil
}

// subnetIPReservationSubnetNameIndexFunc is an index function that indexes SubnetIPReservation by subnet name
func subnetIPReservationSubnetNameIndexFunc(obj client.Object) []string {
	if ipr, ok := obj.(*v1alpha1.SubnetIPReservation); !ok {
		log.Info("Invalid object", "type", reflect.TypeOf(obj))
		return []string{}
	} else {
		if ipr.Spec.Subnet == "" {
			return []string{}
		}
		return []string{ipr.Spec.Subnet}
	}
}

func (r *Reconciler) setupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.SubnetIPReservation{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: common.NumReconcile(),
		}).
		Watches(
			&v1alpha1.Subnet{},
			&common.EnqueueRequestForDependency{
				Client:          r.Client,
				RequeueByUpdate: requeueIPReservationBySubnet,
				ResourceType:    "Subnet",
			},
			builder.WithPredicates(PredicateFuncsForSubnets),
		).
		Complete(r)
}

// +kubebuilder:rbac:groups=crd.nsx.vmware.com,resources=subnetipreservation,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=crd.nsx.vmware.com,resources=subnetipreservation/status,verbs=get;update;patch
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	startTime := time.Now()
	defer func() {
		log.Info("Finished reconciling SubnetIPReservation", "SubnetIPReservation", req.NamespacedName, "duration(ms)", time.Since(startTime).Milliseconds())
	}()
	r.StatusUpdater.IncreaseSyncTotal()

	ipReservationCR := &v1alpha1.SubnetIPReservation{}
	if err := r.Client.Get(ctx, req.NamespacedName, ipReservationCR); err != nil {
		if apierrors.IsNotFound(err) {
			r.StatusUpdater.IncreaseDeleteTotal()
			if err := r.IPReservationService.DeleteIPReservationByCRName(req.Namespace, req.Name); err != nil {
				log.Error(err, "Failed to delete NSX SubnetIPReservation", "SubnetIPReservation", req.NamespacedName)
				r.StatusUpdater.DeleteFail(req.NamespacedName, nil, err)
				return common.ResultRequeue, nil
			}
			r.StatusUpdater.DeleteSuccess(req.NamespacedName, nil)
			return common.ResultNormal, nil
		}
		log.Error(err, "Unable to fetch SubnetIPReservation CR", "SubnetIPReservation", req.NamespacedName)
		return common.ResultRequeue, nil
	}

	// Delete the NSX Subnet IPReservation if the CR is marked for delete
	if !ipReservationCR.ObjectMeta.DeletionTimestamp.IsZero() {
		r.StatusUpdater.IncreaseDeleteTotal()
		if err := r.IPReservationService.DeleteIPReservationByCRId(string(ipReservationCR.UID)); err != nil {
			r.StatusUpdater.DeleteFail(req.NamespacedName, nil, err)
			return common.ResultRequeue, nil
		}
		r.StatusUpdater.DeleteSuccess(req.NamespacedName, nil)
		return common.ResultNormal, nil
	}

	// Check if the Subnet is realized
	r.StatusUpdater.IncreaseUpdateTotal()
	subnetCR, validateErr := r.validateSubnet(ctx, req.Namespace, ipReservationCR.Spec.Subnet)
	if validateErr != nil {
		r.StatusUpdater.UpdateFail(ctx, ipReservationCR, validateErr.error, validateErr.message, setReadyStatusFalse)
		if validateErr.retry {
			return common.ResultRequeue, nil
		}
		return common.ResultNormal, nil
	}

	nsxSubnet, err := r.SubnetService.GetSubnetByCR(subnetCR)
	if err != nil {
		log.Error(err, "failed to get NSX Subnet", "Namespace", subnetCR.Namespace, "Subnet", subnetCR.Name)
		r.StatusUpdater.UpdateFail(ctx, ipReservationCR, err, "failed to get NSX Subnet", setReadyStatusFalse)
		return common.ResultRequeue, nil
	}

	// Create or update SubnetIPReservation
	nsxIPReservation, err := r.IPReservationService.GetOrCreateSubnetIPReservation(ipReservationCR, *nsxSubnet.Path)
	if err != nil {
		log.Error(err, "Failed to get or create NSX SubnetIPReservations")
		r.StatusUpdater.UpdateFail(ctx, ipReservationCR, err, "Failed to get or create NSX SubnetIPReservations", setReadyStatusFalse)
		return common.ResultRequeue, nil
	}

	ipReservationCR.Status.IPs = nsxIPReservation.Ips
	// Update SubnetIPReservation with ready condition
	r.StatusUpdater.UpdateSuccess(ctx, ipReservationCR, setReadyStatusTrue)
	return common.ResultNormal, nil
}

func (r *Reconciler) validateSubnet(ctx context.Context, ns, name string) (*v1alpha1.Subnet, *errorWithRetry) {
	subnetCR := &v1alpha1.Subnet{}
	if err := r.Client.Get(ctx, types.NamespacedName{
		Namespace: ns,
		Name:      name,
	}, subnetCR); err != nil {
		if apierrors.IsNotFound(err) {
			// Not requeue the SubnetIPReservation if Subnet is not created
			log.V(1).Info("Subnet CR does not exists", "Namespace", ns, "Subnet", name)
			return nil, &errorWithRetry{
				error:   err,
				retry:   false,
				message: "Subnet is not created",
			}
		}
		// Retry if fail to get the Subnet due to k8s error
		return nil, &errorWithRetry{
			error:   err,
			retry:   true,
			message: "failed to get Subnet",
		}
	}
	subnetRealized := false
	for _, con := range subnetCR.Status.Conditions {
		if con.Type == v1alpha1.Ready && con.Status == v1.ConditionTrue {
			subnetRealized = true
			break
		}
	}
	if !subnetRealized {
		// Not requeue the SubnetIPReservation if Subnet is not realized
		log.V(1).Info("Subnet is not realized", "Namespace", ns, "Subnet", name)
		return nil, &errorWithRetry{
			error:   fmt.Errorf("Subnet is not realized"),
			retry:   false,
			message: "Subnet is not realized",
		}
	}
	return subnetCR, nil
}

func setReadyStatusTrue(client client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, _ ...interface{}) {
	ipReservationCR := obj.(*v1alpha1.SubnetIPReservation)
	newConditions := []v1alpha1.Condition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionTrue,
			Message:            "NSX SubnetIPReservation has been successfully created/updated",
			Reason:             "SubnetIPReservationReady",
			LastTransitionTime: transitionTime,
		},
	}
	updateStatusConditions(client, ctx, ipReservationCR, newConditions)
}

func setReadyStatusFalse(client client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, err error, args ...interface{}) {
	ipReservationCR := obj.(*v1alpha1.SubnetIPReservation)
	newConditions := []v1alpha1.Condition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionFalse,
			Message:            "NSX SubnetIPReservation could not be created/updated",
			Reason:             "SubnetIPReservationNotReady",
			LastTransitionTime: transitionTime,
		},
	}
	if len(args) > 0 {
		newConditions[0].Message = args[0].(string)
	} else if err != nil {
		newConditions[0].Message = fmt.Sprintf("Error occurred while processing the SubnetIPReservation CR. Please check the config and try again. Error: %v", err)
	}
	updateStatusConditions(client, ctx, ipReservationCR, newConditions)
}

func updateStatusConditions(client client.Client, ctx context.Context, ipReservation *v1alpha1.SubnetIPReservation, newConditions []v1alpha1.Condition) {
	conditionsUpdated := false
	for i := range newConditions {
		if mergeStatusCondition(ipReservation, &newConditions[i]) {
			conditionsUpdated = true
		}
	}
	if conditionsUpdated {
		if err := client.Status().Update(ctx, ipReservation); err != nil {
			log.Error(err, "Failed to update SubnetIPReservation status", "Name", ipReservation.Name, "Namespace", ipReservation.Namespace)
		} else {
			log.Info("Updated SubnetIPReservation", "Name", ipReservation.Name, "Namespace", ipReservation.Namespace, "New Conditions", newConditions)
		}
	}
}

func mergeStatusCondition(ipReservation *v1alpha1.SubnetIPReservation, newCondition *v1alpha1.Condition) bool {
	matchedCondition := getExistingConditionOfType(newCondition.Type, ipReservation.Status.Conditions)
	if reflect.DeepEqual(matchedCondition, newCondition) {
		log.V(2).Info("Conditions already match", "New Condition", newCondition, "Existing Condition", matchedCondition)
		return false
	}

	if matchedCondition != nil {
		matchedCondition.Reason = newCondition.Reason
		matchedCondition.Message = newCondition.Message
		matchedCondition.Status = newCondition.Status
	} else {
		ipReservation.Status.Conditions = append(ipReservation.Status.Conditions, *newCondition)
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

// CollectGarbage collects the stale SubnetIPReservations and deletes them on NSX which have been removed from K8s.
// It implements the interface GarbageCollector method.
func (r *Reconciler) CollectGarbage(ctx context.Context) error {
	startTime := time.Now()
	defer func() {
		log.Info("SubnetIPReservation garbage collection completed", "duration(ms)", time.Since(startTime).Milliseconds())
	}()

	ipReservationIdSetByCRs, err := r.listIPReservationIDsFromCRs(ctx)
	if err != nil {
		log.Error(err, "Failed to list SubnetIPReservation CRs")
		return err
	}
	ipReservationIdSetInStore := r.IPReservationService.ListSubnetIPReservationCRUIDsInStore()

	var errList []error
	for uid := range ipReservationIdSetInStore.Difference(ipReservationIdSetByCRs) {
		log.V(2).Info("GC collected SubnetIPReservation CR", "UID", uid)
		r.StatusUpdater.IncreaseDeleteTotal()
		err = r.IPReservationService.DeleteIPReservationByCRId(uid)
		if err != nil {
			errList = append(errList, err)
			r.StatusUpdater.IncreaseDeleteFailTotal()
		} else {
			r.StatusUpdater.IncreaseDeleteSuccessTotal()
		}
	}
	if len(errList) > 0 {
		return fmt.Errorf("errors found in SubnetIPReservation garbage collection: %s", errList)
	}
	return nil
}

func (r *Reconciler) listIPReservationIDsFromCRs(ctx context.Context) (sets.Set[string], error) {
	iprIDs := sets.New[string]()
	iprList := &v1alpha1.SubnetIPReservationList{}
	err := r.Client.List(ctx, iprList)
	if err != nil {
		return nil, err
	}
	for _, ipr := range iprList.Items {
		iprIDs.Insert(string(ipr.UID))
	}
	return iprIDs, nil
}
