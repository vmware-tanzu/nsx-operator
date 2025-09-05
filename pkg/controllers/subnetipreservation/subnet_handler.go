package subnetipreservation

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
)

var PredicateFuncsForSubnets = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		return false
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldObj, oldOK := e.ObjectOld.(*v1alpha1.Subnet)
		newObj, newOK := e.ObjectNew.(*v1alpha1.Subnet)
		if !oldOK || !newOK {
			return false
		}
		return common.IsObjectUpdateToReady(oldObj.Status.Conditions, newObj.Status.Conditions) ||
			common.IsObjectUpdateToUnready(oldObj.Status.Conditions, newObj.Status.Conditions)

	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return false
	},
	GenericFunc: func(e event.GenericEvent) bool {
		return false
	},
}

func requeueIPReservationBySubnet(ctx context.Context, c client.Client, _, objNew client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	subnet := objNew.(*v1alpha1.Subnet)
	ipReservationList := &v1alpha1.SubnetIPReservationList{}
	err := c.List(ctx, ipReservationList, client.InNamespace(subnet.Namespace), client.MatchingFields{"spec.subnet": subnet.Name})
	if err != nil {
		log.Error(err, "Failed to list SubnetIPReservations with Subnet for update event", "Subnet", subnet.Namespace, "Subnet", subnet.Name)
		return
	}
	for _, ipr := range ipReservationList.Items {
		log.Info("Requeue SubnetIPReservations because the Subnet realization state is changed", "Namespace", ipr.Namespace, "Name", ipr.Name)
		q.Add(reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      ipr.Name,
				Namespace: ipr.Namespace,
			},
		})
	}
}
