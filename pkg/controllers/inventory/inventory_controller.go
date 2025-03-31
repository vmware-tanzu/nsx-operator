package inventory

import (
	"os"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
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

type WatchResourceFunc func(c *InventoryController, mgr ctrl.Manager) error

var (
	log = &logger.Log

	// DeletionHandlingMetaNamespaceKeyFunc checks for
	// DeletedFinalStateUnknown objects before calling
	// MetaNamespaceKeyFunc.
	keyFunc            = cache.DeletionHandlingMetaNamespaceKeyFunc
	WatchResourceFuncs = []WatchResourceFunc{
		watchPod,
		watchNamespace,
		watchService,
		watchEndpoint,
	}
)

type InventoryController struct {
	Client               client.Client
	service              *inventory.InventoryService
	inventoryObjectQueue workqueue.TypedRateLimitingInterface[any]
	keyBuffer            sets.Set[inventory.InventoryKey]
	inventoryMutex       sync.Mutex
	cf                   *config.NSXOperatorConfig
}

func NewInventoryController(Client client.Client, service *inventory.InventoryService, cf *config.NSXOperatorConfig) *InventoryController {
	limiter := workqueue.NewTypedItemExponentialFailureRateLimiter[any](minRetryDelay, maxRetryDelay)
	queueConfig := workqueue.TypedRateLimitingQueueConfig[any]{Name: "inventoryObject"}
	queue := workqueue.NewTypedRateLimitingQueueWithConfig(limiter, queueConfig)
	c := &InventoryController{
		Client:               Client,
		service:              service,
		keyBuffer:            sets.New[inventory.InventoryKey](),
		cf:                   cf,
		inventoryObjectQueue: queue,
	}
	return c
}

func StartInventoryController(mgr ctrl.Manager, service *inventory.InventoryService, cf *config.NSXOperatorConfig) {
	controller := NewInventoryController(mgr.GetClient(), service, cf)
	if err := controller.SetupWithManager(mgr); err != nil {
		log.Error(err, "Failed to create controller", "controller", "Inventory")
		os.Exit(1)
	}
}

func (c *InventoryController) SetupWithManager(mgr ctrl.Manager) error {
	for _, f := range WatchResourceFuncs {
		err := f(c, mgr)
		if err != nil {
			return err
		}
	}
	// Set up the queue
	go c.Run(make(<-chan struct{}))
	return nil
}

func (c *InventoryController) Run(stopCh <-chan struct{}) {
	defer c.inventoryObjectQueue.ShutDown()
	log.Info("Starting inventory controller")

	err := c.CleanStaleInventoryObjects()
	if err != nil {
		log.Error(err, "Failed to clean up stale inventory objects")
		return
	}

	// Batch update inventory based on batch time and size.
	// Inventory worker will be running in forever loop until inventoryMutex is locked by inventoryTimeWorker.
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
		c.keyBuffer = sets.New[inventory.InventoryKey]()
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
	log.Info("Clean stale inventory objects")
	err := c.service.CleanStaleInventoryApplicationInstance()
	if err != nil {
		return err
	}
	err = c.service.CleanStaleInventoryContainerProject()
	if err != nil {
		return err
	}
	return nil
}
