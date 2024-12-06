package subnetbinding

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

var PredicateFuncsForSubnetSets = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		return false
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldObj, oldOK := e.ObjectOld.(*v1alpha1.SubnetSet)
		newObj, newOK := e.ObjectNew.(*v1alpha1.SubnetSet)
		if !oldOK || !newOK {
			return false
		}
		return !reflect.DeepEqual(oldObj.Status.Subnets, newObj.Status.Subnets)
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return false
	},
	GenericFunc: func(e event.GenericEvent) bool {
		return false
	},
}

func requeueBindingMapsBySubnetUpdate(ctx context.Context, c client.Client, _, objNew client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	subnet := objNew.(*v1alpha1.Subnet)
	requeueSubnetConnectionBindingMapsBySubnet(ctx, c, subnet.Namespace, subnet.Name, q)
}

func requeueSubnetConnectionBindingMapsBySubnet(ctx context.Context, c client.Client, namespace string, subnet string, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	bindingMapList := &v1alpha1.SubnetConnectionBindingMapList{}
	err := c.List(ctx, bindingMapList, client.InNamespace(namespace))
	if err != nil {
		log.Error(err, "Failed to list SubnetConnectionBindingMaps with Subnet event", "Namespace", namespace, "Subnet", subnet)
		return
	}
	for _, bm := range bindingMapList.Items {
		if bm.Spec.SubnetName == subnet || bm.Spec.TargetSubnetName == subnet {
			log.Info("Requeue SubnetConnectionBindingMap because the dependent Subnet realization state is changed", "Namespace", namespace, "Name", bm.Name)
			q.Add(reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      bm.Name,
					Namespace: bm.Namespace,
				},
			})
		}
	}
}

func requeueBindingMapsBySubnetSetUpdate(ctx context.Context, c client.Client, objOld, objNew client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	subnetSet := objNew.(*v1alpha1.SubnetSet)
	requeueSubnetConnectionBindingMapsBySubnetSet(ctx, c, subnetSet.Namespace, subnetSet.Name, q)
}

func requeueSubnetConnectionBindingMapsBySubnetSet(ctx context.Context, c client.Client, namespace string, subnetSet string, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	bindingMapList := &v1alpha1.SubnetConnectionBindingMapList{}
	err := c.List(ctx, bindingMapList, client.InNamespace(namespace))
	if err != nil {
		log.Error(err, "Failed to list SubnetConnectionBindingMaps with SubnetSet event", "Namespace", namespace, "SubnetSet", subnetSet)
		return
	}
	for _, bm := range bindingMapList.Items {
		if bm.Spec.TargetSubnetSetName == subnetSet {
			log.Info("Requeue SubnetConnectionBindingMap because the dependent SubnetSet is ready or updated", "Namespace", namespace, "Name", bm.Name)
			q.Add(reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      bm.Name,
					Namespace: bm.Namespace,
				},
			})
		}
	}
}
