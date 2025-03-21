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

func TestWatchNode(t *testing.T) {
	t.Run("SuccessfullyCreateInformer", func(t *testing.T) {
		controller := &InventoryController{}
		mockCache := new(MockCache)
		mockInformer := &MockInformer{handlers: cache.ResourceEventHandlerFuncs{}}
		mockCache.On("GetInformer", context.Background(), &corev1.Node{}).Return(mockInformer, nil)
		mgr := new(MockMgr)
		mgr.On("GetCache").Return(mockCache)
		err := watchNode(controller, mgr)
		assert.Nil(t, err)
	})

	t.Run("CreateInformerFailure", func(t *testing.T) {
		mockCache := new(MockCache)
		mockCache.On("GetInformer", context.Background(), &corev1.Node{}).Return(nil, errors.New("connection timeout"))
		controller := &InventoryController{}
		mgr := new(MockMgr)
		mgr.On("GetCache").Return(mockCache)
		err := watchNode(controller, mgr)

		assert.NotNil(t, err)
		assert.Contains(t, err.Error(), "connection timeout")
	})
}

func TestHandleNode(t *testing.T) {
	cfg := &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}
	queue := MockObjectQueue[any]{}
	inventoryService := &inventory.InventoryService{}
	controller := &InventoryController{
		service:              inventoryService,
		keyBuffer:            sets.New[inventory.InventoryKey](),
		cf:                   cfg,
		inventoryObjectQueue: &queue}
	t.Run("NormalNode", func(t *testing.T) {
		testNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-node",
				UID:  "test-uid",
			},
		}
		queue.On("Add", mock.Anything).Return().Once()
		controller.handleNode(testNode)
		queue.AssertExpectations(t)
	})
	t.Run("DeletedStateWithNode", func(t *testing.T) {
		testNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "deleted-node",
				UID:  "deleted-uid",
			},
		}
		deletedObj := cache.DeletedFinalStateUnknown{Obj: testNode}
		queue = MockObjectQueue[any]{}
		controller.inventoryObjectQueue = &queue
		queue.On("Add", mock.Anything).Return().Once()
		controller.handleNode(deletedObj)
		queue.AssertExpectations(t)
	})
	t.Run("DeletedStateWithInvalidObj", func(t *testing.T) {
		queue = MockObjectQueue[any]{}
		controller.inventoryObjectQueue = &queue

		invalidObj := "deleted node"
		deletedObj := cache.DeletedFinalStateUnknown{Obj: invalidObj}

		controller.handleNode(deletedObj)
		queue.AssertExpectations(t)
	})
}
