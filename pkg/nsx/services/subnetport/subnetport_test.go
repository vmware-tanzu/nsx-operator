package subnetport

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	mpmodel "github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	mock_org_root "github.com/vmware-tanzu/nsx-operator/pkg/mock/orgrootclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/ipaddressallocation"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	subnetPortName       = "subnetport-name"
	subnetPortId1        = "subnetport-name_00000000-0000-0000-0000-000000000001"
	subnetPortPath1      = "/orgs/org1/projects/project1/vpcs/vpc1/subnets/subnet1/ports/subnetport-name_00000000-0000-0000-0000-000000000001"
	subnetPortId2        = "subnetport-name_00000000-0000-0000-0000-000000000002"
	subnetPortPath2      = "/orgs/org1/projects/project1/vpcs/vpc1/subnets/subnet1/ports/subnetport-name_00000000-0000-0000-0000-000000000002"
	subnetPortEntityType = "RealizedLogicalPort"
	stateRealized        = "REALIZED"
	subnetPath           = "/orgs/org1/projects/project1/vpcs/vpc1/subnets/subnet1"
	namespace            = "ns1"
)

type fakeQueryClient struct{}

func (c *fakeQueryClient) List(queryParam string, cursorParam *string, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.SearchResponse, error) {
	return model.SearchResponse{}, nil
}

type fakePortClient struct{}

func (c *fakePortClient) Patch(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, portIdParam string, vpcSubnetPortParam model.VpcSubnetPort) error {
	return nil
}
func (c *fakePortClient) Update(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, portIdParam string, vpcSubnetPortParam model.VpcSubnetPort) (model.VpcSubnetPort, error) {
	return model.VpcSubnetPort{}, nil
}
func (c *fakePortClient) List(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, cursorParam *string, includeMarkForDeleteObjectsParam *bool, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.VpcSubnetPortListResult, error) {
	return model.VpcSubnetPortListResult{}, nil
}
func (c *fakePortClient) Get(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, portIdParam string) (model.VpcSubnetPort, error) {
	return model.VpcSubnetPort{
		Id:   &subnetPortId1,
		Path: &subnetPortPath1,
	}, nil
}
func (c *fakePortClient) Delete(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, portIdParam string) error {
	return nil
}

type fakeRealizedEntitiesClient struct{}

func (c *fakeRealizedEntitiesClient) List(intentPathParam string, sitePathParam *string) (model.GenericPolicyRealizedResourceListResult, error) {

	return model.GenericPolicyRealizedResourceListResult{
		Results: []model.GenericPolicyRealizedResource{
			{
				EntityType: &subnetPortEntityType,
				State:      &stateRealized,
			},
		},
	}, nil
}

type fakePortStateClient struct{}

func (c *fakePortStateClient) Get(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, portIdParam string, enforcementPointPathParam *string, sourceParam *string) (model.SegmentPortState, error) {
	return model.SegmentPortState{
		RealizedBindings: []model.AddressBindingEntry{},
	}, nil
}

type fakeIPPoolClient struct{}

func (c *fakeIPPoolClient) Get(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, poolIdParam string) (model.IpAddressPool, error) {
	return model.IpAddressPool{}, nil
}

func (c *fakeIPPoolClient) List(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, cursorParam *string, includeMarkForDeleteObjectsParam *bool, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.IpAddressPoolListResult, error) {
	return model.IpAddressPoolListResult{}, nil
}

type mockErrorIPPoolClient struct {
	fakeIPPoolClient
	err error
}

func (c *mockErrorIPPoolClient) Get(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, poolIdParam string) (model.IpAddressPool, error) {
	return model.IpAddressPool{}, c.err
}

type mockFuncIPPoolClient struct {
	fakeIPPoolClient
	getFunc func(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, poolIdParam string) (model.IpAddressPool, error)
}

func (c *mockFuncIPPoolClient) Get(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, poolIdParam string) (model.IpAddressPool, error) {
	if c.getFunc != nil {
		return c.getFunc(orgIdParam, projectIdParam, vpcIdParam, subnetIdParam, poolIdParam)
	}
	return c.fakeIPPoolClient.Get(orgIdParam, projectIdParam, vpcIdParam, subnetIdParam, poolIdParam)
}

type fakeStatsClient struct{}

func (c *fakeStatsClient) Get(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, cursorParam *string, enforcementPointPathParam *string, includeMarkForDeleteObjectsParam *bool, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.DhcpServerStatistics, error) {
	return model.DhcpServerStatistics{}, nil
}

type fakeVifsClient struct{}

func (c *fakeVifsClient) List(cursorParam *string, hostIdParam *string, includedFieldsParam *string, lportAttachmentIdParam *string, ownerVmIdParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string, vmIdParam *string) (mpmodel.VirtualNetworkInterfaceListResult, error) {
	return mpmodel.VirtualNetworkInterfaceListResult{}, nil
}

func Test_InitializeSubnetPort(t *testing.T) {
	tests := []struct {
		name        string
		prepareFunc func(*testing.T, *common.Service, context.Context) *gomonkey.Patches
		wantErr     bool
	}{
		{
			name: "searchResourceError",
			prepareFunc: func(t *testing.T, s *common.Service, ctx context.Context) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(&fakeQueryClient{}), "List", func(_ *fakeQueryClient, _ string, _ *string, _ *string, _ *int64, _ *bool, _ *string) (model.SearchResponse, error) {
					return model.SearchResponse{}, fmt.Errorf("mock error")
				})
				return patches
			},
			wantErr: true,
		},
		{
			name: "success",
			prepareFunc: func(t *testing.T, s *common.Service, ctx context.Context) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(&fakeQueryClient{}), "List", func(_ *fakeQueryClient, _ string, _ *string, _ *string, _ *int64, _ *bool, _ *string) (model.SearchResponse, error) {
					return model.SearchResponse{}, nil
				})
				return patches
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.TODO()
			commonService := common.Service{
				Client: fake.NewClientBuilder().Build(),
				NSXClient: &nsx.Client{
					QueryClient: &fakeQueryClient{},
					NsxConfig: &config.NSXOperatorConfig{
						CoeConfig: &config.CoeConfig{
							Cluster: "k8scl-one:test",
						},
					},
				},
			}
			vpcService := &vpc.VPCService{}
			ipAddressAllocationService := &ipaddressallocation.IPAddressAllocationService{}
			patches := tt.prepareFunc(t, &commonService, ctx)
			defer patches.Reset()
			got, err := InitializeSubnetPort(commonService, vpcService, ipAddressAllocationService)
			if (err != nil) != tt.wantErr {
				t.Errorf("InitializeSubnetPort() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got.Service, commonService) {
				t.Errorf("InitializeSubnetPort() got = %v, want %v", got.Service, commonService)
			}
		})
	}
}

func TestSubnetPortService_CreateOrUpdateSubnetPort(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	orgRootClient := mock_org_root.NewMockOrgRootClient(mockCtl)
	nsxClient := &nsx.Client{
		QueryClient:            &fakeQueryClient{},
		PortClient:             &fakePortClient{},
		RealizedEntitiesClient: &fakeRealizedEntitiesClient{},
		PortStateClient:        &fakePortStateClient{},
		OrgRootClient:          orgRootClient,
		Cluster:                &nsx.Cluster{},
		NsxConfig: &config.NSXOperatorConfig{
			CoeConfig: &config.CoeConfig{
				Cluster: "k8scl-one:test",
			},
		},
	}
	commonService := common.Service{
		Client:    k8sClient,
		NSXClient: nsxClient,
		NSXConfig: &config.NSXOperatorConfig{
			CoeConfig: &config.CoeConfig{
				Cluster: "k8scl-one:test",
			},
		},
	}
	builder, _ := common.PolicyPathVpcSubnetPort.NewPolicyTreeBuilder()
	service := &SubnetPortService{
		Service: commonService,
		SubnetPortStore: &SubnetPortStore{ResourceStore: common.ResourceStore{
			Indexer: cache.NewIndexer(
				keyFunc,
				cache.Indexers{
					common.TagScopeSubnetPortCRUID: subnetPortIndexByCRUID,
					common.TagScopePodUID:          subnetPortIndexByPodUID,
				}),
			BindingType: model.VpcSubnetPortBindingType(),
		}},
		builder: builder,
	}

	subnetPortCR := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      subnetPortName,
			Namespace: namespace,
			UID:       "00000000-0000-0000-0000-000000000001",
		},
	}

	nsxSubnet1 := &model.VpcSubnet{
		Path: &subnetPath,
		SubnetDhcpConfig: &model.SubnetDhcpConfig{
			Mode: common.String("DHCP_SERVER"),
		},
	}

	nsxSubnet2 := &model.VpcSubnet{
		Path: &subnetPath,
		SubnetDhcpConfig: &model.SubnetDhcpConfig{
			Mode: common.String("DHCP_DEACTIVATED"),
		},
	}

	nsxSubnetPort := model.VpcSubnetPort{
		Id:          &subnetPortId1,
		Path:        &subnetPortPath1,
		DisplayName: &subnetPortId1,
		Attachment:  &model.PortAttachment{},
		Tags: []model.Tag{
			{
				Scope: common.String(common.TagScopeSubnetPortCRUID),
				Tag:   common.String(string(subnetPortCR.UID)),
			},
		},
	}

	tests := []struct {
		name        string
		prepareFunc func(service *SubnetPortService) *gomonkey.Patches
		wantErr     bool
		nsxSubnet   *model.VpcSubnet
		obj         interface{}
		restore     bool
	}{
		{
			name: "CreateDHCPServer",
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					namespaceCR := &corev1.Namespace{}
					namespaceCR.UID = "ns1"
					return nil
				})
				orgRootClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				patches := gomonkey.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion", func(_ *nsx.Client, _ int) bool {
					return false
				})
				return patches
			},
			wantErr:   false,
			nsxSubnet: nsxSubnet1,
			obj:       subnetPortCR,
		},
		{
			name: "CreateDHCPDeactivated",
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					namespaceCR := &corev1.Namespace{}
					namespaceCR.UID = "ns1"
					return nil
				})
				orgRootClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				patches := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.PortStateClient), "Get", func(c *fakePortStateClient, orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, portIdParam string, enforcementPointPathParam *string, sourceParam *string) (model.SegmentPortState, error) {
					return model.SegmentPortState{
						RealizedBindings: []model.AddressBindingEntry{{Binding: &model.PacketAddressClassifier{IpAddress: common.String("10.0.0.1")}}},
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion", func(_ *nsx.Client, _ int) bool {
					return false
				})
				return patches
			},
			wantErr:   false,
			nsxSubnet: nsxSubnet2,
			obj:       subnetPortCR,
		},
		{
			name: "UpdateAddAddressBindings",
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					namespaceCR := &corev1.Namespace{}
					namespaceCR.UID = "ns1"
					return nil
				})
				service.SubnetPortStore.Add(&nsxSubnetPort)
				orgRootClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				patches := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.PortStateClient), "Get", func(c *fakePortStateClient, orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, portIdParam string, enforcementPointPathParam *string, sourceParam *string) (model.SegmentPortState, error) {
					return model.SegmentPortState{
						RealizedBindings: []model.AddressBindingEntry{{Binding: &model.PacketAddressClassifier{IpAddress: common.String("10.0.0.1")}}},
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion", func(_ *nsx.Client, _ int) bool {
					return false
				})
				return patches
			},
			wantErr:   false,
			nsxSubnet: nsxSubnet2,
			obj: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      subnetPortName,
					Namespace: namespace,
					UID:       "00000000-0000-0000-0000-000000000001",
				},
				Spec: v1alpha1.SubnetPortSpec{
					AddressBindings: []v1alpha1.PortAddressBinding{
						{IPAddress: "10.0.0.10", MACAddress: "00:11:22:33:44:55"},
						{IPAddress: "10.0.0.11", MACAddress: "00:11:22:33:44:55"},
					},
				},
			},
		},
		{
			name: "UpdateRemoveAddressBindings",
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					namespaceCR := &corev1.Namespace{}
					namespaceCR.UID = "ns1"
					return nil
				})
				service.SubnetPortStore.Add(&nsxSubnetPort)
				orgRootClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				patches := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.PortStateClient), "Get", func(c *fakePortStateClient, orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, portIdParam string, enforcementPointPathParam *string, sourceParam *string) (model.SegmentPortState, error) {
					return model.SegmentPortState{
						RealizedBindings: []model.AddressBindingEntry{{Binding: &model.PacketAddressClassifier{IpAddress: common.String("10.0.0.1")}}},
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion", func(_ *nsx.Client, _ int) bool {
					return false
				})
				return patches
			},
			wantErr:   false,
			nsxSubnet: nsxSubnet2,
			obj: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      subnetPortName,
					Namespace: namespace,
					UID:       "00000000-0000-0000-0000-000000000001",
				},
				Spec: v1alpha1.SubnetPortSpec{
					AddressBindings: []v1alpha1.PortAddressBinding{},
				},
			},
		},
		{
			name: "Update",
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					namespaceCR := &corev1.Namespace{}
					namespaceCR.UID = "ns1"
					return nil
				})
				service.SubnetPortStore.Add(&nsxSubnetPort)
				orgRootClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				patches := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.PortStateClient), "Get", func(c *fakePortStateClient, orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, portIdParam string, enforcementPointPathParam *string, sourceParam *string) (model.SegmentPortState, error) {
					return model.SegmentPortState{
						RealizedBindings: []model.AddressBindingEntry{{Binding: &model.PacketAddressClassifier{IpAddress: common.String("10.0.0.1")}}},
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion", func(_ *nsx.Client, _ int) bool {
					return false
				})
				return patches
			},
			wantErr:   false,
			nsxSubnet: nsxSubnet2,
			obj:       subnetPortCR,
		},
		{
			name: "RestorePod",
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					namespaceCR := &corev1.Namespace{}
					namespaceCR.UID = "ns1"
					return nil
				})
				orgRootClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				patches := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.PortStateClient), "Get", func(c *fakePortStateClient, orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, portIdParam string, enforcementPointPathParam *string, sourceParam *string) (model.SegmentPortState, error) {
					return model.SegmentPortState{
						RealizedBindings: []model.AddressBindingEntry{{Binding: &model.PacketAddressClassifier{IpAddress: common.String("10.0.0.1")}}},
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion", func(_ *nsx.Client, _ int) bool {
					return true
				})
				return patches
			},
			wantErr:   false,
			nsxSubnet: nsxSubnet2,
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-1",
					Namespace: "ns-1",
					Annotations: map[string]string{
						common.AnnotationPodMAC:     "aa:bb:cc:dd:ee:ff",
						common.AnnotationAttachment: "attachment-id",
					},
					UID: "00000000-0000-0000-0000-000000000002",
				},
			},
			restore: true,
		},
		{
			name: "RealizeFailure",
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					namespaceCR := &corev1.Namespace{}
					namespaceCR.UID = "ns1"
					return nil
				})

				patches := gomonkey.ApplyMethodSeq(service.NSXClient.RealizedEntitiesClient, "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.GenericPolicyRealizedResourceListResult{}, nsxutil.NewRealizeStateError("realized state error", 0)},
					Times:  1,
				}})
				patches.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion", func(_ *nsx.Client, _ int) bool {
					return false
				})
				return patches
			},
			wantErr:   true,
			nsxSubnet: nsxSubnet1,
			obj:       subnetPortCR,
		},
		{
			name: "NoRealizeFailure",
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					namespaceCR := &corev1.Namespace{}
					namespaceCR.UID = "ns1"
					return nil
				})
				orgRootClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

				patches := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.RealizedEntitiesClient), "List", func(_ *fakeRealizedEntitiesClient, intentPathParam string, sitePathParam *string) (model.GenericPolicyRealizedResourceListResult, error) {
					return model.GenericPolicyRealizedResourceListResult{}, fmt.Errorf("failed to check realized state")
				})
				// Mock NSXTRealizeRetry with shorter backoff: 2 retries, 50ms interval
				patches.ApplyGlobalVar(&util.NSXTRealizeRetry, wait.Backoff{
					Steps:    2,
					Duration: 50 * time.Millisecond,
					Factor:   1.0,
					Jitter:   0.0,
				})
				patches.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion", func(_ *nsx.Client, _ int) bool {
					return false
				})
				return patches
			},
			wantErr:   true,
			nsxSubnet: nsxSubnet1,
			obj:       subnetPortCR,
		},
		{
			name: "IPExhaustedRealizeFailure",
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					namespaceCR := &corev1.Namespace{}
					namespaceCR.UID = "ns1"
					return nil
				})

				patches := gomonkey.ApplyMethodSeq(service.NSXClient.RealizedEntitiesClient, "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.GenericPolicyRealizedResourceListResult{}, nsxutil.NewRealizeStateError("realized state error", nsxutil.IPAllocationErrorCode)},
					Times:  1,
				}})
				patches.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion", func(_ *nsx.Client, _ int) bool {
					return false
				})
				return patches
			},
			wantErr:   true,
			nsxSubnet: nsxSubnet1,
			obj:       subnetPortCR,
		},
		{
			name: "CreateFailure",
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					namespaceCR := &corev1.Namespace{}
					namespaceCR.UID = "ns1"
					return nil
				})
				patches := gomonkey.ApplyMethodSeq(service.NSXClient.PortClient, "Patch", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("mock error")},
					Times:  1,
				}})
				patches.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion", func(_ *nsx.Client, _ int) bool {
					return false
				})
				return patches
			},
			wantErr:   true,
			nsxSubnet: nsxSubnet1,
			obj:       subnetPortCR,
		},
		{
			name: "GetFailure",
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					namespaceCR := &corev1.Namespace{}
					namespaceCR.UID = "ns1"
					return nil
				})
				patches := gomonkey.ApplyMethodSeq(service.NSXClient.PortClient, "Get", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.VpcSubnetPort{}, fmt.Errorf("mock error")},
					Times:  1,
				}})
				patches.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion", func(_ *nsx.Client, _ int) bool {
					return false
				})
				return patches
			},
			wantErr:   true,
			nsxSubnet: nsxSubnet1,
			obj:       subnetPortCR,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := tt.prepareFunc(service)
			if patches != nil {
				defer patches.Reset()
			}
			_, err := service.CreateOrUpdateSubnetPort(tt.obj, tt.nsxSubnet, "", nil, false, tt.restore, "")
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateOrUpdateSubnetPort() error = %v, wantErr %v", err, tt.wantErr)
			}
			err = service.CleanupBeforeVPCDeletion(context.TODO())
			assert.Nil(t, err)
		})
	}

}

func TestSubnetPortService_DeleteSubnetPort(t *testing.T) {
	tests := []struct {
		name        string
		prepareFunc func(service *SubnetPortService) *gomonkey.Patches
		wantErr     bool
	}{
		{
			name: "Success",
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				service.SubnetPortStore.Add(&model.VpcSubnetPort{
					Id:   &subnetPortId1,
					Path: &subnetPortPath1,
				})
				patches := gomonkey.ApplyMethodSeq(service.NSXClient.PortClient, "Delete", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				return patches
			},
			wantErr: false,
		},
		{
			name:        "DeleteNonExisted",
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches { return nil },
			wantErr:     false,
		},
		{
			name: "DeleteFailure",
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				service.SubnetPortStore.Add(&model.VpcSubnetPort{
					Id:   &subnetPortId1,
					Path: &subnetPortPath1,
				})
				patches := gomonkey.ApplyMethodSeq(service.NSXClient.PortClient, "Delete", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("mock error")},
					Times:  1,
				}})
				return patches
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commonService := common.Service{
				NSXClient: &nsx.Client{
					PortClient: &fakePortClient{},
				},
			}
			service := &SubnetPortService{
				Service: commonService,
				SubnetPortStore: &SubnetPortStore{ResourceStore: common.ResourceStore{
					Indexer: cache.NewIndexer(
						keyFunc,
						cache.Indexers{
							common.TagScopeSubnetPortCRUID: subnetPortIndexByCRUID,
							common.TagScopePodUID:          subnetPortIndexByPodUID,
						}),
					BindingType: model.VpcSubnetPortBindingType(),
				}},
			}

			patches := tt.prepareFunc(service)
			if patches != nil {
				defer patches.Reset()
			}

			if err := service.DeleteSubnetPortById(subnetPortId1); (err != nil) != tt.wantErr {
				t.Errorf("DeleteSubnetPort() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSubnetPortService_GetSubnetPathForSubnetPortFromStore(t *testing.T) {
	crUID := types.UID("aaaaaaaa")
	type args struct {
		obj *model.VpcSubnetPort
	}
	tests := []struct {
		name           string
		args           args
		expectedResult string
	}{
		{
			name: "Success for SubnetPort created by Pod",
			args: args{&model.VpcSubnetPort{
				Id:         &subnetPortId1,
				Path:       &subnetPortPath1,
				ParentPath: &subnetPath,
				Tags: []model.Tag{
					{
						Scope: common.String(common.TagScopePodUID),
						Tag:   common.String(string(crUID)),
					},
				},
			}},
			expectedResult: subnetPath,
		},
		{
			name: "Success for SubnetPort created by SubnetPort",
			args: args{&model.VpcSubnetPort{
				Id:         &subnetPortId1,
				Path:       &subnetPortPath1,
				ParentPath: &subnetPath,
				Tags: []model.Tag{
					{
						Scope: common.String(common.TagScopeSubnetPortCRUID),
						Tag:   common.String(string(crUID)),
					},
				},
			}},
			expectedResult: subnetPath,
		},
		{
			name:           "NonExisted",
			args:           args{},
			expectedResult: "",
		},
		{
			name: "NonParentPath",
			args: args{&model.VpcSubnetPort{
				Id:   &subnetPortId1,
				Path: &subnetPortPath1,
			}},
			expectedResult: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &SubnetPortService{
				SubnetPortStore: &SubnetPortStore{ResourceStore: common.ResourceStore{
					Indexer: cache.NewIndexer(
						keyFunc,
						cache.Indexers{
							common.TagScopeSubnetPortCRUID: subnetPortIndexByCRUID,
							common.TagScopePodUID:          subnetPortIndexByPodUID,
						}),
					BindingType: model.VpcSubnetPortBindingType(),
				}},
			}

			if tt.args.obj != nil {
				service.SubnetPortStore.Add(tt.args.obj)
				defer service.SubnetPortStore.Delete(tt.args.obj)
			}

			result := service.GetSubnetPathForSubnetPortFromStore(crUID)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestSubnetPortService_GetPortsOfSubnet(t *testing.T) {
	port := model.VpcSubnetPort{
		Id:         &subnetPortId1,
		Path:       &subnetPortPath1,
		ParentPath: &subnetPath,
	}
	service := &SubnetPortService{
		SubnetPortStore: &SubnetPortStore{ResourceStore: common.ResourceStore{
			Indexer: cache.NewIndexer(
				keyFunc,
				cache.Indexers{
					common.IndexKeySubnetPath: subnetPortIndexBySubnetPath,
				}),
			BindingType: model.VpcSubnetPortBindingType(),
		}},
	}
	service.SubnetPortStore.Add(&port)
	ports := service.GetPortsOfSubnet(subnetPath)
	assert.Equal(t, port, *ports[0])
}

func TestSubnetPortService_Cleanup(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockOrgRootClient := mock_org_root.NewMockOrgRootClient(ctrl)
	mockOrgRootClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil)

	port := model.VpcSubnetPort{
		Id:         &subnetPortId1,
		Path:       &subnetPortPath1,
		ParentPath: &subnetPath,
	}
	builder, _ := common.PolicyPathVpcSubnetPort.NewPolicyTreeBuilder()
	service := &SubnetPortService{
		Service: common.Service{
			NSXClient: &nsx.Client{
				PortClient:    &fakePortClient{},
				OrgRootClient: mockOrgRootClient,
			},
		},
		SubnetPortStore: &SubnetPortStore{ResourceStore: common.ResourceStore{
			Indexer: cache.NewIndexer(
				keyFunc,
				cache.Indexers{
					common.IndexKeySubnetPath: subnetPortIndexBySubnetPath,
				}),
			BindingType: model.VpcSubnetPortBindingType(),
		}},
		builder: builder,
	}

	service.SubnetPortStore.Add(&port)
	err := service.CleanupBeforeVPCDeletion(context.TODO())
	assert.Nil(t, err)
	assert.Nil(t, service.SubnetPortStore.GetByKey(*port.Id))
}

func TestSubnetPortService_ListNSXSubnetPortIDForCR(t *testing.T) {
	subnetPortService := createSubnetPortService(t)
	crName := "fake_subnetport"
	crUUID := "2ccec3b9-7546-4fd2-812a-1e3a4afd7acc"
	subnetPortByCR := &model.VpcSubnetPort{
		DisplayName: common.String(crName),
		Id:          common.String(fmt.Sprintf("%s-%s", crName, crUUID)),
		Tags: []model.Tag{
			{
				Scope: common.String("nsx-op/cluster"),
				Tag:   common.String("fake_cluster"),
			},
			{
				Scope: common.String("nsx-op/version"),
				Tag:   common.String("1.0.0"),
			},
			{
				Scope: common.String("nsx-op/vm_namespace"),
				Tag:   common.String("fake_ns"),
			},
			{
				Scope: common.String("nsx-op/subnetport_name"),
				Tag:   common.String(crName),
			},
			{
				Scope: common.String("nsx-op/subnetport_uid"),
				Tag:   common.String(crUUID),
			},
		},
		Path:       common.String("/orgs/default/projects/default/vpcs/vpc1/subnets/subnet1/ports/ports/fake_subnetport-2ccec3b9-7546-4fd2-812a-1e3a4afd7acc"),
		ParentPath: common.String("/orgs/default/projects/default/vpcs/vpc1/subnets/subnet1"),
		Attachment: &model.PortAttachment{
			AllocateAddresses: common.String("DHCP"),
			Type_:             common.String(model.PortAttachment_TYPE_INDEPENDENT),
			Id:                common.String("66616b65-5f73-4562-ae65-74706f72742d"),
			TrafficTag:        common.Int64(0),
		},
	}
	subnetPortService.SubnetPortStore.Add(subnetPortByCR)
	subnetPortIDs := subnetPortService.ListNSXSubnetPortIDForCR()
	assert.Equal(t, 1, len(subnetPortIDs))
	assert.Equal(t, *subnetPortByCR.Id, subnetPortIDs.UnsortedList()[0])
}

func TestSubnetPortService_ListNSXSubnetPortIDForPod(t *testing.T) {
	subnetPortService := createSubnetPortService(t)
	podName := "fake_pod"
	podUUID := "c5db1800-ce4c-11de-a935-8105ba7ace78"
	subnetPortByPod := &model.VpcSubnetPort{
		DisplayName: common.String(podName),
		Id:          common.String(fmt.Sprintf("fake_pod-%s", podUUID)),
		Tags: []model.Tag{
			{
				Scope: common.String("nsx-op/cluster"),
				Tag:   common.String("fake_cluster"),
			},
			{
				Scope: common.String("nsx-op/version"),
				Tag:   common.String("1.0.0"),
			},
			{
				Scope: common.String("nsx-op/namespace"),
				Tag:   common.String("fake_ns"),
			},
			{
				Scope: common.String("nsx-op/pod_name"),
				Tag:   common.String(podName),
			},
			{
				Scope: common.String("nsx-op/pod_uid"),
				Tag:   common.String(podUUID),
			},
		},
		Path:       common.String("/orgs/default/projects/default/vpcs/vpc1/subnets/subnet1/ports/fake_pod-c5db1800-ce4c-11de-a935-8105ba7ace78"),
		ParentPath: common.String("/orgs/default/projects/default/vpcs/vpc1/subnets/subnet1"),
		Attachment: &model.PortAttachment{
			AllocateAddresses: common.String("DHCP"),
			Type_:             common.String(model.PortAttachment_TYPE_INDEPENDENT),
			Id:                common.String("66616b65-5f70-4f64-ad63-356462313830"),
			TrafficTag:        common.Int64(0),
			AppId:             common.String(podUUID),
			ContextId:         common.String("fake_context_id"),
		},
	}
	subnetPortService.SubnetPortStore.Add(subnetPortByPod)
	subnetPortIDs := subnetPortService.ListNSXSubnetPortIDForPod()
	assert.Equal(t, 1, len(subnetPortIDs))
	assert.Equal(t, *subnetPortByPod.Id, subnetPortIDs.UnsortedList()[0])
}

func TestSubnetPortService_ListSubnetPortIDsFromCRs(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	commonService := common.Service{
		Client: k8sClient,
	}
	service := &SubnetPortService{
		Service:         commonService,
		SubnetPortStore: &SubnetPortStore{},
	}
	subnetPortList := &v1alpha1.SubnetPortList{}
	// List failure
	mockedErr := fmt.Errorf("mock error")
	k8sClient.EXPECT().List(gomock.Any(), subnetPortList).Return(mockedErr)
	_, err := service.ListSubnetPortIDsFromCRs(context.TODO())
	assert.Equal(t, mockedErr, err)
	// Success
	k8sClient.EXPECT().List(gomock.Any(), subnetPortList).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		a := list.(*v1alpha1.SubnetPortList)
		a.Items = append(a.Items, []v1alpha1.SubnetPort{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "subnetPort1",
					UID:  "1",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "subnetPort2",
					UID:  "2",
				},
			},
		}...)
		return nil
	})
	patches := gomonkey.ApplyMethod(reflect.TypeOf(service.SubnetPortStore), "GetVpcSubnetPortByUID", func(subnetPortStore *SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
		switch string(uid) {
		case "1":
			return &model.VpcSubnetPort{
				Id: String("subnetPort1_1"),
			}, nil
		case "2":
			return &model.VpcSubnetPort{
				Id: String("subnetPort2_2"),
			}, nil
		default:
			return nil, nil
		}
	})
	defer patches.Reset()
	crSubnetPortIDsSet, err := service.ListSubnetPortIDsFromCRs(context.TODO())
	assert.Nil(t, err)
	assert.Equal(t, 2, crSubnetPortIDsSet.Len())
	assert.True(t, crSubnetPortIDsSet.Has("subnetPort1_1"))
	assert.True(t, crSubnetPortIDsSet.Has("subnetPort2_2"))
}

func TestSubnetPortService_ListSubnetPortByName(t *testing.T) {
	subnetPortService := createSubnetPortService(t)

	// VM subnet port with subnetport-1 name
	vmSubnetPort1 := &model.VpcSubnetPort{
		Id:         &subnetPortId1,
		Path:       &subnetPortPath1,
		ParentPath: &subnetPath,
		Tags: []model.Tag{
			{
				Scope: common.String(common.TagScopeVMNamespace),
				Tag:   common.String("ns-1"),
			},
			{
				Scope: common.String(common.TagScopeSubnetPortCRName),
				Tag:   common.String("subnetport-1"),
			},
		},
	}

	// VM subnet port with subnetport-2 name
	vmSubnetPort2 := &model.VpcSubnetPort{
		Id:         &subnetPortId2,
		Path:       &subnetPortPath2,
		ParentPath: &subnetPath,
		Tags: []model.Tag{
			{
				Scope: common.String(common.TagScopeVMNamespace),
				Tag:   common.String("ns-1"),
			},
			{
				Scope: common.String(common.TagScopeSubnetPortCRName),
				Tag:   common.String("subnetport-2"),
			},
		},
	}

	// Pod subnet port with subnetport-1 name
	podSubnetPortId := "subnetport-name_3"
	podSubnetPortPath := "/orgs/org1/projects/project1/vpcs/vpc1/subnets/subnet1/ports/subnetport-name_3"
	podSubnetPort := &model.VpcSubnetPort{
		Id:         &podSubnetPortId,
		Path:       &podSubnetPortPath,
		ParentPath: &subnetPath,
		Tags: []model.Tag{
			{
				Scope: common.String(common.TagScopeNamespace),
				Tag:   common.String("ns-1"),
			},
			{
				Scope: common.String(common.TagScopeSubnetPortCRName),
				Tag:   common.String("subnetport-1"),
			},
		},
	}

	// Add all subnet ports to the store
	subnetPortService.SubnetPortStore.Add(vmSubnetPort1)
	subnetPortService.SubnetPortStore.Add(vmSubnetPort2)
	subnetPortService.SubnetPortStore.Add(podSubnetPort)

	// Test 1: Get all subnet ports with name "subnetport-1" in namespace "ns-1"
	// Should return both VM and Pod subnet ports with that name
	subnetPorts := subnetPortService.ListSubnetPortByName("ns-1", "subnetport-1")
	assert.Equal(t, 2, len(subnetPorts))

	// Verify both VM and Pod subnet ports with name "subnetport-1" are returned
	foundVMPort := false
	foundPodPort := false
	for _, port := range subnetPorts {
		if *port.Id == *vmSubnetPort1.Id {
			foundVMPort = true
		}
		if *port.Id == *podSubnetPort.Id {
			foundPodPort = true
		}
	}
	assert.True(t, foundVMPort, "VM subnet port with name 'subnetport-1' should be returned")
	assert.True(t, foundPodPort, "Pod subnet port with name 'subnetport-1' should be returned")

	// Test 2: Get all subnet ports with name "subnetport-2" in namespace "ns-1"
	// Should return only the VM subnet port with that name
	subnetPorts = subnetPortService.ListSubnetPortByName("ns-1", "subnetport-2")
	assert.Equal(t, 1, len(subnetPorts))
	assert.Equal(t, vmSubnetPort2, subnetPorts[0])

	// Test 3: Get all subnet ports with non-existent name
	// Should return empty list
	subnetPorts = subnetPortService.ListSubnetPortByName("ns-1", "non-existent")
	assert.Equal(t, 0, len(subnetPorts))
}

func TestSubnetPortService_ListSubnetPortByPodName(t *testing.T) {
	subnetPortService := createSubnetPortService(t)
	subnetPort1 := &model.VpcSubnetPort{
		Id:         &subnetPortId1,
		Path:       &subnetPortPath1,
		ParentPath: &subnetPath,
		Tags: []model.Tag{
			{
				Scope: common.String("nsx-op/namespace"),
				Tag:   common.String("ns-1"),
			},
			{
				Scope: common.String("nsx-op/pod_name"),
				Tag:   common.String("pod-1"),
			},
		},
	}
	subnetPort2 := &model.VpcSubnetPort{
		Id:         &subnetPortId2,
		Path:       &subnetPortPath2,
		ParentPath: &subnetPath,
		Tags: []model.Tag{
			{
				Scope: common.String("nsx-op/namespace"),
				Tag:   common.String("ns-1"),
			},
			{
				Scope: common.String("nsx-op/pod_name"),
				Tag:   common.String("pod-2"),
			},
		},
	}
	subnetPortService.SubnetPortStore.Add(subnetPort1)
	subnetPortService.SubnetPortStore.Add(subnetPort2)
	subnetPorts := subnetPortService.ListSubnetPortByPodName("ns-1", "pod-2")
	assert.Equal(t, 1, len(subnetPorts))
	assert.Equal(t, subnetPort2, subnetPorts[0])
}

func TestSubnetPortService_ListSubnetPortByStsName(t *testing.T) {
	subnetPortService := createSubnetPortService(t)
	subnetPort1 := &model.VpcSubnetPort{
		Id:         &subnetPortId1,
		Path:       &subnetPortPath1,
		ParentPath: &subnetPath,
		Tags: []model.Tag{
			{
				Scope: common.String("nsx-op/namespace"),
				Tag:   common.String("ns-1"),
			},
			{
				Scope: common.String(common.TagScopeStatefulSetName),
				Tag:   common.String("sts-1"),
			},
		},
	}
	subnetPort2 := &model.VpcSubnetPort{
		Id:         &subnetPortId2,
		Path:       &subnetPortPath2,
		ParentPath: &subnetPath,
		Tags: []model.Tag{
			{
				Scope: common.String("nsx-op/namespace"),
				Tag:   common.String("ns-1"),
			},
			{
				Scope: common.String(common.TagScopeStatefulSetName),
				Tag:   common.String("sts-1"),
			},
		},
	}
	subnetPortDifferentNS := &model.VpcSubnetPort{
		Id:         common.String("port-3"),
		Path:       common.String("/subnet-path-3"),
		ParentPath: &subnetPath,
		Tags: []model.Tag{
			{
				Scope: common.String("nsx-op/namespace"),
				Tag:   common.String("ns-2"),
			},
			{
				Scope: common.String(common.TagScopeStatefulSetName),
				Tag:   common.String("sts-1"),
			},
		},
	}
	subnetPortService.SubnetPortStore.Add(subnetPort1)
	subnetPortService.SubnetPortStore.Add(subnetPort2)
	subnetPortService.SubnetPortStore.Add(subnetPortDifferentNS)

	tests := []struct {
		name          string
		ns            string
		stsName       string
		expectedCount int
	}{
		{
			name:          "found in ns-1",
			ns:            "ns-1",
			stsName:       "sts-1",
			expectedCount: 2,
		},
		{
			name:          "not found different namespace",
			ns:            "ns-2",
			stsName:       "sts-1",
			expectedCount: 1,
		},
		{
			name:          "not found",
			ns:            "ns-1",
			stsName:       "non-existent",
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subnetPorts := subnetPortService.ListSubnetPortByStsName(tt.ns, tt.stsName)
			assert.Equal(t, tt.expectedCount, len(subnetPorts))
		})
	}
}

func TestSubnetPortService_ListSubnetPortByStsUid(t *testing.T) {
	subnetPortService := createSubnetPortService(t)
	subnetPort1 := &model.VpcSubnetPort{
		Id:         &subnetPortId1,
		Path:       &subnetPortPath1,
		ParentPath: &subnetPath,
		Tags: []model.Tag{
			{
				Scope: common.String("nsx-op/namespace"),
				Tag:   common.String("ns-1"),
			},
			{
				Scope: common.String(common.TagScopeStatefulSetUID),
				Tag:   common.String("sts-uid-123"),
			},
		},
	}
	subnetPort2 := &model.VpcSubnetPort{
		Id:         &subnetPortId2,
		Path:       &subnetPortPath2,
		ParentPath: &subnetPath,
		Tags: []model.Tag{
			{
				Scope: common.String("nsx-op/namespace"),
				Tag:   common.String("ns-1"),
			},
			{
				Scope: common.String(common.TagScopeStatefulSetUID),
				Tag:   common.String("sts-uid-123"),
			},
		},
	}
	subnetPortDifferentNS := &model.VpcSubnetPort{
		Id:         common.String("port-3"),
		Path:       common.String("/subnet-path-3"),
		ParentPath: &subnetPath,
		Tags: []model.Tag{
			{
				Scope: common.String("nsx-op/namespace"),
				Tag:   common.String("ns-2"),
			},
			{
				Scope: common.String(common.TagScopeStatefulSetUID),
				Tag:   common.String("sts-uid-123"),
			},
		},
	}
	subnetPortService.SubnetPortStore.Add(subnetPort1)
	subnetPortService.SubnetPortStore.Add(subnetPort2)
	subnetPortService.SubnetPortStore.Add(subnetPortDifferentNS)

	tests := []struct {
		name          string
		ns            string
		stsUid        string
		expectedCount int
	}{
		{
			name:          "found in ns-1",
			ns:            "ns-1",
			stsUid:        "sts-uid-123",
			expectedCount: 2,
		},
		{
			name:          "not found different namespace",
			ns:            "ns-2",
			stsUid:        "sts-uid-123",
			expectedCount: 1,
		},
		{
			name:          "not found",
			ns:            "ns-1",
			stsUid:        "non-existent",
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subnetPorts := subnetPortService.ListSubnetPortByStsUid(tt.ns, tt.stsUid)
			assert.Equal(t, tt.expectedCount, len(subnetPorts))
		})
	}
}

func TestSubnetPortService_AllocateAndReleasePortFromSubnet(t *testing.T) {
	subnetPath := "subnet-path-1"
	subnetId := "subnet-id-1"
	// 1. Test standard IPv4 execution track
	subnet1 := &model.VpcSubnet{
		Ipv4SubnetSize: common.Int64(16),
		IpAddresses:    []string{"10.0.0.1/28"},
		Path:           &subnetPath,
		Id:             &subnetId,
		IpAddressType:  common.String(model.VpcSubnet_IP_ADDRESS_TYPE_IPV4),
		SubnetDhcpConfig: &model.SubnetDhcpConfig{
			Mode: common.String("DHCP_RELAY"),
		},
	}
	subnetPortService := createSubnetPortService(t)
	// This test doesn't exercise the DHCPv6-pool-statistics path; keep NSXCheckVersion(IPv6)
	// false so checkIPv6Capacity's DHCP-sourced branch keeps its unconditional-allow fallback
	// instead of calling the (unmocked) real Cluster.GetVersion().
	patches := gomonkey.ApplyMethod(reflect.TypeOf(subnetPortService.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, _ int) bool {
		return false
	})
	defer patches.Reset()
	// Reset Subnet totalIP without SubnetPort does not influence the port count info
	subnetPortService.ResetSubnetTotalIP(subnetPath)

	ok, err := subnetPortService.AllocatePortFromSubnet(subnet1, false, v1alpha1.IPAddressTypeIPv4, v1alpha1.StaticIPAllocationTypeNone, nil)
	assert.True(t, ok)
	require.NoError(t, err)

	// Verify counter adjustments via internal Store check
	if obj, existed := subnetPortService.SubnetPortStore.PortCountInfo.Load(subnetPath); existed {
		info := obj.(*CountInfo)
		assert.Equal(t, 1, info.dirtyDhcpCount)
		assert.Equal(t, 0, info.dirtyDhcpCountIPv6)
	}

	empty := subnetPortService.IsEmptySubnet(subnetPath)
	assert.False(t, empty)
	subnetPortService.ReleasePortInSubnet(subnetPath, v1alpha1.IPAddressTypeIPv4, v1alpha1.StaticIPAllocationTypeNone, nil)
	empty = subnetPortService.IsEmptySubnet(subnetPath)
	assert.True(t, empty)

	// Update Subnet as exhausted and check port cannot be allocated
	subnetPortService.updateExhaustedSubnet(subnetPath)
	// Reset Subnet totalIP does not change other port count info
	subnetPortService.ResetSubnetTotalIP(subnetPath)
	ok, err = subnetPortService.AllocatePortFromSubnet(subnet1, false, v1alpha1.IPAddressTypeIPv4, v1alpha1.StaticIPAllocationTypeNone, nil)
	assert.False(t, ok)
	assert.Nil(t, err)

	// 2. Test Dual-Stack Stack counter alignment
	subnetPortService.SubnetPortStore.PortCountInfo.Delete(subnetPath) // Clear previous tracking context
	dualStackSubnet := &model.VpcSubnet{
		Path:          &subnetPath,
		Id:            &subnetId,
		IpAddressType: common.String(model.VpcSubnet_IP_ADDRESS_TYPE_IPV4_IPV6),
		SubnetDhcpConfig: &model.SubnetDhcpConfig{
			Mode: common.String("DHCP_RELAY"),
		},
		SubnetDhcpv6Config: &model.SubnetDhcpv6Config{
			Mode: common.String("DHCP_RELAY"),
		},
		Ipv4SubnetSize: common.Int64(16),
	}

	ok, err = subnetPortService.AllocatePortFromSubnet(dualStackSubnet, false, v1alpha1.IPAddressTypeIPv4IPv6, v1alpha1.StaticIPAllocationTypeNone, nil)
	assert.True(t, ok)
	require.NoError(t, err)

	if obj, existed := subnetPortService.SubnetPortStore.PortCountInfo.Load(subnetPath); existed {
		info := obj.(*CountInfo)
		assert.Equal(t, 1, info.dirtyDhcpCount)
		assert.Equal(t, 1, info.dirtyDhcpCountIPv6)
	}
}

func TestSubnetPortService_AllocatePortFromSubnet(t *testing.T) {
	subnetPath := "subnet-path-1"
	subnetId := "subnet-id-1"
	staticSubnet := &model.VpcSubnet{
		Ipv4SubnetSize: common.Int64(16),
		IpAddresses:    []string{"10.0.0.1/28"},
		Path:           &subnetPath,
		Id:             &subnetId,
		IpAddressType:  common.String(model.VpcSubnet_IP_ADDRESS_TYPE_IPV4),
		AdvancedConfig: &model.SubnetAdvancedConfig{
			StaticIpAllocation: &model.StaticIpAllocation{Enabled: common.Bool(true)},
		},
		SubnetDhcpConfig: &model.SubnetDhcpConfig{Mode: common.String("DHCP_DEACTIVATED")},
	}

	staticIPv6Subnet := &model.VpcSubnet{
		Path:          &subnetPath,
		Id:            &subnetId,
		IpAddressType: common.String(model.VpcSubnet_IP_ADDRESS_TYPE_IPV6),
		AdvancedConfig: &model.SubnetAdvancedConfig{
			StaticIpAllocation: &model.StaticIpAllocation{Enabled: common.Bool(true)},
		},
		SubnetDhcpv6Config: &model.SubnetDhcpv6Config{Mode: common.String("DHCP_DEACTIVATED")},
	}

	dualStackStaticSubnet := &model.VpcSubnet{
		Ipv4SubnetSize: common.Int64(16),
		IpAddresses:    []string{"10.0.0.1/28"},
		Path:           &subnetPath,
		Id:             &subnetId,
		IpAddressType:  common.String(model.VpcSubnet_IP_ADDRESS_TYPE_IPV4_IPV6),
		AdvancedConfig: &model.SubnetAdvancedConfig{
			StaticIpAllocation: &model.StaticIpAllocation{Enabled: common.Bool(true)},
		},
		SubnetDhcpConfig:   &model.SubnetDhcpConfig{Mode: common.String("DHCP_DEACTIVATED")},
		SubnetDhcpv6Config: &model.SubnetDhcpv6Config{Mode: common.String("DHCP_DEACTIVATED")},
	}

	staticSubnetWithStaticIPAllocationDisabled := &model.VpcSubnet{
		Ipv4SubnetSize: common.Int64(16),
		IpAddresses:    []string{"10.0.0.1/28"},
		Path:           &subnetPath,
		Id:             &subnetId,
		AdvancedConfig: &model.SubnetAdvancedConfig{
			StaticIpAllocation: &model.StaticIpAllocation{Enabled: common.Bool(false)},
		},
		SubnetDhcpConfig: &model.SubnetDhcpConfig{Mode: common.String("DHCP_DEACTIVATED")},
	}

	dhcpServerSubnet := &model.VpcSubnet{
		Ipv4SubnetSize:   common.Int64(16),
		IpAddresses:      []string{"10.0.0.1/28"},
		Path:             &subnetPath,
		Id:               &subnetId,
		SubnetDhcpConfig: &model.SubnetDhcpConfig{Mode: common.String("DHCP_SERVER")},
	}

	dhcpRelaySubnet := &model.VpcSubnet{
		Ipv4SubnetSize:   common.Int64(16),
		IpAddresses:      []string{"10.0.0.1/28"},
		Path:             &subnetPath,
		Id:               &subnetId,
		SubnetDhcpConfig: &model.SubnetDhcpConfig{Mode: common.String("DHCP_RELAY")},
	}

	dhcpRelaySubnet1 := &model.VpcSubnet{
		Ipv4SubnetSize:   common.Int64(16),
		IpAddresses:      []string{"10.0.0.1/30"},
		Path:             &subnetPath,
		Id:               &subnetId,
		SubnetDhcpConfig: &model.SubnetDhcpConfig{Mode: common.String("DHCP_RELAY")},
	}

	// mixedModeSubnet has both DHCP and static IP allocation enabled at the same time
	// (mixed mode): some SubnetPorts on it draw their IPv4 from the DHCP pool, others
	// from the static pool, decided per-port by StaticIPAllocationType.
	mixedModeSubnet := &model.VpcSubnet{
		Ipv4SubnetSize: common.Int64(16),
		IpAddresses:    []string{"10.0.0.1/28"},
		Path:           &subnetPath,
		Id:             &subnetId,
		AdvancedConfig: &model.SubnetAdvancedConfig{
			StaticIpAllocation: &model.StaticIpAllocation{Enabled: common.Bool(true)},
		},
		SubnetDhcpConfig: &model.SubnetDhcpConfig{Mode: common.String("DHCP_SERVER")},
	}

	tests := []struct {
		name                   string
		subnet                 *model.VpcSubnet
		sharedSubnet           bool
		interfaceIPType        v1alpha1.IPAddressType // Added to test struct
		staticIPAllocationType v1alpha1.StaticIPAllocationType
		addressBindings        []v1alpha1.PortAddressBinding
		prepareFunc            func(service *SubnetPortService) *gomonkey.Patches
		expectedValue          bool
		expectedErr            string
	}{
		{
			name:                   "Failed to get subnet static ip pool",
			subnet:                 staticSubnet,
			interfaceIPType:        v1alpha1.IPAddressTypeIPv4,
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4,
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				service.NSXClient.IPPoolClient = &mockErrorIPPoolClient{err: fmt.Errorf("mock static error")}
				return nil
			},
			expectedValue: false,
			expectedErr:   "mock static error",
		},
		{
			name:            "Failed to get subnet dhcp server config stats",
			subnet:          dhcpServerSubnet,
			interfaceIPType: v1alpha1.IPAddressTypeIPv4,
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				return gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.DhcpServerConfigStatsClient), "Get", func(_ *fakeStatsClient, _, _, _, _ string, _ *string, _ *string, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.DhcpServerStatistics, error) {
					return model.DhcpServerStatistics{}, fmt.Errorf("mock dhcp error")
				})
			},
			expectedValue: false,
			expectedErr:   "mock dhcp error",
		},
		{
			name:                   "Allocate SubnetPort from static Subnet failed",
			subnet:                 staticSubnet,
			interfaceIPType:        v1alpha1.IPAddressTypeIPv4,
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4,
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				service.NSXClient.IPPoolClient = &mockFuncIPPoolClient{
					getFunc: func(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, poolIdParam string) (model.IpAddressPool, error) {
						return model.IpAddressPool{
							PoolUsage: &model.PolicyPoolUsage{TotalIps: common.Int64(0)},
						}, nil
					},
				}
				return nil
			},
			expectedValue: false,
		},
		{
			name:            "Allocate SubnetPort from dhcp server Subnet failed",
			subnet:          dhcpServerSubnet,
			interfaceIPType: v1alpha1.IPAddressTypeIPv4,
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				return gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.DhcpServerConfigStatsClient), "Get", func(_ *fakeStatsClient, _, _, _, _ string, _ *string, _ *string, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.DhcpServerStatistics, error) {
					return model.DhcpServerStatistics{
						IpPoolStats: []model.DhcpIpPoolUsage{{PoolSize: common.Int64(0)}},
					}, nil
				})
			},
			expectedValue: false,
		},
		{
			name:            "Allocate SubnetPort from dhcp relay Subnet failed",
			subnet:          dhcpRelaySubnet1,
			interfaceIPType: v1alpha1.IPAddressTypeIPv4,
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				return nil
			},
			expectedValue: false,
		},
		{
			name:                   "Allocate SubnetPort from static subnet with staticIpAllocation disabled failed",
			subnet:                 staticSubnetWithStaticIPAllocationDisabled,
			interfaceIPType:        v1alpha1.IPAddressTypeIPv4,
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone,
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				return nil
			},
			expectedValue: true,
		},
		{
			name:                   "Allocate SubnetPort from static Subnet",
			subnet:                 staticSubnet,
			interfaceIPType:        v1alpha1.IPAddressTypeIPv4,
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4,
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				service.NSXClient.IPPoolClient = &mockFuncIPPoolClient{
					getFunc: func(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, poolIdParam string) (model.IpAddressPool, error) {
						return model.IpAddressPool{
							PoolUsage: &model.PolicyPoolUsage{TotalIps: common.Int64(10)},
						}, nil
					},
				}
				return nil
			},
			expectedValue: true,
		},
		{
			name:                   "Allocate SubnetPort from static shared Subnet failed",
			subnet:                 staticSubnet,
			interfaceIPType:        v1alpha1.IPAddressTypeIPv4,
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4,
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				service.NSXClient.IPPoolClient = &mockFuncIPPoolClient{
					getFunc: func(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, poolIdParam string) (model.IpAddressPool, error) {
						return model.IpAddressPool{
							PoolUsage: &model.PolicyPoolUsage{TotalIps: common.Int64(10), RequestedIpAllocations: common.Int64(10)},
						}, nil
					},
				}
				return nil
			},
			expectedValue: false,
			sharedSubnet:  true,
		},
		{
			name:            "Allocate SubnetPort from dhcp server Subnet",
			subnet:          dhcpServerSubnet,
			interfaceIPType: v1alpha1.IPAddressTypeIPv4,
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				return gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.DhcpServerConfigStatsClient), "Get", func(_ *fakeStatsClient, _, _, _, _ string, _ *string, _ *string, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.DhcpServerStatistics, error) {
					return model.DhcpServerStatistics{
						IpPoolStats: []model.DhcpIpPoolUsage{{PoolSize: common.Int64(10)}},
					}, nil
				})
			},
			expectedValue: true,
		},
		{
			name:            "Allocate SubnetPort from dhcp relay Subnet",
			subnet:          dhcpRelaySubnet,
			interfaceIPType: v1alpha1.IPAddressTypeIPv4,
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				return nil
			},
			expectedValue: true,
		},
		{
			name:                   "Failed to get static-ipv6-default pool",
			subnet:                 staticIPv6Subnet,
			interfaceIPType:        v1alpha1.IPAddressTypeIPv6,
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv6,
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				service.NSXClient.IPPoolClient = &mockErrorIPPoolClient{err: fmt.Errorf("mock ipv6 static pool error")}
				return nil
			},
			expectedValue: false,
			expectedErr:   "mock ipv6 static pool error",
		},
		{
			name:                   "Allocate SubnetPort from static IPv6 Subnet successful",
			subnet:                 staticIPv6Subnet,
			interfaceIPType:        v1alpha1.IPAddressTypeIPv6,
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv6,
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				service.NSXClient.IPPoolClient = &mockFuncIPPoolClient{
					getFunc: func(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, poolIdParam string) (model.IpAddressPool, error) {
						if poolIdParam == "static-ipv6-default" {
							return model.IpAddressPool{
								PoolUsage: &model.PolicyPoolUsage{TotalIps: common.Int64(20)},
							}, nil
						}
						return model.IpAddressPool{}, nil
					},
				}
				return nil
			},
			expectedValue: true,
		},
		{
			name:                   "Allocate Dual-Stack SubnetPort failed due to IPv4 starvation",
			subnet:                 dualStackStaticSubnet,
			interfaceIPType:        v1alpha1.IPAddressTypeIPv4IPv6,
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4IPv6,
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				service.NSXClient.IPPoolClient = &mockFuncIPPoolClient{
					getFunc: func(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, poolIdParam string) (model.IpAddressPool, error) {
						if poolIdParam == "static-ipv4-default" {
							return model.IpAddressPool{
								PoolUsage: &model.PolicyPoolUsage{TotalIps: common.Int64(0)},
							}, nil
						}
						return model.IpAddressPool{
							PoolUsage: &model.PolicyPoolUsage{TotalIps: common.Int64(100)},
						}, nil
					},
				}
				return nil
			},
			expectedValue: false,
		},
		{
			name:                   "Allocate Dual-Stack SubnetPort failed due to IPv6 starvation",
			subnet:                 dualStackStaticSubnet,
			interfaceIPType:        v1alpha1.IPAddressTypeIPv4IPv6,
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4IPv6,
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				service.NSXClient.IPPoolClient = &mockFuncIPPoolClient{
					getFunc: func(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, poolIdParam string) (model.IpAddressPool, error) {
						if poolIdParam == "static-ipv4-default" {
							return model.IpAddressPool{
								PoolUsage: &model.PolicyPoolUsage{TotalIps: common.Int64(10)},
							}, nil
						}
						if poolIdParam == "static-ipv6-default" {
							return model.IpAddressPool{
								PoolUsage: &model.PolicyPoolUsage{TotalIps: common.Int64(0)},
							}, nil
						}
						return model.IpAddressPool{}, nil
					},
				}
				return nil
			},
			expectedValue: false,
		},
		{
			// Mixed mode: subnet has both DHCP_SERVER and static IP allocation enabled.
			// A SubnetPort requesting a static IPv4 must be checked against the static
			// pool, never the DHCP pool. We prove this by making the DHCP stats call
			// error out - if the code wrongly consulted it, the test would fail.
			name:                   "Mixed mode Subnet - static-sourced port checks static pool only",
			subnet:                 mixedModeSubnet,
			interfaceIPType:        v1alpha1.IPAddressTypeIPv4,
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4,
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.IPPoolClient), "Get", func(_ *fakeIPPoolClient, _, _, _, _, _ string) (model.IpAddressPool, error) {
					return model.IpAddressPool{
						PoolUsage: &model.PolicyPoolUsage{TotalIps: common.Int64(5)},
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(service.NSXClient.DhcpServerConfigStatsClient), "Get", func(_ *fakeStatsClient, _, _, _, _ string, _ *string, _ *string, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.DhcpServerStatistics, error) {
					return model.DhcpServerStatistics{}, fmt.Errorf("dhcp pool must not be checked for a static-sourced port")
				})
				return patches
			},
			expectedValue: true,
		},
		{
			// Same mixed mode subnet, but this port has no static allocation (DHCP-sourced
			// IPv4). It must be checked against the DHCP pool, never the static pool.
			name:                   "Mixed mode Subnet - dhcp-sourced port checks dhcp pool only",
			subnet:                 mixedModeSubnet,
			interfaceIPType:        v1alpha1.IPAddressTypeIPv4,
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone,
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.DhcpServerConfigStatsClient), "Get", func(_ *fakeStatsClient, _, _, _, _ string, _ *string, _ *string, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.DhcpServerStatistics, error) {
					return model.DhcpServerStatistics{
						IpPoolStats: []model.DhcpIpPoolUsage{{PoolSize: common.Int64(5)}},
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(service.NSXClient.IPPoolClient), "Get", func(_ *fakeIPPoolClient, _, _, _, _, _ string) (model.IpAddressPool, error) {
					return model.IpAddressPool{}, fmt.Errorf("static pool must not be checked for a dhcp-sourced port")
				})
				return patches
			},
			expectedValue: true,
		},
		{
			// Regression test: an existing DHCP-sourced port already realized on the
			// mixed-mode Subnet must not count against the static pool's capacity when
			// checking a new static-sourced port. Before this fix, existingIPCount
			// counted every realized port on the Subnet regardless of which pool it
			// drew from, so a lone DHCP-sourced port could falsely exhaust a small
			// static pool it never actually touched.
			name:                   "Mixed mode Subnet - existing dhcp-sourced port does not count against static pool",
			subnet:                 mixedModeSubnet,
			interfaceIPType:        v1alpha1.IPAddressTypeIPv4,
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4,
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				existingDhcpPort := &model.VpcSubnetPort{
					Id:         common.String("existing-dhcp-port"),
					Path:       common.String(subnetPath + "/ports/existing-dhcp-port"),
					ParentPath: &subnetPath,
					Attachment: &model.PortAttachment{
						AllocateAddresses: common.String("NONE"),
					},
				}
				service.SubnetPortStore.Add(existingDhcpPort)
				// TotalIps is 2, not 1: the test harness below calls AllocatePortFromSubnet
				// twice when expectedValue is true, so capacity must cover both calls
				// (1 IP each). If the existing DHCP port were wrongly counted against the
				// static pool, even a single call would fail here.
				return gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.IPPoolClient), "Get", func(_ *fakeIPPoolClient, _, _, _, _, _ string) (model.IpAddressPool, error) {
					return model.IpAddressPool{
						PoolUsage: &model.PolicyPoolUsage{TotalIps: common.Int64(2)},
					}, nil
				})
			},
			expectedValue: true,
		},
		{
			// Mixed mode: the static pool is exhausted while the DHCP pool still has
			// room. A static-sourced port must be rejected, not silently allowed
			// through by looking at the (irrelevant) DHCP pool's capacity.
			name:                   "Mixed mode Subnet - static pool exhausted, dhcp pool available",
			subnet:                 mixedModeSubnet,
			interfaceIPType:        v1alpha1.IPAddressTypeIPv4,
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4,
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.IPPoolClient), "Get", func(_ *fakeIPPoolClient, _, _, _, _, _ string) (model.IpAddressPool, error) {
					return model.IpAddressPool{
						PoolUsage: &model.PolicyPoolUsage{TotalIps: common.Int64(0)},
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(service.NSXClient.DhcpServerConfigStatsClient), "Get", func(_ *fakeStatsClient, _, _, _, _ string, _ *string, _ *string, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.DhcpServerStatistics, error) {
					return model.DhcpServerStatistics{
						IpPoolStats: []model.DhcpIpPoolUsage{{PoolSize: common.Int64(100)}},
					}, nil
				})
				return patches
			},
			expectedValue: false,
		},
		{
			// Mixed mode: the DHCP pool is exhausted while the static pool still has
			// room. A dhcp-sourced port must be rejected, not silently allowed through
			// by looking at the (irrelevant) static pool's capacity.
			name:                   "Mixed mode Subnet - dhcp pool exhausted, static pool available",
			subnet:                 mixedModeSubnet,
			interfaceIPType:        v1alpha1.IPAddressTypeIPv4,
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone,
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.DhcpServerConfigStatsClient), "Get", func(_ *fakeStatsClient, _, _, _, _ string, _ *string, _ *string, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.DhcpServerStatistics, error) {
					return model.DhcpServerStatistics{
						IpPoolStats: []model.DhcpIpPoolUsage{{PoolSize: common.Int64(0)}},
					}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(service.NSXClient.IPPoolClient), "Get", func(_ *fakeIPPoolClient, _, _, _, _, _ string) (model.IpAddressPool, error) {
					return model.IpAddressPool{
						PoolUsage: &model.PolicyPoolUsage{TotalIps: common.Int64(100)},
					}, nil
				})
				return patches
			},
			expectedValue: false,
		},
		{
			// Multi-IP addressBindings: 2 IPv4 addresses requested on a static pool
			// with room for two such allocations (the test harness below calls
			// AllocatePortFromSubnet twice when expectedValue is true, so capacity
			// must cover 2x the requested count).
			name:                   "Multi-IP addressBindings fits in static pool capacity",
			subnet:                 staticSubnet,
			interfaceIPType:        v1alpha1.IPAddressTypeIPv4,
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4,
			addressBindings: []v1alpha1.PortAddressBinding{
				{MACAddress: "00:11:22:33:44:55"},
				{IPAddress: "10.0.0.2", MACAddress: "00:11:22:33:44:55"},
			},
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				return gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.IPPoolClient), "Get", func(_ *fakeIPPoolClient, _, _, _, _, _ string) (model.IpAddressPool, error) {
					return model.IpAddressPool{
						PoolUsage: &model.PolicyPoolUsage{TotalIps: common.Int64(4)},
					}, nil
				})
			},
			expectedValue: true,
		},
		{
			// Multi-IP addressBindings: 3 IPv4 addresses requested but the static pool
			// only has room for 2 - must be rejected, not silently accepted as if only
			// 1 IP were being requested.
			name:                   "Multi-IP addressBindings exceeds static pool capacity",
			subnet:                 staticSubnet,
			interfaceIPType:        v1alpha1.IPAddressTypeIPv4,
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4,
			addressBindings: []v1alpha1.PortAddressBinding{
				{MACAddress: "00:11:22:33:44:55"},
				{IPAddress: "10.0.0.2", MACAddress: "00:11:22:33:44:55"},
				{IPAddress: "10.0.0.3", MACAddress: "00:11:22:33:44:55"},
			},
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				return gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.IPPoolClient), "Get", func(_ *fakeIPPoolClient, _, _, _, _, _ string) (model.IpAddressPool, error) {
					return model.IpAddressPool{
						PoolUsage: &model.PolicyPoolUsage{TotalIps: common.Int64(2)},
					}, nil
				})
			},
			expectedValue: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subnetPortService := createSubnetPortService(t)
			patches := tt.prepareFunc(subnetPortService)
			if patches != nil {
				defer patches.Reset()
			}

			canAllocate, err := subnetPortService.AllocatePortFromSubnet(tt.subnet, tt.sharedSubnet, tt.interfaceIPType, tt.staticIPAllocationType, tt.addressBindings)
			assert.Equal(t, tt.expectedValue, canAllocate)
			if tt.expectedErr != "" {
				assert.NotNil(t, err)
				assert.Contains(t, err.Error(), tt.expectedErr)
			} else {
				assert.Nil(t, err)
			}

			if tt.expectedValue {
				canAllocate, err = subnetPortService.AllocatePortFromSubnet(tt.subnet, tt.sharedSubnet, tt.interfaceIPType, tt.staticIPAllocationType, tt.addressBindings)
				assert.Equal(t, tt.expectedValue, canAllocate)
				if tt.expectedErr != "" {
					assert.NotNil(t, err)
					assert.Contains(t, err.Error(), tt.expectedErr)
				} else {
					assert.Nil(t, err)
				}
			}
		})
	}
}

func createSubnetPortService(t *testing.T) *SubnetPortService {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	orgRootClient := mock_org_root.NewMockOrgRootClient(mockCtl)
	commonService := common.Service{
		Client: k8sClient,
		NSXClient: &nsx.Client{
			QueryClient:                 &fakeQueryClient{},
			PortClient:                  &fakePortClient{},
			IPPoolClient:                &fakeIPPoolClient{},
			DhcpServerConfigStatsClient: &fakeStatsClient{},
			RealizedEntitiesClient:      &fakeRealizedEntitiesClient{},
			PortStateClient:             &fakePortStateClient{},
			OrgRootClient:               orgRootClient,
			VifsClient:                  &fakeVifsClient{},
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
	builder, _ := common.PolicyPathVpcSubnetPort.NewPolicyTreeBuilder()
	return &SubnetPortService{
		Service: commonService,
		SubnetPortStore: &SubnetPortStore{ResourceStore: common.ResourceStore{
			Indexer: cache.NewIndexer(
				keyFunc,
				cache.Indexers{
					common.TagScopeSubnetPortCRUID: subnetPortIndexByCRUID,
					common.TagScopePodUID:          subnetPortIndexByPodUID,
					common.TagScopeVMNamespace:     subnetPortIndexNamespace,
					common.TagScopeNamespace:       subnetPortIndexPodNamespace,
					common.IndexKeySubnetPath:      subnetPortIndexBySubnetPath,
					common.TagScopeStatefulSetUID:  subnetPortIndexByStatefulSetUID,
					common.TagScopeStatefulSetName: subnetPortIndexByStatefulSetName,
				}),
			BindingType: model.VpcSubnetPortBindingType(),
		}},
		builder: builder,
	}
}

func TestSubnetPortService_portAlreadyRealized(t *testing.T) {
	service := &SubnetPortService{}
	nsxSubnetPortWithNONE := &model.VpcSubnetPort{
		Attachment: &model.PortAttachment{
			AllocateAddresses: common.String("NONE"),
		},
	}
	nsxSubnetPortWithBOTH := &model.VpcSubnetPort{
		Attachment: &model.PortAttachment{
			AllocateAddresses: common.String("BOTH"),
			Id:                common.String("attachment-id"),
		},
	}
	// SubnetPort: realized
	subnetPortReady := &v1alpha1.SubnetPort{
		Status: v1alpha1.SubnetPortStatus{
			Conditions: []v1alpha1.Condition{
				{
					Reason: "SubnetPortReady",
					Status: corev1.ConditionTrue,
				},
			},
			Attachment: v1alpha1.PortAttachment{
				ID: "some-id",
			},
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
				IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
					{
						IPAddress: "172.26.26.232",
						Gateway:   "some-gateway",
					},
				},
				MACAddress: "04:50:56:00:b4:24",
			},
		},
	}
	assert.True(t, service.portAlreadyRealized(subnetPortReady, nsxSubnetPortWithBOTH))

	// SubnetPort: not realized
	subnetPortNotReady := &v1alpha1.SubnetPort{
		Status: v1alpha1.SubnetPortStatus{},
	}
	assert.False(t, service.portAlreadyRealized(subnetPortNotReady, nsxSubnetPortWithNONE))

	// SubnetPort: not realized (wrong status)
	subnetPortWrong := &v1alpha1.SubnetPort{
		Status: v1alpha1.SubnetPortStatus{
			Conditions: []v1alpha1.Condition{
				{
					Reason: "SubnetPortReady",
					Status: corev1.ConditionFalse,
				},
			},
			Attachment: v1alpha1.PortAttachment{
				ID: "some-id",
			},
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
				IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
					{
						Gateway: "some-gateway",
					},
				},
			},
		},
	}
	assert.False(t, service.portAlreadyRealized(subnetPortWrong, nsxSubnetPortWithNONE))
	// SubnetPort: not realized (missing IP with AllocateAddresses=BOTH)

	// SubnetPort: multi-IP addressBindings (3 entries, same MAC), only 2 of 3 IPs realized -> not realized
	subnetPortMultiBindingPartial := &v1alpha1.SubnetPort{
		Spec: v1alpha1.SubnetPortSpec{
			AddressBindings: []v1alpha1.PortAddressBinding{
				{IPAddress: "172.26.26.10", MACAddress: "04:50:56:00:b4:24"},
				{IPAddress: "172.26.26.11", MACAddress: "04:50:56:00:b4:24"},
				{IPAddress: "172.26.26.12", MACAddress: "04:50:56:00:b4:24"},
			},
		},
		Status: v1alpha1.SubnetPortStatus{
			Attachment: v1alpha1.PortAttachment{
				ID: "some-id",
			},
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
				IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
					{IPAddress: "172.26.26.10", Gateway: "some-gateway"},
					{IPAddress: "172.26.26.11", Gateway: "some-gateway"},
				},
				MACAddress: "04:50:56:00:b4:24",
			},
		},
	}
	assert.False(t, service.portAlreadyRealized(subnetPortMultiBindingPartial, nsxSubnetPortWithBOTH))

	// SubnetPort: multi-IP addressBindings (3 entries, same MAC), all 3 IPs realized -> realized
	subnetPortMultiBindingFull := &v1alpha1.SubnetPort{
		Spec: subnetPortMultiBindingPartial.Spec,
		Status: v1alpha1.SubnetPortStatus{
			Conditions: []v1alpha1.Condition{
				{
					Reason: "SubnetPortReady",
					Status: corev1.ConditionTrue,
				},
			},
			Attachment: v1alpha1.PortAttachment{
				ID: "some-id",
			},
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
				IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
					{IPAddress: "172.26.26.10", Gateway: "some-gateway"},
					{IPAddress: "172.26.26.11", Gateway: "some-gateway"},
					{IPAddress: "172.26.26.12", Gateway: "some-gateway"},
				},
				MACAddress: "04:50:56:00:b4:24",
			},
		},
	}
	assert.True(t, service.portAlreadyRealized(subnetPortMultiBindingFull, nsxSubnetPortWithBOTH))

	nsxSubnetPortWithMACPOOL := &model.VpcSubnetPort{
		Attachment: &model.PortAttachment{
			AllocateAddresses: common.String("MAC_POOL"),
			Id:                common.String("attachment-id"),
		},
	}

	// DHCP subnet, no addressBindings: realized as soon as MAC lands, IP count is irrelevant
	// since AllocateAddresses is MAC_POOL, not BOTH/IP_POOL.
	subnetPortDHCPNoBindings := &v1alpha1.SubnetPort{
		Spec: v1alpha1.SubnetPortSpec{
			StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone,
		},
		Status: v1alpha1.SubnetPortStatus{
			Conditions: []v1alpha1.Condition{
				{Reason: "SubnetPortReady", Status: corev1.ConditionTrue},
			},
			Attachment: v1alpha1.PortAttachment{ID: "some-id"},
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
				IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{{Gateway: "some-gateway"}},
				MACAddress:  "04:50:56:00:b4:24",
			},
		},
	}
	assert.True(t, service.portAlreadyRealized(subnetPortDHCPNoBindings, nsxSubnetPortWithMACPOOL))

	// DHCP subnet, multiple MAC-only addressBindings (pool-allocated, sharing one MAC):
	// still realized as soon as MAC lands, regardless of how many bindings were requested.
	subnetPortDHCPMultiBindings := &v1alpha1.SubnetPort{
		Spec: v1alpha1.SubnetPortSpec{
			StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone,
			AddressBindings: []v1alpha1.PortAddressBinding{
				{MACAddress: "04:50:56:00:b4:24"},
				{MACAddress: "04:50:56:00:b4:24"},
			},
		},
		Status: v1alpha1.SubnetPortStatus{
			Conditions: []v1alpha1.Condition{
				{Reason: "SubnetPortReady", Status: corev1.ConditionTrue},
			},
			Attachment: v1alpha1.PortAttachment{ID: "some-id"},
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
				IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{{Gateway: "some-gateway"}},
				MACAddress:  "04:50:56:00:b4:24",
			},
		},
	}
	assert.True(t, service.portAlreadyRealized(subnetPortDHCPMultiBindings, nsxSubnetPortWithMACPOOL))

	// Mixed-mode subnet, dual-stack interface but only IPv4 statically allocated, no explicit
	// addressBindings: expect only 1 realized IP, not 2 - the IPv6 side comes from DHCP/SLAAC
	// and is never reflected via AllocateAddresses BOTH/IP_POOL.
	subnetPortMixedNoBindings := &v1alpha1.SubnetPort{
		Spec: v1alpha1.SubnetPortSpec{
			InterfaceIPType:        v1alpha1.IPAddressTypeIPv4IPv6,
			StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4,
		},
		Status: v1alpha1.SubnetPortStatus{
			Conditions: []v1alpha1.Condition{
				{Reason: "SubnetPortReady", Status: corev1.ConditionTrue},
			},
			Attachment: v1alpha1.PortAttachment{ID: "some-id"},
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
				IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
					{IPAddress: "172.26.26.10", Gateway: "some-gateway"},
				},
				MACAddress: "04:50:56:00:b4:24",
			},
		},
	}
	assert.True(t, service.portAlreadyRealized(subnetPortMixedNoBindings, nsxSubnetPortWithBOTH))

	// Mixed-mode subnet, IPv4-only static allocation, 2 explicit addressBindings (multi-IP on
	// the static side): expectedIPCount comes from len(AddressBindings), not StaticIPAllocationType.
	subnetPortMixedMultiBindings := &v1alpha1.SubnetPort{
		Spec: v1alpha1.SubnetPortSpec{
			InterfaceIPType:        v1alpha1.IPAddressTypeIPv4,
			StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4,
			AddressBindings: []v1alpha1.PortAddressBinding{
				{IPAddress: "172.26.26.10", MACAddress: "04:50:56:00:b4:24"},
				{IPAddress: "172.26.26.11", MACAddress: "04:50:56:00:b4:24"},
			},
		},
		Status: v1alpha1.SubnetPortStatus{
			Attachment: v1alpha1.PortAttachment{ID: "some-id"},
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
				IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
					{IPAddress: "172.26.26.10", Gateway: "some-gateway"},
				},
				MACAddress: "04:50:56:00:b4:24",
			},
		},
	}
	assert.False(t, service.portAlreadyRealized(subnetPortMixedMultiBindings, nsxSubnetPortWithBOTH),
		"only 1 of 2 requested static IPs realized - must not be considered ready")

	// Pod: realized (annotation exists)
	pod := &corev1.Pod{}
	pod.Annotations = map[string]string{
		common.AnnotationPodMAC:     "mac",
		common.AnnotationAttachment: "attachment-id",
	}
	assert.True(t, service.portAlreadyRealized(pod, nsxSubnetPortWithBOTH))

	// Pod: not realized (annotation missing)
	podNoMAC := &corev1.Pod{}
	podNoMAC.Annotations = map[string]string{"other": "value"}
	assert.False(t, service.portAlreadyRealized(podNoMAC, nsxSubnetPortWithBOTH))
}

func TestSubnetPortService_GetAllVIFs(t *testing.T) {
	subnetPortService := createSubnetPortService(t)
	tests := []struct {
		name        string
		prepareFunc func(*testing.T, *SubnetPortService, context.Context) *gomonkey.Patches
		wantErr     bool
	}{
		{
			name: "apiError",
			prepareFunc: func(t *testing.T, s *SubnetPortService, ctx context.Context) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.VifsClient, "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{mpmodel.VirtualNetworkInterfaceListResult{}, fmt.Errorf("mock error")},
					Times:  1,
				}})
				return patches
			},
			wantErr: true,
		},
		{
			name: "success",
			prepareFunc: func(t *testing.T, s *SubnetPortService, ctx context.Context) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.VifsClient, "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{mpmodel.VirtualNetworkInterfaceListResult{
						Results: []mpmodel.VirtualNetworkInterface{
							{
								ExternalId: common.String("vif-1"),
							},
						},
					}, nil},
					Times: 1,
				}})
				return patches
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := tt.prepareFunc(t, subnetPortService, context.Background())
			defer patches.Reset()
			_, err := subnetPortService.GetAllVIFs()
			if (err != nil) != tt.wantErr {
				t.Errorf("GetAllVIFs() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSubnetPortService_GetMACByAttachmentID(t *testing.T) {
	tests := []struct {
		name          string
		vifs          []mpmodel.VirtualNetworkInterface
		attachmentID  string
		expectedMAC   string
		expectedError string
	}{
		{
			name: "SingleMatch",
			vifs: []mpmodel.VirtualNetworkInterface{
				{
					ExternalId:        common.String("vif-1"),
					LportAttachmentId: common.String("attachment-1"),
					MacAddress:        common.String("aa:bb:cc:dd:ee:ff"),
				},
			},
			attachmentID: "attachment-1",
			expectedMAC:  "aa:bb:cc:dd:ee:ff",
		},
		{
			name: "MultipleSameMAC",
			vifs: []mpmodel.VirtualNetworkInterface{
				{
					ExternalId:        common.String("vif-2"),
					LportAttachmentId: common.String("attachment-1"),
					MacAddress:        common.String("aa:bb:cc:dd:ee:ff"),
				},
				{
					ExternalId:        common.String("vif-2"),
					LportAttachmentId: common.String("attachment-1"),
					MacAddress:        common.String("aa:bb:cc:dd:ee:ff"),
				},
			},
			attachmentID: "attachment-1",
			expectedMAC:  "aa:bb:cc:dd:ee:ff",
		},
		{
			name: "MultipleDifferentMAC",
			vifs: []mpmodel.VirtualNetworkInterface{
				{
					ExternalId:        common.String("vif-3"),
					LportAttachmentId: common.String("attachment-1"),
					MacAddress:        common.String("aa:bb:cc:dd:ee:ff"),
				},
				{
					ExternalId:        common.String("vif-3"),
					LportAttachmentId: common.String("attachment-1"),
					MacAddress:        common.String("11:22:33:44:55:66"),
				},
			},
			attachmentID:  "attachment-1",
			expectedError: "multiple MAC addresses found",
		},
		{
			name: "NoMACAddress",
			vifs: []mpmodel.VirtualNetworkInterface{
				{
					ExternalId:        common.String("vif-4"),
					LportAttachmentId: common.String("attachment-1"),
				},
			},
			attachmentID:  "attachment-1",
			expectedError: "MAC address not found",
		},
		{
			name: "NoMatchingAttachment",
			vifs: []mpmodel.VirtualNetworkInterface{
				{
					ExternalId:        common.String("vif-5"),
					LportAttachmentId: common.String("attachment-2"),
					MacAddress:        common.String("aa:bb:cc:dd:ee:ff"),
				},
			},
			attachmentID:  "attachment-1",
			expectedError: "VIF not found for attachment ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vifStore := NewVifStore()
			for _, vif := range tt.vifs {
				vifStore.Add(&vif)
			}
			mac, err := vifStore.GetMACByAttachmentID(tt.attachmentID)
			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedMAC, mac)
			}
		})
	}
}

func TestMergeSubnetPortAddressBinding(t *testing.T) {
	tests := []struct {
		name     string
		existing []model.PortAddressBindingEntry
		desired  []model.PortAddressBindingEntry
		expected []model.PortAddressBindingEntry
	}{
		{
			name: "Desired is empty - Use existing",
			existing: []model.PortAddressBindingEntry{
				{IpAddress: common.String("10.0.0.1"), MacAddress: common.String("aa:bb:cc:dd:ee:ff")},
			},
			desired: []model.PortAddressBindingEntry{},
			expected: []model.PortAddressBindingEntry{
				{IpAddress: common.String("10.0.0.1"), MacAddress: common.String("aa:bb:cc:dd:ee:ff")},
			},
		},
		{
			name: "Merge IP from existing - Desired IP is nil",
			existing: []model.PortAddressBindingEntry{
				{IpAddress: common.String("10.0.0.1"), MacAddress: common.String("aa:bb:cc:dd:ee:ff")},
			},
			desired: []model.PortAddressBindingEntry{
				{MacAddress: common.String("aa:bb:cc:dd:ee:ff")},
			},
			expected: []model.PortAddressBindingEntry{
				{IpAddress: common.String("10.0.0.1"), MacAddress: common.String("aa:bb:cc:dd:ee:ff")},
			},
		},
		{
			name: "Merge MAC from existing - Desired MAC is nil",
			existing: []model.PortAddressBindingEntry{
				{IpAddress: common.String("10.0.0.1"), MacAddress: common.String("aa:bb:cc:dd:ee:ff")},
			},
			desired: []model.PortAddressBindingEntry{
				{IpAddress: common.String("10.0.0.1")},
			},
			expected: []model.PortAddressBindingEntry{
				{IpAddress: common.String("10.0.0.1"), MacAddress: common.String("aa:bb:cc:dd:ee:ff")},
			},
		},
		{
			name: "No merge - Desired already has values",
			existing: []model.PortAddressBindingEntry{
				{IpAddress: common.String("192.168.1.1"), MacAddress: common.String("00:11:22:33:44:55")},
			},
			desired: []model.PortAddressBindingEntry{
				{IpAddress: common.String("10.0.0.1"), MacAddress: common.String("aa:bb:cc:dd:ee:ff")},
			},
			expected: []model.PortAddressBindingEntry{
				{IpAddress: common.String("10.0.0.1"), MacAddress: common.String("aa:bb:cc:dd:ee:ff")},
			},
		},
		{
			name:     "Existing is empty - Return desired as is",
			existing: []model.PortAddressBindingEntry{},
			desired: []model.PortAddressBindingEntry{
				{IpAddress: common.String("10.0.0.1")},
			},
			expected: []model.PortAddressBindingEntry{
				{IpAddress: common.String("10.0.0.1")},
			},
		},
		{
			name: "Multi-IP addressBindings sharing one MAC - merge each entry independently",
			existing: []model.PortAddressBindingEntry{
				{IpAddress: common.String("10.0.0.1"), MacAddress: common.String("aa:bb:cc:dd:ee:ff")},
				{IpAddress: common.String("10.0.0.2"), MacAddress: common.String("aa:bb:cc:dd:ee:ff")},
				{IpAddress: common.String("10.0.0.3"), MacAddress: common.String("aa:bb:cc:dd:ee:ff")},
			},
			desired: []model.PortAddressBindingEntry{
				{IpAddress: common.String("10.0.0.1")},
				{IpAddress: common.String("10.0.0.2")},
				{IpAddress: common.String("10.0.0.3")},
			},
			expected: []model.PortAddressBindingEntry{
				{IpAddress: common.String("10.0.0.1"), MacAddress: common.String("aa:bb:cc:dd:ee:ff")},
				{IpAddress: common.String("10.0.0.2"), MacAddress: common.String("aa:bb:cc:dd:ee:ff")},
				{IpAddress: common.String("10.0.0.3"), MacAddress: common.String("aa:bb:cc:dd:ee:ff")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeSubnetPortAddressBinding(tt.existing, tt.desired)

			assert.Equal(t, len(tt.expected), len(result))
			for i := range tt.expected {
				if tt.expected[i].IpAddress != nil {
					assert.Equal(t, *tt.expected[i].IpAddress, *result[i].IpAddress)
				} else {
					assert.Nil(t, result[i].IpAddress)
				}

				if tt.expected[i].MacAddress != nil {
					assert.Equal(t, *tt.expected[i].MacAddress, *result[i].MacAddress)
				} else {
					assert.Nil(t, result[i].MacAddress)
				}
			}
		})
	}
}

func TestCountNSXAddressBindingsByFamily(t *testing.T) {
	tests := []struct {
		name     string
		bindings []model.PortAddressBindingEntry
		wantIPv4 int
		wantIPv6 int
	}{
		{
			name:     "Empty bindings",
			bindings: []model.PortAddressBindingEntry{},
			wantIPv4: 0,
			wantIPv6: 0,
		},
		{
			name: "Binding with nil IP",
			bindings: []model.PortAddressBindingEntry{
				{IpAddress: nil},
			},
			wantIPv4: 1,
			wantIPv6: 0,
		},
		{
			name: "Binding with empty IP string",
			bindings: []model.PortAddressBindingEntry{
				{IpAddress: common.String("")},
			},
			wantIPv4: 1,
			wantIPv6: 0,
		},
		{
			name: "IPv4 binding",
			bindings: []model.PortAddressBindingEntry{
				{IpAddress: common.String("192.168.1.1")},
			},
			wantIPv4: 1,
			wantIPv6: 0,
		},
		{
			name: "IPv6 binding",
			bindings: []model.PortAddressBindingEntry{
				{IpAddress: common.String("2001:db8::1")},
			},
			wantIPv4: 0,
			wantIPv6: 1,
		},
		{
			name: "Mixed bindings",
			bindings: []model.PortAddressBindingEntry{
				{IpAddress: common.String("192.168.1.1")},
				{IpAddress: common.String("2001:db8::1")},
				{IpAddress: nil},
			},
			wantIPv4: 2,
			wantIPv6: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotIPv4, gotIPv6 := countNSXAddressBindingsByFamily(tt.bindings)
			if gotIPv4 != tt.wantIPv4 {
				t.Errorf("countNSXAddressBindingsByFamily() gotIPv4 = %v, want %v", gotIPv4, tt.wantIPv4)
			}
			if gotIPv6 != tt.wantIPv6 {
				t.Errorf("countNSXAddressBindingsByFamily() gotIPv6 = %v, want %v", gotIPv6, tt.wantIPv6)
			}
		})
	}
}

func TestReleasePortInSubnet(t *testing.T) {
	service := &SubnetPortService{
		SubnetPortStore: &SubnetPortStore{
			PortCountInfo: sync.Map{},
		},
	}

	subnetPath := "test-subnet"

	// Create CountInfo to put in store
	info := &CountInfo{
		dirtyDhcpCount:       5,
		dirtyDhcpCountIPv6:   5,
		dirtyStaticCount:     5,
		dirtyStaticCountIPv6: 5,
	}
	service.SubnetPortStore.PortCountInfo.Store(subnetPath, info)

	tests := []struct {
		name                   string
		path                   string
		interfaceIPType        v1alpha1.IPAddressType
		staticIPAllocationType v1alpha1.StaticIPAllocationType
		addressBindings        []v1alpha1.PortAddressBinding
		expectedDhcpCount      int
		expectedDhcpV6Count    int
		expectedStaticCount    int
		expectedStaticV6Count  int
	}{
		{
			name:                   "Subnet not found",
			path:                   "non-existent-subnet",
			interfaceIPType:        v1alpha1.IPAddressTypeIPv4,
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone,
			addressBindings:        nil,
			expectedDhcpCount:      5,
		},
		{
			name:                   "Release DHCP IPv4 (no explicit bindings)",
			path:                   subnetPath,
			interfaceIPType:        v1alpha1.IPAddressTypeIPv4,
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone,
			addressBindings:        nil,
			expectedDhcpCount:      4,
			expectedDhcpV6Count:    5,
			expectedStaticCount:    5,
			expectedStaticV6Count:  5,
		},
		{
			name:                   "Release Static IPv4 (single binding)",
			path:                   subnetPath,
			interfaceIPType:        v1alpha1.IPAddressTypeIPv4,
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4,
			addressBindings: []v1alpha1.PortAddressBinding{
				{IPAddress: "192.168.1.1"},
			},
			expectedDhcpCount:     4,
			expectedDhcpV6Count:   5,
			expectedStaticCount:   4,
			expectedStaticV6Count: 5,
		},
		{
			name:                   "Release Static IPv6 (multiple bindings)",
			path:                   subnetPath,
			interfaceIPType:        v1alpha1.IPAddressTypeIPv6,
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv6,
			addressBindings: []v1alpha1.PortAddressBinding{
				{IPAddress: "2001:db8::1"},
				{IPAddress: "2001:db8::2"},
			},
			expectedDhcpCount:     4,
			expectedDhcpV6Count:   5,
			expectedStaticCount:   4,
			expectedStaticV6Count: 3,
		},
		{
			name:                   "Release DHCP IPv4IPv6 (no explicit bindings)",
			path:                   subnetPath,
			interfaceIPType:        v1alpha1.IPAddressTypeIPv4IPv6,
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone,
			addressBindings:        nil,
			expectedDhcpCount:      3,
			expectedDhcpV6Count:    4,
			expectedStaticCount:    4,
			expectedStaticV6Count:  3,
		},
		{
			name:                   "Underflow protection DHCP",
			path:                   subnetPath,
			interfaceIPType:        v1alpha1.IPAddressTypeIPv4,
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone,
			addressBindings: []v1alpha1.PortAddressBinding{
				{}, {}, {}, {}, {}, {}, {}, // Try to release 7 when only 3 are left
			},
			expectedDhcpCount:     3, // Expected behavior: error logged, counter unchanged
			expectedDhcpV6Count:   4,
			expectedStaticCount:   4,
			expectedStaticV6Count: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service.ReleasePortInSubnet(tt.path, tt.interfaceIPType, tt.staticIPAllocationType, tt.addressBindings)

			if tt.path == subnetPath {
				assert.Equal(t, tt.expectedDhcpCount, info.dirtyDhcpCount)
				assert.Equal(t, tt.expectedDhcpV6Count, info.dirtyDhcpCountIPv6)
				assert.Equal(t, tt.expectedStaticCount, info.dirtyStaticCount)
				assert.Equal(t, tt.expectedStaticV6Count, info.dirtyStaticCountIPv6)
			}
		})
	}
}
