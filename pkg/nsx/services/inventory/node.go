package inventory

import (
	"context"
	"fmt"

	"github.com/antihax/optional"
	nsxt "github.com/vmware/go-vmware-nsxt"
)

func (s *InventoryService) initContainerClusterNode(clusterId string) error {
	cursor := ""
	log.Info("Retrieving ContainerClusterNode for cluster", "cluster id", clusterId)
	for {
		opts := &nsxt.ListContainerClusterNodesOpts{ContainerClusterId: optional.NewString(clusterId)}
		if cursor != "" {
			opts.Cursor = optional.NewString(cursor)
		}
		clusterNodes, _, err := s.NSXClient.NsxApiClient.ContainerClustersApi.ListContainerClusterNodes(context.Background(), opts)
		if err != nil {
			return fmt.Errorf("failed to retrieve ContainerClusterNode err: %w", err)
		}
		for _, clusterNode := range clusterNodes.Results {
			err = s.CluserNodeStore.Add(&clusterNode)
			if err != nil {
				return err
			}
		}
		if cursor = clusterNodes.Cursor; cursor == "" {
			break
		}
	}
	return nil
}
