package inventory

import (
	"context"

	"github.com/vmware/go-vmware-nsxt/containerinventory"
)

func (s *InventoryService) GetContainerCluster() (containerinventory.ContainerCluster, error) {
	log.Info("Send request to NSX to get inventory cluster", "Cluster id", s.NSXConfig.Cluster)
	containerCluster, _, err := s.NSXClient.NsxApiClient.ContainerClustersApi.GetContainerCluster(context.TODO(), s.NSXConfig.Cluster)
	return containerCluster, err
}

func (s *InventoryService) AddContainerCluster(cluster containerinventory.ContainerCluster) (containerinventory.ContainerCluster, error) {
	log.Info("Send request to NSX to create inventory cluster", "Cluster", s.NSXConfig.Cluster)
	cluster.ClusterType = InventoryClusterTypeSupervisor
	cluster, _, err := s.NSXClient.NsxApiClient.ContainerClustersApi.AddContainerCluster(context.TODO(), cluster)
	return cluster, err
}

func (s *InventoryService) DeleteContainerCluster(clusterID string, ctx context.Context) error {
	log.Info("Send request to NSX to delete inventory cluster", "Cluster", clusterID)
	_, err := s.NSXClient.NsxApiClient.ContainerClustersApi.DeleteContainerCluster(ctx, clusterID)
	return err
}
