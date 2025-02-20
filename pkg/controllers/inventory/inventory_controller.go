package inventory

import (
	"context"
	"errors"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	commonservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/inventory"
)

const (

	// How long to wait before retrying
	minRetryDelay = 5 * time.Second
	maxRetryDelay = 300 * time.Second

	inventoryGCJitterFactor = 0.1
)

var (
	log     = &logger.Log
	keyFunc = cache.DeletionHandlingMetaNamespaceKeyFunc
)

type InventoryController struct {
	Client               client.Client
	service              *inventory.InventoryService
	inventoryObjectQueue workqueue.TypedRateLimitingInterface[any]
	keyBuffer            inventory.KeySet
	inventoryMutex       sync.Mutex
	cf                   *config.NSXOperatorConfig
}

func NewInventoryController(Client client.Client, service *inventory.InventoryService, cf *config.NSXOperatorConfig) *InventoryController {
	c := &InventoryController{
		Client:               Client,
		service:              service,
		keyBuffer:            inventory.KeySet{},
		cf:                   cf,
		inventoryObjectQueue: workqueue.NewTypedRateLimitingQueueWithConfig(workqueue.NewTypedItemExponentialFailureRateLimiter[any](minRetryDelay, maxRetryDelay), workqueue.TypedRateLimitingQueueConfig[any]{Name: "inventoryObject"})}
	return c
}

// Initialize sync NSX resources and update local inventory store.
// TODO: Add two E2E tests to cover adding a new cluster and updating an existing container cluster.
func (c *InventoryController) Initialize() error {
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
			log.Error(errors.New("Unknown obj"), "DeletedFinalStateUnknown Obj is not *v1.Pod")
			return
		}
	}
	log.V(1).Info("Inventory processing Pod", "namespace", pod.Namespace, "name", pod.Name)
	key, _ := keyFunc(pod)
	log.V(1).Info("Adding Pod key to inventory object queue", "pod key", key)
	c.inventoryObjectQueue.Add(inventory.InventoryKey{InventoryType: inventory.ContainerApplicationInstance, ExternalId: string(pod.UID), Key: key})
}

func StartupInventoryController(mgr ctrl.Manager, service *inventory.InventoryService, cf *config.NSXOperatorConfig) error {

	controller := NewInventoryController(mgr.GetClient(), service, cf)
	return controller.SetupWithManager(mgr)
}

type WatchResourceFunc func(c *InventoryController, mgr ctrl.Manager) error

var WatchResourceFuncs = []WatchResourceFunc{watchPod}

func (c *InventoryController) SetupWithManager(mgr ctrl.Manager) error {
	for _, f := range WatchResourceFuncs {
		err := f(c, mgr)
		if err != nil {
			return err
		}
	}
	// Set up the queue
	c.Run(make(<-chan struct{}))
	return nil
}

func watchPod(c *InventoryController, mgr ctrl.Manager) error {
	podInformer, err := mgr.GetCache().GetInformer(context.Background(), &v1.Pod{})
	if err != nil {
		log.Error(err, "Create pod Informer error")
		return err
	}

	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			// Handle Pod add event
			c.handlePod(obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			// Handle Pod update event
		},
		DeleteFunc: func(obj interface{}) {
			// Handle Pod delete event
			c.handlePod(obj)
		},
	})
	return nil
}

func (c *InventoryController) Run(stopCh <-chan struct{}) {
	defer c.inventoryObjectQueue.ShutDown()
	log.Info("Starting inventory controller")

	// The sync state of Gateway informer should not be checked here, since no other inventory resource types rely on
	// Gateway store, and the Gateway informer readiness is polled and started in background.
	log.Info("Waiting for caches to sync for inventory controller")

	log.Info("Caches are synced for Inventory controller, cleaning up stale objects")
	err := c.CleanStaleInventoryObjects()
	if err != nil {
		log.Error(err, "Failed to clean up stale inventory objects")
		return
	}

	// Batch update inventory based on batch time and size.
	// Inventory worker will be running in forever loop util inventoryMutex is locked by inventoryTimeWorker.
	// Only one worker processes and sends request to NSX MP at one time.
	go wait.Until(c.inventoryWorker, time.Second, stopCh)
	go wait.Until(c.inventoryTimeWorker, time.Second*time.Duration(c.cf.InventoryBatchPeriod), stopCh)
	go wait.JitterUntil(c.inventoryGCWorker, commonservice.GCInterval, inventoryGCJitterFactor, true, stopCh)
	<-stopCh
}

func (c *InventoryController) inventoryTimeWorker() {
	defer c.inventoryMutex.Unlock()

	c.inventoryMutex.Lock()
	if len(c.keyBuffer) > 0 {
		c.syncInventoryKeys()
	}
}

func (c *InventoryController) inventoryGCWorker() {
	defer c.inventoryMutex.Unlock()

	c.inventoryMutex.Lock()
	if err := c.CleanStaleInventoryObjects(); err != nil {
		log.Error(err, "Failed to garbage collect inventory resources")
	}
}

func (c *InventoryController) inventoryWorker() {
	for c.processNextInventoryWorkItem() {
	}
}

func (c *InventoryController) processNextInventoryWorkItem() bool {
	key, quit := c.inventoryObjectQueue.Get()
	if quit {
		return false
	}

	defer c.inventoryMutex.Unlock()
	c.inventoryMutex.Lock()
	c.keyBuffer.Insert(key.(inventory.InventoryKey))
	if len(c.keyBuffer) >= c.cf.InventoryBatchSize {
		c.syncInventoryKeys()
	}
	return true
}

func (c *InventoryController) syncInventoryKeys() {
	// Remove all the keys from processing and clear keyBuffer.
	defer func() {
		for key := range c.keyBuffer {
			c.inventoryObjectQueue.Done(key)
		}
		c.keyBuffer = inventory.KeySet{}
	}()

	if len(c.keyBuffer) >= 0 {
		retryKeys, err := c.service.SyncInventoryObject(c.keyBuffer)
		if err != nil {
			log.Error(err, "Failed to sync inventory object to NSX")
		}
		for key := range c.keyBuffer {
			// For retry keys, the item is put back on the queue and attempted again after a back-off period.
			// For others, forget here stop the rate limiter from tracking it.
			if retryKeys.Has(key) {
				c.inventoryObjectQueue.AddRateLimited(key)
				log.Info("Enqueue key for retrying", "key", key)
			} else {
				c.inventoryObjectQueue.Forget(key)
			}
		}
	}
}

func (c *InventoryController) CleanStaleInventoryObjects() error {
	return nil
}
