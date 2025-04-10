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
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
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

func TestCleanStaleInventoryNetworkPolicy(t *testing.T) {
	cfg := &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}
	cfg.InventoryBatchPeriod = 60
	cfg.InventoryBatchSize = 100

	inventoryService, _ := createService(t)

	t.Run(("Normal flow, no project found"), func(t *testing.T) {
		inventoryService.NetworkPolicyStore.Add(&containerinventory.ContainerNetworkPolicy{
			DisplayName:        "test",
			ResourceType:       "ContainerNetworkPolicy",
			ContainerProjectId: "qe",
		})

		err := inventoryService.CleanStaleInventoryNetworkPolicy()
		assert.Nil(t, err)
		count := len(inventoryService.NetworkPolicyStore.List())
		assert.Equal(t, 1, count)
	})
	ns1 := containerinventory.ContainerProject{
		DisplayName:  "qe",
		ExternalId:   "123-qe",
		ResourceType: "ContainerNetworkPolicy",
	}
	t.Run(("Normal flow, project found"), func(t *testing.T) {
		inventoryService.NetworkPolicyStore.Add(&containerinventory.ContainerNetworkPolicy{
			DisplayName:        "test",
			ResourceType:       "ContainerNetworkPolicy",
			ContainerProjectId: "123-qe",
		})
		inventoryService.ProjectStore.Add(&ns1)
		patches := gomonkey.ApplyMethod(reflect.TypeOf(inventoryService), "IsNetworkPolicyDeleted", func(_ *InventoryService, _ string, _ string, _ string) bool {
			return true
		})
		defer patches.Reset()
		err := inventoryService.CleanStaleInventoryNetworkPolicy()
		assert.Nil(t, err)
		count := len(inventoryService.NetworkPolicyStore.List())
		assert.Equal(t, 1, count)
	})

	t.Run(("Project found, failed to delete"), func(t *testing.T) {
		inventoryService.NetworkPolicyStore.Add(&containerinventory.ContainerNetworkPolicy{
			DisplayName:        "test",
			ResourceType:       "ContainerNetworkPolicy",
			ContainerProjectId: "123-qe",
		})
		deleteErr := errors.New("failed to delete")
		patches := gomonkey.ApplyMethod(reflect.TypeOf(inventoryService), "DeleteResource", func(_ *InventoryService, _ string, _ InventoryType) error {
			return deleteErr
		})
		patches.ApplyMethod(reflect.TypeOf(inventoryService), "IsNetworkPolicyDeleted", func(_ *InventoryService, _ string, _ string, _ string) bool {
			return true
		})
		defer patches.Reset()
		err := inventoryService.CleanStaleInventoryNetworkPolicy()
		assert.Equal(t, err, deleteErr)
		count := len(inventoryService.NetworkPolicyStore.List())
		assert.Equal(t, 1, count)
		inventoryService.ProjectStore.Delete(&ns1)
		count = len(inventoryService.ProjectStore.List())
		assert.Equal(t, 0, count)
	})
	t.Run(("No project found, failed to delete"), func(t *testing.T) {
		inventoryService.NetworkPolicyStore.Add(&containerinventory.ContainerNetworkPolicy{
			DisplayName:        "test",
			ResourceType:       "ContainerNetworkPolicy",
			ContainerProjectId: "123-qe",
		})
		deleteErr := errors.New("failed to delete")
		patches := gomonkey.ApplyMethod(reflect.TypeOf(inventoryService), "DeleteResource", func(_ *InventoryService, _ string, _ InventoryType) error {
			return deleteErr
		})
		patches.ApplyMethod(reflect.TypeOf(inventoryService), "IsNetworkPolicyDeleted", func(_ *InventoryService, _ string, _ string, _ string) bool {
			return true
		})
		defer patches.Reset()
		err := inventoryService.CleanStaleInventoryNetworkPolicy()
		assert.Equal(t, err, deleteErr)
		count := len(inventoryService.NetworkPolicyStore.List())
		assert.Equal(t, 1, count)
	})
}

func TestInventoryService_IsNetworkPolicyDeleted(t *testing.T) {
	t.Run("NetworkPolicyNotFound", func(t *testing.T) {
		inventoryService, mockClient := createService(t)

		// Mock Client.Get to return a NotFound error
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(apierrors.NewNotFound(networkingv1.Resource("networkpolicy"), "test-networkpolicy"))

		// Call IsNetworkPolicyDeleted
		result := inventoryService.IsNetworkPolicyDeleted("test-namespace", "test-networkpolicy", "external-id-1")

		// Assert the result is true
		assert.True(t, result)
	})

	t.Run("NetworkPolicyUIDMismatch", func(t *testing.T) {
		inventoryService, mockClient := createService(t)

		// Mock Client.Get to return a network policy with a different UID
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ types.NamespacedName, networkPolicy client.Object, opts ...client.GetOption) error {
			networkPolicy.(*networkingv1.NetworkPolicy).UID = "different-uid"
			return nil
		})

		// Call IsNetworkPolicyDeleted
		result := inventoryService.IsNetworkPolicyDeleted("test-namespace", "test-networkpolicy", "external-id-1")

		// Assert the result is true
		assert.True(t, result)
	})

	t.Run("NetworkPolicyExistsWithMatchingUID", func(t *testing.T) {
		inventoryService, mockClient := createService(t)

		// Mock Client.Get to return a network policy with a matching UID
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ types.NamespacedName, networkPolicy client.Object, opts ...client.GetOption) error {
			networkPolicy.(*networkingv1.NetworkPolicy).UID = "external-id-1"
			return nil
		})

		// Call IsNetworkPolicyDeleted
		result := inventoryService.IsNetworkPolicyDeleted("test-namespace", "test-networkpolicy", "external-id-1")

		// Assert the result is false
		assert.False(t, result)
	})

	t.Run("UnexpectedError", func(t *testing.T) {
		inventoryService, mockClient := createService(t)

		// Mock Client.Get to return an unexpected error
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("unexpected error"))

		// Call IsNetworkPolicyDeleted
		result := inventoryService.IsNetworkPolicyDeleted("test-namespace", "test-networkpolicy", "external-id-1")

		// Assert the result is false
		assert.False(t, result)
	})
}
