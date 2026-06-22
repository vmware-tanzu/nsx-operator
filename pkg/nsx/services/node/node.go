package node

import (
	"fmt"
	"strings"
	"time"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

var (
	log              = logger.Log
	ResourceTypeNode = servicecommon.ResourceTypeNode
	MarkedForDelete  = true
)

type NodeService struct {
	servicecommon.Service
	NodeStore *NodeStore
	stopChan  chan struct{}
}

func InitializeNode(service servicecommon.Service) (*NodeService, error) {
	nodeService := &NodeService{
		Service: service,
		NodeStore: &NodeStore{
			ResourceStore: servicecommon.ResourceStore{
				Indexer: cache.NewIndexer(
					keyFunc,
					cache.Indexers{
						servicecommon.IndexKeyNodeName: nodeIndexByNodeName,
					},
				),
				BindingType: model.HostTransportNodeBindingType(),
			},
		},
		stopChan: make(chan struct{}),
	}

	nodeService.startPeriodicSync()

	return nodeService, nil
}

func (service *NodeService) startPeriodicSync() {
	checkInterval := time.Duration(service.NSXClient.NsxConfig.TnIdCheckInterval) * time.Second
	if checkInterval <= 0 {
		checkInterval = 5 * time.Minute
	}
	ticker := time.NewTicker(checkInterval)
	go func() {
		for {
			select {
			case <-ticker.C:
				if err := service.syncAllNodes(); err != nil {
					log.Error(err, "Failed to periodically sync nodes from NSX")
				}
			case <-service.stopChan:
				ticker.Stop()
				return
			}
		}
	}()
}

func (service *NodeService) syncAllNodes() error {
	log.Info("Periodically syncing nodes from NSX")
	nodeResults, err := service.NSXClient.HostTransPortNodesClient.List("default", "default", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		return fmt.Errorf("failed to list HostTransportNodes: %s", err)
	}

	keys := service.NodeStore.ListIndexFuncValues(servicecommon.IndexKeyNodeName)

	nsxNodeMap := make(map[string]*model.HostTransportNode)
	for i := range nodeResults.Results {
		node := nodeResults.Results[i]
		if node.NodeDeploymentInfo != nil && node.NodeDeploymentInfo.Fqdn != nil {
			nsxNodeMap[strings.ToLower(*node.NodeDeploymentInfo.Fqdn)] = &node
		}
	}

	for key := range keys {
		if nsxNode, ok := nsxNodeMap[key]; ok {
			// Node exists in NSX, update local store
			if err := service.NodeStore.Apply(nsxNode); err != nil {
				log.Error(err, "failed to apply node to store", "nodeName", key)
			}
		} else {
			// Node is in local store but no longer in NSX, mark for delete and remove
			log.Info("Node found in local store but missing in NSX, deleting from store", "nodeName", key)
			nodes := service.NodeStore.GetByIndex(servicecommon.IndexKeyNodeName, key)
			for _, node := range nodes {
				node.MarkedForDelete = servicecommon.Bool(true)
				if err := service.NodeStore.Apply(node); err != nil {
					log.Error(err, "failed to delete node from store", "nodeName", key)
				}
			}
		}
	}
	return nil
}

func (service *NodeService) GetNodeByName(nodeName string) []*model.HostTransportNode {
	return service.NodeStore.GetByIndex(servicecommon.IndexKeyNodeName, strings.ToLower(nodeName))
}

func (service *NodeService) SyncNodeStore(nodeName string, deleted bool) error {
	nodeNameLower := strings.ToLower(nodeName)

	if deleted {
		log.Info("Deleting node from store", "nodeName", nodeName)
		nodes := service.NodeStore.GetByIndex(servicecommon.IndexKeyNodeName, nodeNameLower)
		for _, node := range nodes {
			node.MarkedForDelete = servicecommon.Bool(true)
			if err := service.NodeStore.Apply(node); err != nil {
				return fmt.Errorf("failed to delete node %s from store: %s", nodeName, err)
			}
		}
		return nil
	}

	nodes := service.NodeStore.GetByIndex(servicecommon.IndexKeyNodeName, nodeNameLower)
	if len(nodes) > 1 {
		return fmt.Errorf("multiple nodes found for node name %s in store", nodeName)
	}
	if len(nodes) == 1 {
		log.Debug("Node already cached, skipping NSX query in event handler", "nodeName", nodeName)
		return nil
	}

	log.Info("Node cache missed, querying from NSX", "nodeName", nodeName)
	nodeResults, err := service.NSXClient.HostTransPortNodesClient.List("default", "default", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		return fmt.Errorf("failed to list HostTransportNodes: %s", err)
	}

	synced := false
	for i := range nodeResults.Results {
		node := nodeResults.Results[i]
		if node.NodeDeploymentInfo != nil && node.NodeDeploymentInfo.Fqdn != nil && strings.EqualFold(*node.NodeDeploymentInfo.Fqdn, nodeName) {
			if err := service.NodeStore.Apply(&node); err != nil {
				return fmt.Errorf("failed to apply node %s to store: %s", nodeName, err)
			}
			synced = true
			log.Info("Successfully synced node from NSX", "nodeName", nodeName)
			break
		}
	}

	if !synced {
		return fmt.Errorf("node %s not found in NSX", nodeName)
	}
	return nil
}
