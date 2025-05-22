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
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mock_org_root "github.com/vmware-tanzu/nsx-operator/pkg/mock/orgrootclient"
	mocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/staticrouteclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/realizestate"
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
	srNSUID                   = "nsUuid"
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

	staticRouteStore := buildStaticRouteStore()

	service := &StaticRouteService{
		VPCService: &vpc.VPCService{},
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
	sr1 := &model.StaticRoutes{Id: &id, Path: &path, ParentPath: &vpcPath, Tags: []model.Tag{
		{Scope: String(common.TagScopeStaticRouteCRUID), Tag: String(string(srObj.UID))},
	}}

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
	patches2.ApplyMethod(reflect.TypeOf(&service.Service), "GetNamespaceUID",
		func(s *common.Service, ns string) types.UID {
			return types.UID(srNSUID)
		})
	defer patches2.Reset()

	vpcService := &vpc.VPCService{}
	returnservice, err := InitializeStaticRoute(service.Service, vpcService)
	if err != nil {
		t.Error(err)
	}
	id := "12345678"
	sr1 := &v1alpha1.StaticRoute{
		Spec: v1alpha1.StaticRouteSpec{
			Network: "1.1.1.0/24",
		},
	}
	sr1.UID = types.UID(id)

	mockStaticRouteclient.EXPECT().Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1)
	mId := "test_id"
	scope := common.TagScopeStaticRouteCRUID
	m := model.StaticRoutes{
		Id:          &mId,
		DisplayName: String("test"),
		Tags: []model.Tag{
			{Tag: &id, Scope: &scope},
		},
		Network:    String("1.1.1.0/24"),
		ParentPath: &vpcPath,
		Path:       String(fmt.Sprintf("%s/static-routes/%s", vpcPath, mId)),
	}
	mockStaticRouteclient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(m, nil).Times(1)
	patches := gomonkey.ApplyMethod(reflect.TypeOf(returnservice.VPCService), "ListVPCInfo", func(_ common.VPCServiceProvider, ns string) []common.VPCResourceInfo {
		id := "12345678"
		return []common.VPCResourceInfo{{OrgID: "default", ProjectID: "project-1", VPCID: "vpc-1", ID: id}}
	})
	defer patches.Reset()
	err = returnservice.CreateOrUpdateStaticRoute("test", sr1)
	assert.Equal(t, err, nil)

	// no change,  update
	err = returnservice.CreateOrUpdateStaticRoute("test", sr1)
	assert.Equal(t, err, nil)

	staticRoutesInStore := returnservice.StaticRouteStore.GetByIndex(common.TagScopeStaticRouteCRUID, string(sr1.GetUID()))
	require.True(t, len(staticRoutesInStore) > 0)
	staticRoute := staticRoutesInStore[0]
	sr2 := sr1
	sr2.Name = "newStaticRoute"
	err = returnservice.CreateOrUpdateStaticRoute("test", sr1)
	assert.Equal(t, err, nil)
	newStaticRoutesInStore := returnservice.StaticRouteStore.GetByIndex(common.TagScopeStaticRouteCRUID, string(sr1.GetUID()))
	require.True(t, len(staticRoutesInStore) > 0)
	newStaticRoute := newStaticRoutesInStore[0]
	assert.Equal(t, staticRoute, newStaticRoute)
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
		ParentPath: String("vpc1"),
	}
	sr2 := &model.StaticRoutes{
		Id: &id2,
		Tags: []model.Tag{
			{Scope: pointy.String(common.TagScopeStaticRouteCRName), Tag: pointy.String("route2")},
			{Scope: pointy.String(common.TagScopeNamespace), Tag: pointy.String("namespace1")},
		},
		ParentPath: String("vpc1"),
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
		ParentPath: String("vpc1"),
	}
	sr2 := &model.StaticRoutes{
		Id: &id2,
		Tags: []model.Tag{
			{Scope: pointy.String(common.TagScopeStaticRouteCRName), Tag: pointy.String("route2")},
			{Scope: pointy.String(common.TagScopeNamespace), Tag: pointy.String("namespace1")},
		},
		ParentPath: String("vpc1"),
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
		Id:         pointy.String("staticroute1"),
		Path:       &staticRoutePath1,
		ParentPath: String("/orgs/org1/projects/project1/vpcs/vpc1"),
	}
	staticRoute2 := &model.StaticRoutes{
		Id:         pointy.String("staticroute2"),
		Path:       &staticRoutePath2,
		ParentPath: String("/orgs/org1/projects/project1/vpcs/vpc2"),
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
		staticRoute := &model.StaticRoutes{Id: &staticRouteID, ParentPath: String("/orgs/org1/projects/project1/vpcs/vpc1")}
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
		staticRoute := &model.StaticRoutes{Id: &staticRouteID, ParentPath: String("/orgs/org1/projects/project1/vpcs/vpc1")}
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
func TestStaticRouteService_CreateOrUpdateStaticRoute(t *testing.T) {
	service, mockController, mockStaticRouteclient := createService(t)
	defer mockController.Finish()

	// Patch buildStaticRoute to return error
	patchBuild := gomonkey.ApplyPrivateMethod(reflect.TypeOf(service), "buildStaticRoute", func(_ *StaticRouteService, obj *v1alpha1.StaticRoute) (*model.StaticRoutes, error) {
		return nil, fmt.Errorf("build error")
	})
	defer patchBuild.Reset()

	err := service.CreateOrUpdateStaticRoute("ns", &v1alpha1.StaticRoute{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "build error")

	// Patch buildStaticRoute to return valid static route
	patchBuild.Reset()
	staticRouteID := "sr-id"
	staticRoutePath := "/orgs/org1/projects/proj1/vpcs/vpc1/static-routes/sr-id"
	nsxStaticRoute := &model.StaticRoutes{
		Id:   &staticRouteID,
		Path: &staticRoutePath,
	}
	patchBuild = gomonkey.ApplyPrivateMethod(reflect.TypeOf(service), "buildStaticRoute", func(_ *StaticRouteService, obj *v1alpha1.StaticRoute) (*model.StaticRoutes, error) {
		return nsxStaticRoute, nil
	})
	defer patchBuild.Reset()

	// Patch compareStaticRoute to return true (no update needed)
	patchCompare := gomonkey.ApplyPrivateMethod(reflect.TypeOf(service), "compareStaticRoute", func(_ *StaticRouteService, a, b *model.StaticRoutes) bool {
		return true
	})
	defer patchCompare.Reset()

	// Add existing static route to store
	service.StaticRouteStore.Add(nsxStaticRoute)
	err = service.CreateOrUpdateStaticRoute("ns", &v1alpha1.StaticRoute{})
	assert.NoError(t, err)

	// Patch compareStaticRoute to return false (update needed)
	patchCompare.Reset()
	patchCompare = gomonkey.ApplyPrivateMethod(reflect.TypeOf(service), "compareStaticRoute", func(_ *StaticRouteService, a, b *model.StaticRoutes) bool {
		return false
	})
	defer patchCompare.Reset()

	// Patch VPCService.ListVPCInfo to return empty (no VPC found)
	patchVPC := gomonkey.ApplyMethod(reflect.TypeOf(service.VPCService), "ListVPCInfo", func(_ common.VPCServiceProvider, ns string) []common.VPCResourceInfo {
		return []common.VPCResourceInfo{}
	})
	defer patchVPC.Reset()

	err = service.CreateOrUpdateStaticRoute("ns", &v1alpha1.StaticRoute{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no vpc found for ns ns")

	// Patch VPCService.ListVPCInfo to return valid VPC
	patchVPC.Reset()
	patchVPC = gomonkey.ApplyMethod(reflect.TypeOf(service.VPCService), "ListVPCInfo", func(_ common.VPCServiceProvider, ns string) []common.VPCResourceInfo {
		return []common.VPCResourceInfo{{OrgID: "org1", ProjectID: "proj1", VPCID: "vpc1", ID: "vpc1"}}
	})
	defer patchVPC.Reset()

	// Patch patch to return error
	patchPatch := gomonkey.ApplyPrivateMethod(reflect.TypeOf(service), "patch", func(_ *StaticRouteService, orgId, projectId, vpcId string, st *model.StaticRoutes) error {
		return fmt.Errorf("patch error")
	})
	defer patchPatch.Reset()

	err = service.CreateOrUpdateStaticRoute("ns", &v1alpha1.StaticRoute{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "patch error")

	// Patch patch to succeed, but StaticRouteClient.Get returns error
	patchPatch.Reset()
	patchPatch = gomonkey.ApplyPrivateMethod(reflect.TypeOf(service), "patch", func(_ *StaticRouteService, orgId, projectId, vpcId string, st *model.StaticRoutes) error {
		return nil
	})
	defer patchPatch.Reset()

	mockStaticRouteclient.EXPECT().Get("org1", "proj1", "vpc1", staticRouteID).Return(model.StaticRoutes{}, fmt.Errorf("get error")).Times(1)
	err = service.CreateOrUpdateStaticRoute("ns", &v1alpha1.StaticRoute{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "get error")

	// Patch StaticRouteClient.Get to succeed, but realization check fails and delete fails
	mockStaticRouteclient.EXPECT().Get("org1", "proj1", "vpc1", staticRouteID).Return(*nsxStaticRoute, nil).Times(1)
	patchRealize := gomonkey.ApplyFunc((*realizestate.RealizeStateService).CheckRealizeState,
		func(_ *realizestate.RealizeStateService, _ wait.Backoff, _ string, _ []string) error {
			return nsxutil.NewRealizeStateError("mocked realized error", 0)
		})
	defer patchRealize.Reset()
	patchDelete := gomonkey.ApplyMethod(reflect.TypeOf(service), "DeleteStaticRoute", func(_ *StaticRouteService, _ *model.StaticRoutes) error {
		return fmt.Errorf("delete error")
	})
	defer patchDelete.Reset()

	err = service.CreateOrUpdateStaticRoute("ns", &v1alpha1.StaticRoute{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "realization check failed")
	assert.Contains(t, err.Error(), "deletion failed")

	// Patch DeleteStaticRoute to succeed
	patchDelete.Reset()
	patchDelete = gomonkey.ApplyMethod(reflect.TypeOf(service), "DeleteStaticRoute", func(_ *StaticRouteService, _ *model.StaticRoutes) error {
		return nil
	})
	defer patchDelete.Reset()
	mockStaticRouteclient.EXPECT().Get("org1", "proj1", "vpc1", staticRouteID).Return(*nsxStaticRoute, nil).Times(1)
	err = service.CreateOrUpdateStaticRoute("ns", &v1alpha1.StaticRoute{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "realized error")

	// Patch Add to succeed, should return nil
	patchRealize.Reset()
	patchRealize = gomonkey.ApplyFunc((*realizestate.RealizeStateService).CheckRealizeState,
		func(_ *realizestate.RealizeStateService, _ wait.Backoff, _ string, _ []string) error {
			return nil
		})
	defer patchRealize.Reset()
	mockStaticRouteclient.EXPECT().Get("org1", "proj1", "vpc1", staticRouteID).Return(*nsxStaticRoute, nil).Times(1)
	err = service.CreateOrUpdateStaticRoute("ns", &v1alpha1.StaticRoute{})
	assert.NoError(t, err)
}
