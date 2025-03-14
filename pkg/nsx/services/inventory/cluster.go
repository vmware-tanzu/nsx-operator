package inventory

import (
	"context"

	"github.com/vmware/go-vmware-nsxt/containerinventory"
)

func (s *InventoryService) GetContainerCluster() (containerinventory.ContainerCluster, error) {
	log.Info("Send request to NSX to get cluster", "cluster id", s.NSXConfig.Cluster)
	containerCluster, _, err := s.NSXClient.NsxApiClient.ContainerClustersApi.GetContainerCluster(context.TODO(), s.NSXConfig.Cluster)
	return containerCluster, err
}

func (s *InventoryService) AddContainerCluster(cluster containerinventory.ContainerCluster) (containerinventory.ContainerCluster, error) {
	log.Info("Send request to NSX to create cluster", "cluster", s.NSXConfig.Cluster)
	cluster.ClusterType = InventoryClusterTypeWCP
	cluster, _, err := s.NSXClient.NsxApiClient.ContainerClustersApi.AddContainerCluster(context.TODO(), cluster)
	return cluster, err
}
