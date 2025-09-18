package namespace

import (
	"context"
	"reflect"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

// ReconcileQueue is a type alias for workqueue.TypedRateLimitingInterface[reconcile.Request]
type ReconcileQueue = workqueue.TypedRateLimitingInterface[reconcile.Request]

// EnqueueRequestForVPCNetworkConfiguration is a handler for VPCNetworkConfiguration events
// Namespace Controller watches for VPCNetworkConfiguration create and update event,
// if Namespace VPCNetworkConfiguration CR is created or updated with shared subnets;
// auto-creates a Subnet CR for each shared subnet.
type EnqueueRequestForVPCNetworkConfiguration struct {
	Reconciler *NamespaceReconciler
}

func (e *EnqueueRequestForVPCNetworkConfiguration) Create(ctx context.Context, createEvent event.CreateEvent, q ReconcileQueue) {
	obj := createEvent.Object.(*v1alpha1.VPCNetworkConfiguration)
	log.Info("VPCNetworkConfiguration create event", "Name", obj.Name)
	requeueNamespacesByVPCNetworkConfiguration(ctx, e.Reconciler, obj, q)
}

func (e *EnqueueRequestForVPCNetworkConfiguration) Update(ctx context.Context, updateEvent event.UpdateEvent, q ReconcileQueue) {
	objNew := updateEvent.ObjectNew.(*v1alpha1.VPCNetworkConfiguration)
	log.Info("VPCNetworkConfiguration update event", "Name", objNew.Name)
	requeueNamespacesByVPCNetworkConfiguration(ctx, e.Reconciler, objNew, q)
}

func (e *EnqueueRequestForVPCNetworkConfiguration) Delete(_ context.Context, deleteEvent event.DeleteEvent, _ ReconcileQueue) {
	obj := deleteEvent.Object.(*v1alpha1.VPCNetworkConfiguration)
	log.Debug("VPCNetworkConfiguration delete event, do nothing", "Name", obj.Name)
}

func (e *EnqueueRequestForVPCNetworkConfiguration) Generic(_ context.Context, _ event.GenericEvent, _ ReconcileQueue) {
	log.Debug("VPCNetworkConfiguration generic event, do nothing")
}

// PredicateFuncsVPCNetworkConfig filters VPCNetworkConfiguration events
var PredicateFuncsVPCNetworkConfig = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		return true
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldObj := e.ObjectOld.(*v1alpha1.VPCNetworkConfiguration)
		newObj := e.ObjectNew.(*v1alpha1.VPCNetworkConfiguration)
		if reflect.DeepEqual(oldObj.Spec.Subnets, newObj.Spec.Subnets) {
			log.Debug("Subnets in VPCNetworkConfiguration are not changed, skip processing", "name", oldObj.Name)
			return false
		}
		return true
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return false
	},
}

// requeueNamespacesByVPCNetworkConfiguration enqueues reconcile requests for all namespaces
// that use the given VPCNetworkConfiguration
func requeueNamespacesByVPCNetworkConfiguration(_ context.Context, reconciler *NamespaceReconciler, vpcNetConfig *v1alpha1.VPCNetworkConfiguration, q ReconcileQueue) {
	if reconciler == nil {
		log.Error(nil, "Reconciler is nil, cannot requeue Namespaces")
		return
	}

	// Get all namespaces that use this VPCNetworkConfiguration
	namespaces, err := reconciler.VPCService.GetNamespacesByNetworkconfigName(vpcNetConfig.Name)
	if err != nil {
		log.Error(err, "Failed to get Namespaces for VPCNetworkConfiguration", "Name", vpcNetConfig.Name)
		return
	}

	// Requeue each namespace
	for _, ns := range namespaces {
		req := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name: ns,
			},
		}
		q.Add(req)
		log.Info("Requeue Namespace for VPCNetworkConfiguration", "Namespace", ns)
	}
}
