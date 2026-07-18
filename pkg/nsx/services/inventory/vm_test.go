package inventory

import (
	"context"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	vmv1alpha1 "github.com/vmware-tanzu/vm-operator/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	nsxvmwarecomv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
	mockClient "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	commonservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func createVMTestService(t *testing.T) (*InventoryService, *mockClient.MockClient) {
	config2 := nsx.NewConfig("localhost", "1", "1", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{"127.0.0.1"})
	cluster, _ := nsx.NewCluster(config2)
	rc := cluster.NewRestConnector()

	mockCtrl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtrl)

	service := NewInventoryService(commonservice.Service{
		Client: k8sClient,
		NSXClient: &nsx.Client{
			RestConnector: rc,
			Cluster:       cluster,
		},
	})
	return service, k8sClient
}

func TestSyncVirtualMachineTag_VMDeleted(t *testing.T) {
	service, k8sClient := createVMTestService(t)
	service.taggedVMs["uuid-1234"] = "cluster-a"

	k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Name: "vm-1", Namespace: "tks"}, gomock.Any()).
		Return(apierrors.NewNotFound(schema.GroupResource{Group: "vmoperator.vmware.com", Resource: "virtualmachines"}, "vm-1"))

	key := InventoryKey{
		InventoryType: InventoryVirtualMachine,
		ExternalId:    "uuid-1234",
		Key:           "tks/vm-1",
	}
	result := service.SyncVirtualMachineTag("vm-1", "tks", key)

	assert.Nil(t, result)
	_, exists := service.taggedVMs["uuid-1234"]
	assert.False(t, exists, "taggedVMs should be cleaned up on VM deletion")
}

func TestSyncVirtualMachineTag_VMGetError(t *testing.T) {
	service, k8sClient := createVMTestService(t)

	k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Name: "vm-1", Namespace: "tks"}, gomock.Any()).
		Return(assert.AnError)

	key := InventoryKey{
		InventoryType: InventoryVirtualMachine,
		ExternalId:    "uuid-1234",
		Key:           "tks/vm-1",
	}
	result := service.SyncVirtualMachineTag("vm-1", "tks", key)

	assert.NotNil(t, result, "should retry on transient Get error")
	assert.Equal(t, key, *result)
}

func TestSyncVirtualMachineTag_NoInstanceUUID(t *testing.T) {
	service, k8sClient := createVMTestService(t)

	k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Name: "vm-1", Namespace: "tks"}, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			vm := obj.(*vmv1alpha1.VirtualMachine)
			vm.Status.InstanceUUID = ""
			return nil
		})

	key := InventoryKey{
		InventoryType: InventoryVirtualMachine,
		ExternalId:    "",
		Key:           "tks/vm-1",
	}
	result := service.SyncVirtualMachineTag("vm-1", "tks", key)

	assert.Nil(t, result, "should not retry when InstanceUUID is empty")
}

func TestSyncVirtualMachineTag_NoNSXServiceAccount(t *testing.T) {
	service, k8sClient := createVMTestService(t)

	k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Name: "vm-1", Namespace: "tks"}, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			vm := obj.(*vmv1alpha1.VirtualMachine)
			vm.Status.InstanceUUID = "uuid-1234"
			vm.Labels = map[string]string{CAPIClusterNameLabel: "test-cluster"}
			return nil
		})

	k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, list client.ObjectList, opts ...client.ListOption) error {
			saList := list.(*nsxvmwarecomv1alpha1.NSXServiceAccountList)
			saList.Items = []nsxvmwarecomv1alpha1.NSXServiceAccount{}
			return nil
		},
	)

	key := InventoryKey{
		InventoryType: InventoryVirtualMachine,
		ExternalId:    "uuid-1234",
		Key:           "tks/vm-1",
	}
	result := service.SyncVirtualMachineTag("vm-1", "tks", key)

	assert.Nil(t, result, "should not retry when no SA and VM not in taggedVMs")
}

func TestSyncVirtualMachineTag_NoNSXServiceAccountWithTaggedVM(t *testing.T) {
	service, k8sClient := createVMTestService(t)
	service.taggedVMs["uuid-1234"] = "cluster-a"

	k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Name: "vm-1", Namespace: "tks"}, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			vm := obj.(*vmv1alpha1.VirtualMachine)
			vm.Status.InstanceUUID = "uuid-1234"
			vm.Labels = map[string]string{CAPIClusterNameLabel: "test-cluster"}
			return nil
		})

	k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, list client.ObjectList, opts ...client.ListOption) error {
			saList := list.(*nsxvmwarecomv1alpha1.NSXServiceAccountList)
			saList.Items = []nsxvmwarecomv1alpha1.NSXServiceAccount{}
			return nil
		},
	)

	patches := gomonkey.ApplyMethod(reflect.TypeOf(service), "RemoveClusterNameTagFromVM", func(_ *InventoryService, externalID, tagValue string) error {
		assert.Equal(t, "uuid-1234", externalID)
		assert.Equal(t, "cluster-a", tagValue, "should pass the existing tag value for precise removal")
		return nil
	})
	defer patches.Reset()

	key := InventoryKey{
		InventoryType: InventoryVirtualMachine,
		ExternalId:    "uuid-1234",
		Key:           "tks/vm-1",
	}
	result := service.SyncVirtualMachineTag("vm-1", "tks", key)

	assert.Nil(t, result)
	_, exists := service.taggedVMs["uuid-1234"]
	assert.False(t, exists, "taggedVMs entry should be removed after successful untag")
}

func TestSyncVirtualMachineTag_RemoveTagFails(t *testing.T) {
	service, k8sClient := createVMTestService(t)
	service.taggedVMs["uuid-1234"] = "cluster-a"

	k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Name: "vm-1", Namespace: "tks"}, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			vm := obj.(*vmv1alpha1.VirtualMachine)
			vm.Status.InstanceUUID = "uuid-1234"
			vm.Labels = map[string]string{CAPIClusterNameLabel: "test-cluster"}
			return nil
		})

	k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, list client.ObjectList, opts ...client.ListOption) error {
			saList := list.(*nsxvmwarecomv1alpha1.NSXServiceAccountList)
			saList.Items = []nsxvmwarecomv1alpha1.NSXServiceAccount{}
			return nil
		},
	)

	patches := gomonkey.ApplyMethod(reflect.TypeOf(service), "RemoveClusterNameTagFromVM", func(_ *InventoryService, _, _ string) error {
		return assert.AnError
	})
	defer patches.Reset()

	key := InventoryKey{
		InventoryType: InventoryVirtualMachine,
		ExternalId:    "uuid-1234",
		Key:           "tks/vm-1",
	}
	result := service.SyncVirtualMachineTag("vm-1", "tks", key)

	assert.NotNil(t, result, "should retry when remove tag fails")
	_, exists := service.taggedVMs["uuid-1234"]
	assert.True(t, exists, "taggedVMs entry should remain on failure")
}

func TestSyncVirtualMachineTag_AddTag(t *testing.T) {
	service, k8sClient := createVMTestService(t)

	k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Name: "vm-1", Namespace: "tks"}, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			vm := obj.(*vmv1alpha1.VirtualMachine)
			vm.Status.InstanceUUID = "uuid-1234"
			vm.Labels = map[string]string{CAPIClusterNameLabel: "test-cluster"}
			return nil
		})

	k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, list client.ObjectList, opts ...client.ListOption) error {
			saList := list.(*nsxvmwarecomv1alpha1.NSXServiceAccountList)
			saList.Items = []nsxvmwarecomv1alpha1.NSXServiceAccount{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-sa",
						Namespace: "tks",
						OwnerReferences: []metav1.OwnerReference{
							{APIVersion: "cluster.x-k8s.io/v1beta2", Kind: "Cluster", Name: "test-cluster"},
						},
					},
					Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
						Phase:       nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
						ClusterName: "cluster-abc",
					},
				},
			}
			return nil
		},
	)

	patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(service), "addClusterNameTagToVM", func(_ *InventoryService, _ string, _ string) error {
		return nil
	})
	defer patches.Reset()

	key := InventoryKey{
		InventoryType: InventoryVirtualMachine,
		ExternalId:    "uuid-1234",
		Key:           "tks/vm-1",
	}
	result := service.SyncVirtualMachineTag("vm-1", "tks", key)

	assert.Nil(t, result)
	assert.Equal(t, "cluster-abc", service.taggedVMs["uuid-1234"])
}

func TestSyncVirtualMachineTag_MultipleClustersInSameNamespace(t *testing.T) {
	service, k8sClient := createVMTestService(t)

	k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Name: "vm-a", Namespace: "shared-ns"}, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			vm := obj.(*vmv1alpha1.VirtualMachine)
			vm.Status.InstanceUUID = "uuid-a"
			vm.Labels = map[string]string{CAPIClusterNameLabel: "cluster-a"}
			return nil
		})

	k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, list client.ObjectList, opts ...client.ListOption) error {
			saList := list.(*nsxvmwarecomv1alpha1.NSXServiceAccountList)
			saList.Items = []nsxvmwarecomv1alpha1.NSXServiceAccount{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sa-cluster-a",
						Namespace: "shared-ns",
						OwnerReferences: []metav1.OwnerReference{
							{APIVersion: "cluster.x-k8s.io/v1beta2", Kind: "Cluster", Name: "cluster-a"},
						},
					},
					Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
						Phase:       nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
						ClusterName: "nsx-cluster-name-a",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sa-cluster-b",
						Namespace: "shared-ns",
						OwnerReferences: []metav1.OwnerReference{
							{APIVersion: "cluster.x-k8s.io/v1beta2", Kind: "Cluster", Name: "cluster-b"},
						},
					},
					Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
						Phase:       nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
						ClusterName: "nsx-cluster-name-b",
					},
				},
			}
			return nil
		},
	)

	patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(service), "addClusterNameTagToVM", func(_ *InventoryService, externalID string, clusterName string) error {
		assert.Equal(t, "uuid-a", externalID)
		assert.Equal(t, "nsx-cluster-name-a", clusterName, "should use SA for cluster-a, not cluster-b")
		return nil
	})
	defer patches.Reset()

	key := InventoryKey{
		InventoryType: InventoryVirtualMachine,
		ExternalId:    "uuid-a",
		Key:           "shared-ns/vm-a",
	}
	result := service.SyncVirtualMachineTag("vm-a", "shared-ns", key)

	assert.Nil(t, result)
	assert.Equal(t, "nsx-cluster-name-a", service.taggedVMs["uuid-a"],
		"should tag with cluster-a's NSX cluster name, not cluster-b's")
}

func TestSyncVirtualMachineTag_AddTagIdempotent(t *testing.T) {
	service, k8sClient := createVMTestService(t)
	service.taggedVMs["uuid-1234"] = "cluster-abc"

	k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Name: "vm-1", Namespace: "tks"}, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			vm := obj.(*vmv1alpha1.VirtualMachine)
			vm.Status.InstanceUUID = "uuid-1234"
			vm.Labels = map[string]string{CAPIClusterNameLabel: "test-cluster"}
			return nil
		})

	k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, list client.ObjectList, opts ...client.ListOption) error {
			saList := list.(*nsxvmwarecomv1alpha1.NSXServiceAccountList)
			saList.Items = []nsxvmwarecomv1alpha1.NSXServiceAccount{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-sa",
						Namespace: "tks",
						OwnerReferences: []metav1.OwnerReference{
							{APIVersion: "cluster.x-k8s.io/v1beta2", Kind: "Cluster", Name: "test-cluster"},
						},
					},
					Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
						Phase:       nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
						ClusterName: "cluster-abc",
					},
				},
			}
			return nil
		},
	)

	key := InventoryKey{
		InventoryType: InventoryVirtualMachine,
		ExternalId:    "uuid-1234",
		Key:           "tks/vm-1",
	}
	result := service.SyncVirtualMachineTag("vm-1", "tks", key)

	assert.Nil(t, result, "should skip when already tagged (idempotent)")
	assert.Equal(t, "cluster-abc", service.taggedVMs["uuid-1234"])
}

func TestSyncVirtualMachineTag_AddTagFails(t *testing.T) {
	service, k8sClient := createVMTestService(t)

	k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Name: "vm-1", Namespace: "tks"}, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			vm := obj.(*vmv1alpha1.VirtualMachine)
			vm.Status.InstanceUUID = "uuid-1234"
			vm.Labels = map[string]string{CAPIClusterNameLabel: "test-cluster"}
			return nil
		})

	k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, list client.ObjectList, opts ...client.ListOption) error {
			saList := list.(*nsxvmwarecomv1alpha1.NSXServiceAccountList)
			saList.Items = []nsxvmwarecomv1alpha1.NSXServiceAccount{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-sa",
						Namespace: "tks",
						OwnerReferences: []metav1.OwnerReference{
							{APIVersion: "cluster.x-k8s.io/v1beta2", Kind: "Cluster", Name: "test-cluster"},
						},
					},
					Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
						Phase:       nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
						ClusterName: "cluster-abc",
					},
				},
			}
			return nil
		},
	)

	patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(service), "addClusterNameTagToVM", func(_ *InventoryService, _ string, _ string) error {
		return assert.AnError
	})
	defer patches.Reset()

	key := InventoryKey{
		InventoryType: InventoryVirtualMachine,
		ExternalId:    "uuid-1234",
		Key:           "tks/vm-1",
	}
	result := service.SyncVirtualMachineTag("vm-1", "tks", key)

	assert.NotNil(t, result, "should retry when add tag fails")
	_, exists := service.taggedVMs["uuid-1234"]
	assert.False(t, exists, "taggedVMs should not have entry on failure")
}

func TestSyncVirtualMachineTag_EmptyClusterName(t *testing.T) {
	service, k8sClient := createVMTestService(t)

	k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Name: "vm-1", Namespace: "tks"}, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			vm := obj.(*vmv1alpha1.VirtualMachine)
			vm.Status.InstanceUUID = "uuid-1234"
			vm.Labels = map[string]string{CAPIClusterNameLabel: "test-cluster"}
			return nil
		})

	k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, list client.ObjectList, opts ...client.ListOption) error {
			saList := list.(*nsxvmwarecomv1alpha1.NSXServiceAccountList)
			saList.Items = []nsxvmwarecomv1alpha1.NSXServiceAccount{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-sa",
						Namespace: "tks",
						OwnerReferences: []metav1.OwnerReference{
							{APIVersion: "cluster.x-k8s.io/v1beta2", Kind: "Cluster", Name: "test-cluster"},
						},
					},
					Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
						Phase:       nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
						ClusterName: "",
					},
				},
			}
			return nil
		},
	)

	key := InventoryKey{
		InventoryType: InventoryVirtualMachine,
		ExternalId:    "uuid-1234",
		Key:           "tks/vm-1",
	}
	result := service.SyncVirtualMachineTag("vm-1", "tks", key)

	assert.Nil(t, result, "should not retry when clusterName is empty")
	_, exists := service.taggedVMs["uuid-1234"]
	assert.False(t, exists)
}

func TestSyncVirtualMachineTag_ListSAError(t *testing.T) {
	service, k8sClient := createVMTestService(t)

	k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Name: "vm-1", Namespace: "tks"}, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			vm := obj.(*vmv1alpha1.VirtualMachine)
			vm.Status.InstanceUUID = "uuid-1234"
			vm.Labels = map[string]string{CAPIClusterNameLabel: "test-cluster"}
			return nil
		})

	k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(assert.AnError)

	key := InventoryKey{
		InventoryType: InventoryVirtualMachine,
		ExternalId:    "uuid-1234",
		Key:           "tks/vm-1",
	}
	result := service.SyncVirtualMachineTag("vm-1", "tks", key)

	assert.NotNil(t, result, "should retry when List NSXServiceAccount fails")
}

func TestInitTaggedVMs(t *testing.T) {
	t.Run("NormalFlow", func(t *testing.T) {
		service, _ := createVMTestService(t)

		patches := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.Cluster), "HttpGet", func(_ *nsx.Cluster, url string) (map[string]interface{}, error) {
			return map[string]interface{}{
				"results": []interface{}{
					map[string]interface{}{
						"external_id":  "uuid-1",
						"display_name": "vm-1",
						"tags": []interface{}{
							map[string]interface{}{
								"scope": TagScopeClusterName,
								"tag":   "cluster-abc",
							},
						},
					},
					map[string]interface{}{
						"external_id":  "uuid-2",
						"display_name": "vm-2",
						"tags": []interface{}{
							map[string]interface{}{
								"scope": "other-scope",
								"tag":   "other-value",
							},
						},
					},
					map[string]interface{}{
						"external_id":  "uuid-3",
						"display_name": "vm-3",
						"tags": []interface{}{
							map[string]interface{}{
								"scope": TagScopeClusterName,
								"tag":   "cluster-def",
							},
						},
					},
				},
			}, nil
		})
		defer patches.Reset()

		err := service.initTaggedVMs()
		assert.NoError(t, err)
		assert.Equal(t, 2, len(service.taggedVMs))
		assert.Equal(t, "cluster-abc", service.taggedVMs["uuid-1"])
		assert.Equal(t, "cluster-def", service.taggedVMs["uuid-3"])
		_, exists := service.taggedVMs["uuid-2"]
		assert.False(t, exists, "VM without cluster-name tag should not be in store")
	})

	t.Run("HttpGetError", func(t *testing.T) {
		service, _ := createVMTestService(t)

		patches := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.Cluster), "HttpGet", func(_ *nsx.Cluster, url string) (map[string]interface{}, error) {
			return nil, assert.AnError
		})
		defer patches.Reset()

		err := service.initTaggedVMs()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to search tagged virtual machines")
	})

	t.Run("Pagination", func(t *testing.T) {
		service, _ := createVMTestService(t)
		callCount := 0

		patches := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.Cluster), "HttpGet", func(_ *nsx.Cluster, url string) (map[string]interface{}, error) {
			callCount++
			if callCount == 1 {
				return map[string]interface{}{
					"results": []interface{}{
						map[string]interface{}{
							"external_id":  "uuid-page1",
							"display_name": "vm-page1",
							"tags": []interface{}{
								map[string]interface{}{
									"scope": TagScopeClusterName,
									"tag":   "cluster-1",
								},
							},
						},
					},
					"cursor": "page2",
				}, nil
			}
			return map[string]interface{}{
				"results": []interface{}{
					map[string]interface{}{
						"external_id":  "uuid-page2",
						"display_name": "vm-page2",
						"tags": []interface{}{
							map[string]interface{}{
								"scope": TagScopeClusterName,
								"tag":   "cluster-2",
							},
						},
					},
				},
			}, nil
		})
		defer patches.Reset()

		err := service.initTaggedVMs()
		assert.NoError(t, err)
		assert.Equal(t, 2, len(service.taggedVMs))
		assert.Equal(t, "cluster-1", service.taggedVMs["uuid-page1"])
		assert.Equal(t, "cluster-2", service.taggedVMs["uuid-page2"])
		assert.Equal(t, 2, callCount)
	})
}

func TestInitTaggedVMs_InvalidResults(t *testing.T) {
	t.Run("NonMapResult", func(t *testing.T) {
		service, _ := createVMTestService(t)

		patches := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.Cluster), "HttpGet", func(_ *nsx.Cluster, url string) (map[string]interface{}, error) {
			return map[string]interface{}{
				"results": []interface{}{
					"not-a-map",
				},
			}, nil
		})
		defer patches.Reset()

		err := service.initTaggedVMs()
		assert.NoError(t, err)
		assert.Equal(t, 0, len(service.taggedVMs))
	})

	t.Run("NonMapTag", func(t *testing.T) {
		service, _ := createVMTestService(t)

		patches := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.Cluster), "HttpGet", func(_ *nsx.Cluster, url string) (map[string]interface{}, error) {
			return map[string]interface{}{
				"results": []interface{}{
					map[string]interface{}{
						"external_id":  "uuid-1",
						"display_name": "vm-1",
						"tags": []interface{}{
							"not-a-map-tag",
						},
					},
				},
			}, nil
		})
		defer patches.Reset()

		err := service.initTaggedVMs()
		assert.NoError(t, err)
		assert.Equal(t, 0, len(service.taggedVMs))
	})

	t.Run("EmptyResults", func(t *testing.T) {
		service, _ := createVMTestService(t)

		patches := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.Cluster), "HttpGet", func(_ *nsx.Cluster, url string) (map[string]interface{}, error) {
			return map[string]interface{}{}, nil
		})
		defer patches.Reset()

		err := service.initTaggedVMs()
		assert.NoError(t, err)
		assert.Equal(t, 0, len(service.taggedVMs))
	})
}

func TestAddClusterNameTagToVM(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		service, _ := createVMTestService(t)

		patches := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.Cluster), "HttpPost", func(_ *nsx.Cluster, url string, body interface{}) (map[string]interface{}, error) {
			assert.Equal(t, vmAddTagsURL, url)
			return nil, nil
		})
		defer patches.Reset()

		err := service.addClusterNameTagToVM("uuid-1", "cluster-abc")
		assert.NoError(t, err)
	})

	t.Run("Error", func(t *testing.T) {
		service, _ := createVMTestService(t)

		patches := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.Cluster), "HttpPost", func(_ *nsx.Cluster, url string, body interface{}) (map[string]interface{}, error) {
			return nil, assert.AnError
		})
		defer patches.Reset()

		err := service.addClusterNameTagToVM("uuid-1", "cluster-abc")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to add VM tags via NSX API")
	})
}

func TestRemoveClusterNameTagFromVM(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		service, _ := createVMTestService(t)

		patches := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.Cluster), "HttpPost", func(_ *nsx.Cluster, url string, body interface{}) (map[string]interface{}, error) {
			assert.Equal(t, vmRemoveTagsURL, url)
			update := body.(vmTagUpdate)
			assert.Equal(t, "uuid-1", update.ExternalID)
			assert.Equal(t, TagScopeClusterName, update.Tags[0].Scope)
			assert.Equal(t, "cluster-abc", update.Tags[0].Tag, "tag value must match the actual tag on the VM")
			return nil, nil
		})
		defer patches.Reset()

		err := service.RemoveClusterNameTagFromVM("uuid-1", "cluster-abc")
		assert.NoError(t, err)
	})

	t.Run("Error", func(t *testing.T) {
		service, _ := createVMTestService(t)

		patches := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.Cluster), "HttpPost", func(_ *nsx.Cluster, url string, body interface{}) (map[string]interface{}, error) {
			return nil, assert.AnError
		})
		defer patches.Reset()

		err := service.RemoveClusterNameTagFromVM("uuid-1", "cluster-abc")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to remove VM tag via NSX API")
	})
}

func TestFindRealizedNSXServiceAccountForCluster(t *testing.T) {
	t.Run("FoundMatchingCluster", func(t *testing.T) {
		service, k8sClient := createVMTestService(t)

		k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, list client.ObjectList, opts ...client.ListOption) error {
				saList := list.(*nsxvmwarecomv1alpha1.NSXServiceAccountList)
				saList.Items = []nsxvmwarecomv1alpha1.NSXServiceAccount{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sa-other",
							Namespace: "tks",
							OwnerReferences: []metav1.OwnerReference{
								{APIVersion: "cluster.x-k8s.io/v1beta2", Kind: "Cluster", Name: "other-cluster"},
							},
						},
						Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
							Phase:       nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
							ClusterName: "cluster-other",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sa-target",
							Namespace: "tks",
							OwnerReferences: []metav1.OwnerReference{
								{APIVersion: "cluster.x-k8s.io/v1beta2", Kind: "Cluster", Name: "target-cluster"},
							},
						},
						Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
							Phase:       nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
							ClusterName: "cluster-found",
						},
					},
				}
				return nil
			},
		)

		sa, err := service.findRealizedNSXServiceAccountForCluster("tks", "target-cluster")
		assert.NoError(t, err)
		assert.NotNil(t, sa)
		assert.Equal(t, "sa-target", sa.Name)
		assert.Equal(t, "cluster-found", sa.Status.ClusterName)
	})

	t.Run("NoMatchingCluster", func(t *testing.T) {
		service, k8sClient := createVMTestService(t)

		k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, list client.ObjectList, opts ...client.ListOption) error {
				saList := list.(*nsxvmwarecomv1alpha1.NSXServiceAccountList)
				saList.Items = []nsxvmwarecomv1alpha1.NSXServiceAccount{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sa-1",
							Namespace: "tks",
							OwnerReferences: []metav1.OwnerReference{
								{APIVersion: "cluster.x-k8s.io/v1beta2", Kind: "Cluster", Name: "other-cluster"},
							},
						},
						Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
							Phase: nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
						},
					},
				}
				return nil
			},
		)

		sa, err := service.findRealizedNSXServiceAccountForCluster("tks", "target-cluster")
		assert.NoError(t, err)
		assert.Nil(t, sa)
	})

	t.Run("EmptyClusterName", func(t *testing.T) {
		service, k8sClient := createVMTestService(t)

		k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, list client.ObjectList, opts ...client.ListOption) error {
				saList := list.(*nsxvmwarecomv1alpha1.NSXServiceAccountList)
				saList.Items = []nsxvmwarecomv1alpha1.NSXServiceAccount{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sa-1",
							Namespace: "tks",
							OwnerReferences: []metav1.OwnerReference{
								{APIVersion: "cluster.x-k8s.io/v1beta2", Kind: "Cluster", Name: "some-cluster"},
							},
						},
						Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
							Phase: nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized,
						},
					},
				}
				return nil
			},
		)

		sa, err := service.findRealizedNSXServiceAccountForCluster("tks", "")
		assert.NoError(t, err)
		assert.Nil(t, sa)
	})

	t.Run("NoneRealized", func(t *testing.T) {
		service, k8sClient := createVMTestService(t)

		k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, list client.ObjectList, opts ...client.ListOption) error {
				saList := list.(*nsxvmwarecomv1alpha1.NSXServiceAccountList)
				saList.Items = []nsxvmwarecomv1alpha1.NSXServiceAccount{
					{
						ObjectMeta: metav1.ObjectMeta{
							OwnerReferences: []metav1.OwnerReference{
								{APIVersion: "cluster.x-k8s.io/v1beta2", Kind: "Cluster", Name: "target-cluster"},
							},
						},
						Status: nsxvmwarecomv1alpha1.NSXServiceAccountStatus{
							Phase: nsxvmwarecomv1alpha1.NSXServiceAccountPhaseInProgress,
						},
					},
				}
				return nil
			},
		)

		sa, err := service.findRealizedNSXServiceAccountForCluster("tks", "target-cluster")
		assert.NoError(t, err)
		assert.Nil(t, sa)
	})

	t.Run("ListError", func(t *testing.T) {
		service, k8sClient := createVMTestService(t)

		k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(assert.AnError)

		sa, err := service.findRealizedNSXServiceAccountForCluster("tks", "target-cluster")
		assert.Error(t, err)
		assert.Nil(t, sa)
	})
}

func TestOwnerClusterMatches(t *testing.T) {
	tests := []struct {
		name        string
		sa          *nsxvmwarecomv1alpha1.NSXServiceAccount
		clusterName string
		expected    bool
	}{
		{
			name: "Matching cluster",
			sa: &nsxvmwarecomv1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{APIVersion: "cluster.x-k8s.io/v1beta2", Kind: "Cluster", Name: "my-cluster"},
					},
				},
			},
			clusterName: "my-cluster",
			expected:    true,
		},
		{
			name: "Non-matching cluster name",
			sa: &nsxvmwarecomv1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{APIVersion: "cluster.x-k8s.io/v1beta2", Kind: "Cluster", Name: "other-cluster"},
					},
				},
			},
			clusterName: "my-cluster",
			expected:    false,
		},
		{
			name: "Empty cluster name",
			sa: &nsxvmwarecomv1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{APIVersion: "cluster.x-k8s.io/v1beta2", Kind: "Cluster", Name: "my-cluster"},
					},
				},
			},
			clusterName: "",
			expected:    false,
		},
		{
			name: "Non-CAPI OwnerRef",
			sa: &nsxvmwarecomv1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{APIVersion: "apps/v1", Kind: "Deployment", Name: "my-cluster"},
					},
				},
			},
			clusterName: "my-cluster",
			expected:    false,
		},
		{
			name: "No OwnerReferences",
			sa: &nsxvmwarecomv1alpha1.NSXServiceAccount{
				ObjectMeta: metav1.ObjectMeta{},
			},
			clusterName: "my-cluster",
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ownerClusterMatches(tt.sa, tt.clusterName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetVMExternalID(t *testing.T) {
	vm := &vmv1alpha1.VirtualMachine{
		Status: vmv1alpha1.VirtualMachineStatus{
			InstanceUUID: "test-uuid-123",
		},
	}
	assert.Equal(t, "test-uuid-123", getVMExternalID(vm))

	emptyVM := &vmv1alpha1.VirtualMachine{}
	assert.Equal(t, "", getVMExternalID(emptyVM))
}
