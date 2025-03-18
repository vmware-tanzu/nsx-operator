package inventory

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/inventory"
)

func watchPod(c *InventoryController, mgr ctrl.Manager) error {
	podInformer, err := mgr.GetCache().GetInformer(context.Background(), &v1.Pod{})
	if err != nil {
		log.Error(err, "Failed to create Pod informer")
		return err
	}

	_, err = podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			// Handle Pod add event
			c.handlePod(obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			// Handle Pod update event
			c.handlePod(newObj)
		},
		DeleteFunc: func(obj interface{}) {
			// Handle Pod delete event
			c.handlePod(obj)
		},
	})
	if err != nil {
		log.Error(err, "Failed to add Pod event handler")
		return err
	}
	return nil
}

func (c *InventoryController) handlePod(obj interface{}) {
	var pod *v1.Pod
	ok := false
	switch obj1 := obj.(type) {
	case *v1.Pod:
		pod = obj1
	case cache.DeletedFinalStateUnknown:
		pod, ok = obj1.Obj.(*v1.Pod)
		if !ok {
			err := fmt.Errorf("obj is not valid *v1.Pod")
			log.Error(err, "DeletedFinalStateUnknown Obj is not *v1.Pod")
			return
		}
	}
	log.V(1).Info("Inventory processing Pod", "namespace", pod.Namespace, "name", pod.Name)
	key, _ := keyFunc(pod)
	log.V(1).Info("Adding Pod key to inventory object queue", "Pod key", key)
	c.inventoryObjectQueue.Add(inventory.InventoryKey{InventoryType: inventory.ContainerApplicationInstance, ExternalId: string(pod.UID), Key: key})
}
