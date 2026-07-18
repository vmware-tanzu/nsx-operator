package inventory

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	vmv1alpha1 "github.com/vmware-tanzu/vm-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/inventory"
)

func TestWatchVirtualMachine(t *testing.T) {
	t.Run("SuccessfullyCreateInformerAndTriggerCallbacks", func(t *testing.T) {
		queue := MockObjectQueue[any]{}
		controller := &InventoryController{
			service:              &inventory.InventoryService{},
			keyBuffer:            sets.New[inventory.InventoryKey](),
			cf:                   &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}},
			inventoryObjectQueue: &queue,
		}
		mockCache := new(MockCache)
		mockInformer := &MockInformer{handlers: cache.ResourceEventHandlerFuncs{}}
		mockCache.On("GetInformer", context.Background(), &vmv1alpha1.VirtualMachine{}).Return(mockInformer, nil)
		mgr := new(MockMgr)
		mgr.On("GetCache").Return(mockCache)
		err := watchVirtualMachine(controller, mgr)
		assert.Nil(t, err)

		vm := &vmv1alpha1.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "tks", Name: "vm-cb",
				Labels: map[string]string{
					inventory.CAPIClusterNameLabel: "test-cluster",
				},
			},
			Status: vmv1alpha1.VirtualMachineStatus{
				PowerState: vmv1alpha1.VirtualMachinePoweredOn, InstanceUUID: "uuid-cb",
			},
		}
		queue.On("Add", mock.Anything).Return()

		mockInformer.registeredHandler.OnAdd(vm, false)
		mockInformer.registeredHandler.OnUpdate(vm, vm)
		mockInformer.registeredHandler.OnDelete(vm)
	})

	t.Run("CreateInformerFailure", func(t *testing.T) {
		mockCache := new(MockCache)
		mockCache.On("GetInformer", context.Background(), &vmv1alpha1.VirtualMachine{}).Return(nil, errors.New("connection timeout"))
		controller := &InventoryController{}
		mgr := new(MockMgr)
		mgr.On("GetCache").Return(mockCache)
		err := watchVirtualMachine(controller, mgr)

		assert.NotNil(t, err)
		assert.Contains(t, err.Error(), "connection timeout")
	})

	t.Run("AddEventHandlerFailure", func(t *testing.T) {
		controller := &InventoryController{}
		mockCache := new(MockCache)
		mockInformer := &MockInformer{addHandlerErr: errors.New("handler error")}
		mockCache.On("GetInformer", context.Background(), &vmv1alpha1.VirtualMachine{}).Return(mockInformer, nil)
		mgr := new(MockMgr)
		mgr.On("GetCache").Return(mockCache)
		err := watchVirtualMachine(controller, mgr)

		assert.NotNil(t, err)
		assert.Contains(t, err.Error(), "handler error")
	})
}

func TestHandleVirtualMachine(t *testing.T) {
	cfg := &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}
	queue := MockObjectQueue[any]{}
	inventoryService := &inventory.InventoryService{}
	controller := &InventoryController{
		service:              inventoryService,
		keyBuffer:            sets.New[inventory.InventoryKey](),
		cf:                   cfg,
		inventoryObjectQueue: &queue,
	}

	t.Run("RunningVKSVM", func(t *testing.T) {
		vm := &vmv1alpha1.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "tks",
				Name:      "test-vm",
				Labels: map[string]string{
					inventory.CAPIClusterNameLabel: "test-cluster",
				},
			},
			Status: vmv1alpha1.VirtualMachineStatus{
				PowerState:   vmv1alpha1.VirtualMachinePoweredOn,
				InstanceUUID: "uuid-1234",
			},
		}
		queue = MockObjectQueue[any]{}
		controller.inventoryObjectQueue = &queue
		queue.On("Add", mock.Anything).Return().Once()
		controller.handleVirtualMachine(vm)
		queue.AssertExpectations(t)
	})

	t.Run("NotRunningVM", func(t *testing.T) {
		vm := &vmv1alpha1.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "tks",
				Name:      "test-vm",
				Labels: map[string]string{
					inventory.CAPIClusterNameLabel: "test-cluster",
				},
			},
			Status: vmv1alpha1.VirtualMachineStatus{
				PowerState: vmv1alpha1.VirtualMachinePoweredOff,
			},
		}
		queue = MockObjectQueue[any]{}
		controller.inventoryObjectQueue = &queue
		controller.handleVirtualMachine(vm)
		queue.AssertNotCalled(t, "Add", mock.Anything)
	})

	t.Run("NonVKSVM", func(t *testing.T) {
		vm := &vmv1alpha1.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "tks",
				Name:      "test-vm",
			},
			Status: vmv1alpha1.VirtualMachineStatus{
				PowerState: vmv1alpha1.VirtualMachinePoweredOn,
			},
		}
		queue = MockObjectQueue[any]{}
		controller.inventoryObjectQueue = &queue
		controller.handleVirtualMachine(vm)
		queue.AssertNotCalled(t, "Add", mock.Anything)
	})

	t.Run("DeletedFinalStateUnknown", func(t *testing.T) {
		vm := &vmv1alpha1.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "tks",
				Name:      "deleted-vm",
				Labels: map[string]string{
					inventory.CAPIClusterNameLabel: "test-cluster",
				},
			},
			Status: vmv1alpha1.VirtualMachineStatus{
				PowerState:   vmv1alpha1.VirtualMachinePoweredOn,
				InstanceUUID: "uuid-5678",
			},
		}
		deletedObj := cache.DeletedFinalStateUnknown{Obj: vm}
		queue = MockObjectQueue[any]{}
		controller.inventoryObjectQueue = &queue
		queue.On("Add", mock.Anything).Return().Once()
		controller.handleVirtualMachine(deletedObj)
		queue.AssertExpectations(t)
	})

	t.Run("DeletedFinalStateUnknownInvalidObj", func(t *testing.T) {
		deletedObj := cache.DeletedFinalStateUnknown{Obj: "invalid"}
		queue = MockObjectQueue[any]{}
		controller.inventoryObjectQueue = &queue
		controller.handleVirtualMachine(deletedObj)
		queue.AssertNotCalled(t, "Add", mock.Anything)
	})
}

func TestBelongsToVKSCluster(t *testing.T) {
	tests := []struct {
		name     string
		vm       *vmv1alpha1.VirtualMachine
		expected bool
	}{
		{
			name: "Has CAPI cluster-name label",
			vm: &vmv1alpha1.VirtualMachine{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						inventory.CAPIClusterNameLabel: "my-cluster",
					},
				},
			},
			expected: true,
		},
		{
			name: "No labels",
			vm: &vmv1alpha1.VirtualMachine{
				ObjectMeta: metav1.ObjectMeta{},
			},
			expected: false,
		},
		{
			name: "Has other labels but not CAPI",
			vm: &vmv1alpha1.VirtualMachine{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "nginx",
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := belongsToVKSCluster(tt.vm)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsVMRunning(t *testing.T) {
	tests := []struct {
		name     string
		state    vmv1alpha1.VirtualMachinePowerState
		expected bool
	}{
		{"PoweredOn", vmv1alpha1.VirtualMachinePoweredOn, true},
		{"PoweredOff", vmv1alpha1.VirtualMachinePoweredOff, false},
		{"Suspended", vmv1alpha1.VirtualMachineSuspended, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vm := &vmv1alpha1.VirtualMachine{
				Status: vmv1alpha1.VirtualMachineStatus{PowerState: tt.state},
			}
			assert.Equal(t, tt.expected, isVMRunning(vm))
		})
	}
}
