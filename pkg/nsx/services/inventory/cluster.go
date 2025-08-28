package inventory

import (
	"context"
	"fmt"

	"net/http"

	"github.com/vmware/go-vmware-nsxt/containerinventory"

	nsx_util "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

const (
	baseUrl = "api/v1/fabric/container-clusters/%s"
)

func (s *InventoryService) GetContainerCluster(cleanup bool) (containerinventory.ContainerCluster, error) {
	clusterUUID := util.GetClusterUUID(s.NSXConfig.Cluster).String()
	log.Info("Send request to NSX to get inventory cluster", "Cluster id", clusterUUID)
	if cleanup {
		url := fmt.Sprintf(baseUrl, clusterUUID)
		containerCluster := containerinventory.ContainerCluster{}
		err := s.NSXClient.Cluster.HttpGetandDecode(url, &containerCluster)
		if err == nil {
			log.Info("Get inventory cluster", "response", containerCluster)
			return containerCluster, nil
		}
		log.Error(err, "Get inventory cluster failed", "cluster", clusterUUID)
		return containerCluster, err

	}
	containerCluster, resp, err := s.NSXClient.NsxApiClient.ContainerClustersApi.GetContainerCluster(context.TODO(), clusterUUID)
	// there was no error_code in the err, so we need to check the response to return the HttpNotFoundError error
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		return containerCluster, nsx_util.HttpNotFoundError
	}
	if err != nil {
		return containerCluster, err
	}
	return containerCluster, nil
}

func (s *InventoryService) AddContainerCluster(cluster containerinventory.ContainerCluster) (containerinventory.ContainerCluster, error) {
	log.Info("Send request to NSX to create inventory cluster", "Cluster", cluster)
	cluster.ClusterType = InventoryClusterTypeSupervisor
	cluster, _, err := s.NSXClient.NsxApiClient.ContainerClustersApi.AddContainerCluster(context.TODO(), cluster)
	return cluster, err
}

func (s *InventoryService) DeleteContainerCluster(clusterID string, ctx context.Context, cleanup bool) error {
	log.Info("Send request to NSX to delete inventory cluster", "Cluster", clusterID)
	if cleanup {
		err := s.NSXClient.Cluster.HttpDelete(fmt.Sprintf(baseUrl, clusterID))
		log.Error(err, "Delete inventory cluster", "cluster", clusterID)
		return err
	}
	_, err := s.NSXClient.NsxApiClient.ContainerClustersApi.DeleteContainerCluster(ctx, clusterID)
	return err
}
