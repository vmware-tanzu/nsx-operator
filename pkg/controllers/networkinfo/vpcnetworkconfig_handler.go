package networkinfo

import (
	"context"
	"reflect"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

	queue workqueue.TypedRateLimitingInterface[types.NamespacedName]
}

// NewVPCNetworkConfigurationHandler creates a new VPCNetworkConfigurationHandler and starts its worker.
func NewVPCNetworkConfigurationHandler(client client.Client, vpcService commontypes.VPCServiceProvider, ipBlocksInfoService commontypes.IPBlocksInfoServiceProvider) *VPCNetworkConfigurationHandler {
	limiter := workqueue.NewTypedItemExponentialFailureRateLimiter[types.NamespacedName](1*time.Second, 30*time.Second)
	h := &VPCNetworkConfigurationHandler{
		Client:              client,
		vpcService:          vpcService,
		ipBlocksInfoService: ipBlocksInfoService,
		queue:               workqueue.NewTypedRateLimitingQueue[types.NamespacedName](limiter),
	}
	go h.workerLoop()
	return h
}

func (h *VPCNetworkConfigurationHandler) enqueueTask(req types.NamespacedName) {
	h.queue.Add(req)
}

func (h *VPCNetworkConfigurationHandler) workerLoop() {
	for {
		req, shutdown := h.queue.Get()
		if shutdown {
			return
		}

		err := h.processTask(req)
		if err != nil {
			log.Error(err, "Failed to process VPCNetworkConfiguration task, requeuing", "VPCNetworkConfiguration", req.Name)
			h.queue.AddRateLimited(req)
		} else {
			h.queue.Forget(req)
		}
		h.queue.Done(req)
	}
}

func (h *VPCNetworkConfigurationHandler) processTask(req types.NamespacedName) error {
	vpcConfig := &v1alpha1.VPCNetworkConfiguration{}
	err := h.Client.Get(context.Background(), req, vpcConfig)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// VPCNetworkConfiguration has been deleted
			if syncErr := h.ipBlocksInfoService.SyncIPBlocksInfo(context.Background()); syncErr != nil {
				return syncErr
			}
			h.ipBlocksInfoService.ResetPeriodicSync()
			return nil
		}
		return err
	}

	// VPCNetworkConfiguration exists (Create/Update)
	return h.ipBlocksInfoService.UpdateIPBlocksInfo(context.Background(), vpcConfig)
}

func (h *VPCNetworkConfigurationHandler) Create(_ context.Context, e event.CreateEvent, _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	h.enqueueTask(types.NamespacedName{Name: e.Object.GetName(), Namespace: e.Object.GetNamespace()})
}

func (h *VPCNetworkConfigurationHandler) Delete(_ context.Context, e event.DeleteEvent, _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	h.enqueueTask(types.NamespacedName{Name: e.Object.GetName(), Namespace: e.Object.GetNamespace()})
}

func (h *VPCNetworkConfigurationHandler) Generic(_ context.Context, _ event.GenericEvent, _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	log.Debug("VPCNetworkConfiguration generic event, do nothing")
}

func (h *VPCNetworkConfigurationHandler) Update(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	log.Debug("Start processing VPC NetworkConfig update event")
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
		log.Debug("Skip processing VPC NetworkConfig subnets", "newNc", newNc, "oldNc", oldNc)
		return
	}

	h.enqueueTask(types.NamespacedName{Name: newNc.GetName(), Namespace: newNc.GetNamespace()})
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
