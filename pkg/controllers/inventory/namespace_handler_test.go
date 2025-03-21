package inventory

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/inventory"
)

func TestWatchNamespace(t *testing.T) {
	t.Run("SuccessfullyCreateInformer", func(t *testing.T) {
		controller := &InventoryController{}
		mockCache := new(MockCache)
		mockInformer := &MockInformer{handlers: cache.ResourceEventHandlerFuncs{}}
		mockCache.On("GetInformer", context.Background(), &corev1.Namespace{}).Return(mockInformer, nil)
		mgr := new(MockMgr)
		mgr.On("GetCache").Return(mockCache)
		err := watchNamespace(controller, mgr)
		assert.Nil(t, err)
	})

	t.Run("CreateInformerFailure", func(t *testing.T) {
		mockCache := new(MockCache)
		mockCache.On("GetInformer", context.Background(), &corev1.Namespace{}).Return(nil, errors.New("connection timeout"))
		controller := &InventoryController{}
		mgr := new(MockMgr)
		mgr.On("GetCache").Return(mockCache)
		err := watchNamespace(controller, mgr)

		assert.NotNil(t, err)
		assert.Contains(t, err.Error(), "connection timeout")
	})
}

func TestHandleNamespace(t *testing.T) {
	cfg := &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}
	queue := MockObjectQueue[any]{}
	inventoryService := &inventory.InventoryService{}
	controller := &InventoryController{
		service:              inventoryService,
		keyBuffer:            sets.New[inventory.InventoryKey](),
		cf:                   cfg,
		inventoryObjectQueue: &queue}

	t.Run("NormalNamespace", func(t *testing.T) {
		testNamespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-namespace",
				UID:  "test-uid",
			},
		}
		queue.On("Add", mock.Anything).Return().Once()
		controller.handleNamespace(testNamespace)
		queue.AssertExpectations(t)
	})

	t.Run("DeletedStateWithNamespace", func(t *testing.T) {
		testNamespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "deleted-namespace",
				UID:  "deleted-uid",
			},
		}
		deletedObj := cache.DeletedFinalStateUnknown{Obj: testNamespace}

		queue = MockObjectQueue[any]{}
		controller.inventoryObjectQueue = &queue
		queue.On("Add", mock.Anything).Return().Once()

		controller.handleNamespace(deletedObj)
		queue.AssertExpectations(t)
	})

	t.Run("DeletedStateWithInvalidObject", func(t *testing.T) {
		invalidObj := "deleted namespace"
		deletedObj := cache.DeletedFinalStateUnknown{Obj: invalidObj}

		queue = MockObjectQueue[any]{}
		controller.inventoryObjectQueue = &queue

		controller.handleNamespace(deletedObj)
		queue.AssertExpectations(t)
	})
}
