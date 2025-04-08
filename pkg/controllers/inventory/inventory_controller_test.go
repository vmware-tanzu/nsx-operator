package inventory

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/inventory"
)

type MockMgr struct {
	mock.Mock
	ctrl.Manager
}

func (m *MockMgr) GetClient() client.Client {
	args := m.Called()
	return args.Get(0).(client.Client)
}

func (m *MockMgr) GetCache() cache.Cache {
	args := m.Called()
	return args.Get(0).(cache.Cache)
}

type MockCache struct {
	mock.Mock
	cache.Cache
}

func (m *MockCache) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return nil
}

func (m *MockCache) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return nil
}
func (m *MockCache) GetInformer(ctx context.Context, obj client.Object, opts ...cache.InformerGetOption) (cache.Informer, error) {
	args := m.Called(ctx, obj)
	return &MockInformer{}, args.Error(1)
}

type MockInformer struct {
	mock.Mock
	handlers toolscache.ResourceEventHandlerFuncs
	cache.Informer
}

func (m *MockInformer) AddEventHandler(handler toolscache.ResourceEventHandler) (toolscache.ResourceEventHandlerRegistration, error) {
	if m != nil && m.handlers.AddFunc != nil {
		m.handlers.AddFunc(handler)
	}
	return m, nil
}

func TestNewInventoryController(t *testing.T) {
	// Setup test dependencies
	scheme := runtime.NewScheme()
	mockClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	mockService := &inventory.InventoryService{}
	mockConfig := &config.NSXOperatorConfig{}

	t.Run("Initialize controller with correct dependencies", func(t *testing.T) {
		got := NewInventoryController(mockClient, mockService, mockConfig)
		if got.keyBuffer.Len() != 0 {
			t.Errorf("keyBuffer should be initialized empty, got %d", got.keyBuffer.Len())
		}
	})
}

func TestRun(t *testing.T) {
	cfg := &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}
	cfg.InventoryBatchPeriod = 60
	cfg.InventoryBatchSize = 100
	inventoryService := &inventory.InventoryService{}
	controller := &InventoryController{
		service:              inventoryService,
		keyBuffer:            sets.Set[inventory.InventoryKey]{},
		cf:                   cfg,
		inventoryObjectQueue: workqueue.NewTypedRateLimitingQueueWithConfig(workqueue.NewTypedItemExponentialFailureRateLimiter[any](minRetryDelay, maxRetryDelay), workqueue.TypedRateLimitingQueueConfig[any]{Name: "inventoryObject"})}
	controller.inventoryObjectQueue.Add(inventory.InventoryKey{})
	t.Run("NormalStartup", func(t *testing.T) {
		patches := gomonkey.ApplyMethod(reflect.TypeOf(controller), "CleanStaleInventoryObjects", func(service *InventoryController) error {
			return nil
		})
		defer patches.Reset()
		stopCh := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)

		go func() {
			defer wg.Done()
			controller.Run(stopCh)
		}()

		time.Sleep(100 * time.Millisecond)
		close(stopCh)
		wg.Wait()

		assert.True(t, controller.inventoryObjectQueue.ShuttingDown())
	})

	t.Run("CleanupFailure", func(t *testing.T) {
		patches := gomonkey.ApplyMethod(reflect.TypeOf(controller), "CleanStaleInventoryObjects", func(service *InventoryController) error {
			return errors.New("cleanup error")
		})
		defer patches.Reset()
		stopCh := make(chan struct{})
		go controller.Run(stopCh)
		time.Sleep(100 * time.Millisecond)
		close(stopCh)
	})
}

type MockObjectQueue[T comparable] struct {
	len int
	mock.Mock
	workqueue.TypedDelayingInterface[T]
}

func (m *MockObjectQueue[T]) AddRateLimited(key T) {
	m.len = m.len + 1
	m.Called(key)
}
func (m *MockObjectQueue[T]) Forget(key T)          { m.Called(key) }
func (m *MockObjectQueue[T]) Done(key T)            { m.Called(key) }
func (m *MockObjectQueue[T]) Len() int              { return m.len }
func (m *MockObjectQueue[T]) NumRequeues(key T) int { return m.len }
func (m *MockObjectQueue[T]) Add(key T)             { m.Called(key) }

// NumRequeues returns back how many times the item was requeued

func TestSyncInventoryKeys(t *testing.T) {
	cfg := &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}
	cfg.InventoryBatchPeriod = 60
	cfg.InventoryBatchSize = 100
	queue := MockObjectQueue[any]{}
	inventoryService := &inventory.InventoryService{}
	controller := &InventoryController{
		service:              inventoryService,
		keyBuffer:            sets.New[inventory.InventoryKey](),
		cf:                   cfg,
		inventoryObjectQueue: &queue}
	t.Run("EmptyBuffer", func(t *testing.T) {
		wg := &sync.WaitGroup{}
		wg.Add(1)
		controller.syncInventoryKeys()
		assert.Equal(t, 0, queue.Len())
	})
	t.Run("SuccessNoRetry", func(t *testing.T) {
		key1 := inventory.InventoryKey{ExternalId: "key1", InventoryType: inventory.ContainerApplicationInstance, Key: "key1"}
		key2 := inventory.InventoryKey{ExternalId: "key2", InventoryType: inventory.ContainerApplicationInstance, Key: "key2"}
		testKeys := sets.Set[inventory.InventoryKey]{key1: struct{}{}, key2: struct{}{}}

		controller.keyBuffer = testKeys
		queue = MockObjectQueue[any]{}
		patches := gomonkey.ApplyMethod(reflect.TypeOf(inventoryService), "SyncInventoryObject", func(_ *inventory.InventoryService, keys sets.Set[inventory.InventoryKey]) (sets.Set[inventory.InventoryKey], error) {
			return testKeys, nil
		})

		defer patches.Reset()
		wg := &sync.WaitGroup{}
		wg.Add(1)
		queue.On("AddRateLimited", key1).Return()
		queue.On("AddRateLimited", key2).Return()
		queue.On("Done", mock.Anything).Times(2)
		controller.syncInventoryKeys()
		assert.Equal(t, 2, queue.Len())
	})

	t.Run("ErrorWithPartialRetry", func(t *testing.T) {
		key1 := inventory.InventoryKey{ExternalId: "key1", InventoryType: inventory.ContainerApplicationInstance, Key: "key1"}
		key2 := inventory.InventoryKey{ExternalId: "key2", InventoryType: inventory.ContainerApplicationInstance, Key: "key2"}
		retryKeys := sets.Set[inventory.InventoryKey]{key1: struct{}{}}

		queue = MockObjectQueue[any]{}
		controller.inventoryObjectQueue = &queue
		controller.keyBuffer[key2] = struct{}{}
		controller.keyBuffer[key1] = struct{}{}

		patches := gomonkey.ApplyMethod(reflect.TypeOf(inventoryService), "SyncInventoryObject", func(_ *inventory.InventoryService, keys sets.Set[inventory.InventoryKey]) (sets.Set[inventory.InventoryKey], error) {
			return retryKeys, errors.New("sync error")
		})
		defer patches.Reset()
		wg := &sync.WaitGroup{}
		wg.Add(1)

		queue.On("Done", mock.Anything).Times(2)
		queue.On("AddRateLimited", key1).Once()
		queue.On("Forget", key2).Once()
		controller.syncInventoryKeys()
		queue.AssertExpectations(t)
	})
}
func TestStartInventoryController(t *testing.T) {
	t.Run("SuccessfulControllerSetup", func(t *testing.T) {
		// Mock dependencies
		mockMgr := &MockMgr{}
		mockService := &inventory.InventoryService{}
		mockConfig := &config.NSXOperatorConfig{}

		mockClient := fake.NewClientBuilder().Build()
		mockMgr.On("GetClient").Return(mockClient)

		// Mock setupWithManager to return nil (successful setup)
		patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(&InventoryController{}), "setupWithManager", func(_ *InventoryController, mgr ctrl.Manager) error {
			return nil
		})
		defer patches.Reset()

		// Start InventoryController
		controller := NewInventoryController(mockMgr.GetClient(), mockService, mockConfig)
		err := controller.StartController(mockMgr, nil)
		assert.Nil(t, err)
	})

	t.Run("ControllerSetupFailure", func(t *testing.T) {
		// Mock dependencies
		mockMgr := &MockMgr{}
		mockService := &inventory.InventoryService{}
		mockConfig := &config.NSXOperatorConfig{}

		mockClient := fake.NewClientBuilder().Build()
		mockMgr.On("GetClient").Return(mockClient)

		// Mock setupWithManager to return an error
		patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(&InventoryController{}), "setupWithManager", func(_ *InventoryController, mgr ctrl.Manager) error {
			return errors.New("setup error")
		})
		defer patches.Reset()

		// Start InventoryController
		controller := NewInventoryController(mockMgr.GetClient(), mockService, mockConfig)
		err := controller.StartController(mockMgr, nil)
		assert.Error(t, err)
	})
}
