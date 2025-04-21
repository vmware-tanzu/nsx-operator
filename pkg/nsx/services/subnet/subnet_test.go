package subnet

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	apierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	mock_org_root "github.com/vmware-tanzu/nsx-operator/pkg/mock/orgrootclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/realizestate"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func TestGenerateSubnetNSTags(t *testing.T) {
	scheme := clientgoscheme.Scheme
	v1alpha1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	service := &SubnetService{
		Service: common.Service{
			Client:    fakeClient,
			NSXClient: &nsx.Client{},

			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint:   "vmc-enforcementpoint",
					UseAVILoadBalancer: false,
				},
			},
		},
	}

	// Create a test namespace
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-ns",
			UID:  "namespace-uid",
			Labels: map[string]string{
				"env": "test",
			},
		},
	}

	assert.NoError(t, fakeClient.Create(context.TODO(), namespace))

	// Define the Subnet object
	subnet := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-subnet",
			Namespace: "test-ns",
		},
	}

	// Generate tags for the Subnet
	tags := service.GenerateSubnetNSTags(subnet)

	// Validate the tags
	assert.NotNil(t, tags)
	assert.Equal(t, 3, len(tags)) // 3 tags should be generated

	// Check specific tags
	assert.Equal(t, "namespace-uid", *tags[0].Tag)
	assert.Equal(t, "test-ns", *tags[1].Tag)
	assert.Equal(t, "test", *tags[2].Tag)

	// Define the SubnetSet object
	subnetSet := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-subnet-set",
			Namespace: "test-ns",
			Labels: map[string]string{
				common.LabelDefaultSubnetSet: common.LabelDefaultPodSubnetSet,
			},
		},
	}

	// Generate tags for the SubnetSet
	tagsSet := service.GenerateSubnetNSTags(subnetSet)

	// Validate the tags for SubnetSet
	assert.NotNil(t, tagsSet)
	assert.Equal(t, 3, len(tagsSet)) // 3 tags should be generated
	assert.Equal(t, "namespace-uid", *tagsSet[0].Tag)
	assert.Equal(t, "test-ns", *tagsSet[1].Tag)
}

type fakeSubnetsClient struct{}

func (f fakeSubnetsClient) Delete(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string) error {
	return nil
}

func (f fakeSubnetsClient) Get(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string) (model.VpcSubnet, error) {
	return model.VpcSubnet{}, nil
}

func (f fakeSubnetsClient) List(orgIdParam string, projectIdParam string, vpcIdParam string, cursorParam *string, includeMarkForDeleteObjectsParam *bool, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.VpcSubnetListResult, error) {
	return model.VpcSubnetListResult{}, nil
}

func (f fakeSubnetsClient) Patch(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, vpcSubnetParam model.VpcSubnet) error {
	return nil
}

func (f fakeSubnetsClient) Update(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, vpcSubnetParam model.VpcSubnet) (model.VpcSubnet, error) {
	return model.VpcSubnet{}, nil
}

type fakeSubnetStatusClient struct{}

func (f fakeSubnetStatusClient) List(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string) (model.VpcSubnetStatusListResult, error) {
	return model.VpcSubnetStatusListResult{}, nil
}

type fakeRealizedEntitiesClient struct{}

func (f fakeRealizedEntitiesClient) List(intentPathParam string, sitePathParam *string) (model.GenericPolicyRealizedResourceListResult, error) {
	// GenericPolicyRealizedResource
	state := model.GenericPolicyRealizedResource_STATE_REALIZED
	return model.GenericPolicyRealizedResourceListResult{
		Results: []model.GenericPolicyRealizedResource{
			{
				State:      &state,
				EntityType: common.String("RealizedLogicalPort"),
			},
		},
	}, nil
}

type fakeStatusWriter struct{}

func (writer fakeStatusWriter) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	return nil
}

func (writer fakeStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return nil
}

func (writer fakeStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	return nil
}

func TestInitializeSubnetService(t *testing.T) {
	clusterName := "k8scl-one:test"
	subnetID := "fakeSubnetUID"
	subnetName := "fakeSubnetName"
	nsName := "fakeNamespaceName"
	subnet := &v1alpha1.Subnet{
		TypeMeta:   metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{UID: types.UID(subnetID), Name: subnetName, Namespace: nsName},
		Spec:       v1alpha1.SubnetSpec{},
		Status:     v1alpha1.SubnetStatus{},
	}
	nsxSubnetID := util.GenerateIDByObject(subnet)

	basicTags := util.BuildBasicTags(clusterName, subnet, "")
	fakeVPCPath := "/orgs/default/projects/nsx_operator_e2e_test/vpcs/subnet-e2e_8f36f7fc-90cd-4e65-a816-daf3ecd6a0f9"
	fakeSubnetPath := fmt.Sprintf("%s/subnet-e2e_8f36f7fc-90cd-4e65-a816-daf3ecd6a0f9/subnets/%s", fakeVPCPath, nsxSubnetID)

	vpcResourceInfo, _ := common.ParseVPCResourcePath(fakeVPCPath)

	var mockOrgRootClient *mock_org_root.MockOrgRootClient
	testCases := []struct {
		name                          string
		prepareFunc                   func() *gomonkey.Patches
		existingSubnetCR              *v1alpha1.Subnet
		existingVPCInfo               *common.VPCResourceInfo
		subnetCRTags                  []model.Tag
		expectAllSubnetNum            int
		expectAllSubnetNumAfterCreate int
		expectCreateSubnetUID         string
	}{
		{
			name:               "Subnet does not exist",
			existingSubnetCR:   subnet,
			expectAllSubnetNum: 0,
			existingVPCInfo:    &vpcResourceInfo,
			prepareFunc: func() *gomonkey.Patches {
				fakeVpcSubnet := model.VpcSubnet{Path: &fakeSubnetPath, Id: &nsxSubnetID, Tags: basicTags, ParentPath: &fakeVPCPath}
				patches := gomonkey.ApplyMethod(reflect.TypeOf(&fakeSubnetsClient{}), "Get", func(_ *fakeSubnetsClient, orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string) (model.VpcSubnet, error) {
					return fakeVpcSubnet, nil
				})
				// OrgRootClient.Patch is called for cleanup.
				mockOrgRootClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil).Times(1)
				return patches
			},
			subnetCRTags:                  []model.Tag{},
			expectAllSubnetNumAfterCreate: 1,
			expectCreateSubnetUID:         nsxSubnetID,
		},
		{
			name:             "Subnet exists and not change",
			existingSubnetCR: subnet,
			existingVPCInfo:  &vpcResourceInfo,
			prepareFunc: func() *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(&fakeQueryClient{}), "List", func(_ *fakeQueryClient, _ string, _ *string, _ *string, _ *int64, _ *bool, _ *string) (model.SearchResponse, error) {
					cursor := "1"
					resultCount := int64(1)
					tags := data.NewListValue()
					for _, basicTag := range basicTags {
						tags.Add(data.NewStructValue("", map[string]data.DataValue{"scope": data.NewStringValue(*basicTag.Scope), "tag": data.NewStringValue(*basicTag.Tag)}))
					}
					mode := "DHCP_DEACTIVATED"
					dhcpConfig := data.NewStructValue("", map[string]data.DataValue{"mode": data.NewStringValue(mode)})
					return model.SearchResponse{
						Results: []*data.StructValue{data.NewStructValue("",
							map[string]data.DataValue{
								"resource_type":      data.NewStringValue("VpcSubnet"),
								"id":                 data.NewStringValue(nsxSubnetID),
								"display_name":       data.NewStringValue(subnetName),
								"path":               data.NewStringValue(fakeSubnetPath),
								"parent_path":        data.NewStringValue(fakeVPCPath),
								"tags":               tags,
								"subnet_dhcp_config": dhcpConfig,
							})},
						Cursor: &cursor, ResultCount: &resultCount,
					}, nil
				})
				// OrgRootClient.Patch is called for cleanup.
				mockOrgRootClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil).Times(1)
				return patches
			},
			expectAllSubnetNum:            1,
			expectAllSubnetNumAfterCreate: 1,
			expectCreateSubnetUID:         nsxSubnetID,
		},
		{
			name:             "Subnet exists and changed",
			existingSubnetCR: subnet,
			existingVPCInfo:  &vpcResourceInfo,
			prepareFunc: func() *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(&fakeQueryClient{}), "List", func(_ *fakeQueryClient, _ string, _ *string, _ *string, _ *int64, _ *bool, _ *string) (model.SearchResponse, error) {
					cursor := "1"
					resultCount := int64(1)
					tags := data.NewListValue()
					for _, basicTag := range basicTags {
						tags.Add(data.NewStructValue("", map[string]data.DataValue{"scope": data.NewStringValue(*basicTag.Scope), "tag": data.NewStringValue(*basicTag.Tag)}))
					}
					tags.Add(data.NewStructValue("", map[string]data.DataValue{"scope": data.NewStringValue("fakeScope"), "tag": data.NewStringValue("fakeTag")}))
					return model.SearchResponse{
						Results: []*data.StructValue{data.NewStructValue("",
							map[string]data.DataValue{
								"resource_type": data.NewStringValue("VpcSubnet"),
								"id":            data.NewStringValue(nsxSubnetID),
								"display_name":  data.NewStringValue(subnetName),
								"path":          data.NewStringValue(fakeSubnetPath),
								"parent_path":   data.NewStringValue(fakeVPCPath),
								"tags":          tags,
							})},
						Cursor: &cursor, ResultCount: &resultCount,
					}, nil
				})
				fakeVpcSubnet := model.VpcSubnet{Path: &fakeSubnetPath, Id: &nsxSubnetID, Tags: basicTags, ParentPath: &fakeVPCPath}
				patches.ApplyMethod(reflect.TypeOf(&fakeSubnetsClient{}), "Get", func(_ *fakeSubnetsClient, orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string) (model.VpcSubnet, error) {
					return fakeVpcSubnet, nil
				})
				// OrgRootClient.Patch is called for cleanup.
				mockOrgRootClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil).Times(1)
				return patches
			},
			expectAllSubnetNum:            1,
			expectAllSubnetNumAfterCreate: 1,
			expectCreateSubnetUID:         nsxSubnetID,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			mockOrgRootClient = mock_org_root.NewMockOrgRootClient(ctrl)

			newScheme := runtime.NewScheme()
			utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
			utilruntime.Must(v1alpha1.AddToScheme(newScheme))
			fakeClient := fake.NewClientBuilder().WithScheme(newScheme).Build()
			commonService := common.Service{
				Client: fakeClient,
				NSXClient: &nsx.Client{
					OrgRootClient:          mockOrgRootClient,
					SubnetsClient:          &fakeSubnetsClient{},
					QueryClient:            &fakeQueryClient{},
					RealizedEntitiesClient: &fakeRealizedEntitiesClient{},
					NsxConfig: &config.NSXOperatorConfig{
						CoeConfig: &config.CoeConfig{
							Cluster: clusterName,
						},
					},
				},
				NSXConfig: &config.NSXOperatorConfig{
					CoeConfig: &config.CoeConfig{
						Cluster: clusterName,
					},
				},
			}
			var patches *gomonkey.Patches
			if tc.prepareFunc != nil {
				patches = tc.prepareFunc()
				defer patches.Reset()
			}

			service, err := InitializeSubnetService(commonService)

			assert.NoError(t, err)
			res := service.ListAllSubnet()
			assert.Equal(t, tc.expectAllSubnetNum, len(res))

			createdNSXSubnet, err := service.CreateOrUpdateSubnet(tc.existingSubnetCR, *tc.existingVPCInfo, tc.subnetCRTags)
			assert.NoError(t, err)
			assert.Equal(t, tc.expectCreateSubnetUID, *createdNSXSubnet.Id)

			res = service.ListAllSubnet()

			assert.Equal(t, tc.expectAllSubnetNumAfterCreate, len(res))

			nsxSubnets := service.GetSubnetsByIndex(common.TagScopeSubnetCRUID, subnetID)
			assert.Equal(t, 1, len(nsxSubnets))

			getByKey, err := service.GetSubnetByKey(nsxSubnetID)
			assert.NoError(t, err)
			assert.Equal(t, fakeSubnetPath, *getByKey.Path)

			getByPath, err := service.GetSubnetByPath(fakeSubnetPath)
			assert.NoError(t, err)
			assert.Equal(t, nsxSubnetID, *getByPath.Id)

			err = service.CleanupVPCChildResources(context.TODO(), "")
			assert.NoError(t, err)

			assert.Equal(t, 0, len(service.ListAllSubnet()))
		})
	}
}

func TestSubnetService_UpdateSubnetSet(t *testing.T) {
	fakeSubnetPath := "/orgs/default/projects/nsx_operator_e2e_test/vpcs/subnet-e2e_xxxx/subnets/subnet_id"
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	service := &SubnetService{
		Service: common.Service{
			Client: k8sClient,
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "k8scl-one:test",
				},
			},
		},
		SubnetStore: &SubnetStore{
			ResourceStore: common.ResourceStore{
				Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
					common.TagScopeSubnetCRUID:    subnetIndexFunc,
					common.TagScopeSubnetSetCRUID: subnetSetIndexFunc,
					common.TagScopeVMNamespace:    subnetIndexVMNamespaceFunc,
					common.TagScopeNamespace:      subnetIndexNamespaceFunc,
				}),
				BindingType: model.VpcSubnetBindingType(),
			},
		},
	}
	tags := []model.Tag{
		{
			Scope: common.String("nsx-op/subnet_uid"),
			Tag:   common.String("subnet-1"),
		},
		{
			Scope: common.String("nsx-op/subnetset_uid"),
			Tag:   common.String("subnetset-1"),
		},
		{
			Scope: common.String("nsx-op/namespace"),
			Tag:   common.String("ns-1"),
		},
	}
	vpcSubnets := []*model.VpcSubnet{
		{
			Path: &fakeSubnetPath,
			Id:   common.String("subnet-1"),
			Tags: tags,
			SubnetDhcpConfig: &model.SubnetDhcpConfig{
				Mode: common.String("DHCP_SERVER"),
			},
			AdvancedConfig: &model.SubnetAdvancedConfig{
				StaticIpAllocation: &model.StaticIpAllocation{
					Enabled: common.Bool(false),
				},
			},
		},
	}

	k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		subnetSet := obj.(*v1alpha1.SubnetSet)
		subnetSet.Namespace = "ns-1"
		subnetSet.Name = "subnetset-1"
		return nil
	})

	patchesCreateOrUpdateSubnet := gomonkey.ApplyFunc((*SubnetService).createOrUpdateSubnet,
		func(r *SubnetService, obj client.Object, nsxSubnet *model.VpcSubnet, vpcInfo *common.VPCResourceInfo) (*model.VpcSubnet, error) {
			return &model.VpcSubnet{Path: &fakeSubnetPath}, nil
		})
	defer patchesCreateOrUpdateSubnet.Reset()

	err := service.UpdateSubnetSet("ns-1", vpcSubnets, tags, "")
	assert.Nil(t, err)
}

func TestSubnetService_createOrUpdateSubnet(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	service := &SubnetService{
		Service: common.Service{
			Client: k8sClient,
			NSXClient: &nsx.Client{
				SubnetsClient:      &fakeSubnetsClient{},
				SubnetStatusClient: &fakeSubnetStatusClient{},
			},
		},
		SubnetStore: &SubnetStore{
			ResourceStore: common.ResourceStore{
				Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
					common.TagScopeSubnetCRUID:    subnetIndexFunc,
					common.TagScopeSubnetSetCRUID: subnetSetIndexFunc,
					common.TagScopeVMNamespace:    subnetIndexVMNamespaceFunc,
					common.TagScopeNamespace:      subnetIndexNamespaceFunc,
				}),
				BindingType: model.VpcSubnetBindingType(),
			},
		},
	}

	fakeSubnet := model.VpcSubnet{
		Id:   common.String("subnet-1"),
		Path: common.String("/orgs/default/projects/default/vpcs/default/subnets/subnet-path-1"),
		Tags: []model.Tag{
			{
				Scope: common.String(common.TagScopeSubnetSetCRUID),
				Tag:   common.String("subnetset-1"),
			},
		},
	}
	fakewriter := &fakeStatusWriter{}

	testCases := []struct {
		name                 string
		prepareFunc          func() *gomonkey.Patches
		expectedErr          string
		crObj                client.Object
		wantSubnetStoreCount int
	}{
		{
			name: "Update Subnet with RealizedState and deletion error",
			prepareFunc: func() *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*realizestate.RealizeStateService).CheckRealizeState,
					func(_ *realizestate.RealizeStateService, _ wait.Backoff, _ string, _ []string) error {
						return nsxutil.NewRealizeStateError("mocked realized error", 0)
					})
				patches.ApplyFunc((*SubnetService).DeleteSubnet, func(_ *SubnetService, _ model.VpcSubnet) error {
					return errors.New("mocked deletion error")
				})
				patches.ApplyFunc(fakeSubnetsClient.Get, func(f fakeSubnetsClient, orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string) (model.VpcSubnet, error) {
					return fakeSubnet, nil
				})
				return patches
			},
			crObj:                &v1alpha1.Subnet{},
			expectedErr:          "realization check failed: mocked realized error; deletion failed: mocked deletion error",
			wantSubnetStoreCount: 0,
		},
		{
			name: "Create Subnet for SubnetSet Failure",
			prepareFunc: func() *gomonkey.Patches {
				patches := gomonkey.ApplyFunc(fakeSubnetsClient.Patch, func(f fakeSubnetsClient, orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, nsxSubnet model.VpcSubnet) error {
					return apierrors.NewInvalidRequest()
				})
				return patches
			},
			crObj:                &v1alpha1.Subnet{},
			expectedErr:          "com.vmware.vapi.std.errors.invalid_request",
			wantSubnetStoreCount: 0,
		},
		{
			name: "Create Subnet for SubnetSet Success",
			prepareFunc: func() *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*realizestate.RealizeStateService).CheckRealizeState,
					func(_ *realizestate.RealizeStateService, _ wait.Backoff, _ string, _ []string) error {
						return nil
					})
				patches.ApplyFunc(fakeSubnetsClient.Get,
					func(f fakeSubnetsClient, orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string) (model.VpcSubnet, error) {
						return fakeSubnet, nil
					})
				patches.ApplyFunc(fakeSubnetStatusClient.List,
					func(_ fakeSubnetStatusClient, orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string) (model.VpcSubnetStatusListResult, error) {
						return model.VpcSubnetStatusListResult{
							Results: []model.VpcSubnetStatus{
								{
									NetworkAddress:    common.String("10.0.0.0/28"),
									GatewayAddress:    common.String("10.0.0.1/28"),
									DhcpServerAddress: common.String("10.0.0.2/28"),
								},
							},
						}, nil
					})
				k8sClient.EXPECT().Status().Return(fakewriter)
				patches.ApplyFunc(fakeStatusWriter.Update,
					func(writer fakeStatusWriter, ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
						subnetSet := obj.(*v1alpha1.SubnetSet)
						assert.Equal(t, 1, len(subnetSet.Status.Subnets))
						assert.Equal(t, "10.0.0.0/28", subnetSet.Status.Subnets[0].NetworkAddresses[0])
						assert.Equal(t, "10.0.0.1/28", subnetSet.Status.Subnets[0].GatewayAddresses[0])
						assert.Equal(t, "10.0.0.2/28", subnetSet.Status.Subnets[0].DHCPServerAddresses[0])
						return nil
					})
				return patches
			},
			crObj: &v1alpha1.SubnetSet{
				ObjectMeta: metav1.ObjectMeta{UID: "subnetset-1"},
			},
			wantSubnetStoreCount: 1,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			if tt.prepareFunc != nil {
				patches := tt.prepareFunc()
				defer patches.Reset()
			}
			nsxSubnet, err := service.createOrUpdateSubnet(
				tt.crObj,
				&fakeSubnet,
				&common.VPCResourceInfo{},
			)
			if tt.expectedErr != "" {
				assert.Equal(t, tt.expectedErr, err.Error())
			} else {
				assert.Nil(t, err)
				assert.Equal(t, fakeSubnet, *nsxSubnet)
			}
			assert.Equal(t, tt.wantSubnetStoreCount, len(service.SubnetStore.ListKeys()))
		})
	}
}

func TestSubnetService_DeleteSubnet(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	service := &SubnetService{
		Service: common.Service{
			Client: k8sClient,
			NSXClient: &nsx.Client{
				SubnetsClient: &fakeSubnetsClient{},
			},
		},
		SubnetStore: &SubnetStore{
			ResourceStore: common.ResourceStore{
				Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
					common.TagScopeSubnetCRUID:    subnetIndexFunc,
					common.TagScopeSubnetSetCRUID: subnetSetIndexFunc,
					common.TagScopeVMNamespace:    subnetIndexVMNamespaceFunc,
					common.TagScopeNamespace:      subnetIndexNamespaceFunc,
				}),
				BindingType: model.VpcSubnetBindingType(),
			},
		},
	}

	fakeSubnet := model.VpcSubnet{
		Id:   common.String("subnet-1"),
		Path: common.String("/orgs/default/projects/default/vpcs/default/subnets/subnet-path-1"),
		Tags: []model.Tag{
			{
				Scope: common.String(common.TagScopeSubnetSetCRUID),
				Tag:   common.String("subnetset-1"),
			},
		},
	}

	testCases := []struct {
		name                 string
		prepareFunc          func() *gomonkey.Patches
		expectedErr          string
		wantSubnetStoreCount int
	}{
		{
			name: "Delete Subnet for SubnetSet Failure",
			prepareFunc: func() *gomonkey.Patches {
				service.SubnetStore.Apply(&fakeSubnet)

				patches := gomonkey.ApplyFunc(fakeSubnetsClient.Delete, func(f fakeSubnetsClient, orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string) error {
					return apierrors.NewInvalidRequest()
				})
				return patches
			},
			expectedErr:          "com.vmware.vapi.std.errors.invalid_request",
			wantSubnetStoreCount: 1,
		},
		{
			name: "Delete Subnet for SubnetSet Success",
			prepareFunc: func() *gomonkey.Patches {
				service.SubnetStore.Apply(&fakeSubnet)

				patches := gomonkey.ApplyFunc(fakeSubnetsClient.Delete, func(f fakeSubnetsClient, orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string) error {
					return nil
				})
				return patches
			},
			wantSubnetStoreCount: 0,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			if tt.prepareFunc != nil {
				patches := tt.prepareFunc()
				defer patches.Reset()
			}
			err := service.DeleteSubnet(fakeSubnet)
			if tt.expectedErr != "" {
				assert.Equal(t, tt.expectedErr, err.Error())
			} else {
				assert.Nil(t, err)
			}
			assert.Equal(t, tt.wantSubnetStoreCount, len(service.SubnetStore.ListKeys()))
		})
	}
}
