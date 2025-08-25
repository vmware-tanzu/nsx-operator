package ipblocksinfo

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

var (
	ipBlocksPath1 = "/infra/ip-blocks/ipblock1"
	ipBlocksPath2 = "/infra/ip-blocks/ipblock2"
	ipBlocksPath3 = "/infra/ip-blocks/ipblock3"
	ipBlocksPath4 = "/infra/ip-blocks/ipblock4"

	ipBlocksMap = map[string]string{
		ipBlocksPath1: "192.168.0.0/16",
		ipBlocksPath2: "10.172.0.0/16",
		ipBlocksPath3: "10.173.0.0/16",
		ipBlocksPath4: "2002::1234:abcd:ffff:c0a8:101/64",
	}
	vpcConnectivityProfilePath1 = "/orgs/default/projects/default/vpc-connectivity-profiles/vpc-connectivity-profile-1"
	vpcConnectivityProfilePath2 = "/orgs/default/projects/default/vpc-connectivity-profiles/vpc-connectivity-profile-2"
	vpcPath                     = "/orgs/default/projects/default/vpcs/vpc-1"
	vpcAttachmentPath           = vpcPath + "/attachments/default"
	projectPath                 = "/orgs/default/projects/default"
)

func createService(t *testing.T) (*IPBlocksInfoService, *gomock.Controller, *mock_client.MockClient) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	subnetService := &subnet.SubnetService{
		Service: common.Service{
			NSXClient: &nsx.Client{},
			Client:    k8sClient,
		},
	}
	service := &IPBlocksInfoService{
		Service: common.Service{
			NSXClient: &nsx.Client{},
			Client:    k8sClient,
		},
		subnetService: subnetService,
	}
	return service, mockCtl, k8sClient
}

func fakeSearchResource(_ *common.Service, resourceTypeValue string, _ string, store common.Store, _ common.Filter) (uint64, error) {
	var count uint64
	switch resourceTypeValue {
	case common.ResourceTypeVpcAttachment:
		vpcAttachment := &model.VpcAttachment{
			ParentPath:             &vpcPath,
			Path:                   &vpcAttachmentPath,
			VpcConnectivityProfile: &vpcConnectivityProfilePath2,
		}
		store.Apply(vpcAttachment)
		count = 1
	case common.ResourceTypeVpcConnectivityProfile:
		vpcConnectivityProfile1 := &model.VpcConnectivityProfile{
			Path:               &vpcConnectivityProfilePath1,
			ExternalIpBlocks:   []string{ipBlocksPath1},
			PrivateTgwIpBlocks: []string{ipBlocksPath2},
		}
		vpcConnectivityProfile2 := &model.VpcConnectivityProfile{
			Path:               &vpcConnectivityProfilePath2,
			ExternalIpBlocks:   []string{ipBlocksPath4},
			PrivateTgwIpBlocks: []string{ipBlocksPath3},
		}
		store.Apply(vpcConnectivityProfile1)
		store.Apply(vpcConnectivityProfile2)
		count = 2
	case common.ResourceTypeIPBlock:
		for key, value := range ipBlocksMap {
			cidr := value
			ipblocks := &model.IpAddressBlock{
				Path: &key,
				Cidr: &cidr,
			}
			store.Apply(ipblocks)
		}
		count = uint64(len(ipBlocksMap))
	case common.ResourceTypeSubnet:
		count = 0
	default:
		return count, fmt.Errorf("unsupported search type %s", resourceTypeValue)
	}
	return count, nil
}

func TestIPBlocksInfoService_UpdateIPBlocksInfo(t *testing.T) {
	service, mockController, mockK8sClient := createService(t)
	defer mockController.Finish()

	searchResourcePatch := gomonkey.ApplyMethod(reflect.TypeOf(&service.Service), "SearchResource", fakeSearchResource)
	defer searchResourcePatch.Reset()

	mockK8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		ipBlocksInfoCR := obj.(*v1alpha1.IPBlocksInfo)
		ipBlocksInfoCR.ExternalIPCIDRs = []string{ipBlocksMap[ipBlocksPath4]}
		return nil
	})
	mockK8sClient.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
		actualUpdated, ok := obj.(*v1alpha1.IPBlocksInfo)
		assert.True(t, ok, "expected *v1alpha1.IPBlocksInfo when updating CR, got %T")
		assert.True(t, util.CompareArraysWithoutOrder(actualUpdated.ExternalIPCIDRs, []string{ipBlocksMap[ipBlocksPath4], ipBlocksMap[ipBlocksPath1]}))
		assert.Equal(t, actualUpdated.PrivateTGWIPCIDRs, []string{ipBlocksMap[ipBlocksPath2]})
		return nil
	})

	err := service.UpdateIPBlocksInfo(context.TODO(), &v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				common.AnnotationDefaultNetworkConfig: "true",
			},
		},
		Spec: v1alpha1.VPCNetworkConfigurationSpec{
			VPCConnectivityProfile: vpcConnectivityProfilePath1,
			NSXProject:             projectPath,
		},
	})
	assert.Equal(t, nil, err)
}

func TestIPBlocksInfoService_SyncIPBlocksInfo(t *testing.T) {
	service, mockController, mockK8sClient := createService(t)
	defer mockController.Finish()

	searchResourcePatch := gomonkey.ApplyMethod(reflect.TypeOf(&service.Service), "SearchResource", fakeSearchResource)
	defer searchResourcePatch.Reset()

	mockK8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		vpcConfigCRList := list.(*v1alpha1.VPCNetworkConfigurationList)
		vpcConfigCRList.Items = []v1alpha1.VPCNetworkConfiguration{
			{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						common.AnnotationDefaultNetworkConfig: "true",
					},
				},
				Spec: v1alpha1.VPCNetworkConfigurationSpec{
					VPCConnectivityProfile: vpcConnectivityProfilePath2,
					NSXProject:             projectPath,
				},
			},
			{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{
					VPC: vpcPath,
				},
			},
		}
		return nil
	})

	mockK8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(apierrors.NewNotFound(v1alpha1.Resource("VPCNetworkConfiguration"), "vpcConfigName"))
	mockK8sClient.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
		actualCreated, ok := obj.(*v1alpha1.IPBlocksInfo)
		assert.True(t, ok, "expected *v1alpha1.IPBlocksInfo when updating CR, got %T")
		assert.Equal(t, actualCreated.ExternalIPCIDRs, []string{ipBlocksMap[ipBlocksPath4]})
		assert.Equal(t, actualCreated.PrivateTGWIPCIDRs, []string{ipBlocksMap[ipBlocksPath3]})
		return nil
	})
	err := service.SyncIPBlocksInfo(context.TODO())
	assert.Equal(t, nil, err)
}

func TestIPBlocksInfoService_StartPeriodicSync(t *testing.T) {
	ipBlocksInfoService := &IPBlocksInfoService{
		Service:  common.Service{},
		SyncTask: NewIPBlocksInfoSyncTask(time.Millisecond*100, time.Millisecond*50),
	}
	done := make(chan bool)
	go func() {
		syncIPBlocksInfoPatch := gomonkey.ApplyMethod(reflect.TypeOf(ipBlocksInfoService), "SyncIPBlocksInfo", func(_ *IPBlocksInfoService, cxt context.Context) error {
			return fmt.Errorf("mock error")
		})
		defer syncIPBlocksInfoPatch.Reset()
		ipBlocksInfoService.StartPeriodicSync()
		done <- true
	}()

	time.Sleep(time.Millisecond * 20)
	ipBlocksInfoService.SyncTask.resetChan <- struct{}{}

	select {
	case <-done:
		t.Error("StartPeriodicSync stop unexpectedly")
	case <-time.After(time.Millisecond * 500):
		// Stop StartPeriodicSync after some time
	}
}

func TestIPBlocksInfoService_createOrUpdateIPBlocksInfo(t *testing.T) {
	service, mockController, mockK8sClient := createService(t)
	defer mockController.Finish()

	ipBlocksInfo := v1alpha1.IPBlocksInfo{}
	mockErr := fmt.Errorf("mock error")

	// Fail to get IPBlocksInfo CR
	mockK8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(mockErr)

	err := service.createOrUpdateIPBlocksInfo(context.TODO(), &ipBlocksInfo, false)
	assert.ErrorIs(t, err, mockErr)

	// Fail to create IPBlocksInfo CR
	mockK8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(apierrors.NewNotFound(v1alpha1.Resource("IPBlocksInfo"), "ipBlocksInfoName"))
	mockK8sClient.EXPECT().Create(gomock.Any(), gomock.Any()).Return(mockErr)
	err = service.createOrUpdateIPBlocksInfo(context.TODO(), &ipBlocksInfo, false)
	assert.ErrorIs(t, err, mockErr)

	// // Fail to udpate IPBlocksInfo CR
	mockK8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		ipBlocksInfoCR := obj.(*v1alpha1.IPBlocksInfo)
		ipBlocksInfoCR.ExternalIPCIDRs = []string{ipBlocksMap[ipBlocksPath4]}
		return nil
	})
	mockK8sClient.EXPECT().Update(gomock.Any(), gomock.Any()).Return(mockErr)
	err = service.createOrUpdateIPBlocksInfo(context.TODO(), &ipBlocksInfo, false)
	assert.ErrorIs(t, err, mockErr)
}

func TestIPBlocksInfoService_mergeIPCidrs(t *testing.T) {
	service := &IPBlocksInfoService{}

	tests := []struct {
		name     string
		source   []string
		target   []string
		expected []string
	}{
		{
			name:     "source is empty, should add all target",
			source:   []string{},
			target:   []string{"192.168.1.0/24", "192.168.0.32/27"},
			expected: []string{"192.168.1.0/24", "192.168.0.32/27"},
		},
		{
			name:     "target CIDR is subset of source, should not add",
			source:   []string{"192.168.0.0/16", "10.246.0.0/16"},
			target:   []string{"192.168.1.0/24", "192.168.0.32/27", "10.246.0.0/27", "10.246.0.0/16"},
			expected: []string{"192.168.0.0/16", "10.246.0.0/16"},
		},
		{
			name:     "target CIDR is not subset, should add",
			source:   []string{"192.168.0.0/16"},
			target:   []string{"10.0.0.0/8"},
			expected: []string{"192.168.0.0/16", "10.0.0.0/8"},
		},
		{
			name:     "multiple targets, some subset, some not",
			source:   []string{"10.0.0.0/8"},
			target:   []string{"10.1.0.0/16", "192.168.1.0/24"},
			expected: []string{"10.0.0.0/8", "192.168.1.0/24"},
		},
		{
			name:     "empty source, all targets added",
			source:   []string{},
			target:   []string{"10.0.0.0/8", "192.168.1.0/24"},
			expected: []string{"10.0.0.0/8", "192.168.1.0/24"},
		},
		{
			name:     "empty target, source unchanged",
			source:   []string{"10.0.0.0/8"},
			target:   []string{},
			expected: []string{"10.0.0.0/8"},
		},
		{
			name:     "identical CIDRs in source and target, no duplicates",
			source:   []string{"10.0.0.0/8"},
			target:   []string{"10.0.0.0/8"},
			expected: []string{"10.0.0.0/8"},
		},
		{
			name:     "invalid target, source",
			source:   []string{"10.0.0.0/8", "192.168.1.0/24/24"},
			target:   []string{"10.0.0.1", "192.168.1.0/-1"},
			expected: []string{"10.0.0.0/8"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.mergeIPCidrs(tt.source, tt.target)
			assert.ElementsMatch(t, tt.expected, result)
		})
	}
}

func stringPtr(s string) *string {
	return &s
}

func TestIPBlocksInfoService_getCIDRsRangesFromStore(t *testing.T) {
	service := &IPBlocksInfoService{}
	ipBlockStore := &IPBlockStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.IpAddressBlockBindingType(),
	}}

	// Helper to add IpAddressBlock to store
	addBlock := func(path string, cidr *string, cidrs []string, ranges []model.IpPoolRange) {
		block := &model.IpAddressBlock{
			Path:   &path,
			Cidr:   cidr,
			Cidrs:  cidrs,
			Ranges: ranges,
		}
		ipBlockStore.Apply(block)
	}

	// Case: getIPBlockCIDRsFromStore returns NoCIDRsFoundError, fallback to getIPBlockCIDRFromStore
	pathSet := sets.New[string]()
	pathSet.Insert("block1")
	addBlock("block1", stringPtr("10.0.0.0/24"), nil, nil)
	extCIDRs, privCIDRs, extRanges, privRanges, err := service.getCIDRsRangesFromStore(pathSet, sets.New[string](), ipBlockStore)
	assert.NoError(t, err)
	assert.Equal(t, []string{"10.0.0.0/24"}, extCIDRs)
	assert.Empty(t, privCIDRs)
	assert.Empty(t, extRanges)
	assert.Empty(t, privRanges)

	// Case: getIPBlockCIDRsFromStore returns CIDRs, getIPBlockRangesFromStore returns ranges
	ipRange1 := model.IpPoolRange{Start: stringPtr("192.168.1.10"), End: stringPtr("192.168.1.20")}

	pathSet = sets.New[string]()
	pathSet.Insert("block2")
	addBlock("block2", nil, []string{"192.168.1.0/24"}, []model.IpPoolRange{ipRange1})
	extCIDRs, privCIDRs, extRanges, privRanges, err = service.getCIDRsRangesFromStore(pathSet, sets.New[string](), ipBlockStore)
	assert.NoError(t, err)
	assert.Equal(t, []string{"192.168.1.0/24"}, extCIDRs)
	assert.Empty(t, privCIDRs)
	assert.Equal(t, []v1alpha1.IPPoolRange{{Start: "192.168.1.10", End: "192.168.1.20"}}, extRanges)
	assert.Empty(t, privRanges)

	// Case: getIPBlockCIDRsFromStore scan all the blocks even if some are not found
	pathSet = sets.New[string]()
	pathSet.Insert("block3") // block3 not in store
	pathSet.Insert("block2")
	extCIDRs, privCIDRs, extRanges, privRanges, err = service.getCIDRsRangesFromStore(pathSet, sets.New[string](), ipBlockStore)
	assert.ErrorContains(t, err, "failed to get IPBlock")
	assert.Empty(t, extCIDRs)
	assert.Empty(t, privCIDRs)
	assert.Empty(t, extRanges)
	assert.Empty(t, privRanges)

	// Case: privateTGWIPBlockPaths with CIDRs and ranges
	extSet := sets.New[string]()
	privSet := sets.New[string]()
	extSet.Insert("block2")
	privSet.Insert("block4")
	addBlock("block4", nil, []string{"10.1.1.0/24"}, []model.IpPoolRange{
		{Start: stringPtr("10.1.1.10"), End: stringPtr("10.1.1.20")},
	})
	extCIDRs, privCIDRs, extRanges, privRanges, err = service.getCIDRsRangesFromStore(extSet, privSet, ipBlockStore)
	assert.NoError(t, err)
	assert.Equal(t, []string{"192.168.1.0/24"}, extCIDRs)
	assert.Equal(t, []string{"10.1.1.0/24"}, privCIDRs)
	assert.Equal(t, []v1alpha1.IPPoolRange{{Start: "192.168.1.10", End: "192.168.1.20"}}, extRanges)
	assert.Equal(t, []v1alpha1.IPPoolRange{{Start: "10.1.1.10", End: "10.1.1.20"}}, privRanges)

	// Case: only has cidr
	ipBlockStore.Delete("block1")
	ipBlockStore.Delete("block2")
	ipBlockStore.Delete("block3")
	ipBlockStore.Delete("block4")
	ipBlockStore.Delete("block5")
	addBlock("block1", stringPtr("10.0.0.0/24"), nil, nil)
	addBlock("block2", stringPtr("10.0.1.0/24"), nil, nil)
	addBlock("block3", stringPtr("10.0.2.0/24"), nil, nil)
	pathSet = sets.New[string]()
	pathSet.Insert("block1")
	pathSet.Insert("block2")
	pathSet.Insert("block3")

	extCIDRs, privCIDRs, extRanges, privRanges, err = service.getCIDRsRangesFromStore(pathSet, sets.New[string](), ipBlockStore)
	assert.NoError(t, err)
	assert.True(t, util.CompareArraysWithoutOrder([]string{"10.0.0.0/24", "10.0.1.0/24", "10.0.2.0/24"}, extCIDRs))
	assert.Empty(t, privCIDRs)
	assert.Empty(t, extRanges)
	assert.Empty(t, privRanges)
	ipBlockStore.Delete("block1")
	ipBlockStore.Delete("block2")
	ipBlockStore.Delete("block3")

	// Case : part of have ranges
	addBlock("block1", nil, []string{"10.1.1.0/24"}, []model.IpPoolRange{
		{Start: stringPtr("10.1.1.10"), End: stringPtr("10.1.1.20")}})
	addBlock("block2", nil, []string{"10.1.2.0/24"}, nil)
	addBlock("block3", nil, []string{"10.1.3.0/24"}, []model.IpPoolRange{
		{Start: stringPtr("10.1.3.10"), End: stringPtr("10.1.3.20")}})
	addBlock("block4", nil, []string{"10.1.4.0/24"}, nil)
	pathSet.Insert("block4")
	extCIDRs, privCIDRs, extRanges, privRanges, err = service.getCIDRsRangesFromStore(pathSet, sets.New[string](), ipBlockStore)
	assert.NoError(t, err)
	assert.True(t, util.CompareArraysWithoutOrder([]string{"10.1.1.0/24", "10.1.2.0/24", "10.1.3.0/24", "10.1.4.0/24"}, extCIDRs))
	assert.True(t, util.CompareArraysWithoutOrder([]v1alpha1.IPPoolRange{
		{Start: "10.1.1.10", End: "10.1.1.20"},
		{Start: "10.1.3.10", End: "10.1.3.20"},
	}, extRanges))
	assert.Empty(t, privCIDRs)
	assert.Empty(t, privRanges)
	ipBlockStore.Delete("block1")
	ipBlockStore.Delete("block2")
	ipBlockStore.Delete("block3")
	ipBlockStore.Delete("block4")

	// Case: have both cidr/cidrs
	addBlock("block1", stringPtr("10.1.1.0/24"), []string{"10.1.1.0/24", "10.2.1.0/24"}, []model.IpPoolRange{
		{Start: stringPtr("10.1.1.10"), End: stringPtr("10.1.1.20")},
		{Start: stringPtr("10.2.1.10"), End: stringPtr("10.2.1.20")},
	})
	addBlock("block2", stringPtr("10.1.2.0/24"), []string{"10.1.2.0/24"}, nil)
	addBlock("block3", stringPtr("10.1.3.0/24"), []string{"10.1.3.0/24"}, []model.IpPoolRange{
		{Start: stringPtr("10.1.3.10"), End: stringPtr("10.1.3.20")}})
	addBlock("block4", stringPtr("10.1.4.0/24"), []string{"10.1.4.0/24"}, nil)
	extCIDRs, privCIDRs, extRanges, privRanges, err = service.getCIDRsRangesFromStore(pathSet, sets.New[string](), ipBlockStore)
	assert.NoError(t, err)
	assert.True(t, util.CompareArraysWithoutOrder([]string{"10.1.1.0/24", "10.1.2.0/24", "10.1.3.0/24", "10.1.4.0/24", "10.2.1.0/24"}, extCIDRs))
	assert.True(t, util.CompareArraysWithoutOrder([]v1alpha1.IPPoolRange{
		{Start: "10.1.1.10", End: "10.1.1.20"},
		{Start: "10.1.3.10", End: "10.1.3.20"},
		{Start: "10.2.1.10", End: "10.2.1.20"},
	}, extRanges))
	assert.Empty(t, privCIDRs)
	assert.Empty(t, privRanges)
	ipBlockStore.Delete("block1")
	ipBlockStore.Delete("block2")
	ipBlockStore.Delete("block3")
	ipBlockStore.Delete("block4")

	// Case: only have ranges
	addBlock("block1", nil, nil, []model.IpPoolRange{
		{Start: stringPtr("10.1.1.10"), End: stringPtr("10.1.1.20")},
	})
	addBlock("block2", nil, nil, nil)
	addBlock("block3", nil, nil, []model.IpPoolRange{
		{Start: stringPtr("10.1.3.10"), End: stringPtr("10.1.3.20")},
	})
	addBlock("block4", nil, nil, []model.IpPoolRange{
		{Start: stringPtr("10.1.4.10"), End: stringPtr("10.1.4.20")},
	})
	extCIDRs, privCIDRs, extRanges, privRanges, err = service.getCIDRsRangesFromStore(pathSet, sets.New[string](), ipBlockStore)
	assert.NoError(t, err)
	assert.Empty(t, extCIDRs)
	assert.Empty(t, privCIDRs)
	assert.True(t, util.CompareArraysWithoutOrder([]v1alpha1.IPPoolRange{
		{Start: "10.1.1.10", End: "10.1.1.20"},
		{Start: "10.1.3.10", End: "10.1.3.20"},
		{Start: "10.1.4.10", End: "10.1.4.20"},
	}, extRanges))
	assert.Empty(t, privRanges)
	ipBlockStore.Delete("block1")
	ipBlockStore.Delete("block2")
	ipBlockStore.Delete("block3")
	ipBlockStore.Delete("block4")

	// Case: part have only ranges
	addBlock("block1", stringPtr("10.1.1.0/24"), []string{"10.1.1.0/24", "10.2.1.0/24"}, []model.IpPoolRange{
		{Start: stringPtr("10.1.1.10"), End: stringPtr("10.1.1.20")},
		{Start: stringPtr("10.2.1.10"), End: stringPtr("10.2.1.20")},
	})
	addBlock("block2", stringPtr("10.1.2.0/24"), []string{"10.1.2.0/24"}, nil)
	addBlock("block3", nil, nil, []model.IpPoolRange{
		{Start: stringPtr("10.1.3.10"), End: stringPtr("10.1.3.20")}})
	addBlock("block4", stringPtr("10.1.4.0/24"), []string{"10.1.4.0/24"}, nil)
	extCIDRs, privCIDRs, extRanges, privRanges, err = service.getCIDRsRangesFromStore(pathSet, sets.New[string](), ipBlockStore)
	assert.NoError(t, err)
	assert.True(t, util.CompareArraysWithoutOrder([]v1alpha1.IPPoolRange{
		{Start: "10.1.1.10", End: "10.1.1.20"},
		{Start: "10.1.3.10", End: "10.1.3.20"},
		{Start: "10.2.1.10", End: "10.2.1.20"},
	}, extRanges))
	assert.True(t, util.CompareArraysWithoutOrder([]string{"10.1.1.0/24", "10.1.2.0/24", "10.1.4.0/24", "10.2.1.0/24"}, extCIDRs))
	assert.Empty(t, privCIDRs)
	assert.Empty(t, privRanges)
	ipBlockStore.Delete("block1")
	ipBlockStore.Delete("block2")
	ipBlockStore.Delete("block3")
	ipBlockStore.Delete("block4")
}

func TestIPBlocksInfoService_getSharedSubnetsCIDRs(t *testing.T) {
	service, _, _ := createService(t)
	service.defaultProject = "/orgs/default/projects/default"
	publicSubnetPath := "/orgs/default/projects/default/vpcs/vpc1/vpc-subnets/public-subnet"
	privateTgwSubnetPath := "/orgs/default/projects/default/vpcs/vpc1/vpc-subnets/private-tgw-subnet"
	privateTgwSubnetPath1 := "/orgs/default/projects/test-project/vpcs/vpc1/vpc-subnets/private-tgw-subnet"

	const (
		associatePublicSubnet     = "default:vpc1:public-subnet"
		associatePrivateTgwSubnet = "default:vpc1:private-tgw-subnet"
	)

	getSubnetPatch := gomonkey.ApplyMethod(reflect.TypeOf(service.subnetService), "GetNSXSubnetFromCacheOrAPI", func(_ *subnet.SubnetService, associate string) (*model.VpcSubnet, error) {
		public := "Public"
		privateTgw := "Private_TGW"

		publicSubnet := &model.VpcSubnet{
			Path:        &publicSubnetPath,
			AccessMode:  &public,
			IpAddresses: []string{"192.168.10.0/24"},
		}
		privateTgwSubnet := &model.VpcSubnet{
			Path:        &privateTgwSubnetPath,
			AccessMode:  &privateTgw,
			IpAddresses: []string{"10.10.0.0/16"},
		}
		switch associate {
		case associatePublicSubnet:
			return publicSubnet, nil
		case associatePrivateTgwSubnet:
			return privateTgwSubnet, nil
		}
		return nil, fmt.Errorf("subnet not found")
	})

	// Test: both subnets present in Spec.Subnets
	vpcConfigList := []v1alpha1.VPCNetworkConfiguration{
		{
			Spec: v1alpha1.VPCNetworkConfigurationSpec{
				Subnets: []string{publicSubnetPath, privateTgwSubnetPath},
			},
		},
	}
	external, private, err := service.getSharedSubnetsCIDRs(vpcConfigList)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"192.168.10.0/24"}, external)
	assert.ElementsMatch(t, []string{"10.10.0.0/16"}, private)

	// Test: only private_tgw subnet present with different project
	vpcConfigList = []v1alpha1.VPCNetworkConfiguration{
		{
			Spec: v1alpha1.VPCNetworkConfigurationSpec{
				Subnets: []string{privateTgwSubnetPath1},
			},
		},
	}
	external, private, err = service.getSharedSubnetsCIDRs(vpcConfigList)
	assert.NoError(t, err)
	assert.Empty(t, external)
	assert.Empty(t, private)

	// Test: subnet not found in store
	vpcConfigList = []v1alpha1.VPCNetworkConfiguration{
		{
			Spec: v1alpha1.VPCNetworkConfigurationSpec{
				Subnets: []string{"/infra/vpc-subnets/non-existent"},
			},
		},
	}
	external, private, err = service.getSharedSubnetsCIDRs(vpcConfigList)
	assert.NoError(t, err)
	assert.Empty(t, external)
	assert.Empty(t, private)

	// Test: SearchResource returns error
	getSubnetPatch.Reset()
	getSubnetPatch = gomonkey.ApplyMethod(reflect.TypeOf(service.subnetService), "GetNSXSubnetFromCacheOrAPI", func(_ *subnet.SubnetService, associate string) (*model.VpcSubnet, error) {
		return nil, fmt.Errorf("get subnet error")
	})
	defer getSubnetPatch.Reset()
	vpcConfigList = []v1alpha1.VPCNetworkConfiguration{
		{
			Spec: v1alpha1.VPCNetworkConfigurationSpec{
				Subnets: []string{privateTgwSubnetPath1},
			},
		},
	}
	external, private, _ = service.getSharedSubnetsCIDRs(vpcConfigList)
	assert.Empty(t, external)
	assert.Empty(t, private)
}
