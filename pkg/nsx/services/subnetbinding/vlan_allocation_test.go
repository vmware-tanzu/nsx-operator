package subnetbinding

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/mock/gomock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	search_mocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/searchclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vlanpool"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

func TestCollectUsedVlansOnParentSubnets(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	fakeQueryClient := search_mocks.NewMockQueryClient(ctrl)
	svc := &BindingService{
		Service: common.Service{
			NSXClient: &nsx.Client{
				QueryClient: fakeQueryClient,
			},
		},
	}

	bm1 := &model.SubnetConnectionBindingMap{
		Id:             String("id-1"),
		DisplayName:    String("binding1"),
		SubnetPath:     String("/parent1"),
		ParentPath:     String("/child1"),
		Path:           String("/child1/subnet-connection-binding-maps/id-1"),
		VlanTrafficTag: Int64(101),
		ResourceType:   String(ResourceTypeSubnetConnectionBindingMap),
		Tags: []model.Tag{
			{Scope: String(common.TagScopeSubnetBindingCRUID), Tag: String("uuid-1")},
		},
	}
	bm2 := &model.SubnetConnectionBindingMap{
		Id:             String("id-2"),
		DisplayName:    String("binding2"),
		SubnetPath:     String("/parent1"),
		ParentPath:     String("/child1"),
		Path:           String("/child1/subnet-connection-binding-maps/id-2"),
		VlanTrafficTag: Int64(102),
		ResourceType:   String(ResourceTypeSubnetConnectionBindingMap),
		Tags: []model.Tag{
			{Scope: String(common.TagScopeSubnetBindingCRUID), Tag: String("uuid-2")},
		},
	}
	bm3 := &model.SubnetConnectionBindingMap{
		Id:             String("id-3"),
		DisplayName:    String("binding3"),
		SubnetPath:     String("/parent1"),
		ParentPath:     String("/child1"),
		Path:           String("/child1/subnet-connection-binding-maps/id-3"),
		VlanTrafficTag: nil, // no tag
		ResourceType:   String(ResourceTypeSubnetConnectionBindingMap),
	}

	converter := common.NewConverter()
	dv1, _ := converter.ConvertToVapi(bm1, model.SubnetConnectionBindingMapBindingType())
	dv2, _ := converter.ConvertToVapi(bm2, model.SubnetConnectionBindingMapBindingType())
	dv3, _ := converter.ConvertToVapi(bm3, model.SubnetConnectionBindingMapBindingType())

	resultCount := int64(3)
	fakeQueryClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(model.SearchResponse{
		ResultCount: &resultCount,
		Results: []*data.StructValue{
			dv1.(*data.StructValue),
			dv2.(*data.StructValue),
			dv3.(*data.StructValue),
		},
	}, nil).Times(1)

	used, err := svc.CollectUsedVlansOnParentSubnetsFromNSX([]string{"/parent1"}, "uuid-1") // Exclude uuid-1
	require.NoError(t, err)

	assert.False(t, used.Has(101)) // Excluded
	assert.True(t, used.Has(102))  // Included
	assert.Equal(t, 1, used.Len())
}

func TestListBindingMapsByParentSubnetPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	t.Run("Query client not initialized", func(t *testing.T) {
		svc := &BindingService{}
		_, err := svc.listBindingMapsByParentSubnetPath("/parent")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "NSX query client is not initialized")
	})

	t.Run("Success single page", func(t *testing.T) {
		fakeQueryClient := search_mocks.NewMockQueryClient(ctrl)
		svc := &BindingService{
			Service: common.Service{
				NSXClient: &nsx.Client{
					QueryClient: fakeQueryClient,
				},
			},
		}
		bm := &model.SubnetConnectionBindingMap{
			Id:           String("id-1"),
			DisplayName:  String("binding1"),
			SubnetPath:   String("/parent1"),
			ParentPath:   String("/child1"),
			Path:         String("/child1/subnet-connection-binding-maps/id-1"),
			ResourceType: String(ResourceTypeSubnetConnectionBindingMap),
		}
		dv, _ := common.NewConverter().ConvertToVapi(bm, model.SubnetConnectionBindingMapBindingType())
		expectedQuery := fmt.Sprintf("%s:%s AND marked_for_delete:false AND ((subnet_path:\\/parent AND NOT subnet_association:BRANCH) OR (parent_path:\\/parent AND subnet_association:BRANCH))",
			common.ResourceType, ResourceTypeSubnetConnectionBindingMap)
		fakeQueryClient.EXPECT().List(expectedQuery, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(model.SearchResponse{
			Results: []*data.StructValue{dv.(*data.StructValue)},
		}, nil).Times(1)

		bindings, err := svc.listBindingMapsByParentSubnetPath("/parent")
		require.NoError(t, err)
		assert.Len(t, bindings, 1)
		assert.Equal(t, "id-1", *bindings[0].Id)
	})

	t.Run("Page max error retry", func(t *testing.T) {
		fakeQueryClient := search_mocks.NewMockQueryClient(ctrl)
		svc := &BindingService{
			Service: common.Service{
				NSXClient: &nsx.Client{
					QueryClient: fakeQueryClient,
				},
			},
		}

		fakeQueryClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(model.SearchResponse{}, nsxutil.PageMaxError{}).Times(1)
		fakeQueryClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(model.SearchResponse{
			Results: []*data.StructValue{},
		}, nil).Times(1)

		bindings, err := svc.listBindingMapsByParentSubnetPath("/parent")
		require.NoError(t, err)
		assert.Len(t, bindings, 0)
	})

	t.Run("Error listing", func(t *testing.T) {
		fakeQueryClient := search_mocks.NewMockQueryClient(ctrl)
		svc := &BindingService{
			Service: common.Service{
				NSXClient: &nsx.Client{
					QueryClient: fakeQueryClient,
				},
			},
		}
		fakeQueryClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(model.SearchResponse{}, fmt.Errorf("search error")).Times(1)

		_, err := svc.listBindingMapsByParentSubnetPath("/parent")
		require.Error(t, err)
	})
}

func TestBindingMapCRUID(t *testing.T) {
	bm := &model.SubnetConnectionBindingMap{
		Tags: []model.Tag{
			{Scope: String(common.TagScopeSubnetBindingCRUID), Tag: String("test-uuid")},
			{Scope: String(common.TagScopeNamespace), Tag: String("default")},
		},
	}
	uid := bindingMapCRUID(bm)
	assert.Equal(t, "test-uuid", uid)

	bm2 := &model.SubnetConnectionBindingMap{}
	uid2 := bindingMapCRUID(bm2)
	assert.Equal(t, "", uid2)
}

func TestReconcileVlanTrafficTagPreservesStatus(t *testing.T) {
	for _, tc := range []struct {
		name       string
		cachedVlan int64
		conflict   bool
		fromNSX    bool
	}{
		{name: "missing after restore"},
		{name: "mismatched cache", cachedVlan: 1},
		{name: "matching cache", cachedVlan: 2},
		{name: "cache conflict", conflict: true},
		{name: "NSX retry", fromNSX: true},
		{name: "NSX conflict", fromNSX: true, conflict: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &BindingService{BindingStore: SetupStore()}
			binding := &v1alpha1.SubnetConnectionBindingMap{
				ObjectMeta: metav1.ObjectMeta{UID: "binding-uid"},
				Status:     v1alpha1.SubnetConnectionBindingMapStatus{VLANTrafficTag: Int64(2)},
			}
			addBinding := func(id, uid string, vlan int64) {
				require.NoError(t, svc.BindingStore.Apply(&model.SubnetConnectionBindingMap{
					Id:             String(id),
					Path:           String("/orgs/default/projects/default/vpcs/vpc-1/subnets/child/subnet-connection-binding-maps/" + id),
					SubnetPath:     String("/parent"),
					VlanTrafficTag: Int64(vlan),
					Tags:           []model.Tag{{Scope: String(common.TagScopeSubnetBindingCRUID), Tag: String(uid)}},
				}))
			}
			if tc.cachedVlan != 0 {
				addBinding("existing", string(binding.UID), tc.cachedVlan)
			}
			if tc.conflict {
				addBinding("other", "other-uid", 2)
			}
			if tc.fromNSX {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(svc), "CollectUsedVlansOnParentSubnetsFromNSX",
					func(_ *BindingService, paths []string, excludeUID string) (sets.Set[int], error) {
						assert.Equal(t, []string{"/parent"}, paths)
						assert.Equal(t, string(binding.UID), excludeUID)
						if tc.conflict {
							return sets.New[int](2), nil
						}
						return sets.New[int](), nil
					})
				defer patches.Reset()
			}
			got, err := svc.ReconcileVlanTrafficTag(binding, 100, []string{"/parent"}, tc.fromNSX)
			if tc.conflict {
				require.Error(t, err)
				assert.True(t, vlanpool.IsVlanAllocationError(err))
				assert.Zero(t, got)
			} else {
				require.NoError(t, err)
				assert.Equal(t, int64(2), got)
			}
		})
	}
}

func TestReconcileVlanTrafficTagAfterRestoreOrder(t *testing.T) {
	for _, order := range [][]int64{{2, 1}, {1, 2}} {
		svc := &BindingService{BindingStore: SetupStore()}
		for _, vlan := range order {
			binding := &v1alpha1.SubnetConnectionBindingMap{
				Status: v1alpha1.SubnetConnectionBindingMapStatus{VLANTrafficTag: Int64(vlan)},
			}
			got, err := svc.ReconcileVlanTrafficTag(binding, -1, []string{"/parent"}, false)
			require.NoError(t, err)
			assert.Equal(t, vlan, got)
		}
	}
}

func TestReconcileVlanTrafficTag(t *testing.T) {
	bm1 := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "bm-1",
			UID:       "uuid-1",
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:       "subnet-child-1",
			TargetSubnetName: "subnet-parent-1",
			VLANTrafficTag:   v1alpha1.VLANTrafficTagPtr(201),
		},
		Status: v1alpha1.SubnetConnectionBindingMapStatus{VLANTrafficTag: Int64(200)},
	}
	bm2 := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "bm-2",
			UID:       "uuid-2",
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:       "subnet-child-2",
			TargetSubnetName: "subnet-parent-2",
		},
	}

	for _, tc := range []struct {
		name       string
		bindingMap *v1alpha1.SubnetConnectionBindingMap
		preferred  int64
		patches    func(t *testing.T, svc *BindingService) *gomonkey.Patches
		expErr     bool
		expVlan    *int64
		fromNSX    bool
	}{
		{
			name:       "Manual VLAN successfully validated",
			bindingMap: bm1,
			preferred:  -1,
			patches: func(t *testing.T, svc *BindingService) *gomonkey.Patches {
				return gomonkey.ApplyMethod(reflect.TypeOf(svc.VlanPoolService), "ValidateManualVlan", func(_ *vlanpool.Service, parentSubnetPaths []string, vlan int64, excludeCRUID string, fromNSX bool) error {
					return nil
				})
			},
			expErr:  false,
			expVlan: v1alpha1.VLANTrafficTagPtr(201),
		},
		{
			name:       "Manual VLAN validation failed",
			bindingMap: bm1,
			preferred:  -1,
			patches: func(t *testing.T, svc *BindingService) *gomonkey.Patches {
				return gomonkey.ApplyMethod(reflect.TypeOf(svc.VlanPoolService), "ValidateManualVlan", func(_ *vlanpool.Service, parentSubnetPaths []string, vlan int64, excludeCRUID string, fromNSX bool) error {
					return fmt.Errorf("conflict")
				})
			},
			expErr: true,
		},
		{
			name:       "Auto allocate VLAN successfully with preferred VLAN ID",
			bindingMap: bm2,
			preferred:  300,
			patches: func(t *testing.T, svc *BindingService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(svc.VlanPoolService), "Allocate", func(_ *vlanpool.Service, parentSubnetPaths []string, excludeCRUID string, preferred int64, fromNSX bool) (int64, error) {
					assert.Equal(t, int64(300), preferred)
					return 300, nil
				})
				return patches
			},
			expErr:  false,
			expVlan: v1alpha1.VLANTrafficTagPtr(300),
		},
		{
			name:       "Auto allocate VLAN from cache when fromNSX is false",
			bindingMap: bm2,
			preferred:  -1,
			patches: func(t *testing.T, svc *BindingService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(svc.BindingStore), "GetByIndex", func(_ *BindingStore, key string, value string) []*model.SubnetConnectionBindingMap {
					return []*model.SubnetConnectionBindingMap{
						{
							VlanTrafficTag: v1alpha1.VLANTrafficTagPtr(100),
						},
					}
				})
				return patches
			},
			expErr:  false,
			expVlan: v1alpha1.VLANTrafficTagPtr(100),
			fromNSX: false,
		},
		{
			name:       "Bypass cache and allocate VLAN when fromNSX is true",
			bindingMap: bm2,
			preferred:  -1,
			patches: func(t *testing.T, svc *BindingService) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(svc.BindingStore), "GetByIndex", func(_ *BindingStore, key string, value string) []*model.SubnetConnectionBindingMap {
					return []*model.SubnetConnectionBindingMap{
						{
							VlanTrafficTag: v1alpha1.VLANTrafficTagPtr(100),
						},
					}
				})
				patches.ApplyMethod(reflect.TypeOf(svc.VlanPoolService), "Allocate", func(_ *vlanpool.Service, parentSubnetPaths []string, excludeCRUID string, preferred int64, fromNSX bool) (int64, error) {
					assert.True(t, fromNSX)
					return 101, nil
				})
				return patches
			},
			expErr:  false,
			expVlan: v1alpha1.VLANTrafficTagPtr(101),
			fromNSX: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bm := tc.bindingMap.DeepCopy()
			svc := &BindingService{
				BindingStore: SetupStore(),
			}
			svc.VlanPoolService = vlanpool.NewService(svc)
			if tc.patches != nil {
				patches := tc.patches(t, svc)
				defer patches.Reset()
			}
			vlanID, err := svc.ReconcileVlanTrafficTag(bm, tc.preferred, []string{"/parent"}, tc.fromNSX)
			if tc.expErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tc.expVlan != nil {
					assert.Equal(t, *tc.expVlan, vlanID)
				}
			}
		})
	}
}
