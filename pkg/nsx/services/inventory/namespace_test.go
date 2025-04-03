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

func TestInventoryService_InitContainerProject(t *testing.T) {
	inventoryService, _ := createService(t)
	appApiService := &nsxt.ManagementPlaneApiFabricContainerProjectsApiService{}
	expectNum := 0

	// Normal flow with multiple project
	t.Run("NormalFlow", func(t *testing.T) {
		instances := containerinventory.ContainerProjectListResult{
			Results: []containerinventory.ContainerProject{
				{ExternalId: "1", DisplayName: "App1"},
				{ExternalId: "2", DisplayName: "App2"},
			},
			Cursor: "",
		}
		patches := gomonkey.ApplyMethod(reflect.TypeOf(appApiService), "ListContainerProjects", func(_ *nsxt.ManagementPlaneApiFabricContainerProjectsApiService, _ context.Context, _ *nsxt.ListContainerProjectsOpts) (containerinventory.ContainerProjectListResult, *http.Response, error) {
			return instances, nil, nil
		})
		defer patches.Reset()
		err := inventoryService.initContainerProject("cluster1")
		assert.NoError(t, err)
		itemNum := len(inventoryService.ProjectStore.List())
		expectNum += 2
		assert.Equal(t, expectNum, itemNum, "expected %d item in the inventory, got %d", expectNum, itemNum)

	})

	// Error when retrieving projects
	t.Run("ErrorRetrievingProjects", func(t *testing.T) {
		instances := containerinventory.ContainerProjectListResult{
			Results: []containerinventory.ContainerProject{
				{ExternalId: "1", DisplayName: "App1"},
				{ExternalId: "2", DisplayName: "App2"},
			},
			Cursor: "",
		}
		patches := gomonkey.ApplyMethod(reflect.TypeOf(appApiService), "ListContainerProjects", func(_ *nsxt.ManagementPlaneApiFabricContainerProjectsApiService, _ context.Context, _ *nsxt.ListContainerProjectsOpts) (containerinventory.ContainerProjectListResult, *http.Response, error) {
			return instances, nil, errors.New("list error")
		})
		defer patches.Reset()

		err := inventoryService.initContainerProject("cluster1")
		assert.Error(t, err)
		itemNum := len(inventoryService.ProjectStore.List())
		assert.Equal(t, expectNum, itemNum, "expected %d item in the inventory, got %d", expectNum, itemNum)
	})

	t.Run("PaginationHandling", func(t *testing.T) {
		instancesPage3 := containerinventory.ContainerProjectListResult{
			Results: []containerinventory.ContainerProject{
				{ExternalId: "3", DisplayName: "App3"},
			},
			Cursor: "cursor1",
		}
		instancesPage4 := containerinventory.ContainerProjectListResult{
			Results: []containerinventory.ContainerProject{
				{ExternalId: "4", DisplayName: "App4"},
			},
			Cursor: "",
		}
		instances := []containerinventory.ContainerProjectListResult{instancesPage3, instancesPage4}
		times := 0
		patches := gomonkey.ApplyMethod(reflect.TypeOf(appApiService), "ListContainerProjects", func(_ *nsxt.ManagementPlaneApiFabricContainerProjectsApiService, _ context.Context, _ *nsxt.ListContainerProjectsOpts) (containerinventory.ContainerProjectListResult, *http.Response, error) {
			defer func() { times += 1 }()
			return instances[times], nil, nil
		})
		defer patches.Reset()
		err := inventoryService.initContainerProject("cluster1")
		assert.NoError(t, err)
		itemNum := len(inventoryService.ProjectStore.List())
		expectNum += 2
		assert.Equal(t, expectNum, itemNum, "expected %d item in the inventory, got %d", expectNum, itemNum)
	})
}

func TestCleanStaleInventoryContainerProject(t *testing.T) {
	cfg := &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}
	cfg.InventoryBatchPeriod = 60
	cfg.InventoryBatchSize = 100
	inventoryService, _ := createService(t)

	// Add some test container projects to the store
	projects := []containerinventory.ContainerProject{
		{ExternalId: "1", DisplayName: "namespace1"},
		{ExternalId: "2", DisplayName: "namespace2"},
		{ExternalId: "3", DisplayName: "namespace3"},
	}

	for _, project := range projects {
		err := inventoryService.ProjectStore.Add(&project)
		assert.NoError(t, err)
	}

	// Initial count should be 3
	itemNum := len(inventoryService.ProjectStore.List())
	assert.Equal(t, 3, itemNum, "expected 3 items in the inventory, got %d", itemNum)

	// Test successful cleaning of stale projects
	t.Run("CleanStaleProjects", func(t *testing.T) {
		// Mock IsNamespaceDeleted to return true for specific namespaces
		patches := gomonkey.ApplyMethod(reflect.TypeOf(inventoryService), "IsNamespaceDeleted",
			func(_ *InventoryService, name, externalId string) bool {
				// Return true (namespace deleted) for namespace1 and namespace3
				return name == "namespace1" || name == "namespace3"
			})
		defer patches.Reset()

		// Mock DeleteResource to succeed
		deletePatches := gomonkey.ApplyMethod(reflect.TypeOf(inventoryService), "DeleteResource",
			func(_ *InventoryService, externalId string, resourceType InventoryType) error {
				inventoryService.ProjectStore.Delete(&containerinventory.ContainerProject{ExternalId: "1", DisplayName: "namespace1"})
				inventoryService.ProjectStore.Delete(&containerinventory.ContainerProject{ExternalId: "3", DisplayName: "namespace3"})
				return nil
			})
		defer deletePatches.Reset()

		err := inventoryService.CleanStaleInventoryContainerProject()
		assert.NoError(t, err)

		// Should have removed 2 projects (namespace1 and namespace3)
		itemNum = len(inventoryService.ProjectStore.List())
		assert.Equal(t, 1, itemNum, "expected 1 item in the inventory after cleaning stale projects, got %d", itemNum)
	})

	// Test error when deleting a resource
	t.Run("DeleteResourceError", func(t *testing.T) {
		// Add back the deleted projects for the test
		for _, project := range []containerinventory.ContainerProject{
			{ExternalId: "1", DisplayName: "namespace1"},
			{ExternalId: "3", DisplayName: "namespace3"},
		} {
			err := inventoryService.ProjectStore.Add(&project)
			assert.NoError(t, err)
		}

		// Reset the count to 3
		itemNum = len(inventoryService.ProjectStore.List())
		assert.Equal(t, 3, itemNum)

		// Mock IsNamespaceDeleted to return true for all namespaces
		patches := gomonkey.ApplyMethod(reflect.TypeOf(inventoryService), "IsNamespaceDeleted",
			func(_ *InventoryService, name, externalId string) bool {
				return true
			})
		defer patches.Reset()

		// Mock DeleteResource to fail
		deletePatches := gomonkey.ApplyMethod(reflect.TypeOf(inventoryService), "DeleteResource",
			func(_ *InventoryService, externalId string, resourceType InventoryType) error {
				return errors.New("delete error")
			})
		defer deletePatches.Reset()

		err := inventoryService.CleanStaleInventoryContainerProject()
		assert.Error(t, err)
		assert.Equal(t, "delete error", err.Error())

		// Should still have 3 items since deletion failed
		itemNum = len(inventoryService.ProjectStore.List())
		assert.Equal(t, 3, itemNum, "expected 3 items in the inventory after failed deletion, got %d", itemNum)
	})
}
