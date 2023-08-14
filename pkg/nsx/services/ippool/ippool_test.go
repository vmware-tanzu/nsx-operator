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
	commonctl "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/mediator"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

func TestIPPoolService_ListIPPoolID(t *testing.T) {
	ipPoolService := fakeService()
	p := &model.IpAddressPool{Id: String("1"), DisplayName: String("1"),
		Tags: []model.Tag{{Scope: String(common.TagScopeIPPoolCRUID),
			Tag: String("1")}}}
	_ = ipPoolService.ipPoolStore.Operate(p)

	tests := []struct {
		name string
		want sets.String
	}{
		{"test", sets.NewString("1")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, ipPoolService.ListIPPoolID(), "ListIPPoolID()")
		})
	}
}

func TestIPPoolService_acquireCidr(t *testing.T) {
	vpcCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeVPCCRUID: indexFunc})
	resourceStore := common.ResourceStore{
		Indexer:     vpcCacheIndexer,
		BindingType: model.VpcBindingType(),
	}
	vpcStore := &vpc.VPCStore{ResourceStore: resourceStore}
	commonctl.ServiceMediator.VPCService = &vpc.VPCService{VpcStore: vpcStore}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(vpcStore), "GetVPCsByNamespace", func(_ *vpc.VPCStore, ns string) []model.Vpc {
		id := "vpc-1"
		return []model.Vpc{{Path: common.String("/orgs/default/projects/project-1/vpcs/vpc-1"), Id: &id}}
	})
	defer patches.Reset()

	ipPoolService := fakeService()

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
	patch.ApplyMethod(reflect.TypeOf(service), "Operate", func(service *IPPoolService, nsxIPPool *model.IpAddressPool,
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
	vpcCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeVPCCRUID: indexFunc})
	resourceStore := common.ResourceStore{
		Indexer:     vpcCacheIndexer,
		BindingType: model.VpcBindingType(),
	}
	vpcStore := &vpc.VPCStore{ResourceStore: resourceStore}
	commonctl.ServiceMediator.VPCService = &vpc.VPCService{VpcStore: vpcStore}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(vpcStore), "GetVPCsByNamespace", func(_ *vpc.VPCStore, ns string) []model.Vpc {
		id := "vpc-1"
		return []model.Vpc{{Path: common.String("/orgs/default/projects/project-1/vpcs/vpc-1"), Id: &id}}
	})
	defer patches.Reset()
	ipPoolService := fakeService()

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
	vpcCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeVPCCRUID: indexFunc})
	resourceStore := common.ResourceStore{
		Indexer:     vpcCacheIndexer,
		BindingType: model.VpcBindingType(),
	}
	vpcStore := &vpc.VPCStore{ResourceStore: resourceStore}
	commonctl.ServiceMediator.VPCService = &vpc.VPCService{VpcStore: vpcStore}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(vpcStore), "GetVPCsByNamespace", func(_ *vpc.VPCStore, ns string) []model.Vpc {
		id := "vpc-1"
		return []model.Vpc{{Path: common.String("/orgs/default/projects/project-1/vpcs/vpc-1"), Id: &id}}
	})
	defer patches.Reset()
	service := fakeService()
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
	patch := gomonkey.ApplyMethod(reflect.TypeOf(ipPoolStore), "Operate", func(_ *IPPoolStore, nsxIPPool interface{}) error {
		return nil
	})
	patch.ApplyMethod(reflect.TypeOf(ipPoolBlockSubnetStore), "Operate", func(_ *IPPoolBlockSubnetStore, _ interface{}) error {
		return nil
	})
	defer patch.Reset()

	t.Run("1", func(t *testing.T) {
		err := service.Operate(iap, iapbs, true, true)
		assert.NoError(t, err, "Operate(%v)(%v)", iap, iapbs)
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
	md := mediator.ServiceMediator{}
	patch := gomonkey.ApplyMethod(reflect.TypeOf(&md), "ListVPCInfo", func(serviceMediator *mediator.ServiceMediator,
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
	ipPoolStore.Operate(p)

	iapbs := []*model.IpAddressPoolBlockSubnet{
		{Id: String("1"), DisplayName: String("1"),
			Tags: []model.Tag{{Scope: String(common.TagScopeIPPoolCRUID),
				Tag: String("1")}}}}
	ipPoolBlockSubnetStore := &IPPoolBlockSubnetStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeIPPoolCRUID: indexFunc}),
		BindingType: model.IpAddressPoolBlockSubnetBindingType(),
	}}
	ipPoolBlockSubnetStore.Operate(iapbs)
	t.Run("1", func(t *testing.T) {
		commonctl.ServiceMediator = md
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
		func(_ *bindings.TypeConverter, d data.DataValue, b bindings.BindingType) (interface{}, []error) {
			mId, mTag, mScope := "11111", "11111", "11111"
			m := model.IpAddressPool{
				Id:   &mId,
				Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
			}
			var j interface{} = m
			return j, nil
		})
	defer patches2.Reset()

	service.InitializeResourceStore(&wg, fatalErrors, ResourceTypeIPPool, nil, service.ipPoolStore)
	assert.Empty(t, fatalErrors)
	assert.Equal(t, []string{"11111"}, service.ipPoolStore.ListKeys())
}
