package inventory

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/inventory"
)

func watchNamespace(c *InventoryController, mgr ctrl.Manager) error {
	namespaceInformer, err := mgr.GetCache().GetInformer(context.Background(), &v1.Namespace{})
	if err != nil {
		log.Error(err, "Failed to create Namespace informer")
		return err
	}

	_, err = namespaceInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			// Handle Namespace add event
			c.handleNamespace(obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			// Handle Namespace update event
			c.handleNamespace(newObj)
		},
		DeleteFunc: func(obj interface{}) {
			// Handle Namespace delete event
			c.handleNamespace(obj)
		},
	})
	if err != nil {
		log.Error(err, "Failed to add Namespace event handler")
		return err
	}
	return nil
}

func (c *InventoryController) handleNamespace(obj interface{}) {
	var ns *v1.Namespace
	ok := false
	switch obj1 := obj.(type) {
	case *v1.Namespace:
		ns = obj1
	case cache.DeletedFinalStateUnknown:
		ns, ok = obj1.Obj.(*v1.Namespace)
		if !ok {
			err := fmt.Errorf("obj is not valid *v1.Namespace")
			log.Error(err, "DeletedFinalStateUnknown Obj is not *v1.Namespace")
			return
		}
	}
	log.V(1).Info("Inventory processing Namespace", "name", ns.Name)
	// key is ObjectName{Namespace: "", Name: obj.GetName()}
	key, _ := keyFunc(ns)
	log.V(1).Info("Adding Namespace key to inventory object queue", "Namespace key", key)
	c.inventoryObjectQueue.Add(inventory.InventoryKey{InventoryType: inventory.ContainerProject, ExternalId: string(ns.UID), Key: key})
}
