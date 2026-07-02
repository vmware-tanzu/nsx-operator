package node

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra/sites/enforcement_points"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/client-go/tools/cache"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type fakeHostTransportNodesClient struct {
	enforcement_points.HostTransportNodesClient
}

func (f *fakeHostTransportNodesClient) Get(param1 string, param2 string, param3 string) (model.HostTransportNode, error) {
	// Do nothing
	return model.HostTransportNode{}, nil
}

func (f *fakeHostTransportNodesClient) List(param1 string, param2 string, param3 *string, param4 *string, param5 *bool, param6 *string, param7 *string, param8 *string, param9 *int64, param10 *bool, param11 *string, param12 *string) (model.HostTransportNodeListResult, error) {
	nodeName := "test-node"
	mockNode := &model.HostTransportNode{
		UniqueId: servicecommon.String("test-node"),
		NodeDeploymentInfo: &model.FabricHostNode{
			Fqdn: &nodeName,
		},
	}
	return model.HostTransportNodeListResult{
		Results: []model.HostTransportNode{*mockNode},
	}, nil
}

func (f *fakeHostTransportNodesClient) Update(param1 string, param2 string, param3 string, param4 model.HostTransportNode, _ *string, _ *string,
	_ *bool, _ *string, _ *bool, _ *string, _ *string) (model.HostTransportNode, error) {
	// Do nothing
	return model.HostTransportNode{}, nil
}

func createMockNodeService() *NodeService {
	return &NodeService{
		Service: servicecommon.Service{
			NSXClient: &nsx.Client{
				HostTransPortNodesClient: &fakeHostTransportNodesClient{},
				NsxConfig: &config.NSXOperatorConfig{
					CoeConfig: &config.CoeConfig{
						Cluster: "k8scl-one:test",
					},
					NsxConfig: &config.NsxConfig{
						TnIdCheckInterval: 300,
					},
				},
			},
		},
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
}

func TestInitializeNode(t *testing.T) {
	mockService := createMockNodeService()

	nodeService, err := InitializeNode(mockService.Service)
	assert.NoError(t, err)
	assert.NotNil(t, nodeService)
	assert.NotNil(t, nodeService.NodeStore)
}

func TestNodeService_GetNodeByName(t *testing.T) {
	service := createMockNodeService()

	nodeName := "test-node"
	mockNode := &model.HostTransportNode{
		UniqueId: servicecommon.String("test-node"),
		NodeDeploymentInfo: &model.FabricHostNode{
			Fqdn: &nodeName,
		},
	}

	service.NodeStore.Add(mockNode)

	nodes := service.GetNodeByName(nodeName)
	assert.Len(t, nodes, 1)
	assert.Equal(t, nodeName, *nodes[0].NodeDeploymentInfo.Fqdn)

	// Test case-insensitive
	nodesUpper := service.GetNodeByName("TEST-NODE")
	assert.Len(t, nodesUpper, 1)
	assert.Equal(t, nodeName, *nodesUpper[0].NodeDeploymentInfo.Fqdn)
}

func TestNodeService_SyncNodeStore(t *testing.T) {
	logf.SetLogger(logger.ZapCustomLogger(false, 0).Logger)
	service := createMockNodeService()
	nodeName := "test-node"

	// Test case: Node not found
	err := service.SyncNodeStore("non-existent-node", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "node non-existent-node not found in NSX")

	err = service.SyncNodeStore("TEST-NODE", false)
	assert.NoError(t, err)

	nodes := service.GetNodeByName(nodeName)
	assert.Len(t, nodes, 1)
	assert.Equal(t, nodeName, *nodes[0].NodeDeploymentInfo.Fqdn)

	// Test case: Node deletion
	err = service.SyncNodeStore(nodeName, true)
	assert.NoError(t, err)

	nodes = service.NodeStore.GetByIndex(servicecommon.IndexKeyNodeName, strings.ToLower(nodeName))
	assert.Len(t, nodes, 0)
}

func TestNodeService_SyncAllNodes(t *testing.T) {
	logf.SetLogger(logger.ZapCustomLogger(false, 0).Logger)
	service := createMockNodeService()
	nodeName := "test-node"

	// First, add a node to the store (simulating K8s Node Add event)
	err := service.SyncNodeStore(nodeName, false)
	assert.NoError(t, err)

	// Now run syncAllNodes
	err = service.syncAllNodes()
	assert.NoError(t, err)

	nodes := service.GetNodeByName(nodeName)
	assert.Len(t, nodes, 1)
	assert.Equal(t, nodeName, *nodes[0].NodeDeploymentInfo.Fqdn)

	// Test case: Node deleted from NSX side
	// Add a dummy node to local store that doesn't exist in the mocked NSX List response
	dummyNodeName := "dummy-node"
	dummyNodeId := "dummy-id"
	dummyNode := &model.HostTransportNode{
		UniqueId: &dummyNodeId,
		NodeDeploymentInfo: &model.FabricHostNode{
			Fqdn: &dummyNodeName,
		},
	}
	err = service.NodeStore.Add(dummyNode)
	assert.NoError(t, err)

	// Verify dummy node is in store
	nodes = service.GetNodeByName(dummyNodeName)
	assert.Len(t, nodes, 1)

	// Run syncAllNodes, it should detect dummy-node is missing in NSX and delete it from local store
	err = service.syncAllNodes()
	assert.NoError(t, err)

	// Verify dummy node is deleted
	nodes = service.GetNodeByName(dummyNodeName)
	assert.Len(t, nodes, 0)
}

func TestNodeIndexByNodeName(t *testing.T) {
	nodeName := "test-node"
	mockNode := &model.HostTransportNode{
		NodeDeploymentInfo: &model.FabricHostNode{
			Fqdn: &nodeName,
		},
	}

	indexes, _ := nodeIndexByNodeName(mockNode)
	assert.Len(t, indexes, 1)
	assert.Equal(t, nodeName, indexes[0])
}

func TestKeyFunc(t *testing.T) {
	nodeName := "test-node"
	mockNode := &model.HostTransportNode{
		UniqueId: servicecommon.String("test-node"),
		NodeDeploymentInfo: &model.FabricHostNode{
			Fqdn: &nodeName,
		},
	}

	key, err := keyFunc(mockNode)
	assert.NoError(t, err)
	assert.Equal(t, nodeName, key)
}
