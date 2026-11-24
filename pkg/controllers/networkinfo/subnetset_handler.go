package networkinfo

import (
	"context"
	"reflect"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	controllercommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
)

type SubnetSetHandler struct {
	Client client.Client
}

func (h *SubnetSetHandler) Create(ctx context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	for _, req := range networkInfosInNamespace(ctx, h.Client, e.Object.GetNamespace()) {
		q.Add(req)
	}
}

func (h *SubnetSetHandler) Delete(ctx context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	log.Debug("Requeue NetworkInfo when default SubnetSet is deleted")
	obj := e.Object.(*v1alpha1.SubnetSet)
	q.Add(reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      obj.Namespace,
			Namespace: obj.Namespace,
		},
	})
}

func (h *SubnetSetHandler) Generic(_ context.Context, _ event.GenericEvent, _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	log.Debug("SubnetSet generic event, do nothing")
}

func (h *SubnetSetHandler) Update(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	for _, req := range networkInfosInNamespace(ctx, h.Client, e.ObjectNew.GetNamespace()) {
		q.Add(req)
	}
}

var PredicateFuncsSubnetSet = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		return controllercommon.IsDefaultSubnetSet(e.Object.(*v1alpha1.SubnetSet))
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldSet, newSet := e.ObjectOld.(*v1alpha1.SubnetSet), e.ObjectNew.(*v1alpha1.SubnetSet)
		return controllercommon.IsDefaultSubnetSet(newSet) &&
			(controllercommon.IsObjectReady(oldSet.Status.Conditions) != controllercommon.IsObjectReady(newSet.Status.Conditions) ||
				!reflect.DeepEqual(oldSet.Spec.SubnetNames, newSet.Spec.SubnetNames))
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		obj := e.Object.(*v1alpha1.SubnetSet)
		if controllercommon.IsDefaultSubnetSet(obj) {
			log.Debug("Delete default SubnetSet", "Namespace", obj.Namespace, "SubnetSet", obj.Name)
			return true
		}
		return false
	},
	GenericFunc: func(genericEvent event.GenericEvent) bool {
		return false
	},
}
