package inventory

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	nsxt "github.com/vmware/go-vmware-nsxt"
	"github.com/vmware/go-vmware-nsxt/containerinventory"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

func TestInventoryService_GetContainerCluster(t *testing.T) {
	clusterApiService := &nsxt.ManagementPlaneApiFabricContainerClustersApiService{}
	inventoryService, _ := createService(t)
	patches := gomonkey.ApplyMethod(reflect.TypeOf(clusterApiService), "GetContainerCluster", func(_ *nsxt.ManagementPlaneApiFabricContainerClustersApiService, _ context.Context, _ string) (containerinventory.ContainerCluster, *http.Response, error) {
		return containerinventory.ContainerCluster{}, nil, nil
	})
	_, err := inventoryService.GetContainerCluster(false)
	patches.Reset()
	assert.Nil(t, err)

	// Simulate a 404 response, cleanup false
	patches = gomonkey.ApplyMethod(reflect.TypeOf(clusterApiService), "GetContainerCluster", func(_ *nsxt.ManagementPlaneApiFabricContainerClustersApiService, _ context.Context, _ string) (containerinventory.ContainerCluster, *http.Response, error) {
		resp := &http.Response{StatusCode: http.StatusNotFound}
		return containerinventory.ContainerCluster{}, resp, nil
	})
	_, err = inventoryService.GetContainerCluster(false)
	patches.Reset()
	assert.Equal(t, util.HttpNotFoundError, err)

	// Simulate a successful response, cleanup true
	patches = gomonkey.ApplyMethod(reflect.TypeOf(inventoryService.NSXClient.Cluster), "HttpGetAndDecode", func(_ *nsx.Cluster, url string, result interface{}) error {
		if result, ok := result.(*containerinventory.ContainerCluster); ok {
			*result = containerinventory.ContainerCluster{DisplayName: "Cluster1", ClusterType: "WCP"}
		}
		return nil
	})
	cluster, err := inventoryService.GetContainerCluster(true)
	patches.Reset()
	assert.Nil(t, err)
	assert.Equal(t, cluster.DisplayName, "Cluster1")
	assert.Equal(t, cluster.ClusterType, "WCP")

	// Simulate a 404 response, cleanup false
	patches = gomonkey.ApplyMethod(reflect.TypeOf(inventoryService.NSXClient.Cluster), "HttpGetAndDecode", func(_ *nsx.Cluster, url string, result interface{}) error {
		if result, ok := result.(*containerinventory.ContainerCluster); ok {
			*result = containerinventory.ContainerCluster{DisplayName: "Cluster1", ClusterType: "WCP"}
		}
		return util.HttpNotFoundError
	})
	cluster, err = inventoryService.GetContainerCluster(true)
	patches.Reset()
	assert.Equal(t, util.HttpNotFoundError, err)
}

func TestInventoryService_AddContainerCluster(t *testing.T) {
	cluster1 := containerinventory.ContainerCluster{DisplayName: "Cluster1", ClusterType: "WCP"}
	clusterApiService := &nsxt.ManagementPlaneApiFabricContainerClustersApiService{}
	inventoryService, _ := createService(t)
	patches := gomonkey.ApplyMethod(reflect.TypeOf(clusterApiService), "AddContainerCluster", func(_ *nsxt.ManagementPlaneApiFabricContainerClustersApiService, _ context.Context, _ containerinventory.ContainerCluster) (containerinventory.ContainerCluster, *http.Response, error) {
		return containerinventory.ContainerCluster{}, nil, nil
	})
	defer patches.Reset()
	_, err1 := inventoryService.AddContainerCluster(cluster1)
	assert.Nil(t, err1)
}

func TestInventoryService_DeleteContainerCluster(t *testing.T) {
	inventoryService, _ := createService(t)
	patches := gomonkey.ApplyMethod(reflect.TypeOf(inventoryService.NSXClient.Cluster), "HttpDelete", func(_ *nsx.Cluster, url string) error {
		return nil
	})
	defer patches.Reset()
	err := inventoryService.DeleteContainerCluster(baseUrl, context.TODO())
	assert.Nil(t, err)
	patches.Reset()

	patches = gomonkey.ApplyMethod(reflect.TypeOf(inventoryService.NSXClient.Cluster), "HttpDelete", func(_ *nsx.Cluster, url string) error {
		return util.HttpNotFoundError
	})
	defer patches.Reset()
	err = inventoryService.DeleteContainerCluster(baseUrl, context.TODO())
	assert.Equal(t, util.HttpNotFoundError, err)
}
