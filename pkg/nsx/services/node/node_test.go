package node

import (
	"reflect"
	"sync"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
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

	var tc *bindings.TypeConverter
	patches := gomonkey.ApplyMethod(reflect.TypeOf(tc), "ConvertToGolang",
		func(_ *bindings.TypeConverter, d data.DataValue, b bindings.BindingType) (interface{}, []error) {
			mId, mTag, mScope := "11111", "11111", "11111"
			m := model.HostTransportNode{
				Id:   &mId,
				Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
			}
			var j interface{} = m
			return j, nil
		})
	defer patches.Reset()

	patch := gomonkey.ApplyMethod(reflect.TypeOf(&mockService.Service), "InitializeResourceStore", func(_ *servicecommon.Service, wg *sync.WaitGroup,
		fatalErrors chan error, resourceTypeValue string, tags []model.Tag, store servicecommon.Store,
	) {
		wg.Done()
		return
	})
	defer patch.Reset()

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
}

func TestNodeService_SyncNodeStore(t *testing.T) {
	logf.SetLogger(logger.ZapCustomLogger(false, 0, false).Logger)
	service := createMockNodeService()
	nodeName := "test-node"
	// Test case: Node not found
	err := service.SyncNodeStore("non-existent-node", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "node non-existent-node not found yet in NSX side")

	err = service.SyncNodeStore(nodeName, false)
	assert.NoError(t, err)

	nodes := service.GetNodeByName(nodeName)
	assert.Len(t, nodes, 1)
	assert.Equal(t, nodeName, *nodes[0].NodeDeploymentInfo.Fqdn)

	// Test case: Node deletion
	err = service.SyncNodeStore(nodeName, true)
	assert.Error(t, err)

	nodes = service.GetNodeByName(nodeName)
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
