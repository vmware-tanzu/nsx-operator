package vpc

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	apierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	mocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/vpcclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

var (
	vpcName1          = "ns1-vpc-1"
	vpcName2          = "ns1-vpc-2"
	infraVPCName      = "infra-vpc"
	vpcID1            = "ns-vpc-uid-1"
	vpcID2            = "ns-vpc-uid-2"
	vpcID3            = "ns-vpc-uid-3"
	IPv4Type          = "IPv4"
	cluster           = "k8scl-one"
	tagScopeCluster   = common.TagScopeCluster
	tagScopeNamespace = common.TagScopeNamespace
)

func createService(t *testing.T) (*VPCService, *gomock.Controller, *mocks.MockVpcsClient) {
	config2 := nsx.NewConfig("localhost", "1", "1", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})

	cluster, _ := nsx.NewCluster(config2)
	rc, _ := cluster.NewRestConnector()

	mockCtrl := gomock.NewController(t)
	mockVpcclient := mocks.NewMockVpcsClient(mockCtrl)
	k8sClient := mock_client.NewMockClient(mockCtrl)

	vpcStore := &VPCStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeStaticRouteCRUID: indexFunc}),
		BindingType: model.VpcBindingType(),
	}}

	service := &VPCService{
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
		VPCNetworkConfigStore: VPCNetworkInfoStore{
			VPCNetworkConfigMap: map[string]common.VPCNetworkConfigInfo{},
		},
		VPCNSNetworkConfigStore: VPCNsNetworkConfigStore{
			VPCNSNetworkConfigMap: map[string]string{},
		},
	}
	return service, mockCtrl, mockVpcclient
}

type fakeProjectClient struct{}

func (c fakeProjectClient) Get(orgIdParam string, projectIdParam string, shortFormatParam *bool) (model.Project, error) {
	return model.Project{}, nil
}

func (c fakeProjectClient) Delete(orgIdParam string, projectIdParam string, isRecursiveParam *bool) error {
	return nil
}

func (c fakeProjectClient) List(orgIdParam string, cursorParam *string, includeMarkForDeleteObjectsParam *bool, includedFieldsParam *string, instanceIdParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.ProjectListResult, error) {
	return model.ProjectListResult{}, nil
}

func (c fakeProjectClient) Patch(orgIdParam string, projectIdParam string, projectParam model.Project) error {
	return nil
}

func (c fakeProjectClient) Update(orgIdParam string, projectIdParam string, projectParam model.Project) (model.Project, error) {
	return model.Project{}, nil
}

type fakeVPCConnectivityProfilesClient struct{}

func (c fakeVPCConnectivityProfilesClient) Delete(orgIdParam string, projectIdParam string, vpcConnectivityProfileIdParam string) error {
	return nil
}

func (c fakeVPCConnectivityProfilesClient) Get(orgIdParam string, projectIdParam string, vpcConnectivityProfileIdParam string) (model.VpcConnectivityProfile, error) {
	return model.VpcConnectivityProfile{}, nil
}

func (c fakeVPCConnectivityProfilesClient) List(orgIdParam string, projectIdParam string, cursorParam *string, includeMarkForDeleteObjectsParam *bool, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.VpcConnectivityProfileListResult, error) {
	return model.VpcConnectivityProfileListResult{}, nil
}

func (c fakeVPCConnectivityProfilesClient) Patch(orgIdParam string, projectIdParam string, vpcConnectivityProfileIdParam string, vpcConnectivityProfileParam model.VpcConnectivityProfile) error {
	return nil
}

func (c fakeVPCConnectivityProfilesClient) Update(orgIdParam string, projectIdParam string, vpcConnectivityProfileIdParam string, vpcConnectivityProfileParam model.VpcConnectivityProfile) (model.VpcConnectivityProfile, error) {
	return model.VpcConnectivityProfile{}, nil
}

type fakeTransitGatewayAttachmentClient struct{}

func (c fakeTransitGatewayAttachmentClient) Delete(orgIdParam string, projectIdParam string, transitGatewayIdParam string, attachmentIdParam string) error {
	return nil
}

func (c fakeTransitGatewayAttachmentClient) Get(orgIdParam string, projectIdParam string, transitGatewayIdParam string, attachmentIdParam string) (model.TransitGatewayAttachment, error) {
	return model.TransitGatewayAttachment{}, nil
}

func (c fakeTransitGatewayAttachmentClient) List(orgIdParam string, projectIdParam string, transitGatewayIdParam string, cursorParam *string, includeMarkForDeleteObjectsParam *bool, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.TransitGatewayAttachmentListResult, error) {
	return model.TransitGatewayAttachmentListResult{}, nil
}

func (c fakeTransitGatewayAttachmentClient) Patch(orgIdParam string, projectIdParam string, transitGatewayIdParam string, attachmentIdParam string, transitGatewayAttachmentParam model.TransitGatewayAttachment) error {
	return nil
}

func (c fakeTransitGatewayAttachmentClient) Update(orgIdParam string, projectIdParam string, transitGatewayIdParam string, attachmentIdParam string, transitGatewayAttachmentParam model.TransitGatewayAttachment) (model.TransitGatewayAttachment, error) {
	return model.TransitGatewayAttachment{}, nil
}

type fakeVPCLBSClient struct{}

func (c fakeVPCLBSClient) Delete(orgIdParam string, projectIdParam string, vpcIdParam string, vpcLbIdParam string, forceParam *bool) error {
	return nil
}

func (c fakeVPCLBSClient) Get(orgIdParam string, projectIdParam string, vpcIdParam string, vpcLbIdParam string) (model.LBService, error) {
	return model.LBService{}, nil
}

func (c fakeVPCLBSClient) List(orgIdParam string, projectIdParam string, vpcIdParam string, cursorParam *string, includeMarkForDeleteObjectsParam *bool, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.LBServiceListResult, error) {
	return model.LBServiceListResult{}, nil
}

func (c fakeVPCLBSClient) Patch(orgIdParam string, projectIdParam string, vpcIdParam string, vpcLbIdParam string, lbServiceParam model.LBService, actionParam *string) error {
	return nil
}

func (c fakeVPCLBSClient) Update(orgIdParam string, projectIdParam string, vpcIdParam string, vpcLbIdParam string, lbServiceParam model.LBService, actionParam *string) (model.LBService, error) {
	return model.LBService{}, nil
}

func TestGetNetworkConfigFromNS(t *testing.T) {
	service, _, _ := createService(t)
	k8sClient := service.Client.(*mock_client.MockClient)
	fakeErr := errors.New("fake error")
	mockNs := &v1.Namespace{}
	k8sClient.EXPECT().Get(ctx, gomock.Any(), mockNs).Return(fakeErr).Do(func(_ context.Context, k client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		return nil
	})
	ns, err := service.GetNetworkconfigNameFromNS("test")
	assert.Equal(t, fakeErr, err)
	assert.Equal(t, "", ns)

	k8sClient.EXPECT().Get(ctx, gomock.Any(), mockNs).Return(nil).Do(func(_ context.Context, k client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		return nil
	})
	ns, err = service.GetNetworkconfigNameFromNS("test")
	assert.NotNil(t, err)
	assert.Equal(t, "", ns)

	service.RegisterVPCNetworkConfig("fake-cr", common.VPCNetworkConfigInfo{
		IsDefault: true,
		Name:      "test-name",
		Org:       "test-org",
	})
	k8sClient.EXPECT().Get(ctx, gomock.Any(), mockNs).Return(nil).Do(func(_ context.Context, k client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		return nil
	})
	ns, err = service.GetNetworkconfigNameFromNS("test")
	assert.Nil(t, err)
	assert.Equal(t, "test-name", ns)

	k8sClient.EXPECT().Get(ctx, gomock.Any(), mockNs).Return(nil).Do(func(_ context.Context, k client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		obj.SetAnnotations(map[string]string{"nsx.vmware.com/vpc_network_config": "test-nc"})
		return nil
	})
	ns, err = service.GetNetworkconfigNameFromNS("test")
	assert.Nil(t, err)
	assert.Equal(t, "test-nc", ns)
}

func TestGetSharedVPCNamespaceFromNS(t *testing.T) {
	service, _, _ := createService(t)
	k8sClient := service.Client.(*mock_client.MockClient)

	ctx := context.Background()

	tests := []struct {
		name     string
		ns       string
		anno     map[string]string
		expected string
	}{
		{"1", "test-ns-1", map[string]string{"nsx.vmware.com/vpc_network_config": "default"}, ""},
		{"2", "test-ns-2", map[string]string{"nsx.vmware.com/vpc_network_config": "infra", "nsx.vmware.com/shared_vpc_namespace": "kube-system"}, "kube-system"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockNs := &v1.Namespace{}
			k8sClient.EXPECT().Get(ctx, gomock.Any(), mockNs).Return(nil).Do(func(_ context.Context, k client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				v1ns := obj.(*v1.Namespace)
				v1ns.ObjectMeta.Annotations = tt.anno
				return nil
			})
			ns, _err := service.getSharedVPCNamespaceFromNS(tt.ns)
			assert.Equal(t, tt.expected, ns)
			assert.Equal(t, nil, _err)
		})
	}

}

func TestGetDefaultNetworkConfig(t *testing.T) {
	service, _, _ := createService(t)

	nc1 := common.VPCNetworkConfigInfo{
		IsDefault: false,
	}
	service.RegisterVPCNetworkConfig("test-1", nc1)
	exist, _ := service.GetDefaultNetworkConfig()
	assert.Equal(t, false, exist)

	nc2 := common.VPCNetworkConfigInfo{
		Org:       "fake-org",
		IsDefault: true,
	}
	service.RegisterVPCNetworkConfig("test-2", nc2)
	exist, target := service.GetDefaultNetworkConfig()
	assert.Equal(t, true, exist)
	assert.Equal(t, "fake-org", target.Org)
}

func TestGetVPCsByNamespace(t *testing.T) {
	vpcCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{})
	resourceStore := common.ResourceStore{
		Indexer:     vpcCacheIndexer,
		BindingType: model.VpcBindingType(),
	}
	vpcStore := &VPCStore{ResourceStore: resourceStore}
	service := &VPCService{
		Service: common.Service{NSXClient: nil},
		VPCNetworkConfigStore: VPCNetworkInfoStore{
			VPCNetworkConfigMap: map[string]common.VPCNetworkConfigInfo{},
		},
		VPCNSNetworkConfigStore: VPCNsNetworkConfigStore{
			VPCNSNetworkConfigMap: map[string]string{},
		},
	}
	service.VpcStore = vpcStore
	type args struct {
		ns       string
		size     int
		expected string
		infra    string
	}
	ns1 := "test-ns-1"
	tag1 := []model.Tag{
		{
			Scope: &tagScopeCluster,
			Tag:   &cluster,
		},
		{
			Scope: &tagScopeNamespace,
			Tag:   &ns1,
		},
	}
	ns2 := "test-ns-2"
	tag2 := []model.Tag{
		{
			Scope: &tagScopeCluster,
			Tag:   &cluster,
		},
		{
			Scope: &tagScopeNamespace,
			Tag:   &ns2,
		},
	}
	infraNs := "kube-system"
	tag3 := []model.Tag{
		{
			Scope: &tagScopeCluster,
			Tag:   &cluster,
		},
		{
			Scope: &tagScopeNamespace,
			Tag:   &infraNs,
		},
	}
	vpc1 := model.Vpc{

		DisplayName:        &vpcName1,
		Id:                 &vpcID1,
		Tags:               tag1,
		IpAddressType:      &IPv4Type,
		PrivateIpv4Blocks:  []string{"1.1.1.0/24"},
		ExternalIpv4Blocks: []string{"2.2.2.0/24"},
	}
	vpc2 := model.Vpc{

		DisplayName:        &vpcName2,
		Id:                 &vpcID2,
		Tags:               tag2,
		IpAddressType:      &IPv4Type,
		PrivateIpv4Blocks:  []string{"3.3.3.0/24"},
		ExternalIpv4Blocks: []string{"4.4.4.0/24"},
	}
	infravpc := model.Vpc{
		DisplayName:        &infraVPCName,
		Id:                 &vpcID3,
		Tags:               tag3,
		IpAddressType:      &IPv4Type,
		PrivateIpv4Blocks:  []string{"3.3.3.0/24"},
		ExternalIpv4Blocks: []string{"4.4.4.0/24"},
	}
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{"1", args{ns: "invalid", size: 0, expected: "", infra: ""}, assert.NoError},
		{"2", args{ns: "test-ns-1", size: 1, expected: vpcName1, infra: ""}, assert.NoError},
		{"3", args{ns: "test-ns-2", size: 1, expected: vpcName2, infra: ""}, assert.NoError},
		{"4", args{ns: "test-ns-1", size: 1, expected: infraVPCName, infra: "kube-system"}, assert.NoError},
	}

	vpcStore.Apply(&vpc1)
	vpcStore.Apply(&vpc2)
	vpcStore.Apply(&infravpc)
	got := vpcStore.List()
	if len(got) != 3 {
		t.Errorf("size = %v, want %v", len(got), 3)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch := gomonkey.ApplyPrivateMethod(reflect.TypeOf(service), "getSharedVPCNamespaceFromNS", func(_ *VPCService, ns string) (string, error) {
				return tt.args.infra, nil
			})
			vpc_list_1 := service.GetVPCsByNamespace(tt.args.ns)
			if len(vpc_list_1) != tt.args.size {
				t.Errorf("size = %v, want %v", len(vpc_list_1), tt.args.size)
			}

			if tt.args.size != 0 && *vpc_list_1[0].DisplayName != tt.args.expected {
				t.Errorf("name = %v, want %v", vpc_list_1[0].DisplayName, tt.args.expected)
			}

			patch.Reset()
		})
	}
}

func TestListVPCInfo(t *testing.T) {

}

func TestGetLBProvider(t *testing.T) {
	vpcService := &VPCService{
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					UseAVILoadBalancer: false,
				},
			},
			NSXClient: &nsx.Client{
				Cluster: &nsx.Cluster{},
			},
		},
		LbsStore: &LBSStore{ResourceStore: common.ResourceStore{
			Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
			BindingType: model.LBServiceBindingType(),
		}},
	}

	// Test when globalLbProvider is avi lb
	vpcService.Service.NSXConfig.UseAVILoadBalancer = false
	globalLbProvider = AVILB
	lbProvider := vpcService.GetLBProvider()
	assert.True(t, lbProvider == AVILB)

	// Test when globalLbProvider is nsx lb
	globalLbProvider = NSXLB
	lbProvider = vpcService.GetLBProvider()
	assert.True(t, lbProvider == NSXLB)

	// Test when globalLbProvider is none lb
	globalLbProvider = NoneLB
	patch1 := gomonkey.ApplyMethod(reflect.TypeOf(vpcService), "EdgeClusterEnabled", func(_ *VPCService, _ *common.VPCNetworkConfigInfo) bool {
		return false
	})
	patch2 := gomonkey.ApplyPrivateMethod(reflect.TypeOf(vpcService), "getLBProvider", func(_ *VPCService, _ bool) LBProvider {
		return NSXLB
	})
	patch3 := gomonkey.ApplyMethod(reflect.TypeOf(vpcService), "GetVPCNetworkConfig", func(_ *VPCService, _ string) (common.VPCNetworkConfigInfo, bool) {
		return common.VPCNetworkConfigInfo{}, true
	})
	lbProvider = vpcService.GetLBProvider()
	assert.True(t, lbProvider == NSXLB)

	patch3.Reset()
	globalLbProvider = NoneLB
	patch3 = gomonkey.ApplyMethod(reflect.TypeOf(vpcService), "GetVPCNetworkConfig", func(_ *VPCService, _ string) (common.VPCNetworkConfigInfo, bool) {
		return common.VPCNetworkConfigInfo{}, false
	})
	lbProvider = vpcService.GetLBProvider()
	assert.True(t, lbProvider == NoneLB)
	patch2.Reset()
	patch1.Reset()
	patch3.Reset()

}

func TestEdgeClusterEnabled(t *testing.T) {
	vpcService := &VPCService{
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					UseAVILoadBalancer: false,
				},
			},
			NSXClient: &nsx.Client{
				Cluster: &nsx.Cluster{},
			},
		},
		LbsStore: &LBSStore{ResourceStore: common.ResourceStore{
			Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
			BindingType: model.LBServiceBindingType(),
		}},
	}
	nc := common.VPCNetworkConfigInfo{
		IsDefault:              true,
		Org:                    "default",
		Name:                   "system",
		VPCConnectivityProfile: "/default",
		NSXProject:             "proj-2",
		PrivateIPs:             []string{},
		DefaultSubnetSize:      16,
		VPCPath:                "/orgs/default/projects/proj-2/vpcs/vpc-1",
	}

	vpcConnPrfile := model.VpcConnectivityProfile{
		ServiceGateway: &model.VpcServiceGatewayConfig{Enable: common.Bool(false),
			NatConfig: &model.VpcNatConfig{EnableDefaultSnat: common.Bool(true)},
		}}

	patch := gomonkey.ApplyMethod(reflect.TypeOf(vpcService), "GetVpcConnectivityProfile", func(_ *VPCService, _ *common.VPCNetworkConfigInfo, _ string) (*model.VpcConnectivityProfile, error) {
		return &vpcConnPrfile, nil
	})
	enable := vpcService.EdgeClusterEnabled(&nc)
	assert.Equal(t, false, enable)
	vpcConnPrfile.ServiceGateway.Enable = common.Bool(true)
	enable = vpcService.EdgeClusterEnabled(&nc)
	assert.Equal(t, true, enable)
	patch.Reset()

	patch = gomonkey.ApplyMethod(reflect.TypeOf(vpcService), "GetVpcConnectivityProfile", func(_ *VPCService, _ *common.VPCNetworkConfigInfo, _ string) (*model.VpcConnectivityProfile, error) {
		return &vpcConnPrfile, apierrors.NewNotFound()
	})
	enable = vpcService.EdgeClusterEnabled(&nc)
	assert.Equal(t, false, enable)
	patch.Reset()

	patch = gomonkey.ApplyMethod(reflect.TypeOf(vpcService), "GetVpcConnectivityProfile", func(_ *VPCService, _ *common.VPCNetworkConfigInfo, _ string) (*model.VpcConnectivityProfile, error) {
		return &vpcConnPrfile, apierrors.NewInternalServerError()
	})
	enable = vpcService.EdgeClusterEnabled(&nc)
	assert.Equal(t, false, enable)
	patch.Reset()

	// Simulate the scenario when the VpcConnectivityProfile value returned by NSX is nil.
	patch = gomonkey.ApplyMethod(reflect.TypeOf(vpcService), "GetVpcConnectivityProfile", func(_ *VPCService, _ *common.VPCNetworkConfigInfo, _ string) (*model.VpcConnectivityProfile, error) {
		return &model.VpcConnectivityProfile{}, nil
	})
	enable = vpcService.EdgeClusterEnabled(&nc)
	assert.Equal(t, false, enable)
	patch.Reset()
}

func TestGetLbProvider(t *testing.T) {
	vpcService := &VPCService{
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					UseAVILoadBalancer: false,
				},
			},
			NSXClient: &nsx.Client{
				Cluster: &nsx.Cluster{},
			},
		},
		LbsStore: &LBSStore{ResourceStore: common.ResourceStore{
			Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
			BindingType: model.LBServiceBindingType(),
		}},
	}
	// Test when UseAVILoadBalancer is false
	patch := gomonkey.ApplyPrivateMethod(reflect.TypeOf(vpcService.Service.NSXClient.Cluster), "HttpGet", func(_ *nsx.Cluster, path string) (map[string]interface{}, error) {
		return nil, nil
	})
	lbProvider := vpcService.getLBProvider(true)
	assert.Equal(t, NSXLB, lbProvider)

	patch.Reset()
	patch = gomonkey.ApplyPrivateMethod(reflect.TypeOf(vpcService.Service.NSXClient.Cluster), "HttpGet", func(_ *nsx.Cluster, path string) (map[string]interface{}, error) {
		return nil, errors.New("fake error")
	})
	lbProvider = vpcService.getLBProvider(false)
	assert.Equal(t, NoneLB, lbProvider)

	patch.Reset()
	vpcService.Service.NSXConfig.NsxConfig.UseAVILoadBalancer = true
	// Test when UseAVILoadBalancer is true and Alb endpoint found, but no nsx lbs found
	patch = gomonkey.ApplyPrivateMethod(reflect.TypeOf(vpcService.Service.NSXClient.Cluster), "HttpGet", func(_ *nsx.Cluster, path string) (map[string]interface{}, error) {
		return nil, nil
	})
	lbProvider = vpcService.getLBProvider(true)
	assert.Equal(t, AVILB, lbProvider)
	patch.Reset()

	// Test when UseAVILoadBalancer is true, get alb endpoint common error
	retry := 0
	patch = gomonkey.ApplyPrivateMethod(reflect.TypeOf(vpcService.Service.NSXClient.Cluster), "HttpGet", func(_ *nsx.Cluster, path string) (map[string]interface{}, error) {
		retry++
		if strings.Contains(path, "alb-endpoint") {
			return nil, util.HttpCommonError
		} else {
			return nil, nil
		}
	})
	lbProvider = vpcService.getLBProvider(true)
	assert.Equal(t, NSXLB, lbProvider)
	assert.Equal(t, 4, retry)
	patch.Reset()

	// Test when UseAVILoadBalancer is true, get alb endpoint not found error
	retry = 0
	patch = gomonkey.ApplyPrivateMethod(reflect.TypeOf(vpcService.Service.NSXClient.Cluster), "HttpGet", func(_ *nsx.Cluster, path string) (map[string]interface{}, error) {
		retry++
		if strings.Contains(path, "alb-endpoint") {
			return nil, util.HttpNotFoundError
		} else {
			return nil, nil
		}
	})
	lbProvider = vpcService.getLBProvider(true)
	assert.Equal(t, NSXLB, lbProvider)
	assert.Equal(t, 1, retry)
	patch.Reset()

	// Test when UseAVILoadBalancer is true, Alb endpoint found, and NSX lbs found
	patch = gomonkey.ApplyPrivateMethod(reflect.TypeOf(vpcService.Service.NSXClient.Cluster), "HttpGet", func(_ *nsx.Cluster, path string) (map[string]interface{}, error) {
		return nil, nil
	})
	vpcService.LbsStore.Add(&model.LBService{Id: &defaultLBSName, ConnectivityPath: common.String("12345")})
	lbProvider = vpcService.getLBProvider(true)
	assert.Equal(t, NSXLB, lbProvider)
	patch.Reset()

	// Test when UseAVILoadBalancer is true, Alb endpoint found,  but no edge cluster
	patch = gomonkey.ApplyPrivateMethod(reflect.TypeOf(vpcService.Service.NSXClient.Cluster), "HttpGet", func(_ *nsx.Cluster, path string) (map[string]interface{}, error) {
		if strings.Contains(path, "alb-endpoint") {
			return nil, nil
		} else {
			return nil, util.HttpNotFoundError
		}
	})
	vpcService.LbsStore.Add(&model.LBService{Id: &defaultLBSName, ConnectivityPath: common.String("12345")})
	lbProvider = vpcService.getLBProvider(false)
	assert.Equal(t, NoneLB, lbProvider)
	patch.Reset()

	// Test when UseAVILoadBalancer is true, Alb endpoint found, and no NSX lbs found
	patch = gomonkey.ApplyPrivateMethod(reflect.TypeOf(vpcService.Service.NSXClient.Cluster), "HttpGet", func(_ *nsx.Cluster, path string) (map[string]interface{}, error) {
		return nil, nil
	})
	lbs := vpcService.LbsStore.GetByKey("12345")
	err := vpcService.LbsStore.Delete(lbs)
	assert.Equal(t, err, nil)
	lbProvider = vpcService.getLBProvider(false)
	assert.Equal(t, AVILB, lbProvider)
	patch.Reset()
}

func TestGetGatewayConnectionTypeFromConnectionPath(t *testing.T) {
	tests := []struct {
		name          string
		path          string
		expectedType  string
		expectedError error
	}{
		{
			name:          "ValidPath",
			path:          "/infra/distributed-gateway-connections/gateway-101",
			expectedType:  "distributed-gateway-connections",
			expectedError: nil,
		},
		{
			name:          "InvalidPath",
			path:          "invalidPath",
			expectedType:  "",
			expectedError: fmt.Errorf("unexpected connectionPath invalidPath"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, _, _ := createService(t)
			observedType, observedError := service.GetGatewayConnectionTypeFromConnectionPath(tt.path)
			assert.Equal(t, tt.expectedType, observedType)
			assert.Equal(t, tt.expectedError, observedError)
		})
	}
}

func TestValidateGatewayConnectionStatus(t *testing.T) {
	tests := []struct {
		name                 string
		prepareFunc          func(*testing.T, *VPCService) *gomonkey.Patches
		vpcNetworkConfigInfo common.VPCNetworkConfigInfo
		expectedReady        bool
		expectedReason       string
		expectedError        error
	}{
		{
			name: "GatewayConnectionNotSet",
			prepareFunc: func(_ *testing.T, service *VPCService) (patches *gomonkey.Patches) {
				patches = gomonkey.ApplyMethodSeq(reflect.TypeOf(service.NSXClient.ProjectClient), "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						model.Project{
							SiteInfos: []model.SiteInfo{
								{EdgeClusterPaths: []string{"edge"}},
							},
						},
						nil,
					},
					Times: 1,
				}})
				patches.ApplyMethodSeq(reflect.TypeOf(service.NSXClient.VPCConnectivityProfilesClient), "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						model.VpcConnectivityProfileListResult{
							Results: []model.VpcConnectivityProfile{
								{
									TransitGatewayPath: common.String("a/b"),
								},
							},
						},
						nil,
					},
					Times: 1,
				}})
				patches.ApplyMethodSeq(reflect.TypeOf(service.NSXClient.VPCConnectivityProfilesClient), "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						model.VpcConnectivityProfileListResult{
							Results: []model.VpcConnectivityProfile{
								{
									TransitGatewayPath: common.String("a/b"),
								},
							},
						},
						nil,
					},
					Times: 1,
				}})
				patches.ApplyMethodSeq(reflect.TypeOf(service.NSXClient.TransitGatewayAttachmentClient), "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						model.TransitGatewayAttachmentListResult{
							Results: []model.TransitGatewayAttachment{},
						},
						nil,
					},
					Times: 1,
				}})
				return patches
			},
			vpcNetworkConfigInfo: common.VPCNetworkConfigInfo{
				Org:        "default",
				NSXProject: "project-quality",
			},
			expectedReady:  false,
			expectedReason: "GatewayConnectionNotSet",
			expectedError:  nil,
		},
		{
			name: "DistributedGatewayConnectionNotSupported",
			prepareFunc: func(_ *testing.T, service *VPCService) (patches *gomonkey.Patches) {
				patches = gomonkey.ApplyMethodSeq(reflect.TypeOf(service.NSXClient.VPCConnectivityProfilesClient), "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						model.VpcConnectivityProfileListResult{
							Results: []model.VpcConnectivityProfile{
								{
									TransitGatewayPath: common.String("a/b"),
								},
							},
						},
						nil,
					},
					Times: 1,
				}})
				patches.ApplyMethodSeq(reflect.TypeOf(service.NSXClient.VPCConnectivityProfilesClient), "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						model.VpcConnectivityProfileListResult{
							Results: []model.VpcConnectivityProfile{
								{
									TransitGatewayPath: common.String("a/b"),
								},
							},
						},
						nil,
					},
					Times: 1,
				}})
				patches.ApplyMethodSeq(reflect.TypeOf(service.NSXClient.TransitGatewayAttachmentClient), "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						model.TransitGatewayAttachmentListResult{
							Results: []model.TransitGatewayAttachment{
								{
									ConnectionPath: common.String("/infra/distributed-gateway-connections/gateway-101"),
								},
							},
						},
						nil,
					},
					Times: 1,
				}})
				return patches
			},
			vpcNetworkConfigInfo: common.VPCNetworkConfigInfo{
				Org:        "default",
				NSXProject: "project-quality",
			},
			expectedReady:  false,
			expectedReason: "DistributedGatewayConnectionNotSupported",
			expectedError:  nil,
		},
	}

	service, _, _ := createService(t)
	service.NSXClient.ProjectClient = fakeProjectClient{}
	service.NSXClient.VPCConnectivityProfilesClient = fakeVPCConnectivityProfilesClient{}
	service.NSXClient.TransitGatewayAttachmentClient = fakeTransitGatewayAttachmentClient{}
	for _, tt := range tests {
		t.Run(tt.name, func(*testing.T) {
			if tt.prepareFunc != nil {
				patches := tt.prepareFunc(t, service)
				defer patches.Reset()
			}
			ready, reason, err := service.ValidateGatewayConnectionStatus(&tt.vpcNetworkConfigInfo)
			assert.Equal(t, tt.expectedReady, ready)
			assert.Equal(t, tt.expectedReason, reason)
			assert.Equal(t, tt.expectedError, err)
			return
		})
	}
}

func TestIsLBProviderChanged(t *testing.T) {
	vpcService := &VPCService{
		// Initialize any necessary fields or dependencies here
		LbsStore: &LBSStore{}, // Mock or use a fake implementation of LbsStore
	}

	tests := []struct {
		name        string
		prepareFunc func(*testing.T, *VPCService) *gomonkey.Patches
		existingVPC *model.Vpc
		lbProvider  LBProvider
		expected    bool
	}{
		{
			name:        "Nil existingVPC",
			existingVPC: nil,
			lbProvider:  AVILB,
			expected:    false,
		},
		{
			name: "AVILB with Enabled false",
			existingVPC: &model.Vpc{
				LoadBalancerVpcEndpoint: &model.LoadBalancerVPCEndpoint{
					Enabled: common.Bool(false),
				},
			},
			lbProvider: AVILB,
			expected:   true,
		},
		{
			name: "AVILB with Enabled true",
			existingVPC: &model.Vpc{
				LoadBalancerVpcEndpoint: &model.LoadBalancerVPCEndpoint{
					Enabled: common.Bool(true),
				},
			},
			lbProvider: AVILB,
			expected:   false,
		},
		{
			name: "AVILB with Enabled nil",
			existingVPC: &model.Vpc{
				LoadBalancerVpcEndpoint: &model.LoadBalancerVPCEndpoint{
					Enabled: nil,
				},
			},
			lbProvider: AVILB,
			expected:   true,
		},
		{
			name: "NSXLB with nil Lbs",
			existingVPC: &model.Vpc{
				Path: common.String("/fake/path"),
			},
			lbProvider: NSXLB,
			prepareFunc: func(_ *testing.T, service *VPCService) (patches *gomonkey.Patches) {
				return gomonkey.ApplyMethod(reflect.TypeOf(service.LbsStore), "GetByKey", func(_ *LBSStore, _ string) *model.LBService {
					return nil
				})
			},
			expected: true,
		},
		{
			name: "NSXLB with non-nil Lbs",
			existingVPC: &model.Vpc{
				Path: common.String("/fake/path"),
			},
			lbProvider: NSXLB,
			prepareFunc: func(_ *testing.T, service *VPCService) (patches *gomonkey.Patches) {
				return gomonkey.ApplyMethod(reflect.TypeOf(service.LbsStore), "GetByKey", func(_ *LBSStore, _ string) *model.LBService {
					return &model.LBService{
						Path: common.String("/fake/path"),
					}
				})
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.prepareFunc != nil {
				patches := tt.prepareFunc(t, vpcService)
				defer patches.Reset()
			}
			result := vpcService.IsLBProviderChanged(tt.existingVPC, tt.lbProvider)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetLBSsFromNSXByVPC(t *testing.T) {
	vpcService := &VPCService{
		Service: common.Service{
			NSXClient: &nsx.Client{
				Cluster:      &nsx.Cluster{},
				VPCLBSClient: &fakeVPCLBSClient{},
			},
		},
	}
	vpcPath := "/orgs/default/projects/p1/vpcs/pre-vpc"

	for _, tt := range []struct {
		name          string
		prepareFunc   func(*testing.T, *VPCService) *gomonkey.Patches
		expectErr     bool
		expectLBSPath string
	}{
		{
			name: "error when listing LBS under pre-created VPC",
			prepareFunc: func(t *testing.T, service *VPCService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(reflect.TypeOf(service.NSXClient.VPCLBSClient), "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						model.LBServiceListResult{
							Results: []model.LBService{},
						},
						fmt.Errorf("failed to list LBS under VPC"),
					},
					Times: 1,
				}})
				return patches
			},
			expectErr: true,
		}, {
			name: "no LBS exist under VPC",
			prepareFunc: func(t *testing.T, service *VPCService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(reflect.TypeOf(service.NSXClient.VPCLBSClient), "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						model.LBServiceListResult{
							Results: []model.LBService{},
						},
						nil,
					},
					Times: 1,
				}})
				return patches
			},
			expectErr:     false,
			expectLBSPath: "",
		}, {
			name: "one LBS exists under VPC",
			prepareFunc: func(t *testing.T, service *VPCService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(reflect.TypeOf(service.NSXClient.VPCLBSClient), "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						model.LBServiceListResult{
							Results: []model.LBService{
								{
									Path: common.String("lbs-1"),
								},
							},
						},
						nil,
					},
					Times: 1,
				}})
				return patches
			},
			expectErr:     false,
			expectLBSPath: "lbs-1",
		}, {
			name: "multiple LBS exists under VPC, return the first",
			prepareFunc: func(t *testing.T, service *VPCService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(reflect.TypeOf(service.NSXClient.VPCLBSClient), "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						model.LBServiceListResult{
							Results: []model.LBService{
								{
									Path: common.String("lbs-1"),
								}, {
									Path: common.String("lbs-2"),
								},
							},
						},
						nil,
					},
					Times: 1,
				}})
				return patches
			},
			expectErr:     false,
			expectLBSPath: "lbs-1",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.prepareFunc != nil {
				patches := tt.prepareFunc(t, vpcService)
				defer patches.Reset()
			}
			lbsPath, err := vpcService.GetLBSsFromNSXByVPC(vpcPath)
			if tt.expectErr {
				assert.NotNil(t, err)
			} else {
				assert.Equal(t, tt.expectLBSPath, lbsPath)
			}
		})
	}
}
