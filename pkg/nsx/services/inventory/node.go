package inventory

import (
	"context"
	"fmt"

	"github.com/antihax/optional"
	nsxt "github.com/vmware/go-vmware-nsxt"
	"github.com/vmware/go-vmware-nsxt/containerinventory"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

func (s *InventoryService) isClusterNodeDeleted(name, externalId string) bool {
	node := &corev1.Node{}
	err := s.Client.Get(context.TODO(), types.NamespacedName{Name: name}, node)
	return apierrors.IsNotFound(err) || ((err == nil) && (string(node.UID) != externalId))
}

func (s *InventoryService) SyncContainerClusterNode(name string, key InventoryKey) *InventoryKey {
	node := &corev1.Node{}
	err := s.Client.Get(context.TODO(), types.NamespacedName{Name: name}, node)
	externalId := key.ExternalId
	if apierrors.IsNotFound(err) ||
		((err == nil) && (string(node.UID) != externalId)) {
		err = s.DeleteResource(externalId, ContainerClusterNode)
		if err != nil {
			log.Error(err, "Delete ContainerClusterNode Resource error", "key", key)
			return &key
		}
	} else if err == nil {
		retry := s.BuildNode(node)
		if retry {
			return &key
		}
	} else {
		log.Error(err, "Unexpected error is found while processing Node")
	}
	return nil
}

func (s *InventoryService) initContainerClusterNode(clusterId string) error {
	cursor := ""
	log.Info("Retrieving ContainerClusterNodes for cluster", "cluster id", clusterId)
	for {
		opts := &nsxt.ListContainerClusterNodesOpts{ContainerClusterId: optional.NewString(clusterId)}
		if cursor != "" {
			opts.Cursor = optional.NewString(cursor)
		}
		nodes, _, err := s.NSXClient.NsxApiClient.ContainerClustersApi.ListContainerClusterNodes(context.Background(), opts)
		if err != nil {
			return fmt.Errorf("failed to retrieve container cluster nodes err: %w", err)
		}
		for _, node := range nodes.Results {
			err = s.ClusterNodeStore.Add(&node)
			if err != nil {
				return err
			}
		}
		if cursor = nodes.Cursor; cursor == "" {
			break
		}
	}
	return nil
}

func (s *InventoryService) CleanStaleInventoryClusterNode() error {
	log.Info("Clean stale InventoryClusterNode")
	inventoryNodes := s.ClusterNodeStore.List()
	for _, inventoryNode := range inventoryNodes {
		inventoryNode := inventoryNode.(*containerinventory.ContainerClusterNode)
		if s.isClusterNodeDeleted(inventoryNode.DisplayName, inventoryNode.ExternalId) {
			log.Info("Cleaning stale InventoryClusterNode", "Name", inventoryNode.DisplayName, "External Id", inventoryNode.ExternalId)
			err := s.DeleteResource(inventoryNode.ExternalId, ContainerClusterNode)
			if err != nil {
				log.Error(err, "Clean stale InventoryClusterNode", "External Id", inventoryNode.ExternalId)
				return err
			}
		}
	}
	return nil
}
