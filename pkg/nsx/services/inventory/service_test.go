package inventory

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	nsxt "github.com/vmware/go-vmware-nsxt"
	"github.com/vmware/go-vmware-nsxt/containerinventory"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
)

func TestInventoryService_InitContainerApplication(t *testing.T) {
	inventoryService, _ := createService(t)
	appApiService := &nsxt.ManagementPlaneApiFabricContainerApplicationsApiService{}
	expectNum := 0

	// Normal flow with multiple application
	t.Run("NormalFlow", func(t *testing.T) {
		instances := containerinventory.ContainerApplicationListResult{
			Results: []containerinventory.ContainerApplication{
				{ExternalId: "1", ContainerProjectId: "project1", DisplayName: "App1"},
				{ExternalId: "2", ContainerProjectId: "project2", DisplayName: "App2"},
			},
			Cursor: "",
		}
		patches := gomonkey.ApplyMethod(reflect.TypeOf(appApiService), "ListContainerApplications", func(_ *nsxt.ManagementPlaneApiFabricContainerApplicationsApiService, _ context.Context, _ *nsxt.ListContainerApplicationsOpts) (containerinventory.ContainerApplicationListResult, *http.Response, error) {
			return instances, nil, nil
		})
		defer patches.Reset()
		err := inventoryService.initContainerApplication("cluster1")
		assert.NoError(t, err)
		itemNum := len(inventoryService.ApplicationStore.List())
		expectNum += 2
		assert.Equal(t, expectNum, itemNum, "expected %d item in the inventory, got %d", expectNum, itemNum)

	})

	// Error when retrieving application
	t.Run("ErrorRetrievingApplications", func(t *testing.T) {
		instances := containerinventory.ContainerApplicationListResult{
			Results: []containerinventory.ContainerApplication{
				{ExternalId: "1", ContainerProjectId: "project1", DisplayName: "App1"},
				{ExternalId: "2", ContainerProjectId: "project2", DisplayName: "App2"},
			},
			Cursor: "",
		}
		patches := gomonkey.ApplyMethod(reflect.TypeOf(appApiService), "ListContainerApplications", func(_ *nsxt.ManagementPlaneApiFabricContainerApplicationsApiService, _ context.Context, _ *nsxt.ListContainerApplicationsOpts) (containerinventory.ContainerApplicationListResult, *http.Response, error) {
			return instances, nil, errors.New("list error")
		})
		defer patches.Reset()

		err := inventoryService.initContainerApplication("cluster1")
		assert.Error(t, err)
		itemNum := len(inventoryService.ApplicationStore.List())
		assert.Equal(t, expectNum, itemNum, "expected %d item in the inventory, got %d", expectNum, itemNum)
	})

	t.Run("PaginationHandling", func(t *testing.T) {
		instancesPage3 := containerinventory.ContainerApplicationListResult{
			Results: []containerinventory.ContainerApplication{
				{ExternalId: "3", ContainerProjectId: "project3", DisplayName: "App3"},
			},
			Cursor: "cursor1",
		}
		instancesPage4 := containerinventory.ContainerApplicationListResult{
			Results: []containerinventory.ContainerApplication{
				{ExternalId: "4", ContainerProjectId: "project4", DisplayName: "App4"},
			},
			Cursor: "",
		}
		instances := []containerinventory.ContainerApplicationListResult{instancesPage3, instancesPage4}
		times := 0
		patches := gomonkey.ApplyMethod(reflect.TypeOf(appApiService), "ListContainerApplications", func(_ *nsxt.ManagementPlaneApiFabricContainerApplicationsApiService, _ context.Context, _ *nsxt.ListContainerApplicationsOpts) (containerinventory.ContainerApplicationListResult, *http.Response, error) {
			defer func() { times += 1 }()
			return instances[times], nil, nil
		})
		defer patches.Reset()
		err := inventoryService.initContainerApplication("cluster1")
		assert.NoError(t, err)
		itemNum := len(inventoryService.ApplicationStore.List())
		expectNum += 2
		assert.Equal(t, expectNum, itemNum, "expected %d item in the inventory, got %d", expectNum, itemNum)
	})

}

func TestCleanStaleInventoryApplication(t *testing.T) {
	cfg := &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}
	cfg.InventoryBatchPeriod = 60
	cfg.InventoryBatchSize = 100
	inventoryService, _ := createService(t)

	t.Run("Normal flow, no project found", func(t *testing.T) {
		inventoryService.ApplicationStore.Add(&containerinventory.ContainerApplication{
			DisplayName:        "test-app",
			ResourceType:       "ContainerApplication",
			ContainerProjectId: "unknown-project",
		})

		err := inventoryService.CleanStaleInventoryApplication()
		assert.Nil(t, err)
		count := len(inventoryService.ApplicationStore.List())
		assert.Equal(t, 1, count)
	})

	t.Run("Project found, application deleted", func(t *testing.T) {
		ns := containerinventory.ContainerProject{
			DisplayName:  "known-project",
			ExternalId:   "123-known-project",
			ResourceType: "ContainerProject",
		}
		inventoryService.ProjectStore.Add(&ns)
		inventoryService.ApplicationStore.Add(&containerinventory.ContainerApplication{
			DisplayName:        "test-app",
			ResourceType:       "ContainerApplication",
			ContainerProjectId: "123-known-project",
		})

		patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(inventoryService), "isApplicationDeleted", func(_ *InventoryService, _ string,
			_ string, _ string) bool {
			return true
		})
		defer patches.Reset()

		err := inventoryService.CleanStaleInventoryApplication()
		assert.Nil(t, err)
		count := len(inventoryService.ApplicationStore.List())
		assert.Equal(t, 1, count)
	})

	t.Run("Project found, failed to delete application", func(t *testing.T) {
		deleteErr := errors.New("failed to delete")
		inventoryService.ApplicationStore.Add(&containerinventory.ContainerApplication{
			DisplayName:        "test-app",
			ResourceType:       "ContainerApplication",
			ContainerProjectId: "123-known-project",
		})
		patches := gomonkey.ApplyMethod(reflect.TypeOf(inventoryService), "DeleteResource", func(_ *InventoryService, _ string, _ InventoryType) error {
			return deleteErr
		})
		patches.ApplyPrivateMethod(reflect.TypeOf(inventoryService), "isApplicationDeleted", func(_ *InventoryService, _ string, _ string,
			_ string) bool {
			return true
		})
		defer patches.Reset()

		err := inventoryService.CleanStaleInventoryApplication()
		assert.Equal(t, deleteErr, err)
		count := len(inventoryService.ApplicationStore.List())
		assert.Equal(t, 1, count)
	})

	t.Run("No project found, failed to delete application", func(t *testing.T) {
		deleteErr := errors.New("failed to delete")
		inventoryService.ApplicationStore.Add(&containerinventory.ContainerApplication{
			DisplayName:        "test-app",
			ResourceType:       "ContainerApplication",
			ContainerProjectId: "unknown-project",
		})
		patches := gomonkey.ApplyMethod(reflect.TypeOf(inventoryService), "DeleteResource", func(_ *InventoryService, _ string, _ InventoryType) error {
			return deleteErr
		})
		defer patches.Reset()

		err := inventoryService.CleanStaleInventoryApplication()
		assert.Equal(t, deleteErr, err)
		count := len(inventoryService.ApplicationStore.List())
		assert.Equal(t, 1, count)
	})
}
