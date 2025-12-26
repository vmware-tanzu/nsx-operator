package networkinfo

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type SubnetSetHandler struct {
	Client client.Client
}

func (h *SubnetSetHandler) Create(_ context.Context, _ event.CreateEvent, _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	log.Debug("SubnetSet create event, do nothing")
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

func (h *SubnetSetHandler) Update(_ context.Context, _ event.UpdateEvent, _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	log.Debug("SubnetSet update event, do nothing")
}

var PredicateFuncsSubnetSet = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		return false
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		return false
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		obj := e.Object.(*v1alpha1.SubnetSet)
		if isDefaultSubnetSet(obj) {
			log.Debug("Delete default SubnetSet", "Namespace", obj.Namespace, "SubnetSet", obj.Name)
			return true
		}
		return false
	},
	GenericFunc: func(genericEvent event.GenericEvent) bool {
		return false
	},
}

func isDefaultSubnetSet(s *v1alpha1.SubnetSet) bool {
	if _, ok := s.Labels[common.LabelDefaultNetwork]; ok {
		return true
	}
	return s.Name == common.DefaultVMSubnetSet || s.Name == common.DefaultPodSubnetSet
}
