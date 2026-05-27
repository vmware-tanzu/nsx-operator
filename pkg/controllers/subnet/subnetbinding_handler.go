package subnet

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

func requeueSubnetBySubnetBindingCreate(ctx context.Context, c client.Client, objNew client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	newBM := objNew.(*v1alpha1.SubnetConnectionBindingMap)
	enqueueSubnets(ctx, c, newBM, q)
}

func requeueSubnetBySubnetBindingUpdate(ctx context.Context, c client.Client, objOld client.Object, objNew client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	newBM := objNew.(*v1alpha1.SubnetConnectionBindingMap)
	oldBM := objOld.(*v1alpha1.SubnetConnectionBindingMap)

	if newBM.Spec.TargetSubnetName == oldBM.Spec.TargetSubnetName &&
		newBM.Spec.TargetSubnetNamespace == oldBM.Spec.TargetSubnetNamespace {
		return
	}

	if newBM.Spec.TargetSubnetName != "" {
		targetNs := newBM.Spec.ResolveTargetSubnetNamespace(newBM.Namespace)
		if err := enqueue(ctx, c, targetNs, newBM.Spec.TargetSubnetName, q); err != nil {
			log.Error(err, "Failed to enqueue the new target Subnet after SubnetConnectionBindingMap updates", "Namespace", targetNs, "Subnet", newBM.Spec.TargetSubnetName, "SubnetConnectionBindingMap", newBM.Name)
		}
	}
	if oldBM.Spec.TargetSubnetName != "" {
		targetNs := oldBM.Spec.ResolveTargetSubnetNamespace(oldBM.Namespace)
		_ = enqueue(ctx, c, targetNs, oldBM.Spec.TargetSubnetName, q)
	}
}

func enqueueSubnets(ctx context.Context, c client.Client, bindingMap *v1alpha1.SubnetConnectionBindingMap, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	if err := enqueue(ctx, c, bindingMap.Namespace, bindingMap.Spec.SubnetName, q); err != nil {
		return
	}

	if bindingMap.Spec.TargetSubnetName != "" {
		targetNs := bindingMap.Spec.ResolveTargetSubnetNamespace(bindingMap.Namespace)
		_ = enqueue(ctx, c, targetNs, bindingMap.Spec.TargetSubnetName, q)
	}
}

func enqueue(ctx context.Context, c client.Client, namespace, name string, q workqueue.TypedRateLimitingInterface[reconcile.Request]) error {
	subnetCR := &v1alpha1.Subnet{}
	subnetKey := types.NamespacedName{Namespace: namespace, Name: name}
	err := c.Get(ctx, subnetKey, subnetCR)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("Subnet CR doesn't exist, ignore it", "Subnet", subnetKey.String())
			return nil
		}
		log.Error(err, "Failed to get Subnet CR", "Subnet", subnetKey.String())
		return err
	}
	log.Debug("Enqueue Subnet CR", "Subnet", subnetKey.String())
	req := reconcile.Request{NamespacedName: subnetKey}
	q.Add(req)
	return nil
}

func requeueSubnetBySubnetBindingDelete(ctx context.Context, c client.Client, obj client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	bindingMap := obj.(*v1alpha1.SubnetConnectionBindingMap)
	enqueueSubnets(ctx, c, bindingMap, q)
}

func (r *SubnetReconciler) getNSXSubnetBindingsBySubnet(subnetCRUID string) []*v1alpha1.SubnetConnectionBindingMap {
	vpcSubnets := r.SubnetService.ListSubnetCreatedBySubnet(subnetCRUID)
	if len(vpcSubnets) == 0 {
		log.Debug("No VpcSubnet found with Subnet", "SubnetID", subnetCRUID)
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

func (r *SubnetReconciler) getSubnetBindingCRsBySubnet(ctx context.Context, subnetCR *v1alpha1.Subnet) []v1alpha1.SubnetConnectionBindingMap {
	validBindings := make([]v1alpha1.SubnetConnectionBindingMap, 0)
	bindingList := &v1alpha1.SubnetConnectionBindingMapList{}
	err := r.Client.List(ctx, bindingList)
	if err != nil {
		log.Error(err, "Unable to list SubnetConnectionBindingMaps")
		return validBindings
	}
	for _, bm := range bindingList.Items {
		if bm.Spec.SubnetName == subnetCR.Name && bm.Namespace == subnetCR.Namespace {
			validBindings = append(validBindings, bm)
			continue
		}
		if bm.Spec.TargetSubnetName == subnetCR.Name && bm.Spec.ResolveTargetSubnetNamespace(bm.Namespace) == subnetCR.Namespace {
			validBindings = append(validBindings, bm)
		}
	}
	return validBindings
}
