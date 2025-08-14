package inventory

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/inventory"
)

func watchNode(c *InventoryController, mgr ctrl.Manager) error {
	nodeInformer, err := mgr.GetCache().GetInformer(context.Background(), &v1.Node{})
	if err != nil {
		log.Error(err, "Failed to create Node informer")
		return err
	}

	_, err = nodeInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			// Handle Node add event
			c.handleNode(obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			// Handle Node update event
			c.handleNode(newObj)
		},
		DeleteFunc: func(obj interface{}) {
			// Handle Node delete event
			c.handleNode(obj)
		},
	})
	if err != nil {
		log.Error(err, "Failed to add Node event handler")
		return err
	}
	return nil
}

func (c *InventoryController) handleNode(obj interface{}) {
	var node *v1.Node
	ok := false
	switch obj1 := obj.(type) {
	case *v1.Node:
		node = obj1
	case cache.DeletedFinalStateUnknown:
		node, ok = obj1.Obj.(*v1.Node)
		if !ok {
			err := fmt.Errorf("obj is not valid *v1.Node")
			log.Error(err, "DeletedFinalStateUnknown Obj is not *v1.Node")
			return
		}
	}
	log.Debug("Inventory processing Node", "name", node.Name)
	// key is ObjectName{Namespace: "", Name: obj.GetName()}
	key, _ := keyFunc(node)
	log.Debug("Adding Node key to inventory object queue", "Node key", key)
	c.inventoryObjectQueue.Add(inventory.InventoryKey{InventoryType: inventory.ContainerClusterNode, ExternalId: string(node.UID), Key: key})
}
