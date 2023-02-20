package vpc

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/agiledragon/gomonkey"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type fakeQueryClient struct {
}

func (qIface *fakeQueryClient) List(_ string, _ *string, _ *string, _ *int64, _ *bool, _ *string) (model.SearchResponse, error) {
	cursor := "2"
	resultCount := int64(2)
	return model.SearchResponse{
		Results: []*data.StructValue{&data.StructValue{}},
		Cursor:  &cursor, ResultCount: &resultCount,
	}, nil
}

func Test_IndexFunc(t *testing.T) {
	mId, mTag, mScope := "test_id", "test_tag", "nsx-op/vpc_cr_uid"
	v := model.Vpc{
		Id:   &mId,
		Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
	}
	t.Run("1", func(t *testing.T) {
		got, _ := indexFunc(v)
		if !reflect.DeepEqual(got, []string{"test_tag"}) {
			t.Errorf("VPCCRUIDScopeIndexFunc() = %v, want %v", got, model.Tag{Tag: &mTag, Scope: &mScope})
		}
	})
}

func Test_filterTag(t *testing.T) {
	mTag, mScope := "test_tag", "nsx-op/vpc_cr_uid"
	mTag2, mScope2 := "test_tag", "nsx"
	tags := []model.Tag{{Scope: &mScope, Tag: &mTag}}
	tags2 := []model.Tag{{Scope: &mScope2, Tag: &mTag2}}
	var res []string
	var res2 []string
	type args struct {
		v   []model.Tag
		res []string
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{"1", args{v: tags, res: res}, []string{"test_tag"}},
		{"1", args{v: tags2, res: res2}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := filterTag(tt.args.v); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("filterTag() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_KeyFunc(t *testing.T) {
	Id := "test_id"
	v := model.Vpc{Id: &Id}
	t.Run("1", func(t *testing.T) {
		got, _ := keyFunc(v)
		if got != "test_id" {
			t.Errorf("keyFunc() = %v, want %v", got, "test_id")
		}
	})
}

func Test_InitializeVPCStore(t *testing.T) {
	config2 := nsx.NewConfig("localhost", "1", "1", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster, _ := nsx.NewCluster(config2)
	rc, _ := cluster.NewRestConnector()

	service := VPCService{
		Service: common.Service{
			NSXClient: &nsx.Client{
				QueryClient:   &fakeQueryClient{},
				RestConnector: rc,
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
	}
	vpcCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeVPCCRUID: indexFunc})
	vpcStore = &VPCStore{ResourceStore: common.ResourceStore{
		Indexer:     vpcCacheIndexer,
		BindingType: model.VpcBindingType(),
	}}

	wg := sync.WaitGroup{}
	fatalErrors := make(chan error)
	wg.Add(3)

	var tc *bindings.TypeConverter
	patches2 := gomonkey.ApplyMethod(reflect.TypeOf(tc), "ConvertToGolang",
		func(_ *bindings.TypeConverter, d data.DataValue, b bindings.BindingType) (interface{}, []error) {
			mId, mTag, mScope := "test_id", "test_tag", "test_scope"
			m := model.Vpc{
				Id:   &mId,
				Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
			}
			var j interface{} = m
			return j, nil
		})
	defer patches2.Reset()

	service.InitializeResourceStore(&wg, fatalErrors, ResourceTypeVPC, vpcStore)
}

func TestVPCStore_CRUDResource(t *testing.T) {
	vpcCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeVPCCRUID: indexFunc})
	resourceStore := common.ResourceStore{
		Indexer:     vpcCacheIndexer,
		BindingType: model.VpcBindingType(),
	}
	vpcStore = &VPCStore{ResourceStore: resourceStore}
	type args struct {
		i interface{}
	}
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{"1", args{i: &model.Vpc{Id: common.String("1")}}, assert.NoError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, vpcStore.Operate(tt.args.i), fmt.Sprintf("CRUDResource(%v)", tt.args.i))
		})
	}
}

func TestVPCStore_CRUDResource_List(t *testing.T) {
	vpcCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeVPCCRUID: indexFunc})
	resourceStore := common.ResourceStore{
		Indexer:     vpcCacheIndexer,
		BindingType: model.VpcBindingType(),
	}
	vpcStore := &VPCStore{ResourceStore: resourceStore}
	type args struct {
		i interface{}
		j interface{}
	}
	ns1 := "test-ns-1"
	tag1 := []model.Tag{
		{
			Scope: &tagScopeCluster,
			Tag:   &cluster,
		},
		{
			Scope: &tagScopeNamespace,
			Tag:   &ns1,
		},
		{
			Scope: &tagScopeVPCCRName,
			Tag:   &tagValueVPCCRName,
		},
		{
			Scope: &tagScopeVPCCRUID,
			Tag:   &tagValueVPCCRUID,
		},
	}
	ns2 := "test-ns-2"
	tag2 := []model.Tag{
		{
			Scope: &tagScopeCluster,
			Tag:   &cluster,
		},
		{
			Scope: &tagScopeNamespace,
			Tag:   &ns2,
		},
		{
			Scope: &tagScopeVPCCRName,
			Tag:   &tagValueVPCCRName,
		},
		{
			Scope: &tagScopeVPCCRUID,
			Tag:   &tagValueVPCCRUID,
		},
	}
	vpc1 := model.Vpc{

		DisplayName:       &vpcName1,
		Id:                &vpcID1,
		Tags:              tag1,
		IpAddressType:     &IPv4Type,
		PrivateIpv4Blocks: []string{"1.1.1.0/24"},
		PublicIpv4Blocks:  []string{"2.2.2.0/24"},
	}
	vpc2 := model.Vpc{

		DisplayName:       &vpcName2,
		Id:                &vpcID2,
		Tags:              tag2,
		IpAddressType:     &IPv4Type,
		PrivateIpv4Blocks: []string{"3.3.3.0/24"},
		PublicIpv4Blocks:  []string{"4.4.4.0/24"},
	}
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{"1", args{i: vpc1, j: vpc2}, assert.NoError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vpcStore.Operate(&vpc1)
			vpcStore.Operate(&vpc2)
			got := vpcStore.List()
			if len(got) != 2 {
				t.Errorf("size = %v, want %v", len(got), 2)
			}
			vpc_list_1 := vpcStore.GetVPCsByNamespace("invalid")
			if len(vpc_list_1) != 0 {
				t.Errorf("size = %v, want %v", len(vpc_list_1), 0)
			}
			vpc_list_2 := vpcStore.GetVPCsByNamespace(ns2)
			if len(vpc_list_2) != 1 && *vpc_list_2[0].DisplayName != vpcName2 {
				t.Errorf("size = %v, want %v, display = %s, want %s", len(vpc_list_2), 1, *vpc_list_2[0].DisplayName, vpcName2)
			}
		})
	}
}
