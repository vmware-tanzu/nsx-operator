package node

import (
	"fmt"
	"sync"

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
}

func InitializeNode(service servicecommon.Service) (*NodeService, error) {
	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)

	wg.Add(1)

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
	}
	// TODO: confirm whether we can remove the following initialization because node doesn't have the cluster tag so it's a dry run
	go nodeService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeNode, nil, nodeService.NodeStore)

	go func() {
		wg.Wait()
		close(wgDone)
	}()

	select {
	case <-wgDone:
		break
	case err := <-fatalErrors:
		close(fatalErrors)
		return nodeService, err
	}

	return nodeService, nil

}

func (service *NodeService) GetNodeByName(nodeName string) []*model.HostTransportNode {
	return service.NodeStore.GetByIndex(servicecommon.IndexKeyNodeName, nodeName)
}

func (service *NodeService) SyncNodeStore(nodeName string, deleted bool) error {
	nodes := service.NodeStore.GetByIndex(servicecommon.IndexKeyNodeName, nodeName)
	if len(nodes) > 1 {
		return fmt.Errorf("multiple nodes found for node name %s", nodeName)
	}
	// TODO: confirm whether we need to resync the node info from NSX
	if len(nodes) == 1 {
		log.Info("node alreay cached", "node.Fqdn", nodes[0].NodeDeploymentInfo.Fqdn, "node.UniqueId", *nodes[0].UniqueId)
		// updatedNode, err := service.NSXClient.HostTransPortNodesClient.Get("default", "default", nodes[0].Id)
		// if err != nil {
		// 	return fmt.Errorf("failed to get HostTransPortNode for node %s: %s", nodeName, err)
		// }
		// node.NodeStore.Apply(updatedNode)
	}
	nodeResults, err := service.NSXClient.HostTransPortNodesClient.List("default", "default", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		return fmt.Errorf("failed to list HostTransportNodes: %s", err)
	}
	if deleted {
		nodes := service.NodeStore.GetByIndex(servicecommon.IndexKeyNodeName, nodeName)
		if len(nodes) == 0 {
			log.Info("skip deleting node in store because the node is not in store", "nodeName", nodeName)
			return nil
		}
		for _, node := range nodes {
			node.MarkedForDelete = servicecommon.Bool(true)
			service.NodeStore.Apply(node)
		}
	}
	synced := false
	for _, node := range nodeResults.Results {
		node := node
		if *node.NodeDeploymentInfo.Fqdn == nodeName {
			if deleted {
				// Retry until the NSX HostTransportNode is deleted.
				return fmt.Errorf("node %s had beed deleted but HostTransportNodes still exists", nodeName)
			}
			err = service.NodeStore.Apply(&node)
			if err != nil {
				return fmt.Errorf("failed to sync node %s: %s", nodeName, err)
			}
			synced = true
			break
		}
	}
	if !synced && !deleted {
		// Retry until the NSX HostTransportNode is available.
		return fmt.Errorf("node %s not found yet in NSX side", nodeName)
	}
	return nil
}
