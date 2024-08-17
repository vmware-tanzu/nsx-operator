package vpc

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
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

func TestGetNetworkConfigFromNS(t *testing.T) {
	service, _, _ := createService(t)
	k8sClient := service.Client.(*mock_client.MockClient)
	fakeErr := errors.New("fake error")
	mockNs := &v1.Namespace{}
	k8sClient.EXPECT().Get(ctx, gomock.Any(), mockNs).Return(fakeErr).Do(func(_ context.Context, k client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		return nil
	})
	ns, err := service.getNetworkconfigNameFromNS("test")
	assert.Equal(t, fakeErr, err)
	assert.Equal(t, "", ns)

	k8sClient.EXPECT().Get(ctx, gomock.Any(), mockNs).Return(nil).Do(func(_ context.Context, k client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		return nil
	})
	ns, err = service.getNetworkconfigNameFromNS("test")
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
	ns, err = service.getNetworkconfigNameFromNS("test")
	assert.Nil(t, err)
	assert.Equal(t, "test-name", ns)

	k8sClient.EXPECT().Get(ctx, gomock.Any(), mockNs).Return(nil).Do(func(_ context.Context, k client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		obj.SetAnnotations(map[string]string{"nsx.vmware.com/vpc_network_config": "test-nc"})
		return nil
	})
	ns, err = service.getNetworkconfigNameFromNS("test")
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

func TestNSXLBEnabled(t *testing.T) {
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
	vpcService.Service.NSXConfig.UseAVILoadBalancer = false
	assert.True(t, vpcService.NSXLBEnabled())
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
	lbProvider := vpcService.getLBProvider()
	assert.Equal(t, LBProviderNSX, lbProvider)

	vpcService.Service.NSXConfig.NsxConfig.UseAVILoadBalancer = true
	// Test when UseAVILoadBalancer is true and Alb endpoint found, but no nsx lbs found
	patch := gomonkey.ApplyPrivateMethod(reflect.TypeOf(vpcService.Service.NSXClient.Cluster), "HttpGet", func(_ *nsx.Cluster, path string) (map[string]interface{}, error) {
		return nil, nil
	})
	lbProvider = vpcService.getLBProvider()
	assert.Equal(t, LBProviderAVI, lbProvider)
	patch.Reset()

	// Test when UseAVILoadBalancer is true, get alb endpoint common error
	retry := 0
	patch = gomonkey.ApplyPrivateMethod(reflect.TypeOf(vpcService.Service.NSXClient.Cluster), "HttpGet", func(_ *nsx.Cluster, path string) (map[string]interface{}, error) {
		retry++
		return nil, util.HttpCommonError
	})
	lbProvider = vpcService.getLBProvider()
	assert.Equal(t, LBProviderNSX, lbProvider)
	assert.Equal(t, 4, retry)
	patch.Reset()

	// Test when UseAVILoadBalancer is true, get alb endpoint not found error
	retry = 0
	patch = gomonkey.ApplyPrivateMethod(reflect.TypeOf(vpcService.Service.NSXClient.Cluster), "HttpGet", func(_ *nsx.Cluster, path string) (map[string]interface{}, error) {
		retry++
		return nil, util.HttpNotFoundError
	})
	lbProvider = vpcService.getLBProvider()
	assert.Equal(t, LBProviderNSX, lbProvider)
	assert.Equal(t, 1, retry)
	patch.Reset()

	// Test when UseAVILoadBalancer is true, Alb endpoint found, and NSX lbs found
	patch = gomonkey.ApplyPrivateMethod(reflect.TypeOf(vpcService.Service.NSXClient.Cluster), "HttpGet", func(_ *nsx.Cluster, path string) (map[string]interface{}, error) {
		return nil, nil
	})
	vpcService.LbsStore.Add(&model.LBService{Id: common.String("12345")})
	lbProvider = vpcService.getLBProvider()
	assert.Equal(t, LBProviderNSX, lbProvider)
	patch.Reset()

	// Test when UseAVILoadBalancer is true, Alb endpoint found, and no NSX lbs found
	patch = gomonkey.ApplyPrivateMethod(reflect.TypeOf(vpcService.Service.NSXClient.Cluster), "HttpGet", func(_ *nsx.Cluster, path string) (map[string]interface{}, error) {
		return nil, nil
	})
	lbs := vpcService.LbsStore.GetByKey("12345")
	err := vpcService.LbsStore.Delete(lbs)
	assert.Equal(t, err, nil)
	lbProvider = vpcService.getLBProvider()
	assert.Equal(t, LBProviderAVI, lbProvider)
	patch.Reset()
}
