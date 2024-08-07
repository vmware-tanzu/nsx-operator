package staticroute

import (
	"reflect"
	"sync"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/crd.nsx.vmware.com/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/staticrouteclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
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

type fakeQueryClient struct {
}

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
	rc, _ := cluster.NewRestConnector()

	mockCtrl := gomock.NewController(t)
	mockStaticRouteclient := mocks.NewMockStaticRoutesClient(mockCtrl)

	staticRouteStore := &StaticRouteStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeStaticRouteCRUID: indexFunc}),
		BindingType: model.StaticRoutesBindingType(),
	}}

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
		fatalErrors chan error, resourceTypeValue string, tags []model.Tag, store common.Store) {
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

func TestStaticRouteService_DeleteStaticRoute(t *testing.T) {
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
	id := "sr-uid-123"
	path := "/orgs/default/projects/project-1/vpcs/vpc-1"
	sr1 := &model.StaticRoutes{Id: &id, Path: &path}

	// no record found
	mockStaticRouteclient.EXPECT().Delete(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Times(0)
	err = returnservice.DeleteStaticRoute(srObj)
	assert.Equal(t, err, nil)

	returnservice.StaticRouteStore.Add(sr1)

	// delete record
	mockStaticRouteclient.EXPECT().Delete("default", "project-1", "vpc-1", id).Return(nil).Times(1)
	err = returnservice.DeleteStaticRoute(srObj)
	assert.Equal(t, err, nil)
	srs := returnservice.StaticRouteStore.List()
	assert.Equal(t, len(srs), 0)
}

func TestStaticRouteService_CreateorUpdateStaticRoute(t *testing.T) {
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
	id := "12345678"
	sr1 := &v1alpha1.StaticRoute{}
	sr1.UID = types.UID(id)

	mockStaticRouteclient.EXPECT().Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1)
	mId := "test_id"
	scope := common.TagScopeStaticRouteCRUID
	tag := "test_tag"
	m := model.StaticRoutes{
		Id:   &mId,
		Tags: []model.Tag{{Tag: &tag, Scope: &scope}},
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
