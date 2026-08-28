package networkinfo

import (
	"context"
	"reflect"
	"sync"

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

type vpcTaskType int

const (
	vpcTaskUpdate vpcTaskType = iota
	vpcTaskDelete
)

type vpcTask struct {
	taskType  vpcTaskType
	vpcConfig *v1alpha1.VPCNetworkConfiguration
}

type VPCNetworkConfigurationHandler struct {
	Client              client.Client
	vpcService          commontypes.VPCServiceProvider
	ipBlocksInfoService commontypes.IPBlocksInfoServiceProvider

	initOnce sync.Once
	mu       sync.Mutex
	cond     *sync.Cond
	tasks    []vpcTask
}

func (h *VPCNetworkConfigurationHandler) ensureWorker() {
	h.initOnce.Do(func() {
		h.cond = sync.NewCond(&h.mu)
		go h.workerLoop()
	})
}

func (h *VPCNetworkConfigurationHandler) enqueueTask(t vpcTask) {
	h.ensureWorker()
	h.mu.Lock()
	h.tasks = append(h.tasks, t)
	h.mu.Unlock()
	h.cond.Signal()
}

func (h *VPCNetworkConfigurationHandler) workerLoop() {
	for {
		h.mu.Lock()
		for len(h.tasks) == 0 {
			h.cond.Wait()
		}
		t := h.tasks[0]
		h.tasks[0] = vpcTask{}
		h.tasks = h.tasks[1:]
		if len(h.tasks) == 0 {
			h.tasks = nil
		}
		h.mu.Unlock()

		switch t.taskType {
		case vpcTaskUpdate:
			if err := h.ipBlocksInfoService.UpdateIPBlocksInfo(context.Background(), t.vpcConfig); err != nil {
				log.Error(err, "Failed to update the IPBlocksInfo", "VPCNetworkConfiguration", t.vpcConfig.Name)
			}
		case vpcTaskDelete:
			if err := h.ipBlocksInfoService.SyncIPBlocksInfo(context.Background()); err != nil {
				log.Error(err, "failed to synchronize IPBlocksInfo when deleting", "VPCNetworkConfiguration", t.vpcConfig.Name)
			} else {
				h.ipBlocksInfoService.ResetPeriodicSync()
			}
		}
	}
}

func (h *VPCNetworkConfigurationHandler) Create(_ context.Context, e event.CreateEvent, _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	vpcConfigCR := e.Object.(*v1alpha1.VPCNetworkConfiguration)
	// Enqueue update task to single background worker thread to avoid blocking Informer CacheSync
	h.enqueueTask(vpcTask{
		taskType:  vpcTaskUpdate,
		vpcConfig: vpcConfigCR,
	})
}

func (h *VPCNetworkConfigurationHandler) Delete(_ context.Context, e event.DeleteEvent, _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	vpcConfigCR := e.Object.(*v1alpha1.VPCNetworkConfiguration)
	h.enqueueTask(vpcTask{
		taskType:  vpcTaskDelete,
		vpcConfig: vpcConfigCR,
	})
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

	h.enqueueTask(vpcTask{
		taskType:  vpcTaskUpdate,
		vpcConfig: newNc,
	})
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
