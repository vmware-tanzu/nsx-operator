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
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
)

func TestInventoryService_InitContainerApplicationInstance(t *testing.T) {
	inventoryService, _ := createService(t)
	appApiService := &nsxt.ManagementPlaneApiFabricContainerApplicationsApiService{}
	expectNum := 0

	// Normal flow with multiple application instances
	t.Run("NormalFlow", func(t *testing.T) {
		instances := containerinventory.ContainerApplicationInstanceListResult{
			Results: []containerinventory.ContainerApplicationInstance{
				{ExternalId: "1", ContainerProjectId: "project1", DisplayName: "App1"},
				{ExternalId: "2", ContainerProjectId: "project2", DisplayName: "App2"},
			},
			Cursor: "",
		}
		patches := gomonkey.ApplyMethod(reflect.TypeOf(appApiService), "ListContainerApplicationInstances", func(_ *nsxt.ManagementPlaneApiFabricContainerApplicationsApiService, _ context.Context, _ *nsxt.ListContainerApplicationInstancesOpts) (containerinventory.ContainerApplicationInstanceListResult, *http.Response, error) {
			return instances, nil, nil
		})
		defer patches.Reset()
		err := inventoryService.initContainerApplicationInstance("cluster1")
		assert.NoError(t, err)
		itemNum := len(inventoryService.ApplicationInstanceStore.List())
		expectNum += 2
		assert.Equal(t, expectNum, itemNum, "expected %d item in the inventory, got %d", expectNum, itemNum)

	})

	// Error when retrieving application instances
	t.Run("ErrorRetrievingInstances", func(t *testing.T) {
		instances := containerinventory.ContainerApplicationInstanceListResult{
			Results: []containerinventory.ContainerApplicationInstance{
				{ExternalId: "1", ContainerProjectId: "project1", DisplayName: "App1"},
				{ExternalId: "2", ContainerProjectId: "project2", DisplayName: "App2"},
			},
			Cursor: "",
		}
		patches := gomonkey.ApplyMethod(reflect.TypeOf(appApiService), "ListContainerApplicationInstances", func(_ *nsxt.ManagementPlaneApiFabricContainerApplicationsApiService, _ context.Context, _ *nsxt.ListContainerApplicationInstancesOpts) (containerinventory.ContainerApplicationInstanceListResult, *http.Response, error) {
			return instances, nil, errors.New("list error")
		})
		defer patches.Reset()

		err := inventoryService.initContainerApplicationInstance("cluster1")
		assert.Error(t, err)
		itemNum := len(inventoryService.ApplicationInstanceStore.List())
		assert.Equal(t, expectNum, itemNum, "expected %d item in the inventory, got %d", expectNum, itemNum)
	})

	// Application instance with empty ContainerProjectId
	t.Run("EmptyContainerProjectId", func(t *testing.T) {
		instances := containerinventory.ContainerApplicationInstanceListResult{
			Results: []containerinventory.ContainerApplicationInstance{
				{ExternalId: "1", ContainerProjectId: "", DisplayName: "App1"},
			},
			Cursor: "",
		}
		patches := gomonkey.ApplyMethod(reflect.TypeOf(appApiService), "ListContainerApplicationInstances", func(_ *nsxt.ManagementPlaneApiFabricContainerApplicationsApiService, _ context.Context, _ *nsxt.ListContainerApplicationInstancesOpts) (containerinventory.ContainerApplicationInstanceListResult, *http.Response, error) {
			return instances, nil, nil
		})
		defer patches.Reset()
		err := inventoryService.initContainerApplicationInstance("cluster1")
		assert.NoError(t, err)
		assert.NotNil(t, inventoryService.stalePods["1"])
		itemNum := len(inventoryService.ApplicationInstanceStore.List())
		assert.Equal(t, expectNum, itemNum, "expected %d item in the inventory, got %d", expectNum, itemNum)
	})

	t.Run("PaginationHandling", func(t *testing.T) {
		instancesPage3 := containerinventory.ContainerApplicationInstanceListResult{
			Results: []containerinventory.ContainerApplicationInstance{
				{ExternalId: "3", ContainerProjectId: "project3", DisplayName: "App3"},
			},
			Cursor: "cursor1",
		}
		instancesPage4 := containerinventory.ContainerApplicationInstanceListResult{
			Results: []containerinventory.ContainerApplicationInstance{
				{ExternalId: "4", ContainerProjectId: "project4", DisplayName: "App4"},
			},
			Cursor: "",
		}
		instances := []containerinventory.ContainerApplicationInstanceListResult{instancesPage3, instancesPage4}
		times := 0
		patches := gomonkey.ApplyMethod(reflect.TypeOf(appApiService), "ListContainerApplicationInstances", func(_ *nsxt.ManagementPlaneApiFabricContainerApplicationsApiService, _ context.Context, _ *nsxt.ListContainerApplicationInstancesOpts) (containerinventory.ContainerApplicationInstanceListResult, *http.Response, error) {
			defer func() { times += 1 }()
			return instances[times], nil, nil
		})
		defer patches.Reset()
		err := inventoryService.initContainerApplicationInstance("cluster1")
		assert.NoError(t, err)
		itemNum := len(inventoryService.ApplicationInstanceStore.List())
		expectNum += 2
		assert.Equal(t, expectNum, itemNum, "expected %d item in the inventory, got %d", expectNum, itemNum)
	})

}

func TestCleanStaleInventoryApplicationInstance(t *testing.T) {
	cfg := &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}
	cfg.InventoryBatchPeriod = 60
	cfg.InventoryBatchSize = 100

	inventoryService, _ := createService(t)

	t.Run(("Normal flow, no project found"), func(t *testing.T) {
		inventoryService.ApplicationInstanceStore.Add(&containerinventory.ContainerApplicationInstance{
			DisplayName:        "test",
			ResourceType:       "ContainerApplicationInstance",
			ContainerProjectId: "qe",
		})

		err := inventoryService.CleanStaleInventoryApplicationInstance()
		assert.Nil(t, err)
		count := len(inventoryService.ApplicationInstanceStore.List())
		assert.Equal(t, 1, count)
	})
	ns1 := containerinventory.ContainerProject{
		DisplayName:  "qe",
		ExternalId:   "123-qe",
		ResourceType: "ContainerApplicationInstance",
	}
	t.Run(("Normal flow, project found"), func(t *testing.T) {
		inventoryService.ApplicationInstanceStore.Add(&containerinventory.ContainerApplicationInstance{
			DisplayName:        "test",
			ResourceType:       "ContainerApplicationInstance",
			ContainerProjectId: "123-qe",
		})
		inventoryService.ProjectStore.Add(&ns1)
		patches := gomonkey.ApplyMethod(reflect.TypeOf(inventoryService), "IsPodDeleted", func(_ *InventoryService, _ string, _ string, _ string) bool {
			return true
		})
		defer patches.Reset()
		err := inventoryService.CleanStaleInventoryApplicationInstance()
		assert.Nil(t, err)
		count := len(inventoryService.ApplicationInstanceStore.List())
		assert.Equal(t, 1, count)
	})

	t.Run(("Project found, failed to delete"), func(t *testing.T) {
		inventoryService.ApplicationInstanceStore.Add(&containerinventory.ContainerApplicationInstance{
			DisplayName:        "test",
			ResourceType:       "ContainerApplicationInstance",
			ContainerProjectId: "123-qe",
		})
		deleteErr := errors.New("failed to delete")
		patches := gomonkey.ApplyMethod(reflect.TypeOf(inventoryService), "DeleteResource", func(_ *InventoryService, _ string, _ InventoryType) error {
			return deleteErr
		})
		patches.ApplyMethod(reflect.TypeOf(inventoryService), "IsPodDeleted", func(_ *InventoryService, _ string, _ string, _ string) bool {
			return true
		})
		defer patches.Reset()
		err := inventoryService.CleanStaleInventoryApplicationInstance()
		assert.Equal(t, err, deleteErr)
		count := len(inventoryService.ApplicationInstanceStore.List())
		assert.Equal(t, 1, count)
		inventoryService.ProjectStore.Delete(&ns1)
		count = len(inventoryService.ProjectStore.List())
		assert.Equal(t, 0, count)
	})
	t.Run(("No project found, failed to delete"), func(t *testing.T) {
		inventoryService.ApplicationInstanceStore.Add(&containerinventory.ContainerApplicationInstance{
			DisplayName:        "test",
			ResourceType:       "ContainerApplicationInstance",
			ContainerProjectId: "123-qe",
		})
		deleteErr := errors.New("failed to delete")
		patches := gomonkey.ApplyMethod(reflect.TypeOf(inventoryService), "DeleteResource", func(_ *InventoryService, _ string, _ InventoryType) error {
			return deleteErr
		})
		patches.ApplyMethod(reflect.TypeOf(inventoryService), "IsPodDeleted", func(_ *InventoryService, _ string, _ string, _ string) bool {
			return true
		})
		defer patches.Reset()
		err := inventoryService.CleanStaleInventoryApplicationInstance()
		assert.Equal(t, err, deleteErr)
		count := len(inventoryService.ApplicationInstanceStore.List())
		assert.Equal(t, 1, count)
	})

}

func TestInventoryService_IsPodDeleted(t *testing.T) {
	t.Run("PodNotFound", func(t *testing.T) {
		inventoryService, mockClient := createService(t)

		// Mock Client.Get to return a NotFound error
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(apierrors.NewNotFound(v1.Resource("Pod"), "test-Pod"))

		// Call IsPodDeleted
		result := inventoryService.IsPodDeleted("test-namespace", "test-Pod", "external-id-1")

		// Assert the result is true
		assert.True(t, result)
	})

	t.Run("PodUIDMismatch", func(t *testing.T) {
		inventoryService, mockClient := createService(t)

		// Mock Client.Get to return an Pod with a different UID
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ types.NamespacedName, Pod client.Object, opts ...client.GetOption) error {
			Pod.(*v1.Pod).UID = "different-uid"
			return nil
		})

		// Call IsPodDeleted
		result := inventoryService.IsPodDeleted("test-namespace", "test-Pod", "external-id-1")

		// Assert the result is true
		assert.True(t, result)
	})

	t.Run("PodExistsWithMatchingUID", func(t *testing.T) {
		inventoryService, mockClient := createService(t)

		// Mock Client.Get to return an Pod with a matching UID
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ types.NamespacedName, Pod client.Object, opts ...client.GetOption) error {
			Pod.(*v1.Pod).UID = "external-id-1"
			return nil
		})

		// Call IsPodDeleted
		result := inventoryService.IsPodDeleted("test-namespace", "test-Pod", "external-id-1")

		// Assert the result is false
		assert.False(t, result)
	})

	t.Run("UnexpectedError", func(t *testing.T) {
		inventoryService, mockClient := createService(t)

		// Mock Client.Get to return an unexpected error
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("unexpected error"))

		// Call IsPodDeleted
		result := inventoryService.IsPodDeleted("test-namespace", "test-Pod", "external-id-1")

		// Assert the result is false
		assert.False(t, result)
	})
}
