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
