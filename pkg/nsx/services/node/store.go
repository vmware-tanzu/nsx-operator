package node

import (
	"errors"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// NodeStore is a store for node (NSX HostTransportNode)
type NodeStore struct {
	common.ResourceStore
}

func (vs *NodeStore) Operate(i interface{}) error {
	if i == nil {
		return nil
	}
	node := i.(*model.HostTransportNode)
	if node.MarkedForDelete != nil && *node.MarkedForDelete {
		err := vs.Delete(*node)
		log.V(1).Info("delete Node from store", "node", node)
		if err != nil {
			return err
		}
	} else {
		err := vs.Add(*node)
		log.V(1).Info("add Node to store", "node", node)
		if err != nil {
			return err
		}
	}
	return nil
}

func (nodeStore *NodeStore) GetByKey(key string) *model.HostTransportNode {
	var node model.HostTransportNode
	obj := nodeStore.ResourceStore.GetByKey(key)
	if obj != nil {
		node = obj.(model.HostTransportNode)
	}
	return &node
}

func (nodeStore *NodeStore) GetByIndex(key string, value string) []model.HostTransportNode {
	hostTransportNodes := make([]model.HostTransportNode, 0)
	objs := nodeStore.ResourceStore.GetByIndex(key, value)
	for _, node := range objs {
		hostTransportNodes = append(hostTransportNodes, node.(model.HostTransportNode))
	}
	return hostTransportNodes
}

// keyFunc is used to get the key of a resource, usually, which is the ID of the resource
func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case model.HostTransportNode:
		return *v.Id, nil
	default:
		return "", errors.New("keyFunc doesn't support unknown type")
	}
}

func nodeIndexByNodeName(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case model.HostTransportNode:
		return []string{*o.NodeDeploymentInfo.Fqdn}, nil
	default:
		return nil, errors.New("nodeIndexByNodeName doesn't support unknown type")
	}
}
