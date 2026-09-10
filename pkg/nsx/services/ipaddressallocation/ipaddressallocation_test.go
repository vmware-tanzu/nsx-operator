package ipaddressallocation

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/mock/gomock"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
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
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
			common.TagScopeIPAddressAllocationCRUID: indexByIPAddressAllocation,
			common.TagScopeAddressBindingCRUID:      indexByAddressBinding,
			common.TagScopeSubnetPortCRUID:          indexBySubnetPort,
		}),
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

	// Init successfully
	commonService := service.Service
	patch := gomonkey.ApplyMethod(reflect.TypeOf(&commonService), "InitializeResourceStore", func(_ *common.Service, wg *sync.WaitGroup,
		fatalErrors chan error, resourceTypeValue string, tags []model.Tag, store common.Store,
	) {
		wg.Done()
	})

	vpcService := &vpc.VPCService{}

	_, err := InitializeIPAddressAllocation(commonService, vpcService, false)
	assert.NoError(t, err)

	// Init failed
	patch.Reset()
	var callCount int32
	patch = gomonkey.ApplyMethod(reflect.TypeOf(&commonService), "InitializeResourceStore", func(_ *common.Service, wg *sync.WaitGroup,
		fatalErrors chan error, resourceTypeValue string, tags []model.Tag, store common.Store,
	) {
		defer wg.Done()
		if atomic.AddInt32(&callCount, 1) > 1 {
			fatalErrors <- errors.New("init failed")
		} else {
			return
		}
	})
	defer patch.Reset()

	_, err = InitializeIPAddressAllocation(commonService, vpcService, true)
	if err != nil {
		assert.EqualError(t, err, "init failed")
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
	assert.Nil(t, err)

	returnservice.ipAddressAllocationStore.Add(sr1)

	// delete record
	mockVPCIPAddressAllocationclient.EXPECT().Delete("default", "project-1", "vpc-1", id).Return(nil).Times(1)
	err = returnservice.DeleteIPAddressAllocation(srObj)
	assert.Nil(t, err)
	srs := returnservice.ipAddressAllocationStore.List()
	assert.Equal(t, 0, len(srs))
}

func TestIPAddressAllocationService_CreateOrUpdateIPAddressAllocation(t *testing.T) {
	service, mockController, mockVPCIPAddressallocationclient := createIPAddressAllocationService(t)
	defer mockController.Finish()

	nsxAllocID := "guestcluster-workers-a_du8nz"
	allocCRUID := "f0dae8c7-9cdd-49df-aa68-b1cefc8a1291"
	name := "guestcluster-workers-a"
	namespace := "ns-1"
	nsUUID := "nsUuid"
	vpcPath := "/orgs/default/projects/project-1/vpcs/vpc-1"
	size := int64(256)
	alloc := model.VpcIpAddressAllocation{
		Id:          &nsxAllocID,
		DisplayName: &name,
		Tags: []model.Tag{
			{
				Scope: common.String("nsx-op/cluster"),
				Tag:   common.String("k8scl-one:test"),
			},
			{
				Scope: common.String("nsx-op/version"),
				Tag:   common.String("1.0.0"),
			},
			{
				Scope: common.String("nsx-op/namespace"),
				Tag:   common.String(namespace),
			},
			{
				Scope: common.String("nsx-op/ipaddressallocation_name"),
				Tag:   common.String(name),
			},
			{
				Scope: common.String("nsx-op/ipaddressallocation_uid"),
				Tag:   common.String(allocCRUID),
			},
			{
				Scope: String(common.TagScopeNamespaceUID),
				Tag:   String(nsUUID),
			},
		},
		ParentPath:               &vpcPath,
		Path:                     String(fmt.Sprintf("%s/ip-address-allocations/%s", vpcPath, nsxAllocID)),
		AllocationIps:            common.String("192.168.1.0/24"),
		AllocationSize:           &size,
		IpAddressBlockVisibility: common.String("PRIVATE"),
		IpAddressType:            common.String(model.VpcIpAddressAllocation_IP_ADDRESS_TYPE_IPV4),
	}

	var tc *bindings.TypeConverter
	patchConvertToGolang := gomonkey.ApplyMethod(reflect.TypeOf(tc), "ConvertToGolang",
		func(_ *bindings.TypeConverter, d data.DataValue, b bindings.BindingType) (interface{}, []error) {
			var j interface{} = alloc
			return j, nil
		})
	defer patchConvertToGolang.Reset()

	vpcService := &vpc.VPCService{}
	returnservice, err := InitializeIPAddressAllocation(service.Service, vpcService, false)
	if err != nil {
		t.Error(err)
	}
	ipa1 := &v1alpha1.IPAddressAllocation{
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       types.UID(allocCRUID),
		},
		Spec: v1alpha1.IPAddressAllocationSpec{
			AllocationSize:           256,
			IPAddressBlockVisibility: "Private",
		},
	}

	patchGetNamespaceUID := gomonkey.ApplyMethod(reflect.TypeOf(&returnservice.Service), "GetNamespaceUID",
		func(s *common.Service, ns string) types.UID {
			return types.UID(nsUUID)
		})
	defer patchGetNamespaceUID.Reset()

	patchListVPCInfo := gomonkey.ApplyMethod(reflect.TypeOf(returnservice.VPCService), "ListVPCInfo", func(_ common.VPCServiceProvider, ns string) []common.VPCResourceInfo {
		id := "12345678"
		return []common.VPCResourceInfo{{OrgID: "default", ProjectID: "project-1", VPCID: "vpc-1", ID: id}}
	})
	defer patchListVPCInfo.Reset()

	t.Run("test create or update IPAddressAllocation", func(t *testing.T) {
		mockVPCIPAddressallocationclient.EXPECT().Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1)
		mockVPCIPAddressallocationclient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(alloc, nil).Times(1)

		var callCount int
		patchGetByUID := gomonkey.ApplyMethod(reflect.TypeOf(returnservice.ipAddressAllocationStore), "GetByUID", func(_ *IPAddressAllocationStore,
			_ types.UID) (*model.VpcIpAddressAllocation, error) {
			callCount++
			if callCount == 1 {
				return nil, nil
			}
			return &alloc, nil
		})
		defer patchGetByUID.Reset()

		_, err = returnservice.CreateOrUpdateIPAddressAllocation(context.Background(), ipa1, false)
		assert.Nil(t, err)

		// no change, not update
		_, err = returnservice.CreateOrUpdateIPAddressAllocation(context.Background(), ipa1, false)
		assert.Nil(t, err)
	})

	t.Run("test update CR status when operator is restarted", func(t *testing.T) {
		patchGetByUID := gomonkey.ApplyMethod(reflect.TypeOf(returnservice.ipAddressAllocationStore), "GetByUID", func(_ *IPAddressAllocationStore,
			_ types.UID) (*model.VpcIpAddressAllocation, error) {
			return &alloc, nil
		})
		defer patchGetByUID.Reset()

		ipAddressAllocationWithoutStatus := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				UID:       types.UID(allocCRUID),
			},
			Spec: v1alpha1.IPAddressAllocationSpec{
				AllocationSize:           256,
				IPAddressBlockVisibility: "Private",
			},
		}

		changed, err := returnservice.CreateOrUpdateIPAddressAllocation(context.Background(), ipAddressAllocationWithoutStatus, false)
		assert.Nil(t, err)
		assert.True(t, changed)
		assert.Equal(t, "192.168.1.0/24", ipAddressAllocationWithoutStatus.Status.AllocationIPs)
	})

	t.Run("test restore IPAddressAllocation", func(t *testing.T) {
		// restore 192.168.1.0/24
		mockVPCIPAddressallocationclient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(alloc, nil).Times(1)
		mockVPCIPAddressallocationclient.EXPECT().Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1)
		// Expected allocation for the restored alloc
		restoredAlloc := alloc
		restoredAlloc.AllocationSize = nil
		// Expected allocation for the update with a single IP reconciled after restored.
		updatedAlloc := alloc
		updatedAlloc.AllocationIps = common.String("192.168.1.4")
		updatedAlloc.AllocationSize = nil
		patchGetByUIDInRestore := gomonkey.ApplyMethodSeq(reflect.TypeOf(returnservice.ipAddressAllocationStore), "GetByUID", []gomonkey.OutputCell{{
			Values: gomonkey.Params{
				nil,
				nil,
			},
		}, {
			Values: gomonkey.Params{
				&alloc,
				nil,
			},
		}, {
			Values: gomonkey.Params{
				&restoredAlloc,
				nil,
			},
		}, {
			Values: gomonkey.Params{
				&updatedAlloc,
				nil,
			},
		},
		})
		defer patchGetByUIDInRestore.Reset()
		ipAddressAllocationWithStatus := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				UID:       types.UID(allocCRUID),
			},
			Spec: v1alpha1.IPAddressAllocationSpec{
				AllocationSize:           256,
				IPAddressBlockVisibility: "Private",
			},
			Status: v1alpha1.IPAddressAllocationStatus{
				AllocationIPs: "192.168.1.0/24",
			},
		}
		_, err = returnservice.CreateOrUpdateIPAddressAllocation(context.Background(), ipAddressAllocationWithStatus, true)
		assert.Nil(t, err)

		//reconcile 192.168.1.0/24 after restored. No additional patch on NSX.
		_, err = returnservice.CreateOrUpdateIPAddressAllocation(context.Background(), ipAddressAllocationWithStatus, false)
		assert.Nil(t, err)

		// reconcile 192.168.1.4 after restored
		ipAddressAllocationWithStatus = &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				UID:       types.UID(allocCRUID),
			},
			Spec: v1alpha1.IPAddressAllocationSpec{
				AllocationSize:           1,
				IPAddressBlockVisibility: "Private",
			},
			Status: v1alpha1.IPAddressAllocationStatus{
				AllocationIPs: "192.168.1.4",
			},
		}
		_, err = returnservice.CreateOrUpdateIPAddressAllocation(context.Background(), ipAddressAllocationWithStatus, false)
		assert.Nil(t, err)
	})
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
	mockOrgRootClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

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
	assert.NoError(t, err)
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
	assert.Equal(t, 2, len(keys))
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

	patchGetNamespaceUID := gomonkey.ApplyMethod(reflect.TypeOf(&returnservice.Service), "GetNamespaceUID", func(s *common.Service, ns string) types.UID {
		return types.UID("nsUuid")
	})
	defer patchGetNamespaceUID.Reset()

	// Test case: BuildIPAddressAllocation error
	patchBuildIPAddressAllocation := gomonkey.ApplyMethod(reflect.TypeOf(returnservice), "BuildIPAddressAllocation",
		func(_ *IPAddressAllocationService, _ v1.Object, _ *v1alpha1.SubnetPort, _ bool) (*model.VpcIpAddressAllocation, []common.VPCResourceInfo, error) {
			return nil, nil, fmt.Errorf("build error")
		})
	_, err := returnservice.CreateOrUpdateIPAddressAllocation(context.Background(), ipa, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "build error")
	patchBuildIPAddressAllocation.Reset()

	// Test case: Apply error
	patchApply := gomonkey.ApplyMethod(reflect.TypeOf(returnservice), "Apply",
		func(_ *IPAddressAllocationService, _ *model.VpcIpAddressAllocation, _ []common.VPCResourceInfo) error {
			return fmt.Errorf("apply error")
		})
	defer patchApply.Reset()

	mId := "test_id"
	scope := common.TagScopeIPAddressAllocationCRUID
	tag := "test_tag"
	cidr := "192.168.1.0/24"
	m := model.VpcIpAddressAllocation{
		Id:            &mId,
		DisplayName:   String("test"),
		Tags:          []model.Tag{{Tag: &tag, Scope: &scope}},
		AllocationIps: &cidr,
	}
	patchGetByUID := gomonkey.ApplyMethod(reflect.TypeOf(returnservice.ipAddressAllocationStore), "GetByUID", func(_ *IPAddressAllocationStore,
		_ types.UID) (*model.VpcIpAddressAllocation, error) {
		return &m, nil
	})
	defer patchGetByUID.Reset()

	patchListVPCInfo := gomonkey.ApplyMethod(reflect.TypeOf(returnservice.VPCService), "ListVPCInfo", func(_ common.VPCServiceProvider, ns string) []common.VPCResourceInfo {
		id := "12345678"
		return []common.VPCResourceInfo{{OrgID: "default", ProjectID: "project-1", VPCID: "vpc-1", ID: id}}
	})
	defer patchListVPCInfo.Reset()

	_, err = returnservice.CreateOrUpdateIPAddressAllocation(context.Background(), ipa, false)
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

func TestIPAddressAllocationService_CreateIPAddressAllocationForAddressBinding(t *testing.T) {
	ab1 := &v1alpha1.AddressBinding{
		ObjectMeta: v1.ObjectMeta{
			Namespace: "ns-1",
			Name:      "ab-1",
			UID:       "ab-1",
		},
		Spec: v1alpha1.AddressBindingSpec{
			VMName: "vm-1",
		},
		Status: v1alpha1.AddressBindingStatus{
			IPAddress: "192.0.0.8",
		},
	}
	ab2 := &v1alpha1.AddressBinding{
		ObjectMeta: v1.ObjectMeta{
			Namespace: "ns-1",
			Name:      "ab-2",
			UID:       "ab-2",
		},
		Spec: v1alpha1.AddressBindingSpec{
			VMName:                  "vm-2",
			IPAddressAllocationName: "ipa-1",
		},
		Status: v1alpha1.AddressBindingStatus{
			IPAddress: "192.0.0.10",
		},
	}
	subnetport := &v1alpha1.SubnetPort{
		ObjectMeta: v1.ObjectMeta{
			Namespace: "ns-1",
			Name:      "port-1",
			UID:       "port-1",
		},
	}
	ipa := &model.VpcIpAddressAllocation{
		Id: common.String("ab-1"),
		Tags: []model.Tag{
			{
				Scope: common.String(common.TagScopeAddressBindingCRUID),
				Tag:   common.String("ab-1"),
			},
		},
	}
	service, mockController, _ := createIPAddressAllocationService(t)
	defer mockController.Finish()
	// No AddressBinding for SubnetPort
	err := service.CreateIPAddressAllocationForAddressBinding(nil, subnetport, true)
	assert.Nil(t, err)

	// IPAddressAllocation exists for AddressBinding
	service.ipAddressAllocationStore.Add(ipa)
	err = service.CreateIPAddressAllocationForAddressBinding(ab1, subnetport, true)
	assert.Nil(t, err)
	service.ipAddressAllocationStore.Delete(ipa)

	// AddressBinding with specified IPAddressAllocation
	err = service.CreateIPAddressAllocationForAddressBinding(ab2, subnetport, true)
	assert.Nil(t, err)

	// Create IPAddressAllocation for AddressBinding
	patches := gomonkey.ApplyMethod(reflect.TypeOf(service), "Apply",
		func(service *IPAddressAllocationService, nsxIPAddressAllocation *model.VpcIpAddressAllocation, _ []common.VPCResourceInfo) error {
			return nil
		})
	patches.ApplyPrivateMethod(reflect.TypeOf(service), "buildIPAddressAllocationTags",
		func(_ *IPAddressAllocationService, obj v1.Object) []model.Tag {
			return []model.Tag{}
		})
	err = service.CreateIPAddressAllocationForAddressBinding(ab1, subnetport, true)
	assert.Nil(t, err)
	patches.Reset()
}

func TestIPAddressAllocationService_CreateOrUpdateIPAddressAllocation_ReuseVIP(t *testing.T) {
	service, mockController, mockVPCIPAddressallocationclient := createIPAddressAllocationService(t)
	defer mockController.Finish()

	vpcService := &vpc.VPCService{}
	service.VPCService = vpcService

	k8sClient := mock_client.NewMockClient(mockController)
	service.Client = k8sClient

	originalCRUID := "original-cr-uid"
	newCRUID := "new-cr-uid"
	name := "test-ip-alloc"
	namespace := "ns-1"
	nsUUID := "nsUuid"
	vpcPath := "/orgs/default/projects/project-1/vpcs/vpc-1"

	originalCR := &v1alpha1.IPAddressAllocation{
		ObjectMeta: v1.ObjectMeta{
			Name:      "original-cr",
			Namespace: namespace,
			UID:       types.UID(originalCRUID),
		},
	}

	newCR := &v1alpha1.IPAddressAllocation{
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       types.UID(newCRUID),
			Annotations: map[string]string{
				"nsx.vmware.com/reuse-vip": "original-cr",
			},
		},
		Spec: v1alpha1.IPAddressAllocationSpec{
			AllocationSize:           256,
			IPAddressBlockVisibility: "Private",
		},
	}

	originalAlloc := &model.VpcIpAddressAllocation{
		Id:          common.String("original-alloc-id"),
		DisplayName: common.String("original-cr"),
		Tags: []model.Tag{
			{
				Scope: common.String("nsx-op/ipaddressallocation_uid"),
				Tag:   common.String(originalCRUID),
			},
		},
		ParentPath:               &vpcPath,
		Path:                     String(fmt.Sprintf("%s/ip-address-allocations/%s", vpcPath, "original-alloc-id")),
		AllocationIps:            common.String("192.168.1.0/24"),
		IpAddressBlockVisibility: common.String("PRIVATE"),
		IpAddressType:            common.String(model.VpcIpAddressAllocation_IP_ADDRESS_TYPE_IPV4),
	}

	k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: namespace, Name: "original-cr"}, gomock.Any()).DoAndReturn(
		func(_ context.Context, _ types.NamespacedName, obj *v1alpha1.IPAddressAllocation, _ ...interface{}) error {
			*obj = *originalCR
			return nil
		},
	)

	patchGetNamespaceUID := gomonkey.ApplyMethod(reflect.TypeOf(&service.Service), "GetNamespaceUID",
		func(s *common.Service, ns string) types.UID {
			return types.UID(nsUUID)
		})
	defer patchGetNamespaceUID.Reset()

	updatedAlloc := *originalAlloc
	updatedAlloc.Tags = service.buildIPAddressAllocationTags(newCR)

	patchGetByUID := gomonkey.ApplyMethod(reflect.TypeOf(service.ipAddressAllocationStore), "GetByUID", func(_ *IPAddressAllocationStore, uid types.UID) (*model.VpcIpAddressAllocation, error) {
		if uid == types.UID(originalCRUID) {
			return originalAlloc, nil
		}
		if uid == types.UID(newCRUID) {
			return &updatedAlloc, nil
		}
		return nil, nil
	})
	defer patchGetByUID.Reset()

	patchListVPCInfo := gomonkey.ApplyMethod(reflect.TypeOf(service.VPCService), "ListVPCInfo", func(_ common.VPCServiceProvider, ns string) []common.VPCResourceInfo {
		return []common.VPCResourceInfo{{OrgID: "default", ProjectID: "project-1", VPCID: "vpc-1", ID: "12345678"}}
	})
	defer patchListVPCInfo.Reset()

	mockVPCIPAddressallocationclient.EXPECT().Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
	mockVPCIPAddressallocationclient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(updatedAlloc, nil).Times(1)

	_, err := service.CreateOrUpdateIPAddressAllocation(context.Background(), newCR, false)
	assert.Nil(t, err)
	assert.Equal(t, "192.168.1.0/24", newCR.Status.AllocationIPs)
}

func TestIPAddressAllocationService_CreateOrUpdateIPAddressAllocation_ReuseVIP_EdgeCases(t *testing.T) {
	service, mockController, mockVPCIPAddressallocationclient := createIPAddressAllocationService(t)
	defer mockController.Finish()

	vpcService := &vpc.VPCService{}
	service.VPCService = vpcService

	k8sClient := mock_client.NewMockClient(mockController)
	service.Client = k8sClient

	patchGetNamespaceUID := gomonkey.ApplyMethod(reflect.TypeOf(&service.Service), "GetNamespaceUID",
		func(s *common.Service, ns string) types.UID {
			return types.UID("nsUuid")
		})
	defer patchGetNamespaceUID.Reset()

	vpcPath := "/orgs/default/projects/project-1/vpcs/vpc-1"

	t.Run("Whitespace reuseVIP format error", func(t *testing.T) {
		newCR := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "new-cr",
				Namespace: "ns-1",
				UID:       "new-uid",
				Annotations: map[string]string{
					"nsx.vmware.com/reuse-vip": "   ",
				},
			},
		}

		_, err := service.CreateOrUpdateIPAddressAllocation(context.Background(), newCR, false)
		assert.ErrorContains(t, err, "invalid reuse-vip format")
	})

	t.Run("Cross-namespace reuseVIP format ns/name", func(t *testing.T) {
		newCR := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "new-cr",
				Namespace: "ns-2",
				UID:       "new-uid",
				Annotations: map[string]string{
					"nsx.vmware.com/reuse-vip": "ns-1/original-cr",
				},
			},
		}
		originalCR := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "original-cr",
				Namespace: "ns-1",
				UID:       "orig-uid",
			},
		}

		k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "ns-1", Name: "original-cr"}, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ types.NamespacedName, obj *v1alpha1.IPAddressAllocation, _ ...interface{}) error {
				*obj = *originalCR
				return nil
			},
		)

		originalAlloc := &model.VpcIpAddressAllocation{
			Id:          common.String("orig-alloc-id"),
			DisplayName: common.String("original-cr"),
			Path:        String(fmt.Sprintf("%s/ip-address-allocations/orig-alloc-id", vpcPath)),
			Tags: []model.Tag{
				{Scope: common.String(common.TagScopeIPAddressAllocationCRUID), Tag: common.String("orig-uid")},
			},
			AllocationIps: common.String("10.0.0.1"),
		}

		patchGetByUID := gomonkey.ApplyMethod(reflect.TypeOf(service.ipAddressAllocationStore), "GetByUID",
			func(_ *IPAddressAllocationStore, uid types.UID) (*model.VpcIpAddressAllocation, error) {
				if uid == "orig-uid" {
					return originalAlloc, nil
				}
				if uid == "new-uid" {
					return originalAlloc, nil
				}
				return nil, nil
			})
		defer patchGetByUID.Reset()

		patchListVPCInfo := gomonkey.ApplyMethod(reflect.TypeOf(service.VPCService), "ListVPCInfo",
			func(_ common.VPCServiceProvider, _ string) []common.VPCResourceInfo {
				return []common.VPCResourceInfo{{OrgID: "default", ProjectID: "project-1", VPCID: "vpc-1"}}
			})
		defer patchListVPCInfo.Reset()

		mockVPCIPAddressallocationclient.EXPECT().Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
		mockVPCIPAddressallocationclient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(*originalAlloc, nil).Times(1)

		changed, err := service.CreateOrUpdateIPAddressAllocation(context.Background(), newCR, false)
		assert.NoError(t, err)
		assert.True(t, changed)
	})

	t.Run("Get original CR error", func(t *testing.T) {
		newCR := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "new-cr",
				Namespace: "ns-1",
				UID:       "new-uid",
				Annotations: map[string]string{
					"nsx.vmware.com/reuse-vip": "original-cr",
				},
			},
		}

		k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "ns-1", Name: "original-cr"}, gomock.Any()).Return(assert.AnError)

		_, err := service.CreateOrUpdateIPAddressAllocation(context.Background(), newCR, false)
		assert.ErrorIs(t, err, assert.AnError)
	})

	t.Run("Get original NSX alloc error", func(t *testing.T) {
		newCR := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "new-cr",
				Namespace: "ns-1",
				UID:       "new-uid",
				Annotations: map[string]string{
					"nsx.vmware.com/reuse-vip": "original-cr",
				},
			},
		}
		originalCR := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "original-cr",
				Namespace: "ns-1",
				UID:       "orig-uid",
			},
		}

		k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "ns-1", Name: "original-cr"}, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ types.NamespacedName, obj *v1alpha1.IPAddressAllocation, _ ...interface{}) error {
				*obj = *originalCR
				return nil
			},
		)

		patchGetByUID := gomonkey.ApplyMethod(reflect.TypeOf(service.ipAddressAllocationStore), "GetByUID",
			func(_ *IPAddressAllocationStore, uid types.UID) (*model.VpcIpAddressAllocation, error) {
				return nil, assert.AnError
			})
		defer patchGetByUID.Reset()

		_, err := service.CreateOrUpdateIPAddressAllocation(context.Background(), newCR, false)
		assert.ErrorIs(t, err, assert.AnError)
	})

	t.Run("originalNSXAlloc missing UID tag should be updated", func(t *testing.T) {
		newCR := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "new-cr",
				Namespace: "ns-1",
				UID:       "new-uid",
				Annotations: map[string]string{
					"nsx.vmware.com/reuse-vip": "original-cr",
				},
			},
		}
		originalCR := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "original-cr",
				Namespace: "ns-1",
				UID:       "orig-uid",
			},
		}

		k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "ns-1", Name: "original-cr"}, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ types.NamespacedName, obj *v1alpha1.IPAddressAllocation, _ ...interface{}) error {
				*obj = *originalCR
				return nil
			},
		)

		originalAlloc := &model.VpcIpAddressAllocation{
			Id:   common.String("orig-alloc-id"),
			Path: String(fmt.Sprintf("%s/ip-address-allocations/orig-alloc-id", vpcPath)),
			Tags: []model.Tag{
				{Scope: common.String("other-scope"), Tag: common.String("other-tag")},
			},
		}

		patchGetByUID := gomonkey.ApplyMethod(reflect.TypeOf(service.ipAddressAllocationStore), "GetByUID",
			func(_ *IPAddressAllocationStore, uid types.UID) (*model.VpcIpAddressAllocation, error) {
				return originalAlloc, nil
			})
		defer patchGetByUID.Reset()

		patchApply := gomonkey.ApplyMethod(reflect.TypeOf(service), "Apply",
			func(_ *IPAddressAllocationService, alloc *model.VpcIpAddressAllocation, _ []common.VPCResourceInfo) error {
				return assert.AnError
			})
		defer patchApply.Reset()

		_, err := service.CreateOrUpdateIPAddressAllocation(context.Background(), newCR, false)
		assert.ErrorContains(t, err, assert.AnError.Error())
	})

	t.Run("originalNSXAlloc nil", func(t *testing.T) {
		newCR := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "new-cr",
				Namespace: "ns-1",
				UID:       "new-uid",
				Annotations: map[string]string{
					"nsx.vmware.com/reuse-vip": "original-cr",
				},
			},
		}
		originalCR := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "original-cr",
				Namespace: "ns-1",
				UID:       "orig-uid",
			},
		}

		k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "ns-1", Name: "original-cr"}, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ types.NamespacedName, obj *v1alpha1.IPAddressAllocation, _ ...interface{}) error {
				*obj = *originalCR
				return nil
			},
		)

		patchGetByUID := gomonkey.ApplyMethod(reflect.TypeOf(service.ipAddressAllocationStore), "GetByUID",
			func(_ *IPAddressAllocationStore, uid types.UID) (*model.VpcIpAddressAllocation, error) {
				return nil, nil
			})
		defer patchGetByUID.Reset()

		patchBuild := gomonkey.ApplyMethod(reflect.TypeOf(service), "BuildIPAddressAllocation",
			func(_ *IPAddressAllocationService, _ v1.Object, _ *v1alpha1.SubnetPort, _ bool) (*model.VpcIpAddressAllocation, []common.VPCResourceInfo, error) {
				return nil, nil, assert.AnError
			})
		defer patchBuild.Reset()

		_, err := service.CreateOrUpdateIPAddressAllocation(context.Background(), newCR, false)
		assert.NotNil(t, err)
		assert.Contains(t, err.Error(), "original NSX IPAddressAllocation not found in store for UID orig-uid")
	})

	t.Run("originalNSXAlloc Path nil error", func(t *testing.T) {
		newCR := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "new-cr",
				Namespace: "ns-1",
				UID:       "new-uid",
				Annotations: map[string]string{
					"nsx.vmware.com/reuse-vip": "original-cr",
				},
			},
		}
		originalCR := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "original-cr",
				Namespace: "ns-1",
				UID:       "orig-uid",
			},
		}

		k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "ns-1", Name: "original-cr"}, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ types.NamespacedName, obj *v1alpha1.IPAddressAllocation, _ ...interface{}) error {
				*obj = *originalCR
				return nil
			},
		)

		originalAlloc := &model.VpcIpAddressAllocation{
			Id:   common.String("orig-alloc-id"),
			Path: nil,
			Tags: []model.Tag{
				{Scope: common.String(common.TagScopeIPAddressAllocationCRUID), Tag: common.String("other-uid")},
			},
		}

		patchGetByUID := gomonkey.ApplyMethod(reflect.TypeOf(service.ipAddressAllocationStore), "GetByUID",
			func(_ *IPAddressAllocationStore, uid types.UID) (*model.VpcIpAddressAllocation, error) {
				return originalAlloc, nil
			})
		defer patchGetByUID.Reset()

		_, err := service.CreateOrUpdateIPAddressAllocation(context.Background(), newCR, false)
		assert.ErrorContains(t, err, "original NSX IPAddressAllocation path is nil")
	})

	t.Run("ParseVPCResourcePath error", func(t *testing.T) {
		newCR := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "new-cr",
				Namespace: "ns-1",
				UID:       "new-uid",
				Annotations: map[string]string{
					"nsx.vmware.com/reuse-vip": "original-cr",
				},
			},
		}
		originalCR := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "original-cr",
				Namespace: "ns-1",
				UID:       "orig-uid",
			},
		}

		k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "ns-1", Name: "original-cr"}, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ types.NamespacedName, obj *v1alpha1.IPAddressAllocation, _ ...interface{}) error {
				*obj = *originalCR
				return nil
			},
		)

		originalAlloc := &model.VpcIpAddressAllocation{
			Id:   common.String("orig-alloc-id"),
			Path: String("invalid-path"),
			Tags: []model.Tag{
				{Scope: common.String(common.TagScopeIPAddressAllocationCRUID), Tag: common.String("other-uid")},
			},
		}

		patchGetByUID := gomonkey.ApplyMethod(reflect.TypeOf(service.ipAddressAllocationStore), "GetByUID",
			func(_ *IPAddressAllocationStore, uid types.UID) (*model.VpcIpAddressAllocation, error) {
				return originalAlloc, nil
			})
		defer patchGetByUID.Reset()

		_, err := service.CreateOrUpdateIPAddressAllocation(context.Background(), newCR, false)
		assert.Error(t, err)
	})

	t.Run("Apply error on original NSX alloc update", func(t *testing.T) {
		newCR := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "new-cr",
				Namespace: "ns-1",
				UID:       "new-uid",
				Annotations: map[string]string{
					"nsx.vmware.com/reuse-vip": "original-cr",
				},
			},
		}
		originalCR := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "original-cr",
				Namespace: "ns-1",
				UID:       "orig-uid",
			},
		}

		k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "ns-1", Name: "original-cr"}, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ types.NamespacedName, obj *v1alpha1.IPAddressAllocation, _ ...interface{}) error {
				*obj = *originalCR
				return nil
			},
		)

		originalAlloc := &model.VpcIpAddressAllocation{
			Id:   common.String("orig-alloc-id"),
			Path: String(fmt.Sprintf("%s/ip-address-allocations/orig-alloc-id", vpcPath)),
			Tags: []model.Tag{
				{Scope: common.String(common.TagScopeIPAddressAllocationCRUID), Tag: common.String("other-uid")},
			},
		}

		patchGetByUID := gomonkey.ApplyMethod(reflect.TypeOf(service.ipAddressAllocationStore), "GetByUID",
			func(_ *IPAddressAllocationStore, uid types.UID) (*model.VpcIpAddressAllocation, error) {
				return originalAlloc, nil
			})
		defer patchGetByUID.Reset()

		patchApply := gomonkey.ApplyMethod(reflect.TypeOf(service), "Apply",
			func(_ *IPAddressAllocationService, _ *model.VpcIpAddressAllocation, _ []common.VPCResourceInfo) error {
				return assert.AnError
			})
		defer patchApply.Reset()

		_, err := service.CreateOrUpdateIPAddressAllocation(context.Background(), newCR, false)
		assert.ErrorIs(t, err, assert.AnError)
	})

	t.Run("indexedIPAddressAllocation error for new CR UID", func(t *testing.T) {
		cr := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "cr1",
				Namespace: "ns-1",
				UID:       "uid1",
			},
		}

		patchBuild := gomonkey.ApplyMethod(reflect.TypeOf(service), "BuildIPAddressAllocation",
			func(_ *IPAddressAllocationService, _ v1.Object, _ *v1alpha1.SubnetPort, _ bool) (*model.VpcIpAddressAllocation, []common.VPCResourceInfo, error) {
				return &model.VpcIpAddressAllocation{Id: String("id1")}, []common.VPCResourceInfo{{VPCID: "v1"}}, nil
			})
		defer patchBuild.Reset()

		patchGetByUID := gomonkey.ApplyMethod(reflect.TypeOf(service.ipAddressAllocationStore), "GetByUID",
			func(_ *IPAddressAllocationStore, uid types.UID) (*model.VpcIpAddressAllocation, error) {
				return nil, assert.AnError
			})
		defer patchGetByUID.Reset()

		_, err := service.CreateOrUpdateIPAddressAllocation(context.Background(), cr, false)
		assert.ErrorIs(t, err, assert.AnError)
	})

	t.Run("createdIPAddressAllocation AllocationIps nil error", func(t *testing.T) {
		cr := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "cr1",
				Namespace: "ns-1",
				UID:       "uid1",
			},
		}

		patchBuild := gomonkey.ApplyMethod(reflect.TypeOf(service), "BuildIPAddressAllocation",
			func(_ *IPAddressAllocationService, _ v1.Object, _ *v1alpha1.SubnetPort, _ bool) (*model.VpcIpAddressAllocation, []common.VPCResourceInfo, error) {
				return &model.VpcIpAddressAllocation{Id: String("id1")}, []common.VPCResourceInfo{{VPCID: "v1"}}, nil
			})
		defer patchBuild.Reset()

		patchApply := gomonkey.ApplyMethod(reflect.TypeOf(service), "Apply",
			func(_ *IPAddressAllocationService, _ *model.VpcIpAddressAllocation, _ []common.VPCResourceInfo) error {
				return nil
			})
		defer patchApply.Reset()

		allocWithoutIPs := &model.VpcIpAddressAllocation{
			Id:            String("id1"),
			DisplayName:   String("disp1"),
			AllocationIps: nil,
		}

		patchGetByUID := gomonkey.ApplyMethodSeq(reflect.TypeOf(service.ipAddressAllocationStore), "GetByUID", []gomonkey.OutputCell{
			{Values: gomonkey.Params{nil, nil}},
			{Values: gomonkey.Params{allocWithoutIPs, nil}},
		})
		defer patchGetByUID.Reset()

		_, err := service.CreateOrUpdateIPAddressAllocation(context.Background(), cr, false)
		assert.ErrorContains(t, err, "didn't realize available allocation_ips")
	})

	t.Run("Restore mode IP mismatch error", func(t *testing.T) {
		cr := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "cr1",
				Namespace: "ns-1",
				UID:       "uid1",
			},
			Status: v1alpha1.IPAddressAllocationStatus{
				AllocationIPs: "10.0.0.1",
			},
		}

		patchBuild := gomonkey.ApplyMethod(reflect.TypeOf(service), "BuildIPAddressAllocation",
			func(_ *IPAddressAllocationService, _ v1.Object, _ *v1alpha1.SubnetPort, _ bool) (*model.VpcIpAddressAllocation, []common.VPCResourceInfo, error) {
				return &model.VpcIpAddressAllocation{Id: String("id1")}, []common.VPCResourceInfo{{VPCID: "v1"}}, nil
			})
		defer patchBuild.Reset()

		patchApply := gomonkey.ApplyMethod(reflect.TypeOf(service), "Apply",
			func(_ *IPAddressAllocationService, _ *model.VpcIpAddressAllocation, _ []common.VPCResourceInfo) error {
				return nil
			})
		defer patchApply.Reset()

		allocWithDifferentIP := &model.VpcIpAddressAllocation{
			Id:            String("id1"),
			DisplayName:   String("disp1"),
			AllocationIps: String("10.0.0.99"),
		}

		patchGetByUID := gomonkey.ApplyMethodSeq(reflect.TypeOf(service.ipAddressAllocationStore), "GetByUID", []gomonkey.OutputCell{
			{Values: gomonkey.Params{nil, nil}},
			{Values: gomonkey.Params{allocWithDifferentIP, nil}},
		})
		defer patchGetByUID.Reset()

		_, err := service.CreateOrUpdateIPAddressAllocation(context.Background(), cr, true)
		assert.ErrorContains(t, err, "IP mismatches for the restored IPAddressAllocation CR")
	})
}

func TestIPAddressAllocationService_CreateIPAddressAllocationForAddressBinding_Errors(t *testing.T) {
	service, mockController, _ := createIPAddressAllocationService(t)
	defer mockController.Finish()

	ab := &v1alpha1.AddressBinding{
		ObjectMeta: v1.ObjectMeta{
			Namespace: "ns-1",
			Name:      "ab-1",
			UID:       "ab-uid",
		},
		Status: v1alpha1.AddressBindingStatus{
			IPAddress: "192.168.1.1",
		},
	}
	sp := &v1alpha1.SubnetPort{
		ObjectMeta: v1.ObjectMeta{
			Namespace: "ns-1",
			Name:      "sp-1",
			UID:       "sp-uid",
		},
	}

	t.Run("GetIPAddressAllocationByOwner error", func(t *testing.T) {
		patchGetByOwner := gomonkey.ApplyMethod(reflect.TypeOf(service.ipAddressAllocationStore), "GetByUID",
			func(_ *IPAddressAllocationStore, _ types.UID) (*model.VpcIpAddressAllocation, error) {
				return nil, assert.AnError
			})
		defer patchGetByOwner.Reset()

		err := service.CreateIPAddressAllocationForAddressBinding(ab, sp, true)
		assert.ErrorIs(t, err, assert.AnError)
	})

	t.Run("BuildIPAddressAllocation error", func(t *testing.T) {
		patchGetByOwner := gomonkey.ApplyMethod(reflect.TypeOf(service.ipAddressAllocationStore), "GetByUID",
			func(_ *IPAddressAllocationStore, _ types.UID) (*model.VpcIpAddressAllocation, error) {
				return nil, nil
			})
		patchBuild := gomonkey.ApplyMethod(reflect.TypeOf(service), "BuildIPAddressAllocation",
			func(_ *IPAddressAllocationService, _ v1.Object, _ *v1alpha1.SubnetPort, _ bool) (*model.VpcIpAddressAllocation, []common.VPCResourceInfo, error) {
				return nil, nil, assert.AnError
			})
		defer patchGetByOwner.Reset()
		defer patchBuild.Reset()

		err := service.CreateIPAddressAllocationForAddressBinding(ab, sp, true)
		assert.ErrorIs(t, err, assert.AnError)
	})

	t.Run("Apply error", func(t *testing.T) {
		patchGetByOwner := gomonkey.ApplyMethod(reflect.TypeOf(service.ipAddressAllocationStore), "GetByUID",
			func(_ *IPAddressAllocationStore, _ types.UID) (*model.VpcIpAddressAllocation, error) {
				return nil, nil
			})
		patchBuild := gomonkey.ApplyMethod(reflect.TypeOf(service), "BuildIPAddressAllocation",
			func(_ *IPAddressAllocationService, _ v1.Object, _ *v1alpha1.SubnetPort, _ bool) (*model.VpcIpAddressAllocation, []common.VPCResourceInfo, error) {
				return &model.VpcIpAddressAllocation{Id: String("alloc1")}, []common.VPCResourceInfo{{VPCID: "v1"}}, nil
			})
		patchApply := gomonkey.ApplyMethod(reflect.TypeOf(service), "Apply",
			func(_ *IPAddressAllocationService, _ *model.VpcIpAddressAllocation, _ []common.VPCResourceInfo) error {
				return assert.AnError
			})
		defer patchGetByOwner.Reset()
		defer patchBuild.Reset()
		defer patchApply.Reset()

		err := service.CreateIPAddressAllocationForAddressBinding(ab, sp, true)
		assert.ErrorIs(t, err, assert.AnError)
	})
}

func TestIPAddressAllocationService_DeleteIPAddressAllocationForAddressBinding(t *testing.T) {
	service, mockController, mockVPCClient := createIPAddressAllocationService(t)
	defer mockController.Finish()

	owner := &v1alpha1.AddressBinding{
		ObjectMeta: v1.ObjectMeta{
			UID: "owner-uid",
		},
	}

	t.Run("GetIPAddressAllocationByOwner error", func(t *testing.T) {
		patchGetByUID := gomonkey.ApplyMethod(reflect.TypeOf(service.ipAddressAllocationStore), "GetByUID",
			func(_ *IPAddressAllocationStore, _ types.UID) (*model.VpcIpAddressAllocation, error) {
				return nil, assert.AnError
			})
		defer patchGetByUID.Reset()

		err := service.DeleteIPAddressAllocationForAddressBinding(owner)
		assert.ErrorIs(t, err, assert.AnError)
	})

	t.Run("nsxIPAddressAllocation nil", func(t *testing.T) {
		patchGetByUID := gomonkey.ApplyMethod(reflect.TypeOf(service.ipAddressAllocationStore), "GetByUID",
			func(_ *IPAddressAllocationStore, _ types.UID) (*model.VpcIpAddressAllocation, error) {
				return nil, nil
			})
		defer patchGetByUID.Reset()

		err := service.DeleteIPAddressAllocationForAddressBinding(owner)
		assert.NoError(t, err)
	})

	t.Run("Success case", func(t *testing.T) {
		path := "/orgs/default/projects/p1/vpcs/v1/ip-address-allocations/alloc1"
		alloc := &model.VpcIpAddressAllocation{
			Id:   String("alloc1"),
			Path: &path,
		}
		patchGetByUID := gomonkey.ApplyMethod(reflect.TypeOf(service.ipAddressAllocationStore), "GetByUID",
			func(_ *IPAddressAllocationStore, _ types.UID) (*model.VpcIpAddressAllocation, error) {
				return alloc, nil
			})
		defer patchGetByUID.Reset()

		mockVPCClient.EXPECT().Delete("default", "p1", "v1", "alloc1").Return(nil).Times(1)

		err := service.DeleteIPAddressAllocationForAddressBinding(owner)
		assert.NoError(t, err)
	})
}

func TestIPAddressAllocationService_Apply_EdgeCases(t *testing.T) {
	service, mockController, mockVPCClient := createIPAddressAllocationService(t)
	defer mockController.Finish()

	alloc := &model.VpcIpAddressAllocation{Id: String("a1")}

	t.Run("len(VPCInfo) == 0 error", func(t *testing.T) {
		err := service.Apply(alloc, nil)
		assert.Error(t, err)
	})

	t.Run("Patch and Get both fail", func(t *testing.T) {
		vpcInfo := []common.VPCResourceInfo{{OrgID: "o1", ProjectID: "p1", VPCID: "v1"}}
		mockVPCClient.EXPECT().Patch("o1", "p1", "v1", "a1", *alloc).Return(fmt.Errorf("patch err")).Times(1)
		mockVPCClient.EXPECT().Get("o1", "p1", "v1", "a1").Return(model.VpcIpAddressAllocation{}, fmt.Errorf("get err")).Times(1)

		err := service.Apply(alloc, vpcInfo)
		assert.ErrorContains(t, err, "error get get err, error patch patch err")
	})

	t.Run("Get fails alone", func(t *testing.T) {
		vpcInfo := []common.VPCResourceInfo{{OrgID: "o1", ProjectID: "p1", VPCID: "v1"}}
		mockVPCClient.EXPECT().Patch("o1", "p1", "v1", "a1", *alloc).Return(nil).Times(1)
		mockVPCClient.EXPECT().Get("o1", "p1", "v1", "a1").Return(model.VpcIpAddressAllocation{}, fmt.Errorf("get err")).Times(1)

		err := service.Apply(alloc, vpcInfo)
		assert.EqualError(t, err, "get err")
	})

	t.Run("Get returns AllocationIps nil", func(t *testing.T) {
		vpcInfo := []common.VPCResourceInfo{{OrgID: "o1", ProjectID: "p1", VPCID: "v1"}}
		mockVPCClient.EXPECT().Patch("o1", "p1", "v1", "a1", *alloc).Return(nil).Times(1)
		mockVPCClient.EXPECT().Get("o1", "p1", "v1", "a1").Return(model.VpcIpAddressAllocation{Id: String("a1")}, nil).Times(1)

		err := service.Apply(alloc, vpcInfo)
		assert.ErrorContains(t, err, "cidr not realized yet")
	})

	t.Run("ipAddressAllocationStore Apply error", func(t *testing.T) {
		vpcInfo := []common.VPCResourceInfo{{OrgID: "o1", ProjectID: "p1", VPCID: "v1"}}
		realizedAlloc := model.VpcIpAddressAllocation{Id: String("a1"), AllocationIps: String("10.0.0.1")}
		mockVPCClient.EXPECT().Patch("o1", "p1", "v1", "a1", *alloc).Return(nil).Times(1)
		mockVPCClient.EXPECT().Get("o1", "p1", "v1", "a1").Return(realizedAlloc, nil).Times(1)

		patchStoreApply := gomonkey.ApplyMethod(reflect.TypeOf(service.ipAddressAllocationStore), "Apply",
			func(_ *IPAddressAllocationStore, _ interface{}) error {
				return assert.AnError
			})
		defer patchStoreApply.Reset()

		err := service.Apply(alloc, vpcInfo)
		assert.ErrorIs(t, err, assert.AnError)
	})
}

func TestIPAddressAllocationService_DeleteIPAddressAllocation_Types(t *testing.T) {
	service, mockController, mockVPCClient := createIPAddressAllocationService(t)
	defer mockController.Finish()

	path := "/orgs/o1/projects/p1/vpcs/v1/ip-address-allocations/a1"
	alloc := &model.VpcIpAddressAllocation{
		Id:   String("a1"),
		Path: &path,
	}

	t.Run("Delete by types.UID", func(t *testing.T) {
		patchGetByUID := gomonkey.ApplyMethod(reflect.TypeOf(service.ipAddressAllocationStore), "GetByUID",
			func(_ *IPAddressAllocationStore, uid types.UID) (*model.VpcIpAddressAllocation, error) {
				if uid == "uid1" {
					return alloc, nil
				}
				return nil, nil
			})
		defer patchGetByUID.Reset()

		mockVPCClient.EXPECT().Delete("o1", "p1", "v1", "a1").Return(nil).Times(1)

		err := service.DeleteIPAddressAllocation(types.UID("uid1"))
		assert.NoError(t, err)
	})

	t.Run("Delete by string key", func(t *testing.T) {
		service.ipAddressAllocationStore.Add(alloc)
		mockVPCClient.EXPECT().Delete("o1", "p1", "v1", "a1").Return(nil).Times(1)

		err := service.DeleteIPAddressAllocation("a1")
		assert.NoError(t, err)
		service.ipAddressAllocationStore.Delete(alloc)
	})

	t.Run("Delete by *v1alpha1.IPAddressAllocation with store error", func(t *testing.T) {
		patchGetByUID := gomonkey.ApplyMethod(reflect.TypeOf(service.ipAddressAllocationStore), "GetByUID",
			func(_ *IPAddressAllocationStore, _ types.UID) (*model.VpcIpAddressAllocation, error) {
				return nil, assert.AnError
			})
		defer patchGetByUID.Reset()

		cr := &v1alpha1.IPAddressAllocation{ObjectMeta: v1.ObjectMeta{UID: "uid1"}}
		err := service.DeleteIPAddressAllocation(cr)
		assert.NoError(t, err)
	})

	t.Run("Delete by types.UID with store error", func(t *testing.T) {
		patchGetByUID := gomonkey.ApplyMethod(reflect.TypeOf(service.ipAddressAllocationStore), "GetByUID",
			func(_ *IPAddressAllocationStore, _ types.UID) (*model.VpcIpAddressAllocation, error) {
				return nil, assert.AnError
			})
		defer patchGetByUID.Reset()

		err := service.DeleteIPAddressAllocation(types.UID("uid1"))
		assert.NoError(t, err)
	})

	t.Run("Delete by string with wrong type in store", func(t *testing.T) {
		patchGetByKey := gomonkey.ApplyMethod(reflect.TypeOf(service.ipAddressAllocationStore), "GetByKey",
			func(_ *IPAddressAllocationStore, _ string) interface{} {
				return "not-an-alloc-object"
			})
		defer patchGetByKey.Reset()

		err := service.DeleteIPAddressAllocation("key1")
		assert.NoError(t, err)
	})
}

func TestIPAddressAllocationService_Helpers(t *testing.T) {
	service, mockController, _ := createIPAddressAllocationService(t)
	defer mockController.Finish()

	allocWithAB := &model.VpcIpAddressAllocation{
		Id: String("alloc-ab"),
		Tags: []model.Tag{
			{Scope: String(common.TagScopeAddressBindingCRUID), Tag: String("ab-uid-1")},
			{Scope: String(common.TagScopeSubnetPortCRUID), Tag: String("sp-uid-1")},
		},
	}
	allocWithCR := &model.VpcIpAddressAllocation{
		Id: String("alloc-cr"),
		Tags: []model.Tag{
			{Scope: String(common.TagScopeIPAddressAllocationCRUID), Tag: String("cr-uid-1")},
			{Scope: String(common.TagScopeNamespace), Tag: String("ns-test")},
		},
	}

	service.ipAddressAllocationStore.Add(allocWithAB)
	service.ipAddressAllocationStore.Add(allocWithCR)

	t.Run("ListIPAddressAllocationWithAddressBinding", func(t *testing.T) {
		res := service.ListIPAddressAllocationWithAddressBinding()
		assert.Len(t, res, 1)
		assert.Equal(t, "alloc-ab", *res[0].Id)
	})

	t.Run("ListSubnetPortCRUIDFromNSXIPAddressAllocation", func(t *testing.T) {
		res := service.ListSubnetPortCRUIDFromNSXIPAddressAllocation()
		assert.True(t, res.Has("sp-uid-1"))
	})

	t.Run("GetIPAddressAllocationNamespace and GetIPAddressAllocationUID", func(t *testing.T) {
		assert.Equal(t, "ns-test", service.GetIPAddressAllocationNamespace(allocWithCR))
		assert.Equal(t, "", service.GetIPAddressAllocationNamespace(allocWithAB))

		assert.Equal(t, "cr-uid-1", service.GetIPAddressAllocationUID(allocWithCR))
		assert.Equal(t, "", service.GetIPAddressAllocationUID(allocWithAB))
	})
}
