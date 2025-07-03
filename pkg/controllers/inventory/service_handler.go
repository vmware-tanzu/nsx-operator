package inventory

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/inventory"
)

func watchService(c *InventoryController, mgr ctrl.Manager) error {
	serviceInformer, err := mgr.GetCache().GetInformer(context.Background(), &v1.Service{})
	if err != nil {
		log.Error(err, "Failed to create Service informer")
		return err
	}

	_, err = serviceInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			c.handleService(obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			c.handleService(newObj)
		},
		DeleteFunc: func(obj interface{}) {
			c.handleService(obj)
		},
	})
	if err != nil {
		log.Error(err, "Failed to add Service event handler")
		return err
	}
	return nil
}

func (c *InventoryController) handleService(obj interface{}) {
	var service *v1.Service
	ok := false
	switch obj1 := obj.(type) {
	case *v1.Service:
		service = obj1
	case cache.DeletedFinalStateUnknown:
		service, ok = obj1.Obj.(*v1.Service)
		if !ok {
			err := fmt.Errorf("obj is not valid *v1.Service")
			log.Error(err, "DeletedFinalStateUnknown Obj is not *v1.Service")
			return
		}
	}
	log.Debug("Inventory processing Service", "Service", service.Name, "Namespace", service.Namespace)
	key, _ := keyFunc(service)
	log.Debug("Adding Service key to inventory object queue", "Service key", key)
	c.inventoryObjectQueue.Add(inventory.InventoryKey{InventoryType: inventory.ContainerApplication, ExternalId: string(service.UID), Key: key})
}
