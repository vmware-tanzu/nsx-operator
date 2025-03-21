package inventory

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/inventory"
)

func TestWatchNetworkPolicy(t *testing.T) {
	t.Run("SuccessfullyCreateInformer", func(t *testing.T) {
		controller := &InventoryController{}
		mockCache := new(MockCache)
		mockInformer := &MockInformer{handlers: cache.ResourceEventHandlerFuncs{}}
		mockCache.On("GetInformer", context.Background(), &networkingv1.NetworkPolicy{}).Return(mockInformer, nil)
		mgr := new(MockMgr)
		mgr.On("GetCache").Return(mockCache)
		err := watchNetworkPolicy(controller, mgr)
		assert.Nil(t, err)
	})

	t.Run("CreateInformerFailure", func(t *testing.T) {
		mockCache := new(MockCache)
		mockCache.On("GetInformer", context.Background(), &networkingv1.NetworkPolicy{}).Return(nil, errors.New("connection timeout"))
		controller := &InventoryController{}
		mgr := new(MockMgr)
		mgr.On("GetCache").Return(mockCache)
		err := watchNetworkPolicy(controller, mgr)

		assert.NotNil(t, err)
		assert.Contains(t, err.Error(), "connection timeout")
	})
}

func TestHandleNetworkPolicy(t *testing.T) {
	cfg := &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}
	queue := MockObjectQueue[any]{}
	inventoryService := &inventory.InventoryService{}
	controller := &InventoryController{
		service:              inventoryService,
		keyBuffer:            sets.New[inventory.InventoryKey](),
		cf:                   cfg,
		inventoryObjectQueue: &queue}
	t.Run("NormalNetworkPolicy", func(t *testing.T) {

		testNetworkPolicy := &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "deleted-ns",
				Name:      "deleted-networkpolicy",
				UID:       "deleted-uid",
			},
		}
		deletedObj := cache.DeletedFinalStateUnknown{Obj: testNetworkPolicy}
		queue.On("Add", mock.Anything).Return().Once()
		controller.handleNetworkPolicy(deletedObj)
		queue.AssertExpectations(t)
	})
	t.Run("DeletedStateWithNetworkPolicy", func(t *testing.T) {
		queue = MockObjectQueue[any]{}
		controller.inventoryObjectQueue = &queue

		invalidObj := "deleted networkpolicy"
		deletedObj := cache.DeletedFinalStateUnknown{Obj: invalidObj}

		controller.handleNetworkPolicy(deletedObj)
		queue.AssertExpectations(t)
	})
}
