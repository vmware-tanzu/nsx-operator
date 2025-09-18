package inventory

import (
	"context"
	"fmt"

	v1 "k8s.io/api/networking/v1"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/inventory"
)

func watchIngress(c *InventoryController, mgr ctrl.Manager) error {
	ingressInformer, err := mgr.GetCache().GetInformer(context.Background(), &v1.Ingress{})
	if err != nil {
		log.Error(err, "Failed to create Ingress informer")
		return err
	}

	_, err = ingressInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			// Handle Ingress add event
			c.handleIngress(obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			// Handle Ingress update event
			c.handleIngress(newObj)
		},
		DeleteFunc: func(obj interface{}) {
			// Handle Ingress delete event
			c.handleIngress(obj)
		},
	})
	if err != nil {
		log.Error(err, "Failed to add Ingress event handler")
		return err
	}
	return nil
}

func (c *InventoryController) handleIngress(obj interface{}) {
	var ingress *v1.Ingress
	ok := false
	switch obj1 := obj.(type) {
	case *v1.Ingress:
		ingress = obj1
	case cache.DeletedFinalStateUnknown:
		ingress, ok = obj1.Obj.(*v1.Ingress)
		if !ok {
			err := fmt.Errorf("obj is not valid *v1.Ingress")
			log.Error(err, "DeletedFinalStateUnknown Obj is not *v1.Ingress")
			return
		}
	}
	log.Debug("Inventory processing Ingress", "Namespace", ingress.Namespace, "Name", ingress.Name)
	key, _ := keyFunc(ingress)
	log.Debug("Adding Ingress key to inventory object queue", "Ingress key", key)
	c.inventoryObjectQueue.Add(inventory.InventoryKey{InventoryType: inventory.ContainerIngressPolicy, ExternalId: string(ingress.UID), Key: key})
}
