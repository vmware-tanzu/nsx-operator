package ipblocksinfo

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey"
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

	service := &IPBlocksInfoService{
		Service: common.Service{
			NSXClient: &nsx.Client{},
			Client:    k8sClient,
		},
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

func TestIPBlocksInfoService_getIPBlockCIDRsFromStore(t *testing.T) {
	ipBlockStore := &IPBlockStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.IpAddressBlockBindingType(),
	}}
	ipblock1 := model.IpAddressBlock{
		Path: &ipBlocksPath1,
	}
	ipBlockStore.Apply(&ipblock1)
	service := &IPBlocksInfoService{}

	// Fetch non-existed IPBlocks
	pathSet := sets.New[string]()
	pathSet.Insert(ipBlocksPath2)
	_, err := service.getIPBlockCIDRsFromStore(pathSet, ipBlockStore)
	assert.ErrorContains(t, err, "failed to get IPBlock")

	// No CIDR in IPBlocks
	pathSet = sets.New[string]()
	pathSet.Insert(ipBlocksPath1)
	_, err = service.getIPBlockCIDRsFromStore(pathSet, ipBlockStore)
	assert.ErrorContains(t, err, "failed to get CIDR from ipblock")
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

func TestIsDefaultNetworkConfigCR(t *testing.T) {
	testCRD1 := v1alpha1.VPCNetworkConfiguration{}
	testCRD1.Name = "test-1"
	testCRD2 := v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				common.AnnotationDefaultNetworkConfig: "invalid",
			},
		},
	}
	testCRD2.Name = "test-2"
	testCRD3 := v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				common.AnnotationDefaultNetworkConfig: "true",
			},
		},
	}
	testCRD3.Name = "test-3"
	assert.Equal(t, isDefaultNetworkConfigCR(testCRD1), false)
	assert.Equal(t, isDefaultNetworkConfigCR(testCRD2), false)
	assert.Equal(t, isDefaultNetworkConfigCR(testCRD3), true)
}
