package subnetset

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

func requeueSubnetSetBySubnetBindingCreate(ctx context.Context, c client.Client, obj client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	bindingMap := obj.(*v1alpha1.SubnetConnectionBindingMap)
	if bindingMap.Spec.TargetSubnetSetName != "" {
		enqueue(ctx, c, bindingMap.Namespace, bindingMap.Spec.TargetSubnetSetName, q)
	}
}

func requeueSubnetSetBySubnetBindingUpdate(ctx context.Context, c client.Client, objOld, objNew client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	newBM := objNew.(*v1alpha1.SubnetConnectionBindingMap)
	oldBM := objOld.(*v1alpha1.SubnetConnectionBindingMap)

	if newBM.Spec.TargetSubnetSetName == oldBM.Spec.TargetSubnetSetName {
		return
	}

	if newBM.Spec.TargetSubnetSetName != "" {
		if err := enqueue(ctx, c, newBM.Namespace, newBM.Spec.TargetSubnetSetName, q); err != nil {
			log.Error(err, "Failed to enqueue the new target SubnetSet after SubnetConnectionBindingMap updates", "Namespace", newBM.Namespace, "SubnetSet", newBM.Spec.TargetSubnetSetName, "SubnetConnectionBindingMap", newBM.Name)
		}
	}

	if oldBM.Spec.TargetSubnetSetName != "" {
		// Enqueue to ensure the finalizer can be removed from old target SubnetSet if it is not used.
		enqueue(ctx, c, oldBM.Namespace, oldBM.Spec.TargetSubnetSetName, q)
	}
}

func enqueue(ctx context.Context, c client.Client, namespace, name string, q workqueue.TypedRateLimitingInterface[reconcile.Request]) error {
	subnetSetCR := &v1alpha1.SubnetSet{}
	subnetSetKey := types.NamespacedName{Namespace: namespace, Name: name}
	err := c.Get(ctx, subnetSetKey, subnetSetCR)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("SubnetSet CR doesn't exist, ignore it", "SubnetSet", subnetSetKey.String())
			return nil
		}
		log.Error(err, "Failed to get SubnetSet CR", "SubnetSet", subnetSetKey.String())
		return err
	}

	req := reconcile.Request{NamespacedName: subnetSetKey}
	q.Add(req)
	log.Info("Requeue SubnetSet", "SubnetSet", subnetSetKey.String())
	return nil
}

func requeueSubnetSetBySubnetBindingDelete(ctx context.Context, c client.Client, obj client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	bindingMap := obj.(*v1alpha1.SubnetConnectionBindingMap)
	if bindingMap.Spec.TargetSubnetSetName != "" {
		enqueue(ctx, c, bindingMap.Namespace, bindingMap.Spec.TargetSubnetSetName, q)
	}
}

func (r *SubnetSetReconciler) getNSXSubnetBindingsBySubnetSet(subnetSetCRUID string) []*v1alpha1.SubnetConnectionBindingMap {
	vpcSubnets := r.SubnetService.ListSubnetCreatedBySubnetSet(subnetSetCRUID)
	if len(vpcSubnets) == 0 {
		log.Info("No VpcSubnet found with SubnetSet", "SubnetSetID", subnetSetCRUID)
		return nil
	}
	bindingMaps := make([]*v1alpha1.SubnetConnectionBindingMap, 0)
	for _, vpcSubnet := range vpcSubnets {
		bindings := r.BindingService.GetSubnetConnectionBindingMapCRsBySubnet(vpcSubnet)
		if len(bindings) > 0 {
			bindingMaps = append(bindingMaps, bindings...)
		}
	}
	return bindingMaps
}

func (r *SubnetSetReconciler) getSubnetBindingCRsBySubnetSet(ctx context.Context, subnetSetCR *v1alpha1.SubnetSet) []v1alpha1.SubnetConnectionBindingMap {
	validBindings := make([]v1alpha1.SubnetConnectionBindingMap, 0)
	bindingList := &v1alpha1.SubnetConnectionBindingMapList{}
	err := r.Client.List(ctx, bindingList, client.InNamespace(subnetSetCR.Namespace))
	if err != nil {
		log.Error(err, "Unable to list SubnetConnectionBindingMaps", "Namespace", subnetSetCR.Namespace)
		return validBindings
	}
	for _, bm := range bindingList.Items {
		if bm.Spec.TargetSubnetSetName == subnetSetCR.Name {
			validBindings = append(validBindings, bm)
		}
	}
	return validBindings
}
