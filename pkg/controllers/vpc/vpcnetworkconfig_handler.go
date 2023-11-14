package vpc

import (
	"context"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

// VPCNetworkConfigurationHandler handles VPC NetworkConfiguration event, and reconcile VPC event:
// - VPC Network Configuration creation: Add VPC Network Configuration into cache.
// - VPC Network Configuration deletion: Delete VPC Network Configuration from cache.
// - VPC Network Configuration update:	Only support updating external/private ipblocks, update values in cache

type VPCNetworkConfigurationHandler struct {
	Client     client.Client
	vpcService *vpc.VPCService
}

func (h *VPCNetworkConfigurationHandler) Create(_ context.Context, e event.CreateEvent, _ workqueue.RateLimitingInterface) {
	vpcConfigCR := e.Object.(*v1alpha1.VPCNetworkConfiguration)
	vname := vpcConfigCR.GetName()
	ninfo, _err := buildNetworkConfigInfo(*vpcConfigCR)
	if _err != nil {
		log.Error(_err, "processing network config add event failed")
		return
	}
	log.Info("create network config and update to store", "NetworkConfigInfo", ninfo)
	h.vpcService.RegisterVPCNetworkConfig(vname, *ninfo)
}

func (h *VPCNetworkConfigurationHandler) Delete(_ context.Context, e event.DeleteEvent, _ workqueue.RateLimitingInterface) {
	// Currently we do not support deleting networkconfig
	log.V(1).Info("do not support VPC network config deletion")
}

func (h *VPCNetworkConfigurationHandler) Generic(_ context.Context, _ event.GenericEvent, _ workqueue.RateLimitingInterface) {
	log.V(1).Info("VPCNetworkConfiguration generic event, do nothing")
}

func (h *VPCNetworkConfigurationHandler) Update(_ context.Context, e event.UpdateEvent, q workqueue.RateLimitingInterface) {
	log.V(1).Info("start processing VPC network config update event")
	oldNc := e.ObjectOld.(*v1alpha1.VPCNetworkConfiguration)
	newNc := e.ObjectNew.(*v1alpha1.VPCNetworkConfiguration)

	if getListSize(oldNc.Spec.ExternalIPv4Blocks) == getListSize(newNc.Spec.ExternalIPv4Blocks) &&
		getListSize(oldNc.Spec.PrivateIPv4CIDRs) == getListSize(newNc.Spec.PrivateIPv4CIDRs) {
		log.V(1).Info("only support updating external/private ipv4 cidr, no change")
		return
	}

	// update network config info in store
	info, err := buildNetworkConfigInfo(*newNc)
	if err != nil {
		log.Error(err, "failed to process network config update event")
		return
	}
	h.vpcService.RegisterVPCNetworkConfig(newNc.Name, *info)

	nss := h.vpcService.GetNamespacesByNetworkconfigName(newNc.Name)
	ctx := context.Background()
	// find vpcs under each ns, and reconcile the vpc object
	for _, ns := range nss {
		vpcList := &v1alpha1.VPCList{}
		err := h.Client.List(ctx, vpcList, client.InNamespace(ns))
		if err != nil {
			log.Error(err, "failed to list VPCs in namespace", "Namespace", ns)
			continue
		}

		for _, vpc := range vpcList.Items {
			log.Info("reconcile VPC CR due to modifying network config CR", "VPC", vpc.Name, "Namespace", ns, "NetworkConfig", newNc.Name)
			q.Add(reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      vpc.Name,
					Namespace: vpc.Namespace,
				},
			})
		}
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
		return false
	},
	GenericFunc: func(genericEvent event.GenericEvent) bool {
		return false
	},
}

func getListSize(s []string) int {
	if s == nil {
		return 0
	} else {
		return len(s)
	}
}
