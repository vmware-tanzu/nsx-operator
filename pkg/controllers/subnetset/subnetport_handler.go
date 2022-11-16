package subnetset

import (
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

// SubnetPortHandler supports lazy-creation of Subnet, the first Subnet won't
// be created until there is a SubnetPort attached to it.
// - SubnetPort creation: get available Subnet for the SubnetPort, create new
//   Subnet if necessary.
// - SubnetPort deletion: if recycling Subnet is required, delete Subnets without
// SubnetPort attached to it.

type SubnetPortHandler struct {
	Reconciler *SubnetSetReconciler
}

//TODO Remove this handler when confirmed that SubnetPort could get allocated subnet via the interface
// subnetservice.GetAvailableSubnet

// Create allocates Subnet for SubnetPort from SubnetSet.
func (h *SubnetPortHandler) Create(e event.CreateEvent, _ workqueue.RateLimitingInterface) {
	log.V(4).Info("SubnetPort generic event, do nothing")
	//subnetPort := e.Object.(*v1alpha1.SubnetPort)
	//if subnetPort.Spec.Subnet != "" {
	//	// Two possible scenarios:
	//	// - 1. User uses `.Spec.Subnet` directly instead of `.Spec.SubnetSet`.
	//	// - 2. Subnet has been allocated and `.Spec.Subnet` is rendered by SubnetPortHandler.
	//	return
	//}
	//subnetSet := &v1alpha1.SubnetSet{}
	//key := types.NamespacedName{
	//	Namespace: subnetPort.GetNamespace(),
	//	Name:      subnetPort.Spec.SubnetSet,
	//}
	//if err := h.Reconciler.Client.Get(context.Background(), key, subnetSet); err != nil {
	//	log.Error(err, "failed to get SubnetSet", "ns", key.Namespace, "name", key.Name)
	//	return
	//}
	//log.Info("allocating Subnet for SubnetPort")
	//vpcList := &v1alpha1.VPCList{}
	//if err := h.Reconciler.Client.List(context.Background(), vpcList, client.InNamespace(subnetPort.GetNamespace())); err != nil {
	//	log.Error(err, fmt.Sprintf("failed to get VPC under namespace: %s.\n", subnetPort.GetNamespace()))
	//	return
	//}
	//vpcInfo, err := servicecommon.ParseVPCResourcePath(vpcList.Items[0].Status.NSXResourcePath)
	//if err != nil {
	//	log.Error(err, "failed to resolve VPC info")
	//	return
	//}
	//_, err = h.Reconciler.getAvailableSubnet(subnetSet, &vpcInfo)
	//if err != nil {
	//	log.Error(err, "failed to allocate Subnet")
	//}
	// TODO return subnetport id to caller.
}

// Delete TODO Implement this method if required to recycle Subnet without SubnetPort attached.
func (h *SubnetPortHandler) Delete(e event.DeleteEvent, _ workqueue.RateLimitingInterface) {
	log.V(4).Info("SubnetPort generic event, do nothing")
}

func (h *SubnetPortHandler) Generic(_ event.GenericEvent, _ workqueue.RateLimitingInterface) {
	log.V(4).Info("SubnetPort generic event, do nothing")
}

func (h *SubnetPortHandler) Update(_ event.UpdateEvent, _ workqueue.RateLimitingInterface) {
	log.V(4).Info("SubnetPort update event, do nothing")
}

var SubnetPortPredicate = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		return true
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		return false
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		// TODO When recycling Subnet is required, return true.
		return false
	},
	GenericFunc: func(genericEvent event.GenericEvent) bool {
		return false
	},
}
