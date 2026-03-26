package inventory

import (
	"context"
	"fmt"

	vmv1alpha1 "github.com/vmware-tanzu/vm-operator/api/v1alpha1"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	nsxvmwarecomv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/inventory"
)

func watchNSXServiceAccount(c *InventoryController, mgr ctrl.Manager) error {
	nsxSAInformer, err := mgr.GetCache().GetInformer(context.Background(), &nsxvmwarecomv1alpha1.NSXServiceAccount{})
	if err != nil {
		log.Error(err, "Failed to create NSXServiceAccount informer")
		return err
	}

	_, err = nsxSAInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			c.handleNSXServiceAccount(obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			c.handleNSXServiceAccount(newObj)
		},
		DeleteFunc: func(obj interface{}) {
			c.handleNSXServiceAccountDelete(obj)
		},
	})
	if err != nil {
		log.Error(err, "Failed to add NSXServiceAccount event handler")
		return err
	}
	return nil
}

func (c *InventoryController) handleNSXServiceAccount(obj interface{}) {
	var nsxSA *nsxvmwarecomv1alpha1.NSXServiceAccount
	switch v := obj.(type) {
	case *nsxvmwarecomv1alpha1.NSXServiceAccount:
		nsxSA = v
	case cache.DeletedFinalStateUnknown:
		var ok bool
		nsxSA, ok = v.Obj.(*nsxvmwarecomv1alpha1.NSXServiceAccount)
		if !ok {
			err := fmt.Errorf("obj is not valid *NSXServiceAccount")
			log.Error(err, "DeletedFinalStateUnknown Obj is not *NSXServiceAccount")
			return
		}
	}

	if nsxSA.Status.Phase != nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized {
		log.Debug("Skip NSXServiceAccount not yet realized", "namespace", nsxSA.Namespace, "name", nsxSA.Name)
		return
	}

	c.enqueueVMsForCluster(nsxSA)
}

func (c *InventoryController) handleNSXServiceAccountDelete(obj interface{}) {
	var nsxSA *nsxvmwarecomv1alpha1.NSXServiceAccount
	switch v := obj.(type) {
	case *nsxvmwarecomv1alpha1.NSXServiceAccount:
		nsxSA = v
	case cache.DeletedFinalStateUnknown:
		var ok bool
		nsxSA, ok = v.Obj.(*nsxvmwarecomv1alpha1.NSXServiceAccount)
		if !ok {
			err := fmt.Errorf("obj is not valid *NSXServiceAccount")
			log.Error(err, "DeletedFinalStateUnknown Obj is not *NSXServiceAccount")
			return
		}
	}

	log.Info("NSXServiceAccount deleted, enqueuing VMs for tag removal", "namespace", nsxSA.Namespace, "name", nsxSA.Name)
	c.enqueueVMsForCluster(nsxSA)
}

// enqueueVMsForCluster lists all VirtualMachines in the NSXServiceAccount's namespace
// and enqueues them to the inventory queue for VM tag processing.
func (c *InventoryController) enqueueVMsForCluster(nsxSA *nsxvmwarecomv1alpha1.NSXServiceAccount) {
	vmList := &vmv1alpha1.VirtualMachineList{}
	if err := c.Client.List(context.Background(), vmList, &client.ListOptions{
		Namespace: nsxSA.Namespace,
	}); err != nil {
		log.Error(err, "Failed to list VirtualMachines for NSXServiceAccount", "namespace", nsxSA.Namespace)
		return
	}

	for i := range vmList.Items {
		vm := &vmList.Items[i]
		if !belongsToVKSCluster(vm) {
			continue
		}
		log.Debug("Enqueuing VM from NSXServiceAccount event", "namespace", vm.Namespace, "name", vm.Name)
		key, _ := keyFunc(vm)
		c.inventoryObjectQueue.Add(inventory.InventoryKey{
			InventoryType: inventory.InventoryVirtualMachine,
			ExternalId:    vm.Status.InstanceUUID,
			Key:           key,
		})
	}
}
