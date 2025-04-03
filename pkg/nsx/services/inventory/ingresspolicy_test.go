package inventory

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	nsxt "github.com/vmware/go-vmware-nsxt"
	"github.com/vmware/go-vmware-nsxt/containerinventory"
	v1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
)

func TestInventoryService_InitContainerIngressPolicy(t *testing.T) {
	inventoryService, _ := createService(t)
	appApiService := &nsxt.ManagementPlaneApiFabricContainerClustersApiService{}
	expectNum := 0

	// Normal flow with multiple Ingress policies
	t.Run("NormalFlow", func(t *testing.T) {
		instances := containerinventory.ContainerIngressPolicyListResult{
			Results: []containerinventory.ContainerIngressPolicy{
				{ExternalId: "1", DisplayName: "App1"},
				{ExternalId: "2", DisplayName: "App2"},
			},
			Cursor: "",
		}
		patches := gomonkey.ApplyMethod(reflect.TypeOf(appApiService), "ListContainerIngressPolicies", func(_ *nsxt.ManagementPlaneApiFabricContainerClustersApiService, _ context.Context, _ *nsxt.ListContainerIngressPoliciesOpts) (containerinventory.ContainerIngressPolicyListResult, *http.Response, error) {
			return instances, nil, nil
		})
		defer patches.Reset()
		err := inventoryService.initContainerIngressPolicy("cluster1")
		assert.NoError(t, err)
		itemNum := len(inventoryService.IngressPolicyStore.List())
		expectNum += 2
		assert.Equal(t, expectNum, itemNum, "expected %d item in the inventory, got %d", expectNum, itemNum)

	})

	// Error when retrieving Ingress policies
	t.Run("ErrorRetrievingIngressPolicies", func(t *testing.T) {
		instances := containerinventory.ContainerIngressPolicyListResult{
			Results: []containerinventory.ContainerIngressPolicy{
				{ExternalId: "1", DisplayName: "App1"},
				{ExternalId: "2", DisplayName: "App2"},
			},
			Cursor: "",
		}
		patches := gomonkey.ApplyMethod(reflect.TypeOf(appApiService), "ListContainerIngressPolicies", func(_ *nsxt.ManagementPlaneApiFabricContainerClustersApiService, _ context.Context, _ *nsxt.ListContainerIngressPoliciesOpts) (containerinventory.ContainerIngressPolicyListResult, *http.Response, error) {
			return instances, nil, errors.New("list error")
		})
		defer patches.Reset()

		err := inventoryService.initContainerIngressPolicy("cluster1")
		assert.Error(t, err)
		itemNum := len(inventoryService.IngressPolicyStore.List())
		assert.Equal(t, expectNum, itemNum, "expected %d item in the inventory, got %d", expectNum, itemNum)
	})

	t.Run("PaginationHandling", func(t *testing.T) {
		instancesPage3 := containerinventory.ContainerIngressPolicyListResult{
			Results: []containerinventory.ContainerIngressPolicy{
				{ExternalId: "3", DisplayName: "App3"},
			},
			Cursor: "cursor1",
		}
		instancesPage4 := containerinventory.ContainerIngressPolicyListResult{
			Results: []containerinventory.ContainerIngressPolicy{
				{ExternalId: "4", DisplayName: "App4"},
			},
			Cursor: "",
		}
		instances := []containerinventory.ContainerIngressPolicyListResult{instancesPage3, instancesPage4}
		times := 0
		patches := gomonkey.ApplyMethod(reflect.TypeOf(appApiService), "ListContainerIngressPolicies", func(_ *nsxt.ManagementPlaneApiFabricContainerClustersApiService, _ context.Context, _ *nsxt.ListContainerIngressPoliciesOpts) (containerinventory.ContainerIngressPolicyListResult, *http.Response, error) {
			defer func() { times += 1 }()
			return instances[times], nil, nil
		})
		defer patches.Reset()
		err := inventoryService.initContainerIngressPolicy("cluster1")
		assert.NoError(t, err)
		itemNum := len(inventoryService.IngressPolicyStore.List())
		expectNum += 2
		assert.Equal(t, expectNum, itemNum, "expected %d item in the inventory, got %d", expectNum, itemNum)
	})

}
func TestInventoryService_SyncContainerIngressPolicy(t *testing.T) {
	t.Run("IngressNotFound", func(t *testing.T) {
		inventoryService, mockClient := createService(t)
		key := InventoryKey{ExternalId: "external-id-1"}

		// Mock Client.Get to return a NotFound error
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(apierrors.NewNotFound(v1.Resource("ingress"), "test-ingress"))
		// Mock DeleteResource to return nil
		patchesDelete := gomonkey.ApplyMethod(reflect.TypeOf(inventoryService), "DeleteResource", func(_ *InventoryService, _ string, _ InventoryType) error {
			return nil
		})
		defer patchesDelete.Reset()

		result := inventoryService.SyncContainerIngressPolicy("test-ingress", "test-namespace", key)
		assert.Nil(t, result)
	})

	t.Run("IngressUIDMismatch", func(t *testing.T) {
		inventoryService, mockClient := createService(t)
		key := InventoryKey{ExternalId: "external-id-1"}

		// Mock Client.Get to return an ingress with a different UID
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ types.NamespacedName, ingress client.Object, opts ...client.GetOption) error {
			ingress.(*v1.Ingress).UID = "different-uid"
			return nil
		})
		// Mock DeleteResource to return nil
		patchesDelete := gomonkey.ApplyMethod(reflect.TypeOf(inventoryService), "DeleteResource", func(_ *InventoryService, _ string, _ InventoryType) error {
			return nil
		})
		defer patchesDelete.Reset()

		result := inventoryService.SyncContainerIngressPolicy("test-ingress", "test-namespace", key)
		assert.Nil(t, result)
	})

	t.Run("BuildIngressRetry", func(t *testing.T) {
		inventoryService, mockClient := createService(t)
		key := InventoryKey{ExternalId: "external-id-1"}

		// Mock Client.Get to return an ingress with a matching UID
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ types.NamespacedName, ingress client.Object, opts ...client.GetOption) error {
			ingress.(*v1.Ingress).UID = "external-id-1"
			return nil
		})

		// Mock BuildIngress to return true (indicating a retry is needed)
		patchesBuild := gomonkey.ApplyMethod(reflect.TypeOf(inventoryService), "BuildIngress", func(_ *InventoryService, ingress *v1.Ingress) bool {
			return true
		})
		defer patchesBuild.Reset()

		result := inventoryService.SyncContainerIngressPolicy("test-ingress", "test-namespace", key)
		assert.NotNil(t, result)
		assert.Equal(t, &key, result)
	})

	t.Run("UnexpectedError", func(t *testing.T) {
		inventoryService, mockClient := createService(t)
		key := InventoryKey{ExternalId: "external-id-1"}

		// Mock Client.Get to return an unexpected error
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("unexpected error"))
		result := inventoryService.SyncContainerIngressPolicy("test-ingress", "test-namespace", key)
		assert.Nil(t, result)
	})
}

func TestCleanStaleInventoryIngressPolicy(t *testing.T) {
	cfg := &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}
	cfg.InventoryBatchPeriod = 60
	cfg.InventoryBatchSize = 100

	inventoryService, _ := createService(t)

	t.Run(("Normal flow, no project found"), func(t *testing.T) {
		inventoryService.IngressPolicyStore.Add(&containerinventory.ContainerIngressPolicy{
			DisplayName:        "test",
			ResourceType:       "ContainerIngressPolicy",
			ContainerProjectId: "qe",
		})

		err := inventoryService.CleanStaleInventoryIngressPolicy()
		assert.Nil(t, err)
		count := len(inventoryService.IngressPolicyStore.List())
		assert.Equal(t, 1, count)
	})
	ns1 := containerinventory.ContainerProject{
		DisplayName:  "qe",
		ExternalId:   "123-qe",
		ResourceType: "ContainerIngressPolicy",
	}
	t.Run(("Normal flow, project found"), func(t *testing.T) {
		inventoryService.IngressPolicyStore.Add(&containerinventory.ContainerIngressPolicy{
			DisplayName:        "test",
			ResourceType:       "ContainerIngressPolicy",
			ContainerProjectId: "123-qe",
		})
		inventoryService.ProjectStore.Add(&ns1)
		patches := gomonkey.ApplyMethod(reflect.TypeOf(inventoryService), "IsIngressDeleted", func(_ *InventoryService, _ string, _ string, _ string) bool {
			return true
		})
		defer patches.Reset()
		err := inventoryService.CleanStaleInventoryIngressPolicy()
		assert.Nil(t, err)
		count := len(inventoryService.IngressPolicyStore.List())
		assert.Equal(t, 1, count)
	})

	t.Run(("Project found, failed to delete"), func(t *testing.T) {
		inventoryService.IngressPolicyStore.Add(&containerinventory.ContainerIngressPolicy{
			DisplayName:        "test",
			ResourceType:       "ContainerIngressPolicy",
			ContainerProjectId: "123-qe",
		})
		deleteErr := errors.New("failed to delete")
		patches := gomonkey.ApplyMethod(reflect.TypeOf(inventoryService), "DeleteResource", func(_ *InventoryService, _ string, _ InventoryType) error {
			return deleteErr
		})
		patches.ApplyMethod(reflect.TypeOf(inventoryService), "IsIngressDeleted", func(_ *InventoryService, _ string, _ string, _ string) bool {
			return true
		})
		defer patches.Reset()
		err := inventoryService.CleanStaleInventoryIngressPolicy()
		assert.Equal(t, err, deleteErr)
		count := len(inventoryService.IngressPolicyStore.List())
		assert.Equal(t, 1, count)
		inventoryService.ProjectStore.Delete(&ns1)
		count = len(inventoryService.ProjectStore.List())
		assert.Equal(t, 0, count)
	})
	t.Run(("No project found, failed to delete"), func(t *testing.T) {
		inventoryService.IngressPolicyStore.Add(&containerinventory.ContainerIngressPolicy{
			DisplayName:        "test",
			ResourceType:       "ContainerIngressPolicy",
			ContainerProjectId: "123-qe",
		})
		deleteErr := errors.New("failed to delete")
		patches := gomonkey.ApplyMethod(reflect.TypeOf(inventoryService), "DeleteResource", func(_ *InventoryService, _ string, _ InventoryType) error {
			return deleteErr
		})
		patches.ApplyMethod(reflect.TypeOf(inventoryService), "IsIngressDeleted", func(_ *InventoryService, _ string, _ string, _ string) bool {
			return true
		})
		defer patches.Reset()
		err := inventoryService.CleanStaleInventoryIngressPolicy()
		assert.Equal(t, err, deleteErr)
		count := len(inventoryService.IngressPolicyStore.List())
		assert.Equal(t, 1, count)
	})

}
func TestInventoryService_IsIngressDeleted(t *testing.T) {
	t.Run("IngressNotFound", func(t *testing.T) {
		inventoryService, mockClient := createService(t)

		// Mock Client.Get to return a NotFound error
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(apierrors.NewNotFound(v1.Resource("ingress"), "test-ingress"))

		// Call IsIngressDeleted
		result := inventoryService.IsIngressDeleted("test-namespace", "test-ingress", "external-id-1", nil)

		// Assert the result is true
		assert.True(t, result)
	})

	t.Run("IngressUIDMismatch", func(t *testing.T) {
		inventoryService, mockClient := createService(t)

		// Mock Client.Get to return an ingress with a different UID
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ types.NamespacedName, ingress client.Object, opts ...client.GetOption) error {
			ingress.(*v1.Ingress).UID = "different-uid"
			return nil
		})

		// Call IsIngressDeleted
		result := inventoryService.IsIngressDeleted("test-namespace", "test-ingress", "external-id-1", nil)

		// Assert the result is true
		assert.True(t, result)
	})

	t.Run("IngressExistsWithMatchingUID", func(t *testing.T) {
		inventoryService, mockClient := createService(t)

		// Mock Client.Get to return an ingress with a matching UID
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ types.NamespacedName, ingress client.Object, opts ...client.GetOption) error {
			ingress.(*v1.Ingress).UID = "external-id-1"
			return nil
		})

		// Call IsIngressDeleted
		result := inventoryService.IsIngressDeleted("test-namespace", "test-ingress", "external-id-1", nil)

		// Assert the result is false
		assert.False(t, result)
	})

	t.Run("UnexpectedError", func(t *testing.T) {
		inventoryService, mockClient := createService(t)

		// Mock Client.Get to return an unexpected error
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("unexpected error"))

		// Call IsIngressDeleted
		result := inventoryService.IsIngressDeleted("test-namespace", "test-ingress", "external-id-1", nil)

		// Assert the result is false
		assert.False(t, result)
	})
}
