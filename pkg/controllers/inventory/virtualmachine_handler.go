package inventory

import (
	"context"
	"fmt"

	vmv1alpha1 "github.com/vmware-tanzu/vm-operator/api/v1alpha1"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/inventory"
)

func watchVirtualMachine(c *InventoryController, mgr ctrl.Manager) error {
	vmInformer, err := mgr.GetCache().GetInformer(context.Background(), &vmv1alpha1.VirtualMachine{})
	if err != nil {
		log.Error(err, "Failed to create VirtualMachine informer")
		return err
	}

	_, err = vmInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			c.handleVirtualMachine(obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			c.handleVirtualMachine(newObj)
		},
		DeleteFunc: func(obj interface{}) {},
	})
	if err != nil {
		log.Error(err, "Failed to add VirtualMachine event handler")
		return err
	}
	return nil
}

func (c *InventoryController) handleVirtualMachine(obj interface{}) {
	var vm *vmv1alpha1.VirtualMachine
	switch v := obj.(type) {
	case *vmv1alpha1.VirtualMachine:
		vm = v
	case cache.DeletedFinalStateUnknown:
		var ok bool
		vm, ok = v.Obj.(*vmv1alpha1.VirtualMachine)
		if !ok {
			err := fmt.Errorf("obj is not valid *vmv1alpha1.VirtualMachine")
			log.Error(err, "DeletedFinalStateUnknown Obj is not *vmv1alpha1.VirtualMachine")
			return
		}
	}

	if !isVMRunning(vm) {
		log.Debug("Skip VM not in running state", "namespace", vm.Namespace, "name", vm.Name)
		return
	}

	if !belongsToVKSCluster(vm) {
		log.Debug("Skip VM not belonging to a VKS cluster", "namespace", vm.Namespace, "name", vm.Name)
		return
	}

	log.Debug("Inventory processing VirtualMachine", "namespace", vm.Namespace, "name", vm.Name)
	key, _ := keyFunc(vm)
	c.inventoryObjectQueue.Add(inventory.InventoryKey{
		InventoryType: inventory.InventoryVirtualMachine,
		ExternalId:    vm.Status.InstanceUUID,
		Key:           key,
	})
}

func isVMRunning(vm *vmv1alpha1.VirtualMachine) bool {
	return vm.Status.PowerState == vmv1alpha1.VirtualMachinePoweredOn
}

// belongsToVKSCluster checks whether the VM has the CAPI cluster-name label,
// which is guaranteed by the Cluster API contract for all VMs managed by CAPI.
func belongsToVKSCluster(vm *vmv1alpha1.VirtualMachine) bool {
	_, exists := vm.Labels[inventory.CAPIClusterNameLabel]
	return exists
}
