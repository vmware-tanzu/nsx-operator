package ippool

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha2"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

func TestIPPoolService_ListIPPoolID(t *testing.T) {
	ipPoolService := fakeService()
	p := &model.IpAddressPool{Id: String("1"), DisplayName: String("1"),
		Tags: []model.Tag{{Scope: String(common.TagScopeIPPoolCRUID),
			Tag: String("1")}}}
	_ = ipPoolService.ipPoolStore.Apply(p)

	tests := []struct {
		name string
		want sets.Set[string]
	}{
		{"test", sets.New[string]("1")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, ipPoolService.ListIPPoolID(), "ListIPPoolID()")
		})
	}
}

func TestIPPoolService_acquireCidr(t *testing.T) {
	vpcCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{})
	resourceStore := common.ResourceStore{
		Indexer:     vpcCacheIndexer,
		BindingType: model.VpcBindingType(),
	}
	ipPoolService := fakeService()
	vpcStore := &vpc.VPCStore{ResourceStore: resourceStore}
	ipPoolService.VPCService = &vpc.VPCService{VpcStore: vpcStore}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(ipPoolService.VPCService), "ListVPCInfo", func(_ *vpc.VPCService, ns string) []common.VPCResourceInfo {
		id := "vpc-1"
		return []common.VPCResourceInfo{{OrgID: "default", ProjectID: "project-1", VPCID: "vpc-1", ID: id}}
	})

	defer patches.Reset()

	type args struct {
		obj           *v1alpha2.IPPool
		subnetRequest *v1alpha2.SubnetRequest
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr assert.ErrorAssertionFunc
	}{
		{"1", args{obj: &v1alpha2.IPPool{}, subnetRequest: &v1alpha2.SubnetRequest{}}, "1.1.1.1/24", assert.NoError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ipPoolService.acquireCidr(tt.args.obj, tt.args.subnetRequest, 3)
			if !tt.wantErr(t, err, fmt.Sprintf("acquireCidr(%v, %v)", tt.args.obj, tt.args.subnetRequest)) {
				return
			}
			assert.Equalf(t, tt.want, got, "acquireCidr(%v, %v)", tt.args.obj, tt.args.subnetRequest)
		})
	}
}

func TestIPPoolService_DeleteIPPool(t *testing.T) {
	service := fakeService()
	iap := &model.IpAddressPool{Id: String("1"), DisplayName: String("1"),
		Tags: []model.Tag{{Scope: String(common.TagScopeIPPoolCRUID),
			Tag: String("1")}}}
	iapbs := []*model.IpAddressPoolBlockSubnet{
		{Id: String("1"), DisplayName: String("1"),
			Tags: []model.Tag{{Scope: String(common.TagScopeIPPoolCRUID),
				Tag: String("1")}}}}

	patch := gomonkey.ApplyMethod(reflect.TypeOf(service), "BuildIPPool", func(service *IPPoolService, IPPool *v1alpha2.IPPool) (*model.
		IpAddressPool,
		[]*model.IpAddressPoolBlockSubnet) {
		return iap, iapbs
	})
	patch.ApplyMethod(reflect.TypeOf(service), "Apply", func(service *IPPoolService, nsxIPPool *model.IpAddressPool,
		nsxIPSubnets []*model.IpAddressPoolBlockSubnet, IPPoolUpdated bool, IPPoolSubnetsUpdated bool) error {
		return nil
	})
	defer patch.Reset()

	ipPool := &v1alpha2.IPPool{}

	t.Run("1", func(t *testing.T) {
		err := service.DeleteIPPool(ipPool)
		assert.NoError(t, err, "DeleteIPPool(%v)", ipPool)
	})
}

func TestIPPoolService_AcquireRealizedSubnetIP(t *testing.T) {
	vpcCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{})
	resourceStore := common.ResourceStore{
		Indexer:     vpcCacheIndexer,
		BindingType: model.VpcBindingType(),
	}
	vpcStore := &vpc.VPCStore{ResourceStore: resourceStore}
	ipPoolService := fakeService()
	ipPoolService.VPCService = &vpc.VPCService{VpcStore: vpcStore}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(ipPoolService.VPCService), "ListVPCInfo", func(_ *vpc.VPCService, ns string) []common.VPCResourceInfo {
		id := "vpc-1"
		return []common.VPCResourceInfo{{OrgID: "default", ProjectID: "project-1", VPCID: "vpc-1", ID: id}}
	})
	defer patches.Reset()
	ipPool2 := &v1alpha2.IPPool{
		Spec: v1alpha2.IPPoolSpec{
			Subnets: []v1alpha2.SubnetRequest{
				{
					Name: "subnet1",
				},
			},
		},
		Status: v1alpha2.IPPoolStatus{
			Subnets: []v1alpha2.SubnetResult{
				{
					Name: "subnet1",
				},
			},
		},
	}

	result := []v1alpha2.SubnetResult{
		{
			Name: "subnet1",
			CIDR: "1.1.1.1/24",
		},
	}

	t.Run("1", func(t *testing.T) {
		got, got1, err := ipPoolService.AcquireRealizedSubnetIP(ipPool2)
		assert.NoError(t, err, "AcquireRealizedSubnetIP(%v)", ipPool2)
		assert.Equalf(t, result, got, "AcquireRealizedSubnetIP(%v)", ipPool2)
		assert.Equalf(t, true, got1, "AcquireRealizedSubnetIP(%v)", ipPool2)
	})
}

func TestIPPoolService_CRUDResource(t *testing.T) {
	vpcCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{})
	resourceStore := common.ResourceStore{
		Indexer:     vpcCacheIndexer,
		BindingType: model.VpcBindingType(),
	}
	vpcStore := &vpc.VPCStore{ResourceStore: resourceStore}
	service := fakeService()
	service.VPCService = &vpc.VPCService{VpcStore: vpcStore}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(service.VPCService), "ListVPCInfo", func(_ *vpc.VPCService, ns string) []common.VPCResourceInfo {
		id := "vpc-1"
		return []common.VPCResourceInfo{{OrgID: "default", ProjectID: "project-1", VPCID: "vpc-1", ID: id}}
	})
	defer patches.Reset()
	iap := &model.IpAddressPool{Id: String("1"), DisplayName: String("1"),
		Tags: []model.Tag{{Scope: String(common.TagScopeIPPoolCRUID),
			Tag: String("1")}}}
	iapbs := []*model.IpAddressPoolBlockSubnet{
		{Id: String("1"), DisplayName: String("1"), Tags: []model.Tag{{Scope: String(common.TagScopeIPPoolCRUID), Tag: String("1")}}}}

	ipPoolStore := &IPPoolStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeIPPoolCRUID: indexFunc}),
		BindingType: model.IpAddressPoolBindingType(),
	}}
	ipPoolBlockSubnetStore := &IPPoolBlockSubnetStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeIPPoolCRUID: indexFunc}),
		BindingType: model.IpAddressPoolBlockSubnetBindingType(),
	}}
	patch := gomonkey.ApplyMethod(reflect.TypeOf(ipPoolStore), "Apply", func(_ *IPPoolStore, nsxIPPool interface{}) error {
		return nil
	})
	patch.ApplyMethod(reflect.TypeOf(ipPoolBlockSubnetStore), "Apply", func(_ *IPPoolBlockSubnetStore, _ interface{}) error {
		return nil
	})
	defer patch.Reset()

	t.Run("1", func(t *testing.T) {
		err := service.Apply(iap, iapbs, true, true)
		assert.NoError(t, err, "Apply(%v)(%v)", iap, iapbs)
	})
}

func TestIPPoolService_CreateOrUpdateIPPool(t *testing.T) {
	service := fakeService()
	ipPool2 := &v1alpha2.IPPool{
		Spec: v1alpha2.IPPoolSpec{
			Subnets: []v1alpha2.SubnetRequest{
				{
					Name: "subnet1",
				},
			},
			Type: common.IPPoolTypePrivate,
		},
		Status: v1alpha2.IPPoolStatus{
			Subnets: []v1alpha2.SubnetResult{
				{
					Name: "subnet1",
				},
			},
		},
	}

	var vpcinfolist = []common.VPCResourceInfo{
		{OrgID: "1", VPCID: "1", ProjectID: "1", ID: "1", ParentID: "1"},
	}
	service.VPCService = &vpc.VPCService{}
	patch := gomonkey.ApplyMethod(reflect.TypeOf(service.VPCService), "ListVPCInfo", func(vpcService common.VPCServiceProvider,
		ns string) []common.VPCResourceInfo {
		return vpcinfolist
	})
	defer patch.Reset()

	p := &model.IpAddressPool{Id: String("1"), DisplayName: String("1"),
		Tags: []model.Tag{{Scope: String(common.TagScopeIPPoolCRUID),
			Tag: String("1")}}}
	ipPoolStore := &IPPoolStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeIPPoolCRUID: indexFunc}),
		BindingType: model.IpAddressPoolBindingType(),
	}}
	ipPoolStore.Apply(p)

	iapbs := []*model.IpAddressPoolBlockSubnet{
		{Id: String("1"), DisplayName: String("1"),
			Tags: []model.Tag{{Scope: String(common.TagScopeIPPoolCRUID),
				Tag: String("1")}}}}
	ipPoolBlockSubnetStore := &IPPoolBlockSubnetStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeIPPoolCRUID: indexFunc}),
		BindingType: model.IpAddressPoolBlockSubnetBindingType(),
	}}
	ipPoolBlockSubnetStore.Apply(iapbs)
	var vpcinfo = []common.VPCResourceInfo{
		{PrivateIpv4Blocks: []string{"/infra/ip-blocks/block-test"}},
	}
	vpcCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{})
	resourceStore := common.ResourceStore{
		Indexer:     vpcCacheIndexer,
		BindingType: model.VpcBindingType(),
	}
	vpcStore := &vpc.VPCStore{ResourceStore: resourceStore}
	service.VPCService = &vpc.VPCService{VpcStore: vpcStore}
	patch = gomonkey.ApplyMethod(reflect.TypeOf(service.VPCService), "ListVPCInfo", func(vpcService *vpc.VPCService,
		ns string) []common.VPCResourceInfo {
		return vpcinfo
	})
	defer patch.Reset()
	t.Run("1", func(t *testing.T) {
		got, got1, err := service.CreateOrUpdateIPPool(ipPool2)
		assert.NoError(t, err, "CreateOrUpdateIPPool(%v)(%v)", got, got1)
	})
}

func TestInitializeIPPool(t *testing.T) {
	service := fakeService()
	wg := sync.WaitGroup{}
	fatalErrors := make(chan error)
	wg.Add(3)

	var tc *bindings.TypeConverter
	patches2 := gomonkey.ApplyMethod(reflect.TypeOf(tc), "ConvertToGolang",
		func(_ *bindings.TypeConverter, d data.DataValue, b bindings.BindingType) (m interface{}, err []error) {
			mId, mTag, mScope := "11111", "11111", "11111"
			m = model.IpAddressPool{
				Id:   &mId,
				Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
			}
			err = nil
			return
		})
	defer patches2.Reset()

	service.InitializeResourceStore(&wg, fatalErrors, ResourceTypeIPPool, nil, service.ipPoolStore)
	assert.Empty(t, fatalErrors)
	assert.Equal(t, []string{"11111"}, service.ipPoolStore.ListKeys())
}
