package ipaddressallocation

import (
	"context"
	"fmt"
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

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/ipaddressallocation"
	mock_org_root "github.com/vmware-tanzu/nsx-operator/pkg/mock/orgrootclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func createIPAddressAllocationService(t *testing.T) (*IPAddressAllocationService, *gomock.Controller, *mocks.MockIPAddressAllocationClient) {
	config2 := nsx.NewConfig("localhost", "1", "1", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})

	cluster, _ := nsx.NewCluster(config2)
	rc := cluster.NewRestConnector()

	mockCtrl := gomock.NewController(t)
	mockVPCIPAddressAllocationclient := mocks.NewMockIPAddressAllocationClient(mockCtrl)

	ipAddressAllocationStore := &IPAddressAllocationStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeIPAddressAllocationCRUID: indexFunc}),
		BindingType: model.VpcIpAddressAllocationBindingType(),
	}}

	service := &IPAddressAllocationService{
		Service: common.Service{
			NSXClient: &nsx.Client{
				QueryClient:               &fakeQueryClient{},
				IPAddressAllocationClient: mockVPCIPAddressAllocationclient,
				RestConnector:             rc,
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
		ipAddressAllocationStore: ipAddressAllocationStore,
	}
	return service, mockCtrl, mockVPCIPAddressAllocationclient
}

func Test_InitializeIPAddressAllocationStore(t *testing.T) {
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

	_, err := InitializeIPAddressAllocation(commonService, vpcService, false)
	if err != nil {
		t.Error(err)
	}
}

func TestIPAddressAllocationService_DeleteIPAddressAllocation(t *testing.T) {
	service, mockController, mockVPCIPAddressAllocationclient := createIPAddressAllocationService(t)
	defer mockController.Finish()

	var tc *bindings.TypeConverter
	vpcPath := "/orgs/default/projects/project-1/vpcs/vpc-1"
	patchConvertToGolang := gomonkey.ApplyMethod(reflect.TypeOf(tc), "ConvertToGolang",
		func(_ *bindings.TypeConverter, d data.DataValue, b bindings.BindingType) (interface{}, []error) {
			mId, mTag, mScope := "test_id", "test_tag", "test_scope"
			m := model.VpcIpAddressAllocation{
				Id:         &mId,
				Tags:       []model.Tag{{Tag: &mTag, Scope: &mScope}},
				Path:       String(fmt.Sprintf("%s/ip-address-allocations/%s", vpcPath, mId)),
				ParentPath: String(vpcPath),
			}
			var j interface{} = m
			return j, nil
		})
	defer patchConvertToGolang.Reset()
	vpcService := &vpc.VPCService{}
	returnservice, err := InitializeIPAddressAllocation(service.Service, vpcService, false)
	if err != nil {
		t.Error(err)
	}

	srObj := &v1alpha1.IPAddressAllocation{
		ObjectMeta: v1.ObjectMeta{
			UID:  "uid-123",
			Name: "sr",
		},
	}
	id := util.GenerateIDByObject(srObj)
	tags := util.BuildBasicTags(service.NSXConfig.Cluster, srObj, "")
	path := fmt.Sprintf("%s/ip-address-allocations/%s", vpcPath, id)
	sr1 := &model.VpcIpAddressAllocation{Id: &id, Path: &path, Tags: tags, ParentPath: &vpcPath}

	// no record found
	mockVPCIPAddressAllocationclient.EXPECT().Delete(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Times(0)
	err = returnservice.DeleteIPAddressAllocation(srObj)
	assert.Equal(t, err, nil)

	returnservice.ipAddressAllocationStore.Add(sr1)

	// delete record
	mockVPCIPAddressAllocationclient.EXPECT().Delete("default", "project-1", "vpc-1", id).Return(nil).Times(1)
	err = returnservice.DeleteIPAddressAllocation(srObj)
	assert.Equal(t, err, nil)
	srs := returnservice.ipAddressAllocationStore.List()
	assert.Equal(t, len(srs), 1)
}

func TestIPAddressAllocationService_CreateorUpdateIPAddressAllocation(t *testing.T) {
	service, mockController, mockVPCIPAddressallocationclient := createIPAddressAllocationService(t)
	defer mockController.Finish()

	vpcPath := "/orgs/default/projects/project-1/vpcs/vpc-1"
	var tc *bindings.TypeConverter
	patchConvertToGolang := gomonkey.ApplyMethod(reflect.TypeOf(tc), "ConvertToGolang",
		func(_ *bindings.TypeConverter, d data.DataValue, b bindings.BindingType) (interface{}, []error) {
			mId, mTag, mScope := "test_id", "test_tag", "test_scope"
			m := model.VpcIpAddressAllocation{
				Id:         &mId,
				Tags:       []model.Tag{{Tag: &mTag, Scope: &mScope}},
				ParentPath: &vpcPath,
				Path:       String(fmt.Sprintf("%s/ip-address-allocations/%s", vpcPath, mId)),
			}
			var j interface{} = m
			return j, nil
		})
	defer patchConvertToGolang.Reset()

	vpcService := &vpc.VPCService{}
	returnservice, err := InitializeIPAddressAllocation(service.Service, vpcService, false)
	if err != nil {
		t.Error(err)
	}
	id := "test_id"
	ipa1 := &v1alpha1.IPAddressAllocation{}
	ipa1.UID = types.UID(id)

	mockVPCIPAddressallocationclient.EXPECT().Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1)
	mId := "test_id"
	scope := common.TagScopeIPAddressAllocationCRUID
	tag := "test_tag"
	cidr := "192.168.1.0/24"
	m := model.VpcIpAddressAllocation{
		Id:            &mId,
		Tags:          []model.Tag{{Tag: &tag, Scope: &scope}},
		ParentPath:    &vpcPath,
		Path:          String(fmt.Sprintf("%s/ip-address-allocations/%s", vpcPath, mId)),
		AllocationIps: &cidr,
	}
	mockVPCIPAddressallocationclient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(m, nil).Times(2)
	patchListVPCInfo := gomonkey.ApplyMethod(reflect.TypeOf(returnservice.VPCService), "ListVPCInfo", func(_ common.VPCServiceProvider, ns string) []common.VPCResourceInfo {
		id := "12345678"
		return []common.VPCResourceInfo{{OrgID: "default", ProjectID: "project-1", VPCID: "vpc-1", ID: id}}
	})
	defer patchListVPCInfo.Reset()

	patchGetByIndex := gomonkey.ApplyMethod(reflect.TypeOf(returnservice.ipAddressAllocationStore), "GetByIndex", func(_ *IPAddressAllocationStore,
		_ types.UID) (*model.VpcIpAddressAllocation, error) {
		return &m, nil
	})
	defer patchGetByIndex.Reset()

	_, err = returnservice.CreateOrUpdateIPAddressAllocation(ipa1)
	assert.Equal(t, err, nil)

	// no change, update
	mockVPCIPAddressallocationclient.EXPECT().Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1)
	_, err = returnservice.CreateOrUpdateIPAddressAllocation(ipa1)
	assert.Equal(t, err, nil)
}

func TestIPAddressAllocationService_Cleanup(t *testing.T) {
	service, mockController, _ := createIPAddressAllocationService(t)
	defer mockController.Finish()

	mockOrgRootClient := mock_org_root.NewMockOrgRootClient(mockController)
	service.NSXClient.OrgRootClient = mockOrgRootClient

	vpcPath := "/orgs/default/projects/project-1/vpcs/vpc-1"
	var tc *bindings.TypeConverter
	patchConvertToGolang := gomonkey.ApplyMethod(reflect.TypeOf(tc), "ConvertToGolang",
		func(_ *bindings.TypeConverter, d data.DataValue, b bindings.BindingType) (interface{}, []error) {
			mId, mTag, mScope := "test_id", "test_tag", "test_scope"
			m := model.VpcIpAddressAllocation{
				Id:         &mId,
				Tags:       []model.Tag{{Tag: &mTag, Scope: &mScope}},
				Path:       String(fmt.Sprintf("%s/ip-address-allocations/%s", vpcPath, mId)),
				ParentPath: &vpcPath,
			}
			var j interface{} = m
			return j, nil
		})
	defer patchConvertToGolang.Reset()

	vpcService := &vpc.VPCService{}
	returnService, err := InitializeIPAddressAllocation(service.Service, vpcService, false)
	assert.NoError(t, err)

	// Set up expectations
	mockOrgRootClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil)

	// Call Cleanup
	ctx := context.Background()
	err = returnService.CleanupVPCChildResources(ctx, "")

	// Assert
	assert.NoError(t, err)
	assert.Equal(t, 0, len(returnService.ipAddressAllocationStore.List()), "IPAddressAllocation store should be empty after cleanup")

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel the context immediately

	returnService, err = InitializeIPAddressAllocation(service.Service, vpcService, false)
	assert.NoError(t, err)

	err = returnService.CleanupVPCChildResources(cancelledCtx, "")
	assert.Error(t, err)
}

func TestIPAddressAllocationService_ListIPAddressAllocationID(t *testing.T) {
	service, mockController, _ := createIPAddressAllocationService(t)
	defer mockController.Finish()

	vpcPath := "/orgs/default/projects/project-1/vpcs/vpc-1"
	var tc *bindings.TypeConverter
	patchConvertToGolang := gomonkey.ApplyMethod(reflect.TypeOf(tc), "ConvertToGolang",
		func(_ *bindings.TypeConverter, d data.DataValue, b bindings.BindingType) (interface{}, []error) {
			mId, mTag, mScope := "test_id", "test_tag", "test_scope"
			m := model.VpcIpAddressAllocation{
				Id:         &mId,
				Tags:       []model.Tag{{Tag: &mTag, Scope: &mScope}},
				ParentPath: &vpcPath,
				Path:       String(fmt.Sprintf("%s/ip-address-allocations/%s", vpcPath, mId)),
			}
			var j interface{} = m
			return j, nil
		})
	defer patchConvertToGolang.Reset()

	vpcService := &vpc.VPCService{}
	returnService, err := InitializeIPAddressAllocation(service.Service, vpcService, false)
	assert.NoError(t, err)

	// Add test data
	ipa1 := &v1alpha1.IPAddressAllocation{
		ObjectMeta: v1.ObjectMeta{
			UID:  types.UID("uid-123"),
			Name: "ipa-1",
		},
	}
	ipa2 := &v1alpha1.IPAddressAllocation{
		ObjectMeta: v1.ObjectMeta{
			UID:  types.UID("uid-456"),
			Name: "ipa-2",
		},
	}

	id1 := util.GenerateIDByObject(ipa1)
	id2 := util.GenerateIDByObject(ipa2)

	sr1 := &model.VpcIpAddressAllocation{Id: &id1, ParentPath: &vpcPath, Path: String(fmt.Sprintf("%s/ip-address-allocations/%s", vpcPath, id1)), Tags: util.BuildBasicTags(service.NSXConfig.Cluster, ipa1, "")}
	sr2 := &model.VpcIpAddressAllocation{Id: &id2, ParentPath: &vpcPath, Path: String(fmt.Sprintf("%s/ip-address-allocations/%s", vpcPath, id2)), Tags: util.BuildBasicTags(service.NSXConfig.Cluster, ipa2, "")}

	returnService.ipAddressAllocationStore.Add(sr1)
	returnService.ipAddressAllocationStore.Add(sr2)

	// Test ListIPAddressAllocationID
	idSet := returnService.ListIPAddressAllocationID()
	assert.Equal(t, 2, idSet.Len())
	assert.True(t, idSet.Has(string(ipa1.UID)))
	assert.True(t, idSet.Has(string(ipa2.UID)))
}

func TestIPAddressAllocationService_ListIPAddressAllocationKeys(t *testing.T) {
	service, mockController, _ := createIPAddressAllocationService(t)
	defer mockController.Finish()

	vpcPath := "/orgs/default/projects/project-1/vpcs/vpc-1"
	var tc *bindings.TypeConverter
	patchConvertToGolang := gomonkey.ApplyMethod(reflect.TypeOf(tc), "ConvertToGolang",
		func(_ *bindings.TypeConverter, d data.DataValue, b bindings.BindingType) (interface{}, []error) {
			mId, mTag, mScope := "test_id", "test_tag", "test_scope"
			m := model.VpcIpAddressAllocation{
				Id:         &mId,
				Tags:       []model.Tag{{Tag: &mTag, Scope: &mScope}},
				ParentPath: &vpcPath,
				Path:       String(fmt.Sprintf("%s/ip-address-allocations/%s", vpcPath, mId)),
			}
			var j interface{} = m
			return j, nil
		})
	defer patchConvertToGolang.Reset()

	vpcService := &vpc.VPCService{}
	returnService, err := InitializeIPAddressAllocation(service.Service, vpcService, false)
	assert.NoError(t, err)

	// Add test data
	ipa1 := &v1alpha1.IPAddressAllocation{
		ObjectMeta: v1.ObjectMeta{
			UID:  types.UID("uid-123"),
			Name: "ipa-1",
		},
	}
	ipa2 := &v1alpha1.IPAddressAllocation{
		ObjectMeta: v1.ObjectMeta{
			UID:  types.UID("uid-456"),
			Name: "ipa-2",
		},
	}

	id1 := util.GenerateIDByObject(ipa1)
	id2 := util.GenerateIDByObject(ipa2)

	sr1 := &model.VpcIpAddressAllocation{Id: &id1, ParentPath: &vpcPath, Path: String(fmt.Sprintf("%s/ip-address-allocations/%s", vpcPath, id1)), Tags: util.BuildBasicTags(service.NSXConfig.Cluster, ipa1, "")}
	sr2 := &model.VpcIpAddressAllocation{Id: &id2, ParentPath: &vpcPath, Path: String(fmt.Sprintf("%s/ip-address-allocations/%s", vpcPath, id2)), Tags: util.BuildBasicTags(service.NSXConfig.Cluster, ipa2, "")}

	returnService.ipAddressAllocationStore.Add(sr1)
	returnService.ipAddressAllocationStore.Add(sr2)

	// Test ListIPAddressAllocationKeys
	keys := returnService.ListIPAddressAllocationKeys()
	assert.Equal(t, 3, len(keys))
	assert.Contains(t, keys, id1)
	assert.Contains(t, keys, id2)
}

func TestIPAddressAllocationService_CreateOrUpdateIPAddressAllocation_Errors(t *testing.T) {
	service, mockController, _ := createIPAddressAllocationService(t)
	defer mockController.Finish()

	vpcPath := "/orgs/default/projects/project-1/vpcs/vpc-1"
	var tc *bindings.TypeConverter
	patchConvertToGolang := gomonkey.ApplyMethod(reflect.TypeOf(tc), "ConvertToGolang",
		func(_ *bindings.TypeConverter, d data.DataValue, b bindings.BindingType) (interface{}, []error) {
			mId := "test_id"
			m := model.VpcIpAddressAllocation{
				Id:         &mId,
				ParentPath: &vpcPath,
				Path:       String(fmt.Sprintf("%s/ip-address-allocations/%s", vpcPath, mId)),
			}
			var j interface{} = m
			return j, nil
		})
	defer patchConvertToGolang.Reset()
	vpcService := &vpc.VPCService{}
	returnservice, _ := InitializeIPAddressAllocation(service.Service, vpcService, false)

	ipa := &v1alpha1.IPAddressAllocation{
		ObjectMeta: v1.ObjectMeta{
			UID:  types.UID("test-uid"),
			Name: "test-ipa",
		},
	}

	// Test case: BuildIPAddressAllocation error
	patchBuildIPAddressAllocation := gomonkey.ApplyMethod(reflect.TypeOf(returnservice), "BuildIPAddressAllocation",
		func(_ *IPAddressAllocationService, _ *v1alpha1.IPAddressAllocation) (*model.VpcIpAddressAllocation, error) {
			return nil, fmt.Errorf("build error")
		})
	_, err := returnservice.CreateOrUpdateIPAddressAllocation(ipa)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "build error")
	patchBuildIPAddressAllocation.Reset()

	// Test case: Apply error
	patchApply := gomonkey.ApplyMethod(reflect.TypeOf(returnservice), "Apply",
		func(_ *IPAddressAllocationService, _ *model.VpcIpAddressAllocation) error {
			return fmt.Errorf("apply error")
		})
	defer patchApply.Reset()

	mId := "test_id"
	scope := common.TagScopeIPAddressAllocationCRUID
	tag := "test_tag"
	cidr := "192.168.1.0/24"
	m := model.VpcIpAddressAllocation{
		Id:            &mId,
		Tags:          []model.Tag{{Tag: &tag, Scope: &scope}},
		AllocationIps: &cidr,
	}
	patchGetByIndex := gomonkey.ApplyMethod(reflect.TypeOf(returnservice.ipAddressAllocationStore), "GetByIndex", func(_ *IPAddressAllocationStore,
		_ types.UID) (*model.VpcIpAddressAllocation, error) {
		return &m, nil
	})
	defer patchGetByIndex.Reset()

	patchListVPCInfo := gomonkey.ApplyMethod(reflect.TypeOf(returnservice.VPCService), "ListVPCInfo", func(_ common.VPCServiceProvider, ns string) []common.VPCResourceInfo {
		id := "12345678"
		return []common.VPCResourceInfo{{OrgID: "default", ProjectID: "project-1", VPCID: "vpc-1", ID: id}}
	})
	defer patchListVPCInfo.Reset()

	_, err = returnservice.CreateOrUpdateIPAddressAllocation(ipa)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "apply error")
}

func TestIPAddressAllocationService_DeleteIPAddressAllocation_Errors(t *testing.T) {
	service, mockController, mockVPCIPAddressAllocationclient := createIPAddressAllocationService(t)
	defer mockController.Finish()

	vpcPath := "/orgs/default/projects/project-1/vpcs/vpc-1"
	var tc *bindings.TypeConverter
	patchConvertToGolang := gomonkey.ApplyMethod(reflect.TypeOf(tc), "ConvertToGolang",
		func(_ *bindings.TypeConverter, d data.DataValue, b bindings.BindingType) (interface{}, []error) {
			mId, mTag, mScope := "test_id", "test_tag", "test_scope"
			m := model.VpcIpAddressAllocation{
				Id:         &mId,
				ParentPath: &vpcPath,
				Path:       String(fmt.Sprintf("/orgs/default/projects/project-1/vpcs/vpc-1/ip-address-allocations/%s", mId)),
				Tags:       []model.Tag{{Tag: &mTag, Scope: &mScope}},
			}
			var j interface{} = m
			return j, nil
		})
	defer patchConvertToGolang.Reset()
	vpcService := &vpc.VPCService{}
	returnservice, err := InitializeIPAddressAllocation(service.Service, vpcService, false)
	if err != nil {
		t.Error(err)
	}

	srObj := &v1alpha1.IPAddressAllocation{
		ObjectMeta: v1.ObjectMeta{
			UID:  "uid-123",
			Name: "sr",
		},
	}
	id := util.GenerateIDByObject(srObj)
	tags := util.BuildBasicTags(service.NSXConfig.Cluster, srObj, "")
	sr1 := &model.VpcIpAddressAllocation{Id: &id, ParentPath: &vpcPath, Path: String(fmt.Sprintf("%s/ip-address-allocations/%s", vpcPath, id)), Tags: tags}

	returnservice.ipAddressAllocationStore.Add(sr1)

	// Test case: ParseVPCResourcePath error
	patchParseVPCResourcePath := gomonkey.ApplyFunc(common.ParseVPCResourcePath,
		func(_ string) (common.VPCResourceInfo, error) {
			return common.VPCResourceInfo{}, fmt.Errorf("parse error")
		})

	err = returnservice.DeleteIPAddressAllocation(id)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse error")
	patchParseVPCResourcePath.Reset()

	// Test case: IPAddressAllocationClient.Delete error
	mockVPCIPAddressAllocationclient.EXPECT().Delete(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(fmt.Errorf("delete error")).Times(1)

	err = returnservice.DeleteIPAddressAllocation(id)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "delete error")

}

func TestIPAddressAllocationService_DeleteIPAddressAllocationByNamespacedName(t *testing.T) {
	service, mockController, _ := createIPAddressAllocationService(t)
	defer mockController.Finish()

	srObj := &v1alpha1.IPAddressAllocation{
		ObjectMeta: v1.ObjectMeta{
			UID:       "uid-123",
			Name:      "ipa-1",
			Namespace: "ns-1",
		},
	}
	id := util.GenerateIDByObject(srObj)
	tags := util.BuildBasicTags(service.NSXConfig.Cluster, srObj, "")
	path := "/orgs/default/projects/project-1/vpcs/vpc-1"
	sr1 := &model.VpcIpAddressAllocation{Id: &id, Path: &path, Tags: tags}

	service.ipAddressAllocationStore.Add(sr1)
	// Successful deletion
	patches := gomonkey.ApplyFunc((*IPAddressAllocationService).DeleteIPAddressAllocation, func(service *IPAddressAllocationService, obj interface{}) error {
		ipAddressAllocation, ok := obj.(model.VpcIpAddressAllocation)
		assert.True(t, ok)
		assert.Equal(t, id, *ipAddressAllocation.Id)
		return nil
	})
	err := service.DeleteIPAddressAllocationByNamespacedName("ns-1", "ipa-1")
	assert.Nil(t, err)
	patches.Reset()
	// failed deletion
	patches = gomonkey.ApplyFunc((*IPAddressAllocationService).DeleteIPAddressAllocation, func(service *IPAddressAllocationService, obj interface{}) error {
		return fmt.Errorf("delete error")
	})
	err = service.DeleteIPAddressAllocationByNamespacedName("ns-1", "ipa-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "delete error")
	patches.Reset()
}

func TestIPAddressAllocationService_Cleanup_Error(t *testing.T) {
	service, mockController, _ := createIPAddressAllocationService(t)
	defer mockController.Finish()
	var tc *bindings.TypeConverter
	vpcPath := "/orgs/default/projects/project-1/vpcs/vpc-1"
	patchConvertToGolang := gomonkey.ApplyMethod(reflect.TypeOf(tc), "ConvertToGolang",
		func(_ *bindings.TypeConverter, d data.DataValue, b bindings.BindingType) (interface{}, []error) {
			mId, mTag, mScope := "test_id", "test_tag", "test_scope"
			m := model.VpcIpAddressAllocation{
				Id:         &mId,
				Tags:       []model.Tag{{Tag: &mTag, Scope: &mScope}},
				ParentPath: &vpcPath,
				Path:       String(fmt.Sprintf("%s/ip-address-allocations/%s", vpcPath, mId)),
			}
			var j interface{} = m
			return j, nil
		})
	defer patchConvertToGolang.Reset()

	vpcService := &vpc.VPCService{}
	returnservice, _ := InitializeIPAddressAllocation(service.Service, vpcService, false)
	mockOrgRootClient := mock_org_root.NewMockOrgRootClient(mockController)
	returnservice.NSXClient.OrgRootClient = mockOrgRootClient

	// Add a test IPAddressAllocation to the store
	testIPA := &model.VpcIpAddressAllocation{
		Id:         String("test-id"),
		Path:       String("/test/path"),
		ParentPath: String(vpcPath),
	}
	returnservice.ipAddressAllocationStore.Add(testIPA)

	// Test case: DeleteIPAddressAllocation error
	mockOrgRootClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(fmt.Errorf("delete error"))

	ctx := context.Background()
	err := returnservice.CleanupVPCChildResources(ctx, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "delete error")
}
