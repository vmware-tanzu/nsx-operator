package inventory

import (
	"context"
	"errors"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	vmv1alpha1 "github.com/vmware-tanzu/vm-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	nsxvmwarecomv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mockClient "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/inventory"
)

func TestWatchNSXServiceAccount(t *testing.T) {
	t.Run("SuccessfullyCreateInformerAndTriggerCallbacks", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		k8sClient := mockClient.NewMockClient(mockCtrl)

		queue := MockObjectQueue[any]{}
		controller := &InventoryController{
			Client:               k8sClient,
			service:              &inventory.InventoryService{},
			keyBuffer:            sets.New[inventory.InventoryKey](),
			cf:                   &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}},
			inventoryObjectQueue: &queue,
		}
		mockCacheObj := new(MockCache)
		mockInformer := &MockInformer{handlers: cache.ResourceEventHandlerFuncs{}}
		mockCacheObj.On("GetInformer", context.Background(), &nsxvmwarecomv1alpha1.NSXServiceAccount{}).Return(mockInformer, nil)
		mgr := new(MockMgr)
		mgr.On("GetCache").Return(mockCacheObj)
		err := watchNSXServiceAccount(controller, mgr)
		assert.Nil(t, err)

		nsxSA := &nsxvmwarecomv1alpha1.NSXServiceAccount{
			ObjectMeta: metav1.ObjectMeta{Namespace: "tks", Name: "test-sa"},
			Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
				Phase: nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
			},
		}

		k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, list client.ObjectList, opts ...client.ListOption) error {
				vmList := list.(*vmv1alpha1.VirtualMachineList)
				vmList.Items = []vmv1alpha1.VirtualMachine{}
				return nil
			},
		).Times(3)

		queue.On("Add", mock.Anything).Return()

		mockInformer.registeredHandler.OnAdd(nsxSA, false)
		mockInformer.registeredHandler.OnUpdate(nsxSA, nsxSA)
		mockInformer.registeredHandler.OnDelete(nsxSA)
	})

	t.Run("CreateInformerFailure", func(t *testing.T) {
		mockCacheObj := new(MockCache)
		mockCacheObj.On("GetInformer", context.Background(), &nsxvmwarecomv1alpha1.NSXServiceAccount{}).Return(nil, errors.New("connection timeout"))
		controller := &InventoryController{}
		mgr := new(MockMgr)
		mgr.On("GetCache").Return(mockCacheObj)
		err := watchNSXServiceAccount(controller, mgr)

		assert.NotNil(t, err)
		assert.Contains(t, err.Error(), "connection timeout")
	})

	t.Run("AddEventHandlerFailure", func(t *testing.T) {
		controller := &InventoryController{}
		mockCacheObj := new(MockCache)
		mockInformer := &MockInformer{addHandlerErr: errors.New("handler error")}
		mockCacheObj.On("GetInformer", context.Background(), &nsxvmwarecomv1alpha1.NSXServiceAccount{}).Return(mockInformer, nil)
		mgr := new(MockMgr)
		mgr.On("GetCache").Return(mockCacheObj)
		err := watchNSXServiceAccount(controller, mgr)

		assert.NotNil(t, err)
		assert.Contains(t, err.Error(), "handler error")
	})
}

func TestHandleNSXServiceAccount(t *testing.T) {
	cfg := &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}

	t.Run("RealizedSAEnqueuesVMs", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		k8sClient := mockClient.NewMockClient(mockCtrl)

		queue := MockObjectQueue[any]{}
		controller := &InventoryController{
			Client:               k8sClient,
			service:              &inventory.InventoryService{},
			keyBuffer:            sets.New[inventory.InventoryKey](),
			cf:                   cfg,
			inventoryObjectQueue: &queue,
		}

		k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, list client.ObjectList, opts ...client.ListOption) error {
				vmList := list.(*vmv1alpha1.VirtualMachineList)
				vmList.Items = []vmv1alpha1.VirtualMachine{
					{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "tks",
							Name:      "vm-1",
							OwnerReferences: []metav1.OwnerReference{
								{APIVersion: "vmware.infrastructure.cluster.x-k8s.io/v1beta1", Kind: "VSphereMachine"},
							},
						},
						Status: vmv1alpha1.VirtualMachineStatus{
							InstanceUUID: "uuid-1",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "tks",
							Name:      "vm-non-vks",
						},
						Status: vmv1alpha1.VirtualMachineStatus{
							InstanceUUID: "uuid-2",
						},
					},
				}
				return nil
			},
		)

		nsxSA := &nsxvmwarecomv1alpha1.NSXServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "tks",
				Name:      "test-sa",
			},
			Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
				Phase: nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
			},
		}

		queue.On("Add", mock.MatchedBy(func(key interface{}) bool {
			k, ok := key.(inventory.InventoryKey)
			return ok && k.ExternalId == "uuid-1"
		})).Return().Once()

		controller.handleNSXServiceAccount(nsxSA)
		queue.AssertExpectations(t)
	})

	t.Run("UnrealizedSASkipped", func(t *testing.T) {
		queue := MockObjectQueue[any]{}
		controller := &InventoryController{
			service:              &inventory.InventoryService{},
			keyBuffer:            sets.New[inventory.InventoryKey](),
			cf:                   cfg,
			inventoryObjectQueue: &queue,
		}

		nsxSA := &nsxvmwarecomv1alpha1.NSXServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "tks",
				Name:      "test-sa",
			},
			Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
				Phase: nsxvmwarecomv1alpha1.NSXServiceAccountPhaseInProgress,
			},
		}

		controller.handleNSXServiceAccount(nsxSA)
		queue.AssertNotCalled(t, "Add", mock.Anything)
	})

	t.Run("DeletedFinalStateUnknownRealized", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		k8sClient := mockClient.NewMockClient(mockCtrl)

		queue := MockObjectQueue[any]{}
		controller := &InventoryController{
			Client:               k8sClient,
			service:              &inventory.InventoryService{},
			keyBuffer:            sets.New[inventory.InventoryKey](),
			cf:                   cfg,
			inventoryObjectQueue: &queue,
		}

		k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, list client.ObjectList, opts ...client.ListOption) error {
				vmList := list.(*vmv1alpha1.VirtualMachineList)
				vmList.Items = []vmv1alpha1.VirtualMachine{}
				return nil
			},
		)

		nsxSA := &nsxvmwarecomv1alpha1.NSXServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "tks",
				Name:      "test-sa",
			},
			Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
				Phase: nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
			},
		}
		deletedObj := cache.DeletedFinalStateUnknown{Obj: nsxSA}
		controller.handleNSXServiceAccount(deletedObj)
		queue.AssertNotCalled(t, "Add", mock.Anything)
	})

	t.Run("DeletedFinalStateUnknownInvalidObj", func(t *testing.T) {
		queue := MockObjectQueue[any]{}
		controller := &InventoryController{
			service:              &inventory.InventoryService{},
			keyBuffer:            sets.New[inventory.InventoryKey](),
			cf:                   cfg,
			inventoryObjectQueue: &queue,
		}

		deletedObj := cache.DeletedFinalStateUnknown{Obj: "invalid"}
		controller.handleNSXServiceAccount(deletedObj)
		queue.AssertNotCalled(t, "Add", mock.Anything)
	})
}

func TestHandleNSXServiceAccountDelete(t *testing.T) {
	cfg := &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}

	t.Run("DeletedSAEnqueuesAllVKSVMs", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		k8sClient := mockClient.NewMockClient(mockCtrl)

		queue := MockObjectQueue[any]{}
		controller := &InventoryController{
			Client:               k8sClient,
			service:              &inventory.InventoryService{},
			keyBuffer:            sets.New[inventory.InventoryKey](),
			cf:                   cfg,
			inventoryObjectQueue: &queue,
		}

		k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, list client.ObjectList, opts ...client.ListOption) error {
				vmList := list.(*vmv1alpha1.VirtualMachineList)
				vmList.Items = []vmv1alpha1.VirtualMachine{
					{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "tks",
							Name:      "vm-1",
							OwnerReferences: []metav1.OwnerReference{
								{APIVersion: "vmware.infrastructure.cluster.x-k8s.io/v1beta1", Kind: "VSphereMachine"},
							},
						},
						Status: vmv1alpha1.VirtualMachineStatus{
							InstanceUUID: "uuid-1",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "tks",
							Name:      "vm-2",
							OwnerReferences: []metav1.OwnerReference{
								{APIVersion: "vmware.infrastructure.cluster.x-k8s.io/v1beta1", Kind: "VSphereMachine"},
							},
						},
						Status: vmv1alpha1.VirtualMachineStatus{
							InstanceUUID: "uuid-2",
						},
					},
				}
				return nil
			},
		)

		nsxSA := &nsxvmwarecomv1alpha1.NSXServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "tks",
				Name:      "deleted-sa",
			},
		}

		queue.On("Add", mock.Anything).Return().Times(2)
		controller.handleNSXServiceAccountDelete(nsxSA)
		queue.AssertExpectations(t)
	})

	t.Run("DeletedFinalStateUnknownSA", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		k8sClient := mockClient.NewMockClient(mockCtrl)

		queue := MockObjectQueue[any]{}
		controller := &InventoryController{
			Client:               k8sClient,
			service:              &inventory.InventoryService{},
			keyBuffer:            sets.New[inventory.InventoryKey](),
			cf:                   cfg,
			inventoryObjectQueue: &queue,
		}

		k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, list client.ObjectList, opts ...client.ListOption) error {
				vmList := list.(*vmv1alpha1.VirtualMachineList)
				vmList.Items = []vmv1alpha1.VirtualMachine{
					{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "tks",
							Name:      "vm-1",
							OwnerReferences: []metav1.OwnerReference{
								{APIVersion: "vmware.infrastructure.cluster.x-k8s.io/v1beta1", Kind: "VSphereMachine"},
							},
						},
						Status: vmv1alpha1.VirtualMachineStatus{
							InstanceUUID: "uuid-1",
						},
					},
				}
				return nil
			},
		)

		nsxSA := &nsxvmwarecomv1alpha1.NSXServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "tks",
				Name:      "deleted-sa",
			},
		}
		deletedObj := cache.DeletedFinalStateUnknown{Obj: nsxSA}

		queue.On("Add", mock.Anything).Return().Once()
		controller.handleNSXServiceAccountDelete(deletedObj)
		queue.AssertExpectations(t)
	})

	t.Run("DeletedFinalStateUnknownInvalidObj", func(t *testing.T) {
		queue := MockObjectQueue[any]{}
		controller := &InventoryController{
			service:              &inventory.InventoryService{},
			keyBuffer:            sets.New[inventory.InventoryKey](),
			cf:                   cfg,
			inventoryObjectQueue: &queue,
		}

		deletedObj := cache.DeletedFinalStateUnknown{Obj: "invalid"}
		controller.handleNSXServiceAccountDelete(deletedObj)
		queue.AssertNotCalled(t, "Add", mock.Anything)
	})

	t.Run("DeletedSAListError", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		k8sClient := mockClient.NewMockClient(mockCtrl)

		queue := MockObjectQueue[any]{}
		controller := &InventoryController{
			Client:               k8sClient,
			service:              &inventory.InventoryService{},
			keyBuffer:            sets.New[inventory.InventoryKey](),
			cf:                   cfg,
			inventoryObjectQueue: &queue,
		}

		k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("list error"))

		nsxSA := &nsxvmwarecomv1alpha1.NSXServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "tks",
				Name:      "deleted-sa",
			},
		}

		controller.handleNSXServiceAccountDelete(nsxSA)
		queue.AssertNotCalled(t, "Add", mock.Anything)
	})
}
