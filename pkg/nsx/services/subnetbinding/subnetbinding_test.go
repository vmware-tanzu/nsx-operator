package subnetbinding

import (
	"context"
	"fmt"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	orgroot_mocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/orgrootclient"
	search_mocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/searchclient"
	bindingmap_mocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/subnetconnectionbindingmapclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

var (
	createdBM1 = &model.SubnetConnectionBindingMap{
		Id:             String(bm1ID),
		DisplayName:    String("binding1"),
		SubnetPath:     String(parentSubnetPath1),
		ParentPath:     String(childSubnetPath1),
		Path:           String(fmt.Sprintf("%s/subnet-connection-binding-maps/%s", childSubnetPath1, bm1ID)),
		VlanTrafficTag: Int64(201),
		ResourceType:   String(ResourceTypeSubnetConnectionBindingMap),
		Tags: []model.Tag{
			{
				Scope: String(common.TagScopeCluster),
				Tag:   String("fake_cluster"),
			},
			{
				Scope: String(common.TagScopeVersion),
				Tag:   String("1.0.0"),
			},
			{
				Scope: String(common.TagScopeNamespace),
				Tag:   String("default"),
			},
			{
				Scope: String(common.TagScopeSubnetBindingCRName),
				Tag:   String("binding1"),
			},
			{
				Scope: String(common.TagScopeSubnetBindingCRUID),
				Tag:   String("uuid-binding1"),
			},
		},
	}
	createdBM2 = &model.SubnetConnectionBindingMap{
		Id:             String(bm2ID),
		DisplayName:    String("binding1"),
		SubnetPath:     String(parentSubnetPath2),
		ParentPath:     String(childSubnetPath1),
		Path:           String(fmt.Sprintf("%s/subnet-connection-binding-maps/%s", childSubnetPath1, bm2ID)),
		VlanTrafficTag: Int64(201),
		ResourceType:   String(ResourceTypeSubnetConnectionBindingMap),
		Tags: []model.Tag{
			{
				Scope: String(common.TagScopeCluster),
				Tag:   String("fake_cluster"),
			},
			{
				Scope: String(common.TagScopeVersion),
				Tag:   String("1.0.0"),
			},
			{
				Scope: String(common.TagScopeNamespace),
				Tag:   String("default"),
			},
			{
				Scope: String(common.TagScopeSubnetBindingCRName),
				Tag:   String("binding1"),
			},
			{
				Scope: String(common.TagScopeSubnetBindingCRUID),
				Tag:   String("uuid-binding1"),
			},
		},
	}
)

func TestGetCRNameBySubnetConnectionBindingMap(t *testing.T) {
	svc := mockService()
	crName := svc.GetSubnetConnectionBindingMapCRName(bindingMap1)
	require.Equal(t, "binding1", crName)
	crName = svc.GetSubnetConnectionBindingMapCRName(nil)
	require.Equal(t, "", crName)
	crName = svc.GetSubnetConnectionBindingMapCRName(&model.SubnetConnectionBindingMap{
		Id:             String(bm1ID),
		DisplayName:    String("binding1"),
		SubnetPath:     String(parentSubnetPath1),
		VlanTrafficTag: Int64(201)})
	require.Equal(t, "", crName)
}

func TestGetSubnetConnectionBindingMapCRsBySubnet(t *testing.T) {
	svc := mockService()
	svc.BindingStore = SetupStore()

	// Case: SubnetConnectionBindingMap does not exist.
	svc.BindingStore.Apply(nil)
	gotBMs := svc.GetSubnetConnectionBindingMapCRsBySubnet(parentSubnet1)
	require.Equal(t, 0, len(gotBMs))
	gotBMs = svc.GetSubnetConnectionBindingMapCRsBySubnet(childSubnet)
	require.Equal(t, 0, len(gotBMs))

	// Case: tags are missing on NSX SubnetConnectionBindingMap
	svc.BindingStore.Apply(incompleteBindingMap)
	gotBMs1 := svc.GetSubnetConnectionBindingMapCRsBySubnet(parentSubnet1)
	require.Equal(t, 0, len(gotBMs1))

	// Case: success.
	bindingMaps := svc.buildSubnetBindings(binding1, []*model.VpcSubnet{parentSubnet1})
	require.Equal(t, 1, len(bindingMaps))
	bm := bindingMaps[0]
	bm.ParentPath = childSubnet.Path
	bm.Path = String(fmt.Sprintf("%s/subnet-connection-binding-maps/%s", *bm.ParentPath, *bm.Id))
	svc.BindingStore.Apply(bm)

	gotBMs1 = svc.GetSubnetConnectionBindingMapCRsBySubnet(parentSubnet1)
	require.Equal(t, 1, len(gotBMs1))
	gotBinding := gotBMs1[0]
	assert.Equal(t, binding1.UID, gotBinding.UID)
	assert.Equal(t, binding1.Namespace, gotBinding.Namespace)
	assert.Equal(t, binding1.Name, gotBinding.Name)

	gotBMs2 := svc.GetSubnetConnectionBindingMapCRsBySubnet(childSubnet)
	require.Equal(t, 1, len(gotBMs1))
	gotBinding2 := gotBMs2[0]
	assert.Equal(t, gotBinding, gotBinding2)
}

func TestListSubnetConnectionBindingMapCRUIDsInStore(t *testing.T) {
	svc := mockService()
	svc.BindingStore = SetupStore()

	// Case: SubnetConnectionBindingMap with incomplete tags in store.
	svc.BindingStore.Apply(incompleteBindingMap)
	crIDs := svc.ListSubnetConnectionBindingMapCRUIDsInStore()
	require.Equal(t, 0, crIDs.Len())

	// Case: success
	bm := svc.buildSubnetBindings(binding1, []*model.VpcSubnet{parentSubnet1})[0]
	bm.ParentPath = String(childSubnetPath1)
	bm.Path = String(fmt.Sprintf("%s/subnet-connection-binding-maps/%s", *bm.ParentPath, *bm.Id))
	bm2 := svc.buildSubnetBindings(binding2, []*model.VpcSubnet{parentSubnet2})[0]
	bm2.ParentPath = String(childSubnetPath2)
	bm2.Path = String(fmt.Sprintf("%s/subnet-connection-binding-maps/%s", *bm2.ParentPath, *bm2.Id))
	svc.BindingStore.Apply(bm)
	svc.BindingStore.Apply(bm2)
	crIDs = svc.ListSubnetConnectionBindingMapCRUIDsInStore()
	require.Equal(t, 2, crIDs.Len())
	assert.ElementsMatch(t, []string{"uuid-binding1", "uuid-binding2"}, crIDs.UnsortedList())
}

func TestInitializeService(t *testing.T) {
	ctrl := gomock.NewController(t)
	fakeQueryClient := search_mocks.NewMockQueryClient(ctrl)
	commonService := common.Service{
		NSXClient: &nsx.Client{
			QueryClient: fakeQueryClient,
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

	for _, tc := range []struct {
		name            string
		prepareFunc     func()
		expErrString    string
		expCountInStore int
	}{
		{
			name: "Failed to search SubnetConnectionBindingMaps",
			prepareFunc: func() {
				fakeQueryClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					Return(model.SearchResponse{}, fmt.Errorf("NSX access error"))
			},
			expErrString: "NSX access error",
		}, {
			name: "Success to search SubnetConnectionBindingMaps",
			prepareFunc: func() {
				fakeQueryClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					Return(model.SearchResponse{}, nil)
			},
			expErrString: "",
		}, {
			name: "Multiple SubnetConnectionBindingMaps are searched",
			prepareFunc: func() {
				cursor := "1"
				resultCount := int64(1)
				dv, _ := common.NewConverter().ConvertToVapi(createdBM1, model.SubnetConnectionBindingMapBindingType())
				fakeQueryClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					Return(model.SearchResponse{
						ResultCount: &resultCount,
						Results:     []*data.StructValue{dv.(*data.StructValue)},
						Cursor:      &cursor,
					}, nil)
			},
			expErrString:    "",
			expCountInStore: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.prepareFunc()
			svc, err := InitializeService(commonService)
			if tc.expErrString != "" {
				require.EqualError(t, err, tc.expErrString)
			} else {
				require.Nil(t, err)
				values := svc.BindingStore.List()
				assert.Equal(t, tc.expCountInStore, len(values))
			}
		})
	}
}

func TestCreateOrUpdateSubnetConnectionBindingMap(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockOrgRootClient := orgroot_mocks.NewMockOrgRootClient(ctrl)
	mockSubnetBindingClient := bindingmap_mocks.NewMockSubnetConnectionBindingMapsClient(ctrl)

	oriBM2 := *bindingMap2
	oriBM2.Path = String(fmt.Sprintf("%s/subnet-connection-binding-maps/%s", *childSubnet.Path, *oriBM2.Id))
	oriBM2.VlanTrafficTag = Int64(200)

	expAddBM := *bindingMap2
	expAddBM.ResourceType = String(ResourceTypeSubnetConnectionBindingMap)
	expDelBM := *createdBM1
	expDelBM.MarkedForDelete = Bool(true)

	for _, tc := range []struct {
		name                  string
		prepareFunc           func()
		existingBindingMaps   []*model.SubnetConnectionBindingMap
		expErr                string
		expBindingMapsInStore []*model.SubnetConnectionBindingMap
	}{
		{
			name: "success to create new",
			prepareFunc: func() {
				count := int64(1)
				orgConfig := map[string]map[string]map[string]map[string][]*model.SubnetConnectionBindingMap{
					"default": {
						"default": {
							"vpc1": {
								"subnet1": []*model.SubnetConnectionBindingMap{
									&expAddBM,
								},
							},
						},
					}}
				expOrg, _ := wrapOrgRoot(orgConfig)
				mockOrgRootClient.EXPECT().Patch(&orgRootMatcher{expOrg}, &enforceRevisionCheckParam).Return(nil)
				mockSubnetBindingClient.EXPECT().List("default", "default", "vpc1", "subnet1", nil, nil, nil, nil, nil, nil).
					Return(model.SubnetConnectionBindingMapListResult{
						ResultCount: &count,
						Results: []model.SubnetConnectionBindingMap{
							*createdBM2,
						},
					}, nil)
			},
			existingBindingMaps:   []*model.SubnetConnectionBindingMap{},
			expBindingMapsInStore: []*model.SubnetConnectionBindingMap{createdBM2},
		}, {
			name: "success to update existing ones and delete stale ones",
			prepareFunc: func() {
				count := int64(1)
				orgConfig := map[string]map[string]map[string]map[string][]*model.SubnetConnectionBindingMap{
					"default": {
						"default": {
							"vpc1": {
								"subnet1": []*model.SubnetConnectionBindingMap{
									&expAddBM,
									&expDelBM,
								},
							},
						},
					}}
				expOrg, _ := wrapOrgRoot(orgConfig)
				mockOrgRootClient.EXPECT().Patch(&orgRootMatcher{expOrg}, &enforceRevisionCheckParam).Return(nil)
				mockSubnetBindingClient.EXPECT().List("default", "default", "vpc1", "subnet1", nil, nil, nil, nil, nil, nil).
					Return(model.SubnetConnectionBindingMapListResult{
						ResultCount: &count,
						Results: []model.SubnetConnectionBindingMap{
							*createdBM2,
						},
					}, nil)
			},
			existingBindingMaps:   []*model.SubnetConnectionBindingMap{createdBM1, &oriBM2},
			expBindingMapsInStore: []*model.SubnetConnectionBindingMap{createdBM2},
		}, {
			name: "failed to patch org root",
			prepareFunc: func() {
				mockOrgRootClient.EXPECT().Patch(gomock.Any(), &enforceRevisionCheckParam).Return(fmt.Errorf("fake-error"))
			},
			expErr:                "fake-error",
			existingBindingMaps:   []*model.SubnetConnectionBindingMap{createdBM1, &oriBM2},
			expBindingMapsInStore: []*model.SubnetConnectionBindingMap{createdBM1, &oriBM2},
		}, {
			name: "failed to list from NSX",
			prepareFunc: func() {
				mockOrgRootClient.EXPECT().Patch(gomock.Any(), &enforceRevisionCheckParam).Return(nil)
				mockSubnetBindingClient.EXPECT().List("default", "default", "vpc1", "subnet1", nil, nil, nil, nil, nil, nil).
					Return(model.SubnetConnectionBindingMapListResult{}, fmt.Errorf("fake-error"))
			},
			expErr:                "fake-error",
			existingBindingMaps:   []*model.SubnetConnectionBindingMap{createdBM1, &oriBM2},
			expBindingMapsInStore: []*model.SubnetConnectionBindingMap{createdBM1, &oriBM2},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &BindingService{
				Service: common.Service{
					NSXClient: &nsx.Client{
						OrgRootClient:                     mockOrgRootClient,
						SubnetConnectionBindingMapsClient: mockSubnetBindingClient,
					},
					NSXConfig: &config.NSXOperatorConfig{
						CoeConfig: &config.CoeConfig{
							Cluster: "fake_cluster",
						},
					},
				},
				BindingStore: SetupStore(),
			}
			svc.builder, _ = common.PolicyPathVpcSubnetConnectionBindingMap.NewPolicyTreeBuilder()
			for _, bm := range tc.existingBindingMaps {
				svc.BindingStore.Add(bm)
			}
			tc.prepareFunc()

			err := svc.CreateOrUpdateSubnetConnectionBindingMap(binding1, childSubnet, []*model.VpcSubnet{parentSubnet2})
			if tc.expErr != "" {
				require.EqualError(t, err, tc.expErr)
			} else {
				require.Nil(t, err)
			}

			bms := svc.BindingStore.List()
			assert.ElementsMatch(t, tc.expBindingMapsInStore, bms)
		})
	}
}

func TestDeleteMultiSubnetConnectionBindingMapsByCRs(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockOrgRootClient := orgroot_mocks.NewMockOrgRootClient(ctrl)

	for _, tc := range []struct {
		name                  string
		bindingCRIDs          []string
		prepareFunc           func()
		expErr                string
		expBindingMapsInStore []*model.SubnetConnectionBindingMap
	}{
		{
			name:                  "Empty CR IDs",
			bindingCRIDs:          []string{},
			expErr:                "",
			expBindingMapsInStore: []*model.SubnetConnectionBindingMap{createdBM1, createdBM2},
		},
		{
			name:         "Succeeded deletion",
			bindingCRIDs: []string{"uuid-binding1"},
			prepareFunc: func() {
				expDelBM1 := *createdBM1
				expDelBM1.MarkedForDelete = Bool(true)
				expDelBM2 := *createdBM2
				expDelBM2.MarkedForDelete = Bool(true)
				orgConfig := map[string]map[string]map[string]map[string][]*model.SubnetConnectionBindingMap{
					"default": {
						"default": {
							"vpc1": {
								"subnet1": []*model.SubnetConnectionBindingMap{
									&expDelBM1, &expDelBM2,
								},
							},
						},
					}}

				expOrg, _ := wrapOrgRoot(orgConfig)
				mockOrgRootClient.EXPECT().Patch(&orgRootMatcher{expOrg}, &enforceRevisionCheckParam).Return(nil)
			},
			expBindingMapsInStore: []*model.SubnetConnectionBindingMap{},
		}, {
			name:         "failed to patch on NSX",
			bindingCRIDs: []string{"uuid-binding1"},
			prepareFunc: func() {
				mockOrgRootClient.EXPECT().Patch(gomock.Any(), &enforceRevisionCheckParam).Return(fmt.Errorf("fake error"))
			},
			expErr:                "fake error",
			expBindingMapsInStore: []*model.SubnetConnectionBindingMap{createdBM1, createdBM2},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			builder, err := common.PolicyPathVpcSubnetConnectionBindingMap.NewPolicyTreeBuilder()
			require.NoError(t, err)
			svc := &BindingService{
				Service: common.Service{
					NSXClient: &nsx.Client{
						OrgRootClient: mockOrgRootClient,
					},
					NSXConfig: &config.NSXOperatorConfig{
						CoeConfig: &config.CoeConfig{
							Cluster: "fake_cluster",
						},
					},
				},
				BindingStore: SetupStore(),
				builder:      builder,
			}
			svc.BindingStore.Add(createdBM1)
			svc.BindingStore.Add(createdBM2)

			if tc.prepareFunc != nil {
				tc.prepareFunc()
			}

			err = svc.DeleteMultiSubnetConnectionBindingMapsByCRs(sets.New[string](tc.bindingCRIDs...))
			if tc.expErr != "" {
				require.EqualError(t, err, tc.expErr)
			} else {
				require.NoError(t, err)
			}
			bms := svc.BindingStore.List()
			assert.ElementsMatch(t, tc.expBindingMapsInStore, bms)
		})
	}
}

func TestDeleteSubnetConnectionBindingMaps(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockOrgRootClient := orgroot_mocks.NewMockOrgRootClient(ctrl)

	for _, tc := range []struct {
		name                  string
		deleteFn              func(svc *BindingService) error
		expErr                string
		expBindingMapsInStore []*model.SubnetConnectionBindingMap
	}{
		{
			name: "test with DeleteSubnetConnectionBindingMapsByCRName",
			deleteFn: func(svc *BindingService) error {
				mockOrgRootClient.EXPECT().Patch(gomock.Any(), &enforceRevisionCheckParam).Return(nil)
				return svc.DeleteSubnetConnectionBindingMapsByCRName(binding1.Name, binding1.Namespace)
			},
			expErr:                "",
			expBindingMapsInStore: []*model.SubnetConnectionBindingMap{},
		}, {
			name: "test with DeleteSubnetConnectionBindingMapsByParentSubnet",
			deleteFn: func(svc *BindingService) error {
				mockOrgRootClient.EXPECT().Patch(gomock.Any(), &enforceRevisionCheckParam).Return(nil)
				return svc.DeleteSubnetConnectionBindingMapsByParentSubnet(parentSubnet1)
			},
			expErr:                "",
			expBindingMapsInStore: []*model.SubnetConnectionBindingMap{},
		}, {
			name: "test with clean up",
			deleteFn: func(svc *BindingService) error {
				mockOrgRootClient.EXPECT().Patch(gomock.Any(), &enforceRevisionCheckParam).Return(nil)
				ctx := context.Background()
				return svc.CleanupBeforeVPCDeletion(ctx)
			},
			expErr:                "",
			expBindingMapsInStore: []*model.SubnetConnectionBindingMap{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			builder, _ := common.PolicyPathVpcSubnetConnectionBindingMap.NewPolicyTreeBuilder()
			svc := &BindingService{
				Service: common.Service{
					NSXClient: &nsx.Client{
						OrgRootClient: mockOrgRootClient,
					},
					NSXConfig: &config.NSXOperatorConfig{
						CoeConfig: &config.CoeConfig{
							Cluster: "fake_cluster",
						},
					},
				},
				BindingStore: SetupStore(),
				builder:      builder,
			}
			svc.BindingStore.Add(createdBM1)
			err := tc.deleteFn(svc)
			if tc.expErr != "" {
				require.EqualError(t, err, tc.expErr)
			} else {
				require.NoError(t, err)
			}
			bms := svc.BindingStore.List()
			assert.ElementsMatch(t, tc.expBindingMapsInStore, bms)
		})
	}
}
