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
)

func TestInventoryService_GetContainerCluster(t *testing.T) {
	clusterApiService := &nsxt.ManagementPlaneApiFabricContainerClustersApiService{}
	inventoryService, _ := createService(t)
	patches := gomonkey.ApplyMethod(reflect.TypeOf(clusterApiService), "GetContainerCluster", func(_ *nsxt.ManagementPlaneApiFabricContainerClustersApiService, _ context.Context, _ string) (containerinventory.ContainerCluster, *http.Response, error) {
		return containerinventory.ContainerCluster{}, nil, nil
	})
	defer patches.Reset()
	_, err := inventoryService.GetContainerCluster()
	assert.Nil(t, err)

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
