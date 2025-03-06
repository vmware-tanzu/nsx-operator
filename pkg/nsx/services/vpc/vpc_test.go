package vpc

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	k8sapierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	mocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/vpcclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/realizestate"
	nsxUtil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

var (
	vpcName1             = "ns1-vpc-1"
	vpcName2             = "ns1-vpc-2"
	vpcName3             = "ns1-vpc-3"
	infraVPCName         = "infra-vpc"
	vpcID1               = "ns-vpc-uid-1"
	vpcID2               = "ns-vpc-uid-2"
	vpcID3               = "ns-vpc-uid-3"
	IPv4Type             = "IPv4"
	cluster              = "k8scl-one"
	tagScopeCluster      = common.TagScopeCluster
	tagScopeNamespace    = common.TagScopeNamespace
	tagScopeNamespaceUID = common.TagScopeNamespaceUID
)

func createService(t *testing.T) (*VPCService, *gomock.Controller, *mocks.MockVpcsClient, *mock_client.MockClient) {
	config2 := nsx.NewConfig("localhost", "1", "1", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})

	cluster, _ := nsx.NewCluster(config2)
	rc := cluster.NewRestConnector()

	mockCtrl := gomock.NewController(t)
	mockVpcclient := mocks.NewMockVpcsClient(mockCtrl)
	k8sClient := mock_client.NewMockClient(mockCtrl)

	vpcStore := &VPCStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.VpcBindingType(),
	}}

	lbsStore := &LBSStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.LBServiceBindingType(),
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
		LbsStore: lbsStore,
	}
	return service, mockCtrl, mockVpcclient, k8sClient
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

func TestGetSharedVPCNamespaceFromNS(t *testing.T) {
	tests := []struct {
		name                    string
		ns                      string
		existingNames           []*v1.Namespace
		expectedNS              string
		expectedSharedNamespace string
		expectedErrStr          string
	}{
		{
			name:                    "Test the Namespace not found",
			ns:                      "test-ns-1",
			existingNames:           nil,
			expectedNS:              "",
			expectedSharedNamespace: "",
			expectedErrStr:          "not found",
		},
		{
			name: "Not shared Namespace",
			ns:   "test-ns-2",
			existingNames: []*v1.Namespace{
				{
					TypeMeta:   metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{Name: "test-ns-2", Annotations: map[string]string{"nsx.vmware.com/vpc_network_config": "default"}},
					Spec:       v1.NamespaceSpec{},
					Status:     v1.NamespaceStatus{},
				},
			},
			expectedNS:              "test-ns-2",
			expectedSharedNamespace: "",
		},
		{
			name: "Got the shared Namespace",
			ns:   "test-ns-1",
			existingNames: []*v1.Namespace{
				{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-ns-1",
						Annotations: map[string]string{"nsx.vmware.com/vpc_network_config": "default", "nsx.vmware.com/shared_vpc_namespace": "test-ns-2"},
					},
					Spec:   v1.NamespaceSpec{},
					Status: v1.NamespaceStatus{},
				},
				{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-ns-2",
						Annotations: map[string]string{"nsx.vmware.com/vpc_network_config": "default"},
					},
					Spec:   v1.NamespaceSpec{},
					Status: v1.NamespaceStatus{},
				},
			},
			expectedSharedNamespace: "test-ns-2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, _, _, _ := createService(t)
			newScheme := runtime.NewScheme()
			utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
			fakeClient := fake.NewClientBuilder().WithScheme(newScheme).WithObjects().Build()
			service.Client = fakeClient

			for _, ns := range tt.existingNames {
				err := service.Client.Create(context.TODO(), ns)
				assert.NoError(t, err)
			}

			ns, sharedNS, _err := service.resolveSharedVPCNamespace(context.Background(), tt.ns)

			if tt.expectedSharedNamespace != "" {
				assert.NotNil(t, sharedNS)
				assert.Equal(t, tt.expectedSharedNamespace, sharedNS.Name)
			} else {
				assert.Nil(t, sharedNS)
			}

			if tt.expectedNS != "" {
				assert.NotNil(t, ns)
				assert.Equal(t, tt.expectedNS, ns.Name)
			} else {
				assert.Nil(t, ns)
			}

			if tt.expectedErrStr != "" {
				assert.ErrorContains(t, _err, tt.expectedErrStr)
			} else {
				assert.NoError(t, _err)
			}
		})
	}
}

func TestGetVPCsByNamespace(t *testing.T) {
	ctx := context.Background()
	vpcCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{
		common.TagScopeNamespaceUID: vpcIndexNamespaceIDFunc,
		common.TagScopeNamespace:    vpcIndexNamespaceNameFunc,
	})
	resourceStore := common.ResourceStore{
		Indexer:     vpcCacheIndexer,
		BindingType: model.VpcBindingType(),
	}
	vpcStore := &VPCStore{ResourceStore: resourceStore}
	service := &VPCService{
		Service: common.Service{NSXClient: nil},
	}
	service.VpcStore = vpcStore
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
		{
			Scope: &tagScopeNamespaceUID,
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
		{
			Scope: &tagScopeNamespaceUID,
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
		{
			Scope: &tagScopeNamespaceUID,
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
	staleNamespaceID := "fakeStaleNamespaceID"
	tagStale := []model.Tag{
		{
			Scope: &tagScopeCluster,
			Tag:   &cluster,
		},
		{
			Scope: &tagScopeNamespace,
			Tag:   &ns2,
		},
		{
			Scope: &tagScopeNamespaceUID,
			Tag:   &staleNamespaceID,
		},
	}
	vpcStaleName := "fakeStaleVPCName"
	vpcStaleID := "vpcStaleID"
	vpcStale := model.Vpc{
		DisplayName:        &vpcStaleName,
		Id:                 &vpcStaleID,
		Tags:               tagStale,
		IpAddressType:      &IPv4Type,
		PrivateIpv4Blocks:  []string{"3.3.3.0/24"},
		ExternalIpv4Blocks: []string{"4.4.4.0/24"},
	}
	vpc3 := vpc2
	vpc3.DisplayName = &vpcName3
	fakeVPCID := "fakeVPCID"
	vpc3.Id = &fakeVPCID
	infravpc := model.Vpc{
		DisplayName:        &infraVPCName,
		Id:                 &vpcID3,
		Tags:               tag3,
		IpAddressType:      &IPv4Type,
		PrivateIpv4Blocks:  []string{"3.3.3.0/24"},
		ExternalIpv4Blocks: []string{"4.4.4.0/24"},
	}
	tests := []struct {
		name                string
		ns                  string
		expectVPCNum        int
		expectFreshVPCNum   int
		expectVPCNames      []string
		expectFreshVPCNames []string
		prepareFunc         func() *gomonkey.Patches
		expectErrStr        string
	}{
		{
			name: "Namespace not found",
			ns:   "invalid",
			prepareFunc: func() *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(service), "resolveSharedVPCNamespace", func(_ *VPCService, ns string) (*v1.Namespace, *v1.Namespace, error) {
					return nil, nil, errors.New("error Namespace not found")
				})
				return patches
			},
			expectVPCNum:      0,
			expectVPCNames:    nil,
			expectFreshVPCNum: 0,
		},
		{
			name:              "Get VPC by Namespace",
			ns:                "test-ns-1",
			expectVPCNum:      1,
			expectVPCNames:    []string{vpcName1},
			expectFreshVPCNum: 1,
			prepareFunc: func() *gomonkey.Patches {
				patch := gomonkey.ApplyPrivateMethod(reflect.TypeOf(service), "resolveSharedVPCNamespace", func(_ *VPCService, ns string) (*v1.Namespace, *v1.Namespace, error) {
					return &v1.Namespace{
						ObjectMeta: metav1.ObjectMeta{Name: "test-ns-1", UID: types.UID("test-ns-1")},
					}, nil, nil
				})
				return patch
			},
			expectFreshVPCNames: []string{vpcName1},
		},
		// It can support multiple VPCs for one Namespace at the code level
		{
			name:         "One Namespace has two VPC",
			ns:           "test-ns-2",
			expectVPCNum: 3,
			prepareFunc: func() *gomonkey.Patches {
				patch := gomonkey.ApplyPrivateMethod(reflect.TypeOf(service), "resolveSharedVPCNamespace", func(_ *VPCService, ns string) (*v1.Namespace, *v1.Namespace, error) {
					return &v1.Namespace{
						ObjectMeta: metav1.ObjectMeta{Name: "test-ns-2", UID: types.UID("test-ns-2")},
					}, nil, nil
				})
				return patch
			},
			expectVPCNames:      []string{vpcName2, vpcName3, vpcStaleName},
			expectFreshVPCNum:   2,
			expectFreshVPCNames: []string{vpcName2, vpcName3},
		},
		{
			name:           "Shared Namespace",
			ns:             "test-ns-1",
			expectVPCNum:   1,
			expectVPCNames: []string{vpcName1},
			prepareFunc: func() *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(service), "resolveSharedVPCNamespace", func(_ *VPCService, ns string) (*v1.Namespace, *v1.Namespace, error) {
					return nil, &v1.Namespace{
						ObjectMeta: metav1.ObjectMeta{Name: "kube-system", UID: types.UID("kube-system")},
					}, nil
				})
				return patches
			},
			expectFreshVPCNum:   1,
			expectFreshVPCNames: []string{infraVPCName},
		},
	}

	vpcStore.Apply(&vpc1)
	vpcStore.Apply(&vpc2)
	vpcStore.Apply(&vpc3)
	vpcStore.Apply(&vpcStale)
	vpcStore.Apply(&infravpc)
	got := vpcStore.List()
	assert.Equal(t, 5, len(got))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.prepareFunc != nil {
				patches := tt.prepareFunc()
				defer patches.Reset()
			}

			gotVPCs := service.GetVPCsByNamespace(tt.ns)
			assert.Equal(t, tt.expectVPCNum, len(gotVPCs))

			for _, vpc := range gotVPCs {
				assert.Contains(t, tt.expectVPCNames, *vpc.DisplayName)
			}

			freshVPCs := service.GetCurrentVPCsByNamespace(ctx, tt.ns)
			assert.Equal(t, tt.expectFreshVPCNum, len(freshVPCs))

			for _, vpc := range freshVPCs {
				assert.Contains(t, tt.expectFreshVPCNames, *vpc.DisplayName)
			}
		})
	}
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

	testCases := []struct {
		name             string
		prepareFuncs     func() *gomonkey.Patches
		lbprovider       LBProvider
		expectLBProvider LBProvider
	}{
		{
			name:             "Test when globalLbProvider is avi lb",
			lbprovider:       AVILB,
			expectLBProvider: AVILB,
		},
		{
			name:             "Test when globalLbProvider is nsx lb",
			lbprovider:       NSXLB,
			expectLBProvider: NSXLB,
		},
		{
			name:             "Test when globalLbProvider is none lb and GetVPCNetworkConfig",
			lbprovider:       NoneLB,
			expectLBProvider: NSXLB,
			prepareFuncs: func() *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(&VPCService{}), "EdgeClusterEnabled", func(_ *VPCService, _ *common.VPCNetworkConfigInfo) bool {
					return false
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(&VPCService{}), "getLBProvider", func(_ *VPCService, _ bool) LBProvider {
					return NSXLB
				})
				patches.ApplyMethod(reflect.TypeOf(&VPCService{}), "GetVPCNetworkConfig", func(_ *VPCService, _ string) (*common.VPCNetworkConfigInfo, bool, error) {
					return &common.VPCNetworkConfigInfo{}, true, nil
				})
				return patches
			},
		},
		{
			name:             "Test when globalLbProvider is none lb GetVPCNetworkConfig none",
			lbprovider:       NoneLB,
			expectLBProvider: NoneLB,
			prepareFuncs: func() *gomonkey.Patches {
				return gomonkey.ApplyMethod(reflect.TypeOf(vpcService), "GetVPCNetworkConfig", func(_ *VPCService, _ string) (*common.VPCNetworkConfigInfo, bool, error) {
					return &common.VPCNetworkConfigInfo{}, false, nil
				})
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			globalLbProvider = testCase.lbprovider
			vpcService.Service.NSXConfig.UseAVILoadBalancer = false

			if testCase.prepareFuncs != nil {
				patches := testCase.prepareFuncs()
				defer patches.Reset()
			}

			lbProvider, err := vpcService.GetLBProvider()
			assert.Nil(t, err)
			assert.Equal(t, testCase.expectLBProvider, lbProvider)
		})
	}
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
		ServiceGateway: &model.VpcServiceGatewayConfig{
			Enable:    common.Bool(false),
			NatConfig: &model.VpcNatConfig{EnableDefaultSnat: common.Bool(true)},
		},
	}

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
			return nil, nsxUtil.HttpCommonError
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
			return nil, nsxUtil.HttpNotFoundError
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
			return nil, nsxUtil.HttpNotFoundError
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
			service, _, _, _ := createService(t)
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
				patches = gomonkey.ApplyMethodSeq(reflect.TypeOf(fakeVPCConnectivityProfilesClient{}), "List", []gomonkey.OutputCell{{
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

	service, _, _, _ := createService(t)
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

func TestVPCService_ValidateNetworkConfig(t *testing.T) {
	service, _, _, _ := createService(t)

	tests := []struct {
		name string
		nc   common.VPCNetworkConfigInfo
		want bool
	}{
		{
			name: "is pre-created vpc",
			nc:   common.VPCNetworkConfigInfo{VPCPath: "fake-path"},
			want: true,
		},
		{
			name: "pre-created vpc with nil private ips",
			nc:   common.VPCNetworkConfigInfo{VPCPath: "", PrivateIPs: nil},
			want: false,
		},
		{
			name: "pre-created vpc with empty private ips",
			nc:   common.VPCNetworkConfigInfo{VPCPath: "", PrivateIPs: []string{}},
			want: false,
		},
		{
			name: "pre-created vpc with valid private ips",
			nc:   common.VPCNetworkConfigInfo{VPCPath: "", PrivateIPs: []string{"1.1.1.1/16"}},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := service.ValidateNetworkConfig(tt.nc); got != tt.want {
				t.Errorf("VPCService.ValidateNetworkConfig() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetVPCNetworkConfigByNamespace(t *testing.T) {
	service, _, _, k8sClient := createService(t)
	tests := []struct {
		name           string
		prepareFunc    func() *gomonkey.Patches
		expectedError  error
		expectedResult *common.VPCNetworkConfigInfo
	}{
		{
			name: "ConfigFoundError",
			prepareFunc: func() *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(fmt.Errorf("mock error"))
				patches := gomonkey.ApplyFunc((*VPCService).GetNetworkconfigNameFromNS,
					func(s *VPCService, ctx context.Context, ns string) (string, error) {
						return "config-1", nil
					})
				return patches
			},
			expectedError: fmt.Errorf("mock error"),
		},
		{
			name: "ConfigFoundSuccess",
			prepareFunc: func() *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					configCR := obj.(*v1alpha1.VPCNetworkConfiguration)
					configCR.Name = "config-1"
					configCR.Spec = v1alpha1.VPCNetworkConfigurationSpec{
						VPCConnectivityProfile: "profile",
						PrivateIPs:             []string{"10.0.0.4"},
						DefaultSubnetSize:      64,
						VPC:                    "/vpc-path",
						NSXProject:             "/orgs/org/projects/project",
					}
					return nil
				})
				patches := gomonkey.ApplyFunc((*VPCService).GetNetworkconfigNameFromNS,
					func(s *VPCService, ctx context.Context, ns string) (string, error) {
						return "config-1", nil
					})
				return patches
			},
			expectedResult: &common.VPCNetworkConfigInfo{
				IsDefault:              false,
				Org:                    "org",
				Name:                   "config-1",
				VPCConnectivityProfile: "profile",
				NSXProject:             "project",
				PrivateIPs:             []string{"10.0.0.4"},
				DefaultSubnetSize:      64,
				VPCPath:                "/vpc-path",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := tt.prepareFunc()
			defer patches.Reset()
			info, err := service.GetVPCNetworkConfigByNamespace("ns-1")
			if tt.expectedError != nil {
				assert.Equal(t, tt.expectedError, err)
			} else {
				assert.Nil(t, tt.expectedError)
				assert.Equal(t, tt.expectedResult, info)
			}
		})
	}
}

func TestVPCService_ListVPCInfo(t *testing.T) {
	service, _, _, k8sClient := createService(t)
	tests := []struct {
		name            string
		prepareFunc     func() *gomonkey.Patches
		expectedResults []common.VPCResourceInfo
	}{
		{
			name: "FailedToGetNetworkConfig",
			prepareFunc: func() *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*VPCService).GetNetworkconfigNameFromNS,
					func(s *VPCService, ctx context.Context, ns string) (string, error) {
						return "", errors.New("mock error")
					})
				return patches
			},
			expectedResults: []common.VPCResourceInfo{},
		},
		{
			name: "NoDefaultNetworkConfig",
			prepareFunc: func() *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					namespaceCR := obj.(*v1.Namespace)
					namespaceCR.Name = "test-ns"
					return nil
				})
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil)
				return nil
			},
			expectedResults: []common.VPCResourceInfo{},
		},
		{
			name: "PrecreatedVPC",
			prepareFunc: func() *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*VPCService).GetVPCNetworkConfigByNamespace,
					func(s *VPCService, ns string) (*common.VPCNetworkConfigInfo, error) {
						return &common.VPCNetworkConfigInfo{
							VPCPath: "/orgs/org/projects/project/vpcs/vpc",
						}, nil
					})
				return patches
			},
			expectedResults: []common.VPCResourceInfo{
				{
					OrgID:     "org",
					ProjectID: "project",
					VPCID:     "vpc",
					ID:        "vpc",
					ParentID:  "project",
				},
			},
		},
		{
			name: "NonPrecreatedVPC",
			prepareFunc: func() *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*VPCService).GetNetworkconfigNameFromNS,
					func(s *VPCService, ctx context.Context, ns string) (string, error) {
						return "nc-1", nil
					})
				patches.ApplyFunc((*VPCService).GetCurrentVPCsByNamespace,
					func(s *VPCService, ctx context.Context, namespace string) []*model.Vpc {
						return []*model.Vpc{
							{
								Path:              common.String("/orgs/org/projects/project/vpcs/vpc"),
								PrivateIpv4Blocks: []string{"10.0.0.1"},
							},
						}
					})
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(k8sapierrors.NewNotFound(v1alpha1.Resource("vpcnetworkconfiguration"), ""))
				return patches
			},
			expectedResults: []common.VPCResourceInfo{
				{
					OrgID:             "org",
					ProjectID:         "project",
					VPCID:             "vpc",
					ID:                "vpc",
					ParentID:          "project",
					PrivateIpv4Blocks: []string{"10.0.0.1"},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.prepareFunc != nil {
				patches := tt.prepareFunc()
				if patches != nil {
					defer patches.Reset()
				}
			}
			results := service.ListVPCInfo("test-ns")
			assert.Equal(t, len(tt.expectedResults), len(results))
			if len(tt.expectedResults) > 0 {
				assert.Equal(t, tt.expectedResults[0], results[0])
			}
		})
	}
}

func TestVPCService_GetNamespacesByNetworkconfigName(t *testing.T) {
	service, _, _, k8sClient := createService(t)

	ns1 := v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ns-1",
			Annotations: map[string]string{
				common.AnnotationVPCNetworkConfig: "fake-config",
			},
		},
	}
	ns2 := v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ns-2",
			Annotations: map[string]string{
				common.AnnotationVPCNetworkConfig: "fake-config",
			},
		},
	}
	ns3 := v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ns-3",
			Annotations: map[string]string{
				common.AnnotationVPCNetworkConfig: "dummy-config",
			},
		},
	}

	ns4 := v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ns-4",
		},
	}

	tests := []struct {
		name            string
		expectedResults []string
		expectedError   string
		configName      string
	}{
		{
			name:            "OneNamespace",
			expectedResults: []string{"ns-3"},
			configName:      "dummy-config",
		},
		{
			name:            "NoNamespace",
			expectedResults: []string{},
			configName:      "non-existed",
		},
		{
			name:            "MultipleNamespace",
			expectedResults: []string{"ns-1", "ns-2"},
			configName:      "fake-config",
		},
		{
			name:            "DefaultNamespace",
			expectedResults: []string{"ns-4"},
			configName:      "default",
		},
		{
			name:          "ListError",
			configName:    "default",
			expectedError: "mock error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.expectedError != "" {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(fmt.Errorf("mock error"))
			} else {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					a := list.(*v1.NamespaceList)
					a.Items = append(a.Items, ns1, ns2, ns3, ns4)
					return nil
				})
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					a := list.(*v1alpha1.VPCNetworkConfigurationList)
					a.Items = append(a.Items, v1alpha1.VPCNetworkConfiguration{
						ObjectMeta: metav1.ObjectMeta{
							Name: "default",
							Annotations: map[string]string{
								common.AnnotationDefaultNetworkConfig: "true",
							},
						},
						Spec: v1alpha1.VPCNetworkConfigurationSpec{
							NSXProject: "/orgs/org/projects/project",
						},
					})
					return nil
				})
			}
			ret, err := service.GetNamespacesByNetworkconfigName(tt.configName)
			if tt.expectedError != "" {
				assert.NotNil(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, len(tt.expectedResults), len(ret))
				if len(tt.expectedResults) != 0 {
					for _, expectedResult := range tt.expectedResults {
						assert.Contains(t, ret, expectedResult)
					}
				}
			}
		})
	}
}

func TestVPCService_DeleteVPC(t *testing.T) {
	mockVpc := "mockVpc"
	mockLb := "mockLb"
	mockConnectityPath := fmt.Sprintf("/org/default/projects/proj1/vpcs/%s/connectivity", mockVpc)
	mockLBKey := combineVPCIDAndLBSID(mockVpc, mockLb)
	service, _, _, _ := createService(t)
	fakeErr := errors.New("fake-errors")
	tests := []struct {
		name          string
		prepareFunc   func(*testing.T, *VPCService) *gomonkey.Patches
		LbsStore      *LBSStore
		path          string
		Lb            *model.LBService
		Vpc           *model.Vpc
		checkLBStore  bool
		checkVPCStore bool
		wantErr       bool
		want          error
	}{
		{
			name:          "parse vpc info error",
			path:          "/in/correct/path",
			Lb:            nil,
			Vpc:           nil,
			want:          nil,
			wantErr:       true,
			checkLBStore:  false,
			checkVPCStore: false,
		},
		{
			name: "delete vpc error",
			prepareFunc: func(_ *testing.T, service *VPCService) (patches *gomonkey.Patches) {
				patches = gomonkey.ApplyMethodSeq(reflect.TypeOf(service.NSXClient.VPCClient), "Delete", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						fakeErr,
					},
					Times: 1,
				}})
				return patches
			},
			path:          "/orgs/default/projects/proj1/vpcs/mockVpc",
			Lb:            nil,
			Vpc:           nil,
			wantErr:       true,
			want:          nsxUtil.TransNSXApiError(fakeErr),
			checkLBStore:  false,
			checkVPCStore: false,
		},
		{
			name: "lb in store but vpc not",
			prepareFunc: func(_ *testing.T, service *VPCService) (patches *gomonkey.Patches) {
				patches = gomonkey.ApplyMethodSeq(reflect.TypeOf(service.NSXClient.VPCClient), "Delete", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						nil,
					},
					Times: 1,
				}})
				return patches
			},
			path: "/orgs/default/projects/proj1/vpcs/mockVpc",
			Lb: &model.LBService{
				Id:               &mockLb,
				ConnectivityPath: &mockConnectityPath,
			},
			Vpc:           nil,
			wantErr:       false,
			want:          nil,
			checkLBStore:  true,
			checkVPCStore: false,
		},
		{
			name: "delete vpc store fail",
			prepareFunc: func(_ *testing.T, service *VPCService) (patches *gomonkey.Patches) {
				patches = gomonkey.ApplyMethodSeq(reflect.TypeOf(service.NSXClient.VPCClient), "Delete", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						nil,
					},
					Times: 1,
				}})
				patches.ApplyMethodSeq(reflect.TypeOf(service.VpcStore), "Apply", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						fakeErr,
					},
					Times: 1,
				}})
				return patches
			},
			Lb: &model.LBService{
				Id:               &mockLb,
				ConnectivityPath: &mockConnectityPath,
			},
			Vpc: &model.Vpc{
				Id: &mockVpc,
			},
			path:          "/orgs/default/projects/proj1/vpcs/mockVpc",
			want:          fakeErr,
			wantErr:       true,
			checkLBStore:  true,
			checkVPCStore: false,
		},
		{
			name: "happy pass",
			prepareFunc: func(_ *testing.T, service *VPCService) (patches *gomonkey.Patches) {
				patches = gomonkey.ApplyMethodSeq(reflect.TypeOf(service.NSXClient.VPCClient), "Delete", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						nil,
					},
					Times: 1,
				}})
				patches.ApplyMethodSeq(reflect.TypeOf(service.VpcStore), "Apply", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						nil,
					},
					Times: 1,
				}})
				return patches
			},
			Lb: &model.LBService{
				Id:               &mockLb,
				ConnectivityPath: &mockConnectityPath,
			},
			Vpc: &model.Vpc{
				Id: &mockVpc,
			},
			path:          "/orgs/default/projects/proj1/vpcs/mockVpc",
			want:          nil,
			wantErr:       false,
			checkLBStore:  true,
			checkVPCStore: true,
		},
	}
	// We do not need to verify copylocks for test case.
	// nolint: copylocks
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.prepareFunc != nil {
				patches := tt.prepareFunc(t, service)
				defer patches.Reset()
			}

			if tt.Lb != nil {
				service.LbsStore.Add(tt.Lb)
			}

			if tt.Vpc != nil {
				service.VpcStore.Add(tt.Vpc)
			}

			err := service.DeleteVPC(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("VPCService.DeleteVPC() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.want != nil {
				assert.Equal(t, err, tt.want)
			}

			if tt.checkLBStore {
				// if prepare func executed, then lb store should be empty
				lb := service.LbsStore.GetByKey(mockLBKey)
				assert.Nil(t, lb)
			}

			if tt.checkVPCStore {
				// if prepare func executed, then vpc store should be empty
				lb := service.LbsStore.GetByKey(mockLBKey)
				assert.Nil(t, lb)
			}
		})
	}
}

func TestListAllVPCsFromNSX(t *testing.T) {
	for _, tc := range []struct {
		name      string
		vpcs      []*data.StructValue
		err       error
		expVpcMap map[string]model.Vpc
	}{
		{
			name: "Failed to list VPC from NSX",
			err:  fmt.Errorf("connection issue"),
		},
		{
			name: "success",
			vpcs: []*data.StructValue{
				data.NewStructValue("",
					map[string]data.DataValue{
						"resource_type": data.NewStringValue("Vpc"),
						"id":            data.NewStringValue("vpc1"),
						"path":          data.NewStringValue("/orgs/default/projects/default/vpcs/vpc1"),
					}),
				data.NewStructValue("",
					map[string]data.DataValue{
						"resource_type": data.NewStringValue("Vpc"),
						"id":            data.NewStringValue("vpc2"),
						"path":          data.NewStringValue("/orgs/default/projects/default/vpcs/vpc2"),
					}),
			},
			expVpcMap: map[string]model.Vpc{
				"/orgs/default/projects/default/vpcs/vpc1": {
					Id:           common.String("vpc1"),
					Path:         common.String("/orgs/default/projects/default/vpcs/vpc1"),
					ResourceType: common.String("Vpc"),
				},
				"/orgs/default/projects/default/vpcs/vpc2": {
					Id:           common.String("vpc2"),
					Path:         common.String("/orgs/default/projects/default/vpcs/vpc2"),
					ResourceType: common.String("Vpc"),
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &VPCService{
				Service: common.Service{},
			}
			searchResourcePatch := gomonkey.ApplyMethod(reflect.TypeOf(&s.Service), "SearchResource",
				func(_ *common.Service, _ string, _ string, store common.Store, _ common.Filter) (uint64, error) {
					for i := range tc.vpcs {
						vpc := tc.vpcs[i]
						store.TransResourceToStore(vpc)
					}
					return uint64(len(tc.vpcs)), tc.err
				})
			defer searchResourcePatch.Reset()
			vpcMap := s.GetAllVPCsFromNSX()
			require.Equal(t, len(tc.expVpcMap), len(vpcMap))
			for k, v := range tc.expVpcMap {
				actVpc, ok := vpcMap[k]
				require.True(t, ok)
				require.Equal(t, v, actVpc)
			}
		})
	}
}

func TestVPCService_Cleanup(t *testing.T) {
	vpcCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{})
	resourceStore := common.ResourceStore{
		Indexer:     vpcCacheIndexer,
		BindingType: model.VpcBindingType(),
	}

	mockCtrl := gomock.NewController(t)
	mockVpcclient := mocks.NewMockVpcsClient(mockCtrl)

	vpcService := &VPCService{
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					UseAVILoadBalancer: false,
				},
			},
			NSXClient: &nsx.Client{
				Cluster:   &nsx.Cluster{},
				VPCClient: mockVpcclient,
				NsxConfig: &config.NSXOperatorConfig{
					DefaultConfig: nil,
					CoeConfig: &config.CoeConfig{
						Cluster:          "",
						EnableVPCNetwork: false,
					},
					NsxConfig: nil,
					K8sConfig: nil,
					VCConfig:  nil,
					HAConfig:  nil,
					LibMode:   false,
				},
			},
		},

		LbsStore: &LBSStore{ResourceStore: common.ResourceStore{
			Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
			BindingType: model.LBServiceBindingType(),
		}},
		VpcStore: &VPCStore{resourceStore},
	}

	fakeVPCID := "fakeID"
	vpcPath := "/orgs/default/projects/default/vpcs/vpc2"
	err := vpcService.VpcStore.Add(&model.Vpc{Id: &fakeVPCID, Path: &vpcPath})
	assert.NoError(t, err)

	mockCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	patches := gomonkey.ApplyFunc(httpGetAviPortsPaths, func(cluster *nsx.Cluster, vpcPath string) (sets.Set[string], error) {
		return nil, nil
	})
	patches.ApplyMethodSeq(reflect.TypeOf(vpcService.NSXClient.VPCClient), "Delete", []gomonkey.OutputCell{{
		Values: gomonkey.Params{
			nil,
		},
		Times: 1,
	}})

	patches.ApplyMethod(reflect.TypeOf(&common.Service{}), "SearchResource", func(_ *common.Service, _ string, _ string, store common.Store, _ common.Filter) (uint64, error) {
		return 0, nil
	})

	defer patches.Reset()

	err = vpcService.Cleanup(mockCtx)

	assert.NoError(t, err)
}

func createFakeVPCService(t *testing.T, objs []client.Object) *VPCService {
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))
	fakeClient := fake.NewClientBuilder().WithScheme(newScheme).WithObjects(objs...).Build()

	mockCtrl := gomock.NewController(t)
	mockVpcClient := mocks.NewMockVpcsClient(mockCtrl)

	vpcStore := &VPCStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.VpcBindingType(),
	}}

	service := &VPCService{
		Service: common.Service{
			Client: fakeClient,
			NSXClient: &nsx.Client{
				VPCClient: mockVpcClient,
			},
			NSXConfig: &config.NSXOperatorConfig{
				DefaultConfig: nil,
				CoeConfig: &config.CoeConfig{
					Cluster:          "fakeCluster",
					EnableVPCNetwork: false,
				},
			},
		},
	}
	service.VpcStore = vpcStore
	return service
}

func TestVPCService_CreateOrUpdateVPC(t *testing.T) {
	fakeVPCID := "fakeVPCID"
	testCases := []struct {
		name                         string
		exitingNamespace             *v1.Namespace
		existingNetworkInfo          *v1alpha1.NetworkInfo
		existingVPCNetworkConfigInfo *common.VPCNetworkConfigInfo
		lbProvider                   LBProvider
		prepareFunc                  func(service *VPCService) *gomonkey.Patches
		expectErrStr                 string
		expectVPCModel               *model.Vpc
	}{
		{
			name: "Unable to get Namespace",
			existingNetworkInfo: &v1alpha1.NetworkInfo{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Namespace: "testNamespace"},
				VPCs:       nil,
			},
			expectErrStr: "namespaces \"testNamespace\" not found",
		},
		{
			name: "IsPreCreatedVPC",
			existingNetworkInfo: &v1alpha1.NetworkInfo{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Namespace: "testNamespace"},
				VPCs:       nil,
			},
			exitingNamespace: &v1.Namespace{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "testNamespace"},
				Spec:       v1.NamespaceSpec{},
				Status:     v1.NamespaceStatus{},
			},
			existingVPCNetworkConfigInfo: &common.VPCNetworkConfigInfo{
				Name:    "fakeVPCNetworkConfigInfo",
				VPCPath: "/orgs/default/projects/default/vpcs/vpc3",
			},
			prepareFunc: func(vpcService *VPCService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(reflect.TypeOf(vpcService.NSXClient.VPCClient), "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						model.Vpc{Id: &fakeVPCID}, nil,
					},
					Times: 1,
				}})
				return patches
			},
			expectErrStr:   "",
			expectVPCModel: &model.Vpc{Id: &fakeVPCID},
		},
		{
			name: "IsPreCreatedVPC with invalid VPC path",
			existingNetworkInfo: &v1alpha1.NetworkInfo{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Namespace: "testNamespace"},
				VPCs:       nil,
			},
			exitingNamespace: &v1.Namespace{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "testNamespace"},
				Spec:       v1.NamespaceSpec{},
				Status:     v1.NamespaceStatus{},
			},
			existingVPCNetworkConfigInfo: &common.VPCNetworkConfigInfo{
				Name:    "fakeVPCNetworkConfigInfo",
				VPCPath: "vpc3",
			},
			prepareFunc: func(vpcService *VPCService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(reflect.TypeOf(vpcService.NSXClient.VPCClient), "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						model.Vpc{Id: &fakeVPCID}, nil,
					},
					Times: 1,
				}})
				return patches
			},
			expectErrStr: "invalid path",
		},
		{
			name: "IsPreCreatedVPC and GetVPCFromNSXByPath error",
			existingNetworkInfo: &v1alpha1.NetworkInfo{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Namespace: "testNamespace"},
				VPCs:       nil,
			},
			exitingNamespace: &v1.Namespace{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "testNamespace"},
				Spec:       v1.NamespaceSpec{},
				Status:     v1.NamespaceStatus{},
			},
			existingVPCNetworkConfigInfo: &common.VPCNetworkConfigInfo{
				Name:    "fakeVPCNetworkConfigInfo",
				VPCPath: "/orgs/default/projects/default/vpcs/vpc3",
			},
			prepareFunc: func(vpcService *VPCService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(reflect.TypeOf(vpcService.NSXClient.VPCClient), "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						nil, errors.New("get VPC error"),
					},
					Times: 1,
				}})
				return patches
			},
			expectErrStr: "get VPC error",
		},
		{
			name: "Check IsSharedVPCNamespaceByNS error",
			existingNetworkInfo: &v1alpha1.NetworkInfo{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Namespace: "testNamespace"},
				VPCs:       nil,
			},
			exitingNamespace: &v1.Namespace{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "testNamespace"},
				Spec:       v1.NamespaceSpec{},
				Status:     v1.NamespaceStatus{},
			},
			prepareFunc: func(vpcService *VPCService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(vpcService), "IsSharedVPCNamespaceByNS", func(_ *VPCService, ctx context.Context, _ string) (bool, error) {
					return false, errors.New("check IsSharedVPCNamespaceByNS error")
				})
				return patches
			},
			expectErrStr: "check IsSharedVPCNamespaceByNS error",
		},
		{
			name: "Shared Namespace and VPC existing",
			existingNetworkInfo: &v1alpha1.NetworkInfo{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Namespace: "testNamespace"},
				VPCs:       nil,
			},
			exitingNamespace: &v1.Namespace{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "testNamespace"},
				Spec:       v1.NamespaceSpec{},
				Status:     v1.NamespaceStatus{},
			},
			prepareFunc: func(vpcService *VPCService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(vpcService), "IsSharedVPCNamespaceByNS", func(_ *VPCService, ctx context.Context, _ string) (bool, error) {
					return true, nil
				})
				patches.ApplyMethod(reflect.TypeOf(vpcService), "GetCurrentVPCsByNamespace", func(_ *VPCService, ctx context.Context, _ string) []*model.Vpc {
					vpcPath := "/vpc/1"
					return []*model.Vpc{{Path: &vpcPath, Id: &fakeVPCID}}
				})
				return patches
			},
			expectVPCModel: &model.Vpc{Id: &fakeVPCID},
		},
		{
			name: "is Shared Namespace but VPC not existing",
			existingNetworkInfo: &v1alpha1.NetworkInfo{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Namespace: "testNamespace"},
				VPCs:       nil,
			},
			exitingNamespace: &v1.Namespace{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "testNamespace"},
				Spec:       v1.NamespaceSpec{},
				Status:     v1.NamespaceStatus{},
			},
			prepareFunc: func(vpcService *VPCService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(vpcService), "IsSharedVPCNamespaceByNS", func(_ *VPCService, ctx context.Context, _ string) (bool, error) {
					return true, nil
				})
				patches.ApplyMethod(reflect.TypeOf(vpcService), "GetCurrentVPCsByNamespace", func(_ *VPCService, ctx context.Context, _ string) []*model.Vpc {
					return []*model.Vpc{}
				})
				return patches
			},
			expectErrStr: "the shared VPC is not created yet",
		},
		{
			name: "is not Shared Namespace should update the VPC",
			existingNetworkInfo: &v1alpha1.NetworkInfo{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Namespace: "testNamespace"},
				VPCs:       nil,
			},
			exitingNamespace: &v1.Namespace{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "testNamespace"},
				Spec:       v1.NamespaceSpec{},
				Status:     v1.NamespaceStatus{},
			},
			existingVPCNetworkConfigInfo: &common.VPCNetworkConfigInfo{
				IsDefault:         false,
				Name:              "fakeVPCNetworkConfigInfo",
				NSXProject:        "",
				PrivateIPs:        nil,
				DefaultSubnetSize: 0,
				VPCPath:           "",
			},
			prepareFunc: func(vpcService *VPCService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(vpcService), "IsSharedVPCNamespaceByNS", func(_ *VPCService, ctx context.Context, _ string) (bool, error) {
					return false, nil
				})
				patches.ApplyMethod(reflect.TypeOf(vpcService), "GetCurrentVPCsByNamespace", func(_ *VPCService, ctx context.Context, _ string) []*model.Vpc {
					vpcPath := "/vpc/1"
					return []*model.Vpc{
						{Path: &vpcPath, Id: &fakeVPCID},
					}
				})
				return patches
			},
			expectVPCModel: &model.Vpc{Id: &fakeVPCID},
		},
		{
			name: "is not Shared Namespace should create new VPC",
			existingNetworkInfo: &v1alpha1.NetworkInfo{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Namespace: "testNamespace"},
				VPCs:       nil,
			},
			exitingNamespace: &v1.Namespace{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "testNamespace"},
				Spec:       v1.NamespaceSpec{},
				Status:     v1.NamespaceStatus{},
			},
			existingVPCNetworkConfigInfo: &common.VPCNetworkConfigInfo{
				IsDefault:         false,
				Name:              "fakeVPCNetworkConfigInfo",
				NSXProject:        "",
				PrivateIPs:        nil,
				DefaultSubnetSize: 0,
				VPCPath:           "",
			},
			prepareFunc: func(vpcService *VPCService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(vpcService), "IsSharedVPCNamespaceByNS", func(_ *VPCService, ctx context.Context, _ string) (bool, error) {
					return false, nil
				})
				patches.ApplyMethod(reflect.TypeOf(vpcService), "GetCurrentVPCsByNamespace", func(_ *VPCService, ctx context.Context, _ string) []*model.Vpc {
					return []*model.Vpc{}
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(vpcService), "createNSXVPC", func(_ *VPCService, createdVpc *model.Vpc, nc *common.VPCNetworkConfigInfo, orgRoot *model.OrgRoot) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(vpcService), "checkVPCRealizationState", func(_ *VPCService, createdVpc *model.Vpc, newVpcPath string) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(vpcService), "checkLBSRealization", func(_ *VPCService, createdLBS *model.LBService, createdVpc *model.Vpc, nc *common.VPCNetworkConfigInfo, newVpcPath string) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(vpcService), "checkVpcAttachmentRealization", func(_ *VPCService, createdAttachment *model.VpcAttachment, createdVpc *model.Vpc, nc *common.VPCNetworkConfigInfo, newVpcPath string) error {
					return nil
				})
				vpcPath := "/vpc/1"
				patches.ApplyMethodSeq(reflect.TypeOf(vpcService.NSXClient.VPCClient), "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{
						model.Vpc{Id: &fakeVPCID, Path: &vpcPath}, nil,
					},
					Times: 1,
				}})
				return patches
			},
			expectVPCModel: &model.Vpc{Id: &fakeVPCID},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var objs []client.Object
			if tc.exitingNamespace != nil {
				objs = append(objs, tc.exitingNamespace)
			}
			service := createFakeVPCService(t, objs)
			ctx := context.Background()

			if tc.prepareFunc != nil {
				patches := tc.prepareFunc(service)
				defer patches.Reset()
			}

			newVPCModel, err := service.CreateOrUpdateVPC(ctx, tc.existingNetworkInfo, tc.existingVPCNetworkConfigInfo, tc.lbProvider)

			if tc.expectErrStr != "" {
				assert.ErrorContains(t, err, tc.expectErrStr)
			} else {
				assert.NoError(t, err)
			}

			if tc.expectVPCModel != nil {
				assert.Equal(t, *tc.expectVPCModel.Id, *newVPCModel.Id)
			}
		})
	}
}

type fakeOrgRootClient struct{}

func (f fakeOrgRootClient) Get(basePathParam *string, filterParam *string, typeFilterParam *string) (model.OrgRoot, error) {
	return model.OrgRoot{}, nil
}

func (f fakeOrgRootClient) Patch(orgRootParam model.OrgRoot, enforceRevisionCheckParam *bool) error {
	return nil
}

type fakeRealizedEntitiesClient struct{}

func (f fakeRealizedEntitiesClient) List(intentPathParam string, sitePathParam *string) (model.GenericPolicyRealizedResourceListResult, error) {
	state := model.GenericPolicyRealizedResource_STATE_REALIZED
	return model.GenericPolicyRealizedResourceListResult{Results: []model.GenericPolicyRealizedResource{{State: &state}}}, nil
}

func TestInitializeVPC(t *testing.T) {
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))
	fakeClient := fake.NewClientBuilder().WithScheme(newScheme).Build()
	commonService := common.Service{
		Client: fakeClient,
		NSXClient: &nsx.Client{
			OrgRootClient:          &fakeOrgRootClient{},
			QueryClient:            &fakeQueryClient{},
			RealizedEntitiesClient: &fakeRealizedEntitiesClient{},
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
	}
	testCases := []struct {
		name                string
		prepareFunc         func() *gomonkey.Patches
		expectVPCGetByIndex int
		expectAllVPCNum     int
		searchKey           string
	}{
		{
			name: "InitializeVPC with NSX VPC",
			prepareFunc: func() *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(&fakeQueryClient{}), "List", func(_ *fakeQueryClient, _ string, _ *string, _ *string, _ *int64, _ *bool, _ *string) (model.SearchResponse, error) {
					cursor := "1"
					resultCount := int64(1)
					return model.SearchResponse{
						Results: []*data.StructValue{data.NewStructValue("",
							map[string]data.DataValue{
								"resource_type":     data.NewStringValue("Vpc"),
								"id":                data.NewStringValue("vpc1"),
								"path":              data.NewStringValue("/orgs/default/projects/default/vpcs/vpc1"),
								"connectivity_path": data.NewStringValue("/orgs/default/projects/project-quality/vpc-connectivity-profiles/default"),
							})},
						Cursor: &cursor, ResultCount: &resultCount,
					}, nil
				})
				return patches
			},
			expectVPCGetByIndex: 0,
			expectAllVPCNum:     1,
			searchKey:           "",
		},
	}

	for _, tc := range testCases {
		if tc.prepareFunc != nil {
			patches := tc.prepareFunc()
			defer patches.Reset()
		}
		service, err := InitializeVPC(commonService)
		assert.NoError(t, err)
		res := service.GetVPCsByNamespace(tc.searchKey)
		assert.Equal(t, tc.expectVPCGetByIndex, len(res))
		allVPCs := service.ListVPC()
		assert.Equal(t, tc.expectAllVPCNum, len(allVPCs))
	}
}

func TestGetNSXLBSNATIP(t *testing.T) {
	vpcService := &VPCService{
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					UseAVILoadBalancer: false,
					UseNSXLoadBalancer: ptr.To(true),
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

	vpc1 := model.Vpc{
		DisplayName: &vpcName1,
		Id:          &vpcID1,
		Path:        ptr.To("/orgs/default/projects/project-quality/vpcs/ns-vpc-uid-1"),
	}

	testCases := []struct {
		name         string
		vpc          model.Vpc
		prepareFuncs func() *gomonkey.Patches
		wantObj      string
		wantErr      string
	}{
		{
			name:    "Test normal case",
			vpc:     vpc1,
			wantObj: "100.64.0.3",
			prepareFuncs: func() *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*realizestate.RealizeStateService).GetPolicyTier1UplinkPortIP,
					func(_ *realizestate.RealizeStateService, _ string) (string, error) {
						return "100.64.0.3", nil
					})
				return patches
			},
		},
		{
			name: "nsx lb uplink port IP not found error",
			vpc:  vpc1,
			prepareFuncs: func() *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*realizestate.RealizeStateService).GetPolicyTier1UplinkPortIP,
					func(_ *realizestate.RealizeStateService, _ string) (string, error) {
						return "", fmt.Errorf("fake-vpc tier1 uplink port IP not found")
					})
				return patches
			},
			wantErr: "tier1 uplink port IP not found",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.prepareFuncs != nil {
				patches := testCase.prepareFuncs()
				defer patches.Reset()
			}

			got, err := vpcService.GetNSXLBSNATIP(testCase.vpc)
			if testCase.wantErr != "" {
				assert.ErrorContains(t, err, testCase.wantErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, testCase.wantObj, got)
			}
		})
	}
}

func TestGetNamespacesWithPreCreatedVPCs(t *testing.T) {
	objs := []client.Object{
		&v1alpha1.VPCNetworkConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: "vpc-config-1"},
			Spec:       v1alpha1.VPCNetworkConfigurationSpec{VPC: "vpc-path"},
		},
		&v1alpha1.VPCNetworkConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: "vpc-config-2"},
		},
		&v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "ns-1",
				Annotations: map[string]string{common.AnnotationVPCNetworkConfig: "vpc-config-1"},
			},
		},
		&v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "ns-2",
				Annotations: map[string]string{common.AnnotationVPCNetworkConfig: "vpc-config-1"},
			},
		},
	}

	service := createFakeVPCService(t, objs)
	nsVpcMap, err := service.GetNamespacesWithPreCreatedVPCs()

	assert.Nil(t, err)
	assert.Equal(t, "vpc-path", nsVpcMap["ns-1"])
	assert.Equal(t, "vpc-path", nsVpcMap["ns-2"])
}
