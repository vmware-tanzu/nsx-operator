package inventory

import (
	"context"
	"fmt"

	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/inventory"
)

func watchNetworkPolicy(c *InventoryController, mgr ctrl.Manager) error {
	networkPolicyInformer, err := mgr.GetCache().GetInformer(context.Background(), &networkingv1.NetworkPolicy{})
	if err != nil {
		log.Error(err, "Failed to create NetworkPolicy informer")
		return err
	}

	_, err = networkPolicyInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			c.handleNetworkPolicy(obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			c.handleNetworkPolicy(newObj)
		},
		DeleteFunc: func(obj interface{}) {
			c.handleNetworkPolicy(obj)
		},
	})
	if err != nil {
		log.Error(err, "Failed to add NetworkPolicy event handler")
		return err
	}
	return nil
}

func (c *InventoryController) handleNetworkPolicy(obj interface{}) {
	var networkPolicy *networkingv1.NetworkPolicy
	ok := false
	switch obj1 := obj.(type) {
	case *networkingv1.NetworkPolicy:
		networkPolicy = obj1
	case cache.DeletedFinalStateUnknown:
		networkPolicy, ok = obj1.Obj.(*networkingv1.NetworkPolicy)
		if !ok {
			err := fmt.Errorf("obj is not valid *networkingv1.NetworkPolicy")
			log.Error(err, "DeletedFinalStateUnknown Obj is not *networkingv1.NetworkPolicy")
			return
		}
	}
	log.V(1).Info("Inventory processing NetworkPolicy", "Name", networkPolicy.Name, "Namespace", networkPolicy.Namespace)
	key, err := keyFunc(networkPolicy)
	if err != nil {
		log.Error(err, "Failed to get key for NetworkPolicy", "NetworkPolicy", networkPolicy)
		return
	}
	log.V(1).Info("Adding NetworkPolicy key to inventory object queue", "NetworkPolicy key", key)
	c.inventoryObjectQueue.Add(inventory.InventoryKey{InventoryType: inventory.ContainerNetworkPolicy, ExternalId: string(networkPolicy.UID), Key: key})
}
