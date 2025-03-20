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

func TestInventoryService_InitContainerNetworkPolicy(t *testing.T) {
	inventoryService, _ := createService(t)
	appApiService := &nsxt.ManagementPlaneApiFabricContainerClustersApiService{}
	expectNum := 0

	// Normal flow with multiple network policies
	t.Run("NormalFlow", func(t *testing.T) {
		instances := containerinventory.ContainerNetworkPolicyListResult{
			Results: []containerinventory.ContainerNetworkPolicy{
				{ExternalId: "1", DisplayName: "App1"},
				{ExternalId: "2", DisplayName: "App2"},
			},
			Cursor: "",
		}
		patches := gomonkey.ApplyMethod(reflect.TypeOf(appApiService), "ListContainerNetworkPolicies", func(_ *nsxt.ManagementPlaneApiFabricContainerClustersApiService, _ context.Context, _ *nsxt.ListContainerNetworkPoliciesOpts) (containerinventory.ContainerNetworkPolicyListResult, *http.Response, error) {
			return instances, nil, nil
		})
		defer patches.Reset()
		err := inventoryService.initContainerNetworkPolicy("cluster1")
		assert.NoError(t, err)
		itemNum := len(inventoryService.NetworkPolicyStore.List())
		expectNum += 2
		assert.Equal(t, expectNum, itemNum, "expected %d item in the inventory, got %d", expectNum, itemNum)

	})

	// Error when retrieving network policies
	t.Run("ErrorRetrievingNetworkPolices", func(t *testing.T) {
		instances := containerinventory.ContainerNetworkPolicyListResult{
			Results: []containerinventory.ContainerNetworkPolicy{
				{ExternalId: "1", DisplayName: "App1"},
				{ExternalId: "2", DisplayName: "App2"},
			},
			Cursor: "",
		}
		patches := gomonkey.ApplyMethod(reflect.TypeOf(appApiService), "ListContainerNetworkPolicies", func(_ *nsxt.ManagementPlaneApiFabricContainerClustersApiService, _ context.Context, _ *nsxt.ListContainerNetworkPoliciesOpts) (containerinventory.ContainerNetworkPolicyListResult, *http.Response, error) {
			return instances, nil, errors.New("list error")
		})
		defer patches.Reset()

		err := inventoryService.initContainerNetworkPolicy("cluster1")
		assert.Error(t, err)
		itemNum := len(inventoryService.NetworkPolicyStore.List())
		assert.Equal(t, expectNum, itemNum, "expected %d item in the inventory, got %d", expectNum, itemNum)
	})

	t.Run("PaginationHandling", func(t *testing.T) {
		instancesPage3 := containerinventory.ContainerNetworkPolicyListResult{
			Results: []containerinventory.ContainerNetworkPolicy{
				{ExternalId: "3", DisplayName: "App3"},
			},
			Cursor: "cursor1",
		}
		instancesPage4 := containerinventory.ContainerNetworkPolicyListResult{
			Results: []containerinventory.ContainerNetworkPolicy{
				{ExternalId: "4", DisplayName: "App4"},
			},
			Cursor: "",
		}
		instances := []containerinventory.ContainerNetworkPolicyListResult{instancesPage3, instancesPage4}
		times := 0
		patches := gomonkey.ApplyMethod(reflect.TypeOf(appApiService), "ListContainerNetworkPolicies", func(_ *nsxt.ManagementPlaneApiFabricContainerClustersApiService, _ context.Context, _ *nsxt.ListContainerNetworkPoliciesOpts) (containerinventory.ContainerNetworkPolicyListResult, *http.Response, error) {
			defer func() { times += 1 }()
			return instances[times], nil, nil
		})
		defer patches.Reset()
		err := inventoryService.initContainerNetworkPolicy("cluster1")
		assert.NoError(t, err)
		itemNum := len(inventoryService.NetworkPolicyStore.List())
		expectNum += 2
		assert.Equal(t, expectNum, itemNum, "expected %d item in the inventory, got %d", expectNum, itemNum)
	})

}
