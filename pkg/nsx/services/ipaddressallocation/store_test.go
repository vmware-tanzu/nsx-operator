package ipaddressallocation

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

func TestIPAddressAllocationStore_CRUDResource(t *testing.T) {
	ipAddressAllocationCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeIPAddressAllocationCRUID: indexFunc})
	resourceStore := common.ResourceStore{
		Indexer:     ipAddressAllocationCacheIndexer,
		BindingType: model.VpcIpAddressAllocationBindingType(),
	}
	ipAddressAllocationStore := &IPAddressAllocationStore{ResourceStore: resourceStore}
	type args struct {
		i interface{}
	}
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{"1", args{i: &model.VpcIpAddressAllocation{Id: String("1")}}, assert.NoError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, ipAddressAllocationStore.Apply(tt.args.i), fmt.Sprintf("Apply(%v)", tt.args.i))
		})
	}
}

func TestIPAddressAllocationStore_GetByIndex(t *testing.T) {
	p := &model.VpcIpAddressAllocation{Id: String("1"), DisplayName: String("1"),
		Tags: []model.Tag{{Scope: String(common.TagScopeIPAddressAllocationCRUID),
			Tag: String("1")}}}
	ipAddressAllocationStore := &IPAddressAllocationStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeIPAddressAllocationCRUID: indexFunc}),
		BindingType: model.VpcIpAddressBindingType(),
	}}
	_ = ipAddressAllocationStore.Apply(p)
	type args struct {
		uid types.UID
	}
	tests := []struct {
		name    string
		args    args
		want    *model.VpcIpAddressAllocation
		wantErr bool
	}{
		{"1", args{uid: "1"}, &model.VpcIpAddressAllocation{Id: String("1"), DisplayName: String("1"),
			Tags: []model.Tag{{Scope: String(common.TagScopeIPAddressAllocationCRUID), Tag: String("1")}}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ipAddressAllocationStore.GetByIndex(tt.args.uid)
			if (err != nil) != tt.wantErr {
				t.Errorf("indexedIPAddressAllocation() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("indexedIPAddressAllocation() got = %v, want %v", got, tt.want)
			}
		})
	}
	markForDelete := true
	p.MarkedForDelete = &markForDelete
	err := ipAddressAllocationStore.Apply(p)
	if err != nil {
		t.Errorf("failed to apply: %v", err)
	}
	_ = ipAddressAllocationStore.Apply(p)
	got, er := ipAddressAllocationStore.GetByIndex("1")
	if er != nil {
		t.Errorf("failed to get: %v", er)
	}
	if got != nil {
		t.Errorf("got != nil")
	}
}

func Test_indexFunc(t *testing.T) {
	mId, mTag, mScope := "11111", "11111", common.TagScopeIPAddressAllocationCRUID
	m := &model.VpcIpAddressAllocation{
		Id:   &mId,
		Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
	}
	t.Run("IndexFuncVpcIpAddressAllocation", func(t *testing.T) {
		got, _ := indexFunc(m)
		if !reflect.DeepEqual(got, []string{"11111"}) {
			t.Errorf("indexFunc() = %v, want %v", got, model.Tag{Tag: &mTag, Scope: &mScope})
		}
	})
	modelGenericPolicyRealizedResource := model.GenericPolicyRealizedResource{
		Id:   &mId,
		Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
	}
	t.Run("IndexFuncGenericPolicyRealizedResource", func(t *testing.T) {
		got, _ := indexFunc(&modelGenericPolicyRealizedResource)
		if !reflect.DeepEqual(got, []string{"11111"}) {
			t.Errorf("indexFunc() = %v, want %v", got, model.Tag{Tag: &mTag, Scope: &mScope})
		}
	})
	modelUnknown := model.SecurityPolicy{
		Id:   &mId,
		Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
	}
	t.Run("IndexFuncUnknown", func(t *testing.T) {
		_, err := indexFunc(&modelUnknown)
		if err == nil {
			t.Errorf("err should not be nil")
		}
	})
}

func Test_keyFunc(t *testing.T) {
	Id := "11111"
	g := &model.VpcIpAddressAllocation{Id: &Id}
	t.Run("KeyFuncVpcIpAddressAllocation", func(t *testing.T) {
		got, _ := keyFunc(g)
		if got != "11111" {
			t.Errorf("keyFunc() = %v, want %v", got, "11111")
		}
	})
	modelGenericPolicyRealizedResource := model.GenericPolicyRealizedResource{Id: &Id}
	t.Run("KeyFuncGenericPolicyRealizedResource", func(t *testing.T) {
		got, _ := keyFunc(&modelGenericPolicyRealizedResource)
		if got != "11111" {
			t.Errorf("keyFunc() = %v, want %v", got, "11111")
		}
	})
	modelUnknown := model.SecurityPolicy{Id: &Id}
	t.Run("KeyFuncUnknown", func(t *testing.T) {
		_, err := keyFunc(&modelUnknown)
		if err == nil {
			t.Errorf("err should not be nil")
		}
	})
}
