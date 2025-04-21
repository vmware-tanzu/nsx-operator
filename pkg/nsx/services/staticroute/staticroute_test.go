package staticroute

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/openlyinc/pointy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mock_org_root "github.com/vmware-tanzu/nsx-operator/pkg/mock/orgrootclient"
	mocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/staticrouteclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	staticrouteName1          = "ns1-staticroute-1"
	staticrouteName2          = "ns1-staticroute-2"
	staticrouteID1            = "ns-staticroute-uid-1"
	staticrouteID2            = "ns-staticroute-uid-2"
	IPv4Type                  = "IPv4"
	cluster                   = "k8scl-one"
	tagScopeStaticRouteCRName = common.TagScopeStaticRouteCRName
	tagScopeStaticRouteCRUID  = common.TagScopeStaticRouteCRUID
	tagValueStaticRouteCRName = "staticrouteA"
	tagValueStaticRouteCRUID  = "uidA"
	tagScopeCluster           = common.TagScopeCluster
	tagScopeNamespace         = common.TagScopeNamespace
)

type fakeQueryClient struct{}

func (qIface *fakeQueryClient) List(queryParam string, cursorParam *string, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.SearchResponse, error) {
	resultCount := int64(1)
	return model.SearchResponse{
		Results: []*data.StructValue{},
		Cursor:  nil, ResultCount: &resultCount,
	}, nil
}

func createService(t *testing.T) (*StaticRouteService, *gomock.Controller, *mocks.MockStaticRoutesClient) {
	config2 := nsx.NewConfig("localhost", "1", "1", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})

	cluster, _ := nsx.NewCluster(config2)
	rc := cluster.NewRestConnector()

	mockCtrl := gomock.NewController(t)
	mockStaticRouteclient := mocks.NewMockStaticRoutesClient(mockCtrl)

	staticRouteStore := &StaticRouteStore{ResourceStore: common.ResourceStore{
		BindingType: model.StaticRoutesBindingType(),
	}}
	staticRouteStore.Indexer = cache.NewIndexer(keyFunc, cache.Indexers{
		common.TagScopeStaticRouteCRUID: indexFunc,
		common.TagScopeNamespace:        indexStaticRouteNamespace,
	})

	service := &StaticRouteService{
		Service: common.Service{
			NSXClient: &nsx.Client{
				QueryClient:       &fakeQueryClient{},
				StaticRouteClient: mockStaticRouteclient,
				RestConnector:     rc,
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
		StaticRouteStore: staticRouteStore,
	}
	return service, mockCtrl, mockStaticRouteclient
}

func Test_InitializeStaticRouteStore(t *testing.T) {
	service, mockController, _ := createService(t)
	defer mockController.Finish()
	commonService := service.Service
	patch := gomonkey.ApplyMethod(reflect.TypeOf(&commonService), "InitializeResourceStore", func(_ *common.Service, wg *sync.WaitGroup,
		fatalErrors chan error, resourceTypeValue string, tags []model.Tag, store common.Store,
	) {
		wg.Done()
		return
	})
	defer patch.Reset()

	vpcService := &vpc.VPCService{}

	_, err := InitializeStaticRoute(commonService, vpcService)
	if err != nil {
		t.Error(err)
	}
}

func TestStaticRouteService_DeleteStaticRouteByCR(t *testing.T) {
	service, mockController, mockStaticRouteclient := createService(t)
	defer mockController.Finish()

	var tc *bindings.TypeConverter
	patches2 := gomonkey.ApplyMethod(reflect.TypeOf(tc), "ConvertToGolang",
		func(_ *bindings.TypeConverter, d data.DataValue, b bindings.BindingType) (interface{}, []error) {
			mId, mTag, mScope := "test_id", "test_tag", "test_scope"
			m := model.StaticRoutes{
				Id:   &mId,
				Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
			}
			var j interface{} = m
			return j, nil
		})
	defer patches2.Reset()
	vpcService := &vpc.VPCService{}
	returnservice, err := InitializeStaticRoute(service.Service, vpcService)
	if err != nil {
		t.Error(err)
	}

	srObj := &v1alpha1.StaticRoute{
		ObjectMeta: v1.ObjectMeta{
			UID:  "uid-123",
			Name: "sr",
		},
	}
	id := util.GenerateIDByObject(srObj)
	vpcPath := "/orgs/default/projects/project-1/vpcs/vpc-1"
	path := fmt.Sprintf("%s/static-routes/%s", vpcPath, id)
	sr1 := &model.StaticRoutes{Id: &id, Path: &path, ParentPath: &vpcPath}

	// no record found
	mockStaticRouteclient.EXPECT().Delete(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Times(0)
	err = returnservice.DeleteStaticRouteByCR(srObj)
	assert.Equal(t, err, nil)

	returnservice.StaticRouteStore.Add(sr1)

	// delete record
	mockStaticRouteclient.EXPECT().Delete("default", "project-1", "vpc-1", id).Return(nil).Times(1)
	err = returnservice.DeleteStaticRouteByCR(srObj)
	assert.Equal(t, err, nil)
	srs := returnservice.StaticRouteStore.List()
	assert.Equal(t, len(srs), 0)
}

func TestStaticRouteService_CreateorUpdateStaticRoute(t *testing.T) {
	service, mockController, mockStaticRouteclient := createService(t)
	defer mockController.Finish()
	vpcPath := "/orgs/default/projects/project-1/vpcs/vpc-1"
	var tc *bindings.TypeConverter
	patches2 := gomonkey.ApplyMethod(reflect.TypeOf(tc), "ConvertToGolang",
		func(_ *bindings.TypeConverter, d data.DataValue, b bindings.BindingType) (interface{}, []error) {
			mId, mTag, mScope := "test_id", "test_tag", "test_scope"
			m := model.StaticRoutes{
				Id:   &mId,
				Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
			}
			var j interface{} = m
			return j, nil
		})
	defer patches2.Reset()

	vpcService := &vpc.VPCService{}
	returnservice, err := InitializeStaticRoute(service.Service, vpcService)
	if err != nil {
		t.Error(err)
	}
	id := "12345678"
	sr1 := &v1alpha1.StaticRoute{}
	sr1.UID = types.UID(id)

	mockStaticRouteclient.EXPECT().Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1)
	mId := "test_id"
	scope := common.TagScopeStaticRouteCRUID
	tag := "test_tag"
	m := model.StaticRoutes{
		Id:         &mId,
		Tags:       []model.Tag{{Tag: &tag, Scope: &scope}},
		ParentPath: &vpcPath,
		Path:       String(fmt.Sprintf("%s/static-routes/%s", vpcPath, mId)),
	}
	mockStaticRouteclient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(m, nil).Times(2)
	patches := gomonkey.ApplyMethod(reflect.TypeOf(returnservice.VPCService), "ListVPCInfo", func(_ common.VPCServiceProvider, ns string) []common.VPCResourceInfo {
		id := "12345678"
		return []common.VPCResourceInfo{{OrgID: "default", ProjectID: "project-1", VPCID: "vpc-1", ID: id}}
	})
	defer patches.Reset()
	err = returnservice.CreateOrUpdateStaticRoute("test", sr1)
	assert.Equal(t, err, nil)

	// no change,  update
	mockStaticRouteclient.EXPECT().Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1)
	err = returnservice.CreateOrUpdateStaticRoute("test", sr1)
	assert.Equal(t, err, nil)
}

func TestGetUID(t *testing.T) {
	service := &StaticRouteService{}

	// Test case: Static route is nil
	assert.Nil(t, service.GetUID(nil))

	// Test case: Static route with no tags
	staticRouteNoTags := &model.StaticRoutes{}
	assert.Nil(t, service.GetUID(staticRouteNoTags))

	// Test case: Static route with tags but no matching scope
	staticRouteNoMatchingScope := &model.StaticRoutes{
		Tags: []model.Tag{
			{Scope: pointy.String("other-scope"), Tag: pointy.String("some-tag")},
		},
	}
	assert.Nil(t, service.GetUID(staticRouteNoMatchingScope))

	// Test case: Static route with matching scope
	staticRouteMatchingScope := &model.StaticRoutes{
		Tags: []model.Tag{
			{Scope: pointy.String(common.TagScopeStaticRouteCRUID), Tag: pointy.String("expected-uid")},
		},
	}
	assert.Equal(t, pointy.String("expected-uid"), service.GetUID(staticRouteMatchingScope))
}

func TestListStaticRouteByName(t *testing.T) {
	service, mockController, _ := createService(t)
	defer mockController.Finish()
	srObj := &v1alpha1.StaticRoute{
		ObjectMeta: v1.ObjectMeta{
			UID:  "uid-123",
			Name: "sr",
		},
	}
	id := util.GenerateIDByObject(srObj)
	id1 := fmt.Sprintf("%s-%s", id, "1")
	id2 := fmt.Sprintf("%s-%s", id, "2")
	sr1 := &model.StaticRoutes{
		Id: &id1,
		Tags: []model.Tag{
			{Scope: pointy.String(common.TagScopeStaticRouteCRName), Tag: pointy.String("route1")},
			{Scope: pointy.String(common.TagScopeNamespace), Tag: pointy.String("namespace1")},
		},
	}
	sr2 := &model.StaticRoutes{
		Id: &id2,
		Tags: []model.Tag{
			{Scope: pointy.String(common.TagScopeStaticRouteCRName), Tag: pointy.String("route2")},
			{Scope: pointy.String(common.TagScopeNamespace), Tag: pointy.String("namespace1")},
		},
	}
	service.StaticRouteStore.Add(sr1)
	service.StaticRouteStore.Add(sr2)

	// Test case: List static routes by name
	result := service.ListStaticRouteByName("namespace1", "route1")
	assert.Len(t, result, 1)
	name := nsxutil.FindTag(result[0].Tags, common.TagScopeStaticRouteCRName)
	assert.Equal(t, "route1", name)

	// Test case: No static routes found
	result = service.ListStaticRouteByName("namespace1", "nonexistent")
	assert.Len(t, result, 0)
}

func TestListStaticRoute(t *testing.T) {
	service, mockController, _ := createService(t)
	defer mockController.Finish()

	result := service.ListStaticRoute()
	assert.Len(t, result, 0)
	srObj := &v1alpha1.StaticRoute{
		ObjectMeta: v1.ObjectMeta{
			UID:  "uid-123",
			Name: "sr",
		},
	}
	id := util.GenerateIDByObject(srObj)
	id1 := fmt.Sprintf("%s-%s", id, "1")
	id2 := fmt.Sprintf("%s-%s", id, "2")
	sr1 := &model.StaticRoutes{
		Id: &id1,
		Tags: []model.Tag{
			{Scope: pointy.String(common.TagScopeStaticRouteCRName), Tag: pointy.String("route1")},
			{Scope: pointy.String(common.TagScopeNamespace), Tag: pointy.String("namespace1")},
		},
	}
	sr2 := &model.StaticRoutes{
		Id: &id2,
		Tags: []model.Tag{
			{Scope: pointy.String(common.TagScopeStaticRouteCRName), Tag: pointy.String("route2")},
			{Scope: pointy.String(common.TagScopeNamespace), Tag: pointy.String("namespace1")},
		},
	}
	service.StaticRouteStore.Add(sr1)
	service.StaticRouteStore.Add(sr2)

	// Test case: List static routes
	result = service.ListStaticRoute()
	assert.Len(t, result, 2)

	// Test case: Delete one static routes
	service.StaticRouteStore.Delete(sr1)
	result = service.ListStaticRoute()
	assert.Len(t, result, 1)
}

func TestStaticRouteService_Cleanup(t *testing.T) {
	service, mockController, mockStaticRouteclient := createService(t)
	defer mockController.Finish()
	builder, _ := common.PolicyPathVpcStaticRoutes.NewPolicyTreeBuilder()
	service.builder = builder
	mockOrgRootClient := mock_org_root.NewMockOrgRootClient(mockController)

	ctx := context.Background()

	// Mock static routes
	staticRoutePath1 := "/orgs/org1/projects/project1/vpcs/vpc1/staticroutes/staticroute1"
	staticRoutePath2 := "/orgs/org2/projects/project2/vpcs/vpc2/staticroutes/staticroute2"
	staticRoute1 := &model.StaticRoutes{
		Id:   pointy.String("staticroute1"),
		Path: &staticRoutePath1,
	}
	staticRoute2 := &model.StaticRoutes{
		Id:   pointy.String("staticroute2"),
		Path: &staticRoutePath2,
	}

	t.Run("Successful cleanup", func(t *testing.T) {
		mockOrgRootClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil)
		service.StaticRouteStore.Add(staticRoute1)
		service.StaticRouteStore.Add(staticRoute2)
		mockStaticRouteclient = mocks.NewMockStaticRoutesClient(mockController)
		service.NSXClient.StaticRouteClient = mockStaticRouteclient
		service.NSXClient.OrgRootClient = mockOrgRootClient

		err := service.CleanupVPCChildResources(ctx, "")
		assert.NoError(t, err)
	})

	t.Run("Context canceled", func(t *testing.T) {
		service.StaticRouteStore.Add(staticRoute1)
		ctx, cancel := context.WithCancel(ctx)
		cancel()

		err := service.CleanupVPCChildResources(ctx, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "context canceled")
	})

	t.Run("Delete static route error", func(t *testing.T) {
		mockOrgRootClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(fmt.Errorf("delete error"))

		service.StaticRouteStore.Add(staticRoute1)
		mockStaticRouteclient = mocks.NewMockStaticRoutesClient(mockController)
		service.NSXClient.StaticRouteClient = mockStaticRouteclient

		err := service.CleanupVPCChildResources(ctx, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "delete error")
	})
}

func TestStaticRouteService_DeleteStaticRoute(t *testing.T) {
	service, mockController, mockStaticRouteclient := createService(t)
	defer mockController.Finish()

	t.Run("Error parsing path", func(t *testing.T) {
		staticRouteID := "nonexistent-id"

		err := service.DeleteStaticRoute(&model.StaticRoutes{
			Path: &staticRouteID,
			Id:   &staticRouteID,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid path 'nonexistent-id'")
	})

	t.Run("Static route exists and is deleted successfully", func(t *testing.T) {
		staticRouteID := "staticroute1"
		staticRoute := &model.StaticRoutes{Id: &staticRouteID}
		service.StaticRouteStore.Add(staticRoute)

		mockStaticRouteclient.EXPECT().Delete("org1", "project1", "vpc1", staticRouteID).Return(nil).Times(1)
		err := service.DeleteStaticRoute(&model.StaticRoutes{
			Path: common.String(fmt.Sprintf("/orgs/org1/projects/project1/vpcs/vpc1/static-routes/%s", staticRouteID)),
			Id:   &staticRouteID,
		})
		assert.NoError(t, err)
	})

	t.Run("Error deleting static route from NSX", func(t *testing.T) {
		staticRouteID := "staticroute2"
		staticRoute := &model.StaticRoutes{Id: &staticRouteID}
		service.StaticRouteStore.Add(staticRoute)

		mockStaticRouteclient.EXPECT().Delete("org1", "project1", "vpc1", staticRouteID).Return(fmt.Errorf("delete error")).Times(1)

		err := service.DeleteStaticRoute(&model.StaticRoutes{
			Path: common.String(fmt.Sprintf("/orgs/org1/projects/project1/vpcs/vpc1/static-routes/%s", staticRouteID)),
			Id:   &staticRouteID,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "delete error")
	})
}
