package ippool

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestIPPoolBlockSubnetStore_CRUDResource(t *testing.T) {
	ipPoolBlockSubnetCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeIPPoolCRUID: indexFunc})
	resourceStore := common.ResourceStore{
		Indexer:     ipPoolBlockSubnetCacheIndexer,
		BindingType: model.IpAddressPoolBlockSubnetBindingType(),
	}
	ipPoolBlockSubnetStore := &IPPoolBlockSubnetStore{ResourceStore: resourceStore}
	type args struct {
		i interface{}
	}
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{"1", args{i: []*model.IpAddressPoolBlockSubnet{{Id: String("1")}}}, assert.NoError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, ipPoolBlockSubnetStore.Operate(tt.args.i), fmt.Sprintf("Operate(%v)", tt.args.i))
		})
	}
}

func TestIPPoolStore_GetByIndex(t *testing.T) {
	p := &model.IpAddressPool{Id: String("1"), DisplayName: String("1"),
		Tags: []model.Tag{{Scope: String(common.TagScopeIPPoolCRUID),
			Tag: String("1")}}}
	ipPoolStore := &IPPoolStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeIPPoolCRUID: indexFunc}),
		BindingType: model.IpAddressPoolBindingType(),
	}}
	ipPoolStore.Operate(p)
	type args struct {
		uid types.UID
	}
	tests := []struct {
		name    string
		args    args
		want    *model.IpAddressPool
		wantErr bool
	}{
		{"1", args{uid: "1"}, &model.IpAddressPool{Id: String("1"), DisplayName: String("1"),
			Tags: []model.Tag{{Scope: String(common.TagScopeIPPoolCRUID), Tag: String("1")}}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ipPoolStore.GetByIndex(tt.args.uid)
			if (err != nil) != tt.wantErr {
				t.Errorf("indexedIPPool() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("indexedIPPool() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIPPoolBlockSubnetStore_GetByIndex(t *testing.T) {
	p := []*model.IpAddressPoolBlockSubnet{
		{Id: String("1"), DisplayName: String("1"),
			Tags: []model.Tag{{Scope: String(common.TagScopeIPPoolCRUID),
				Tag: String("1")}}}}
	ipPoolBlockSubnetStore := &IPPoolBlockSubnetStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeIPPoolCRUID: indexFunc}),
		BindingType: model.IpAddressPoolBlockSubnetBindingType(),
	}}
	ipPoolBlockSubnetStore.Operate(p)
	type args struct {
		uid types.UID
	}
	tests := []struct {
		name    string
		args    args
		want    []*model.IpAddressPoolBlockSubnet
		wantErr bool
	}{
		{"1", args{uid: "1"}, []*model.IpAddressPoolBlockSubnet{{Id: String("1"), DisplayName: String("1"), Tags: []model.Tag{{Scope: String(common.TagScopeIPPoolCRUID), Tag: String("1")}}}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ipPoolBlockSubnetStore.GetByIndex(tt.args.uid)
			if (err != nil) != tt.wantErr {
				t.Errorf("indexedIPPoolSubnets() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("indexedIPPoolSubnets() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIPPoolStore_CRUDResource(t *testing.T) {
	ipPoolCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeIPPoolCRUID: indexFunc})
	resourceStore := common.ResourceStore{
		Indexer:     ipPoolCacheIndexer,
		BindingType: model.IpAddressPoolBindingType(),
	}
	ipPoolStore := &IPPoolStore{ResourceStore: resourceStore}
	type args struct {
		i interface{}
	}
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{"1", args{i: &model.IpAddressPool{Id: String("1")}}, assert.NoError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, ipPoolStore.Operate(tt.args.i), fmt.Sprintf("Operate(%v)", tt.args.i))
		})
	}
}

func Test_indexFunc(t *testing.T) {
	mId, mTag, mScope := "11111", "11111", common.TagScopeIPPoolCRUID
	m := model.IpAddressPool{
		Id:   &mId,
		Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
	}
	r := model.IpAddressPoolBlockSubnet{
		Id:   &mId,
		Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
	}
	t.Run("1", func(t *testing.T) {
		got, _ := indexFunc(m)
		if !reflect.DeepEqual(got, []string{"11111"}) {
			t.Errorf("indexFunc() = %v, want %v", got, model.Tag{Tag: &mTag, Scope: &mScope})
		}
	})
	t.Run("2", func(t *testing.T) {
		got, _ := indexFunc(r)
		if !reflect.DeepEqual(got, []string{"11111"}) {
			t.Errorf("indexFunc() = %v, want %v", got, model.Tag{Tag: &mTag, Scope: &mScope})
		}
	})
}

func Test_ipPoolAssertion(t *testing.T) {
	mId, mTag, mScope := "11111", "11111", common.TagScopeIPPoolCRUID
	m := model.IpAddressPool{
		Id:   &mId,
		Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
	}
	type args struct {
		i interface{}
	}
	tests := []struct {
		name string
		args args
		want interface{}
	}{
		{"1", args{i: m}, m},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ipPoolAssertion(tt.args.i); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ipPoolAssertion() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_ipPoolBlockSubnetAssertion(t *testing.T) {
	mId, mTag, mScope := "11111", "11111", common.TagScopeIPPoolCRUID
	r := model.IpAddressPoolBlockSubnet{
		Id:   &mId,
		Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
	}
	type args struct {
		i interface{}
	}
	tests := []struct {
		name string
		args args
		want interface{}
	}{
		{"1", args{i: r}, r},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ipPoolBlockSubnetAssertion(tt.args.i); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ipPoolBlockSubnetAssertion() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_keyFunc(t *testing.T) {
	Id := "11111"
	g := model.IpAddressPool{Id: &Id}
	s := model.IpAddressPoolBlockSubnet{Id: &Id}
	t.Run("1", func(t *testing.T) {
		got, _ := keyFunc(s)
		if got != "11111" {
			t.Errorf("keyFunc() = %v, want %v", got, "11111")
		}
	})
	t.Run("2", func(t *testing.T) {
		got, _ := keyFunc(g)
		if got != "11111" {
			t.Errorf("keyFunc() = %v, want %v", got, "11111")
		}
	})
}

func TestIPPoolService_indexedIPPoolAndIPPoolSubnets1(t *testing.T) {
	ipPoolService := fakeService()
	p := &model.IpAddressPool{Id: String("1"), DisplayName: String("1"),
		Tags: []model.Tag{{Scope: String(common.TagScopeIPPoolCRUID),
			Tag: String("1")}}}
	ipPoolService.ipPoolStore.Operate(p)

	iapbs := []*model.IpAddressPoolBlockSubnet{
		{Id: String("1"), DisplayName: String("1"),
			Tags: []model.Tag{{Scope: String(common.TagScopeIPPoolCRUID),
				Tag: String("1")}}}}
	ipPoolService.ipPoolBlockSubnetStore.Operate(iapbs)

	type args struct {
		uid types.UID
	}
	tests := []struct {
		name    string
		args    args
		want    []*model.IpAddressPoolBlockSubnet
		want2   *model.IpAddressPool
		wantErr bool
	}{
		{
			"1",
			args{uid: "1"},
			[]*model.IpAddressPoolBlockSubnet{{Id: String("1"), DisplayName: String("1"),
				Tags: []model.Tag{{Scope: String(common.TagScopeIPPoolCRUID), Tag: String("1")}}}},
			&model.IpAddressPool{Id: String("1"), DisplayName: String("1"),
				Tags: []model.Tag{{Scope: String(common.TagScopeIPPoolCRUID), Tag: String("1")}}},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got2, _ := ipPoolService.indexedIPPoolAndIPPoolSubnets(tt.args.uid)
			if !reflect.DeepEqual(got, tt.want2) {
				t.Errorf("indexedIPPool() got = %v, want %v", got, tt.want)
			}
			if !reflect.DeepEqual(got2, tt.want) {
				t.Errorf("indexedIPPoolSubnets() got = %v, want %v", got, tt.want)
			}
		})
	}
}
