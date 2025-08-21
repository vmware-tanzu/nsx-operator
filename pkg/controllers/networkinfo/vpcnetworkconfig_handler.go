package networkinfo

import (
	"context"
	"reflect"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	commontypes "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

// VPCNetworkConfigurationHandler handles VPC NetworkConfiguration event, and reconcile VPC event:
// - VPC Network Configuration creation: Add VPC Network Configuration into cache.
// - VPC Network Configuration deletion: Delete VPC Network Configuration from cache.
// - VPC Network Configuration update:	Only support updating external/private ipblocks, update values in cache

type VPCNetworkConfigurationHandler struct {
	Client              client.Client
	vpcService          commontypes.VPCServiceProvider
	ipBlocksInfoService commontypes.IPBlocksInfoServiceProvider
}

func (h *VPCNetworkConfigurationHandler) Create(ctx context.Context, e event.CreateEvent, _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	vpcConfigCR := e.Object.(*v1alpha1.VPCNetworkConfiguration)
	vname := vpcConfigCR.GetName()
	// Update IPBlocks info
	if err := h.ipBlocksInfoService.UpdateIPBlocksInfo(ctx, vpcConfigCR); err != nil {
		log.Error(err, "Failed to update the IPBlocksInfo", "VPCNetworkConfiguration", vname)
	}
}

func (h *VPCNetworkConfigurationHandler) Delete(ctx context.Context, e event.DeleteEvent, _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	vpcConfigCR := e.Object.(*v1alpha1.VPCNetworkConfiguration)
	if err := h.ipBlocksInfoService.SyncIPBlocksInfo(ctx); err != nil {
		log.Error(err, "failed to synchronize IPBlocksInfo when deleting %s", vpcConfigCR.Name)
	} else {
		h.ipBlocksInfoService.ResetPeriodicSync()
	}
}

func (h *VPCNetworkConfigurationHandler) Generic(_ context.Context, _ event.GenericEvent, _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	log.V(1).Info("VPCNetworkConfiguration generic event, do nothing")
}

func (h *VPCNetworkConfigurationHandler) Update(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	log.V(1).Info("Start processing VPC NetworkConfig update event")
	newNc := e.ObjectNew.(*v1alpha1.VPCNetworkConfiguration)

	oldNc := e.ObjectOld.(*v1alpha1.VPCNetworkConfiguration)
	if reflect.DeepEqual(oldNc.Spec, newNc.Spec) {
		log.Info("Skip processing VPC NetworkConfig update event", "newNc", newNc, "oldNc", oldNc)
		return
	}

	nss, err := h.vpcService.GetNamespacesByNetworkconfigName(newNc.Name)
	if err != nil {
		log.Error(err, "Failed to get Namespaces with NetworkConfig", "VPCNetworkConfig", newNc.Name)
		return
	}
	for _, ns := range nss {
		networkInfos := &v1alpha1.NetworkInfoList{}
		err := h.Client.List(ctx, networkInfos, client.InNamespace(ns))
		if err != nil {
			log.Error(err, "Failed to list VPCs in namespace", "Namespace", ns)
			continue
		}

		for _, networkInfo := range networkInfos.Items {
			log.Info("Requeue NetworkInfo CR due to modifying NetworkConfig CR", "NetworkInfo", networkInfo.Name, "Namespace", ns, "NetworkConfig", newNc.Name)
			q.Add(reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      networkInfo.Name,
					Namespace: networkInfo.Namespace,
				},
			})
		}
	}
	if util.CompareArraysWithoutOrder(oldNc.Spec.Subnets, newNc.Spec.Subnets) {
		log.V(1).Info("Skip processing VPC NetworkConfig subnets", "newNc", newNc, "oldNc", oldNc)
		return
	}

	if err := h.ipBlocksInfoService.UpdateIPBlocksInfo(ctx, newNc); err != nil {
		log.Error(err, "Failed to update the IPBlocksInfo", "VPCNetworkConfiguration", newNc.Name)
	}
}

var VPCNetworkConfigurationPredicate = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		return true
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		return true
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return true
	},
	GenericFunc: func(genericEvent event.GenericEvent) bool {
		return false
	},
}
