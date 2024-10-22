package subnetport

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

var (
	subnetPortName       = "subnetport-name"
	subnetPortId1        = "subnetport-name_00000000-0000-0000-0000-000000000001"
	subnetPortPath1      = "/orgs/org1/projects/project1/vpcs/vpc1/subnets/subnet1/ports/subnetport-name_00000000-0000-0000-0000-000000000001"
	subnetPortId2        = "subnetport-name_00000000-0000-0000-0000-000000000002"
	subnetPortPath2      = "/orgs/org1/projects/project1/vpcs/vpc1/subnets/subnet1/ports/subnetport-name_00000000-0000-0000-0000-000000000002"
	subnetPortEntityType = "RealizedLogicalPort"
	stateRealized        = "REALIZED"
	subnetId             = "subnet1"
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

func (c *fakeRealizedEntitiesClient) List(orgIdParam string, projectIdParam string, intentPathParam string, sitePathParam *string) (model.GenericPolicyRealizedResourceListResult, error) {

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

type fakeSubnetStatusClient struct{}

func (c *fakeSubnetStatusClient) List(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string) (model.VpcSubnetStatusListResult, error) {
	return model.VpcSubnetStatusListResult{}, nil
}

func Test_InitializeSubnetPort(t *testing.T) {
	tests := []struct {
		name        string
		prepareFunc func(*testing.T, *common.Service, context.Context) *gomonkey.Patches
		wantErr     bool
	}{
		{
			name: "error",
			prepareFunc: func(t *testing.T, s *common.Service, ctx context.Context) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.QueryClient, "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.SearchResponse{}, fmt.Errorf("mock error")},
					Times:  1,
				}})
				return patches
			},
			wantErr: true,
		},
		{
			name: "success",
			prepareFunc: func(t *testing.T, s *common.Service, ctx context.Context) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(s.NSXClient.QueryClient, "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.SearchResponse{}, nil},
					Times:  1,
				}})
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
			patches := tt.prepareFunc(t, &commonService, ctx)
			defer patches.Reset()
			got, err := InitializeSubnetPort(commonService)
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
	commonService := common.Service{
		Client: k8sClient,
		NSXClient: &nsx.Client{
			QueryClient:            &fakeQueryClient{},
			PortClient:             &fakePortClient{},
			RealizedEntitiesClient: &fakeRealizedEntitiesClient{},
			PortStateClient:        &fakePortStateClient{},
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
	service := &SubnetPortService{
		Service: commonService,
		SubnetPortStore: &SubnetPortStore{ResourceStore: common.ResourceStore{
			Indexer: cache.NewIndexer(
				keyFunc,
				cache.Indexers{
					common.TagScopeSubnetPortCRUID: subnetPortIndexByCRUID,
					common.TagScopePodUID:          subnetPortIndexByPodUID,
					common.IndexKeySubnetID:        subnetPortIndexBySubnetID,
				}),
			BindingType: model.VpcSubnetPortBindingType(),
		}},
	}

	subnetPortCR := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      subnetPortName,
			Namespace: namespace,
			UID:       "00000000-0000-0000-0000-000000000001",
		},
	}

	enableDHCP := true
	nsxSubnet := &model.VpcSubnet{
		Path: &subnetPath,
		DhcpConfig: &model.VpcSubnetDhcpConfig{
			EnableDhcp: &enableDHCP,
		},
	}

	nsxSubnetPort := model.VpcSubnetPort{
		Id:         &subnetPortId1,
		Path:       &subnetPortPath1,
		Attachment: &model.PortAttachment{},
	}

	tests := []struct {
		name        string
		prepareFunc func(service *SubnetPortService) *gomonkey.Patches
		wantErr     bool
	}{
		{
			name: "Create",
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					namespaceCR := &corev1.Namespace{}
					namespaceCR.UID = "ns1"
					return nil
				})
				return nil
			},
			wantErr: false,
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
				return nil
			},
			wantErr: false,
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
					Values: gomonkey.Params{model.GenericPolicyRealizedResourceListResult{}, fmt.Errorf("ERROR")},
					Times:  1,
				}})
				return patches
			},
			wantErr: true,
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
				return patches
			},
			wantErr: true,
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
				return patches
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := tt.prepareFunc(service)
			if patches != nil {
				defer patches.Reset()
			}
			_, err := service.CreateOrUpdateSubnetPort(subnetPortCR, nsxSubnet, "", nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateOrUpdateSubnetPort() error = %v, wantErr %v", err, tt.wantErr)
			}
			err = service.Cleanup(context.TODO())
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
							common.IndexKeySubnetID:        subnetPortIndexBySubnetID,
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

func TestSubnetPortService_GetGatewayPrefixForSubnetPort(t *testing.T) {
	gatewayAddress := "10.0.0.1/26"
	invalidGatewayAddress1 := "10.0.0.256"
	invalidGatewayAddress2 := "10.0.0.1/a"
	tests := []struct {
		name        string
		prepareFunc func(service *SubnetPortService) *gomonkey.Patches
		wantErr     bool
	}{
		{
			name: "Success",
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(service.NSXClient.SubnetStatusClient, "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.VpcSubnetStatusListResult{
						Results: []model.VpcSubnetStatus{
							{GatewayAddress: &gatewayAddress},
						},
					}, nil},
					Times: 1,
				}})
				return patches
			},
			wantErr: false,
		},
		{
			name: "ListSubnetStatusFailure",
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(service.NSXClient.SubnetStatusClient, "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.VpcSubnetStatusListResult{}, fmt.Errorf("mock error")},
					Times:  1,
				}})
				return patches
			},
			wantErr: true,
		},
		{
			name: "EmptySubnetStatus",
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(service.NSXClient.SubnetStatusClient, "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.VpcSubnetStatusListResult{
						Results: []model.VpcSubnetStatus{},
					}, nil},
					Times: 1,
				}})
				return patches
			},
			wantErr: true,
		},
		{
			name: "NoGatewayAddress",
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(service.NSXClient.SubnetStatusClient, "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.VpcSubnetStatusListResult{
						Results: []model.VpcSubnetStatus{
							{GatewayAddress: nil},
						},
					}, nil},
					Times: 1,
				}})
				return patches
			},
			wantErr: true,
		},
		{
			name: "InvalidIP",
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(service.NSXClient.SubnetStatusClient, "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.VpcSubnetStatusListResult{
						Results: []model.VpcSubnetStatus{
							{GatewayAddress: &invalidGatewayAddress1},
						},
					}, nil},
					Times: 1,
				}})
				return patches
			},
			wantErr: true,
		},
		{
			name: "InvalidIP",
			prepareFunc: func(service *SubnetPortService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(service.NSXClient.SubnetStatusClient, "List", []gomonkey.OutputCell{{
					Values: gomonkey.Params{model.VpcSubnetStatusListResult{
						Results: []model.VpcSubnetStatus{
							{GatewayAddress: &invalidGatewayAddress2},
						},
					}, nil},
					Times: 1,
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
					SubnetStatusClient: &fakeSubnetStatusClient{},
				},
			}
			service := &SubnetPortService{
				Service: commonService,
			}

			patches := tt.prepareFunc(service)
			if patches != nil {
				defer patches.Reset()
			}
			gateway, prefix, err := service.GetGatewayPrefixForSubnetPort(nil, subnetPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("DeleteSubnetPort() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil {
				assert.Equal(t, "10.0.0.1", gateway)
				assert.Equal(t, 26, prefix)
			}
		})
	}
}

func TestSubnetPortService_GetSubnetPathForSubnetPortFromStore(t *testing.T) {
	type args struct {
		obj *model.VpcSubnetPort
	}
	tests := []struct {
		name           string
		args           args
		expectedResult string
	}{
		{
			name: "Success",
			args: args{&model.VpcSubnetPort{
				Id:         &subnetPortId1,
				Path:       &subnetPortPath1,
				ParentPath: &subnetPath,
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
							common.IndexKeySubnetID:        subnetPortIndexBySubnetID,
						}),
					BindingType: model.VpcSubnetPortBindingType(),
				}},
			}

			if tt.args.obj != nil {
				service.SubnetPortStore.Add(tt.args.obj)
				defer service.SubnetPortStore.Delete(tt.args.obj)
			}

			result := service.GetSubnetPathForSubnetPortFromStore(subnetPortId1)
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
					common.IndexKeySubnetID: subnetPortIndexBySubnetID,
				}),
			BindingType: model.VpcSubnetPortBindingType(),
		}},
	}
	service.SubnetPortStore.Add(&port)
	ports := service.GetPortsOfSubnet(subnetId)
	assert.Equal(t, port, *ports[0])
}

func TestSubnetPortService_Cleanup(t *testing.T) {
	port := model.VpcSubnetPort{
		Id:         &subnetPortId1,
		Path:       &subnetPortPath1,
		ParentPath: &subnetPath,
	}
	service := &SubnetPortService{
		Service: common.Service{
			NSXClient: &nsx.Client{
				PortClient: &fakePortClient{},
			},
		},
		SubnetPortStore: &SubnetPortStore{ResourceStore: common.ResourceStore{
			Indexer: cache.NewIndexer(
				keyFunc,
				cache.Indexers{
					common.IndexKeySubnetID: subnetPortIndexBySubnetID,
				}),
			BindingType: model.VpcSubnetPortBindingType(),
		}},
	}
	service.SubnetPortStore.Add(&port)
	err := service.Cleanup(context.TODO())
	assert.Nil(t, err)
	assert.Nil(t, service.SubnetPortStore.GetByKey(*port.Id))
}

func TestSubnetPortService_ListNSXSubnetPortIDForCR(t *testing.T) {
	subnetPortService := createSubnetPortService()
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
			Type_:             common.String("STATIC"),
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
	subnetPortService := createSubnetPortService()
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
			Type_:             common.String("STATIC"),
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
		Service: commonService,
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
	crSubnetPortIDsSet, err := service.ListSubnetPortIDsFromCRs(context.TODO())
	assert.Nil(t, err)
	assert.Equal(t, 2, crSubnetPortIDsSet.Len())
	assert.True(t, crSubnetPortIDsSet.Has("subnetPort1_1"))
	assert.True(t, crSubnetPortIDsSet.Has("subnetPort2_2"))
}

func TestSubnetPortService_ListSubnetPortByName(t *testing.T) {
	subnetPortService := createSubnetPortService()
	subnetPort1 := &model.VpcSubnetPort{
		Id:   &subnetPortId1,
		Path: &subnetPortPath1,
		Tags: []model.Tag{
			{
				Scope: common.String("nsx-op/vm_namespace"),
				Tag:   common.String("ns-1"),
			},
			{
				Scope: common.String("nsx-op/subnetport_name"),
				Tag:   common.String("subnetport-1"),
			},
		},
	}
	subnetPort2 := &model.VpcSubnetPort{
		Id:   &subnetPortId2,
		Path: &subnetPortPath2,
		Tags: []model.Tag{
			{
				Scope: common.String("nsx-op/vm_namespace"),
				Tag:   common.String("ns-1"),
			},
			{
				Scope: common.String("nsx-op/subnetport_name"),
				Tag:   common.String("subnetport-2"),
			},
		},
	}
	subnetPortService.SubnetPortStore.Add(subnetPort1)
	subnetPortService.SubnetPortStore.Add(subnetPort2)
	subnetPorts := subnetPortService.ListSubnetPortByName("ns-1", "subnetport-1")
	assert.Equal(t, 1, len(subnetPorts))
	assert.Equal(t, subnetPort1, subnetPorts[0])
}

func TestSubnetPortService_ListSubnetPortByPodName(t *testing.T) {
	subnetPortService := createSubnetPortService()
	subnetPort1 := &model.VpcSubnetPort{
		Id:   &subnetPortId1,
		Path: &subnetPortPath1,
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
		Id:   &subnetPortId2,
		Path: &subnetPortPath2,
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

func createSubnetPortService() *SubnetPortService {
	return &SubnetPortService{
		SubnetPortStore: &SubnetPortStore{ResourceStore: common.ResourceStore{
			Indexer: cache.NewIndexer(
				keyFunc,
				cache.Indexers{
					common.TagScopeSubnetPortCRUID: subnetPortIndexByCRUID,
					common.TagScopePodUID:          subnetPortIndexByPodUID,
					common.TagScopeVMNamespace:     subnetPortIndexNamespace,
					common.TagScopeNamespace:       subnetPortIndexPodNamespace,
					common.IndexKeySubnetID:        subnetPortIndexBySubnetID,
				}),
			BindingType: model.VpcSubnetPortBindingType(),
		}},
	}
}
