package ipaddressallocation

import (
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/zhengxiexie/vsphere-automation-sdk-go/runtime/data"
	"github.com/zhengxiexie/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	mocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/vpcclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
)

type fakeQueryClient struct {
}

func (qIface *fakeQueryClient) List(_ string, _ *string, _ *string, _ *int64, _ *bool, _ *string) (model.SearchResponse, error) {
	cursor := "2"
	resultCount := int64(2)
	return model.SearchResponse{
		Results: []*data.StructValue{{}},
		Cursor:  &cursor, ResultCount: &resultCount,
	}, nil
}

func createService(t *testing.T) (*vpc.VPCService, *gomock.Controller, *mocks.MockVpcsClient) {
	config2 := nsx.NewConfig("localhost", "1", "1", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})

	cluster, _ := nsx.NewCluster(config2)
	rc, _ := cluster.NewRestConnector()

	mockCtrl := gomock.NewController(t)
	mockVpcclient := mocks.NewMockVpcsClient(mockCtrl)
	k8sClient := mock_client.NewMockClient(mockCtrl)

	vpcStore := &vpc.VPCStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeStaticRouteCRUID: indexFunc}),
		BindingType: model.VpcBindingType(),
	}}

	service := &vpc.VPCService{
		Service: common.Service{
			Client: k8sClient,
			NSXClient: &nsx.Client{
				QueryClient:   &fakeQueryClient{},
				VPCClient:     mockVpcclient,
				RestConnector: rc,
				NsxConfig: &config.NSXOperatorConfig{
					CoeConfig: &config.CoeConfig{
						Cluster: "k8scl-one:test",
					},
				},
			},
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "k8scl-one:test",
				},
			},
		},
		VpcStore: vpcStore,
		VPCNetworkConfigStore: vpc.VPCNetworkInfoStore{
			VPCNetworkConfigMap: map[string]common.VPCNetworkConfigInfo{},
		},
		VPCNSNetworkConfigStore: vpc.VPCNsNetworkConfigStore{
			VPCNSNetworkConfigMap: map[string]string{},
		},
	}
	return service, mockCtrl, mockVpcclient
}

func TestBuildIPAddressAllocation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	vpcService, _, _ := createService(t)
	ipAllocService := &IPAddressAllocationService{
		VPCService: vpcService,
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
				CoeConfig: &config.CoeConfig{
					Cluster: "default",
				},
			},
		},
	}

	t.Run("VPCInfo is empty", func(t *testing.T) {
		ipAlloc := &v1alpha1.IPAddressAllocation{}
		ipAlloc.Namespace = "default"
		ipAlloc.Name = "test-ip-alloc"

		patch := gomonkey.ApplyMethod(reflect.TypeOf(ipAllocService.VPCService), "ListVPCInfo", func(_ *vpc.VPCService, _ string) []common.VPCResourceInfo {
			return []common.VPCResourceInfo{}
		})
		defer patch.Reset()

		result, err := ipAllocService.BuildIPAddressAllocation(ipAlloc)
		assert.Nil(t, result)
		assert.EqualError(t, err, "failed to find VPCInfo for IPAddressAllocation CR test-ip-alloc in namespace default")
	})

	t.Run("IPAddressBlockVisibility is External and IP blocks are not exhausted", func(t *testing.T) {
		ipAlloc := &v1alpha1.IPAddressAllocation{
			Spec: v1alpha1.IPAddressAllocationSpec{
				IPAddressBlockVisibility: v1alpha1.IPAddressVisibilityExternal,
				AllocationSize:           1,
			},
		}
		ipAlloc.Namespace = "default"
		ipAlloc.Name = "test-ip-alloc"
		ipAlloc.UID = "default"

		patch := gomonkey.ApplyMethod(reflect.TypeOf(ipAllocService.VPCService), "ListVPCInfo", func(_ *vpc.VPCService, _ string) []common.VPCResourceInfo {
			return []common.VPCResourceInfo{
				{ExternalIPv4Blocks: []string{"block1"}},
			}
		})
		defer patch.Reset()

		result, err := ipAllocService.BuildIPAddressAllocation(ipAlloc)
		assert.NotNil(t, result)
		assert.Nil(t, err)
		assert.Equal(t, "block1", *result.IpBlock)
	})

	t.Run("IPAddressBlockVisibility is Private and all IP blocks are exhausted", func(t *testing.T) {
		ipAlloc := &v1alpha1.IPAddressAllocation{
			Spec: v1alpha1.IPAddressAllocationSpec{
				IPAddressBlockVisibility: v1alpha1.IPAddressVisibilityPrivate,
				AllocationSize:           1,
			},
		}
		ipAlloc.Namespace = "default"
		ipAlloc.Name = "test-ip-alloc"
		ipAlloc.UID = "default"

		patch := gomonkey.ApplyMethod(reflect.TypeOf(ipAllocService.VPCService), "ListVPCInfo", func(_ *vpc.VPCService, _ string) []common.VPCResourceInfo {
			return []common.VPCResourceInfo{
				{ExternalIPv4Blocks: []string{"block1", "block2"}},
			}
		})
		ipAllocService.ExhaustedIPBlock = []string{"block1", "block2"}
		defer patch.Reset()

		result, err := ipAllocService.BuildIPAddressAllocation(ipAlloc)
		assert.Nil(t, result)
		assert.EqualError(t, err, "all ip blocks are exhausted")
	})
}
