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
