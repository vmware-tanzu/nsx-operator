package staticroute

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/client-go/tools/cache"

	common "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func Test_indexFunc(t *testing.T) {
	mId, mTag, mScope := "test_id", "test_tag", common.TagScopeStaticRouteCRUID
	v := model.StaticRoutes{
		Id:   &mId,
		Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
	}
	t.Run("1", func(t *testing.T) {
		got, _ := indexFunc(v)
		if !reflect.DeepEqual(got, []string{"test_tag"}) {
			t.Errorf("indexFunc() = %v, want %v", got, model.Tag{Tag: &mTag, Scope: &mScope})
		}
	})
}

func Test_KeyFunc(t *testing.T) {
	Id := "test_id"
	v := model.StaticRoutes{Id: &Id}
	t.Run("1", func(t *testing.T) {
		got, _ := keyFunc(v)
		if got != "test_id" {
			t.Errorf("keyFunc() = %v, want %v", got, "test_id")
		}
	})
}

func TestStaticRouteStore_CRUDResource(t *testing.T) {
	staticRouteCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeStaticRouteCRUID: indexFunc})
	resourceStore := common.ResourceStore{
		Indexer:     staticRouteCacheIndexer,
		BindingType: model.StaticRoutesBindingType(),
	}
	staticRouteStore := &StaticRouteStore{ResourceStore: resourceStore}
	type args struct {
		i interface{}
	}
	delete := true
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{"1", args{i: &model.StaticRoutes{Id: common.String("1")}}, assert.NoError},
		{"2", args{i: &model.StaticRoutes{Id: common.String("2"), MarkedForDelete: &delete}}, assert.NoError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, staticRouteStore.Operate(tt.args.i), fmt.Sprintf("CRUDResource(%v)", tt.args.i))
		})
	}
}

func TestStaticRouteStore_CRUDResource_List(t *testing.T) {
	staticRouteCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeStaticRouteCRUID: indexFunc})
	resourceStore := common.ResourceStore{
		Indexer:     staticRouteCacheIndexer,
		BindingType: model.StaticRoutesBindingType(),
	}
	staticRouteStore := &StaticRouteStore{ResourceStore: resourceStore}
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
			Scope: &tagScopeStaticRouteCRName,
			Tag:   &tagValueStaticRouteCRName,
		},
		{
			Scope: &tagScopeStaticRouteCRUID,
			Tag:   &tagValueStaticRouteCRUID,
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
			Scope: &tagScopeStaticRouteCRName,
			Tag:   &tagValueStaticRouteCRName,
		},
		{
			Scope: &tagScopeStaticRouteCRUID,
			Tag:   &tagValueStaticRouteCRUID,
		},
	}
	staticRoute1 := model.StaticRoutes{

		DisplayName: &staticrouteName1,
		Id:          &staticrouteID1,
		Tags:        tag1,
	}
	staticRoute2 := model.StaticRoutes{

		DisplayName: &staticrouteName2,
		Id:          &staticrouteID2,
		Tags:        tag2,
	}
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{"1", args{i: staticRoute1, j: staticRoute2}, assert.NoError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			staticRouteStore.Add(staticRoute1)
			staticRouteStore.Add(staticRoute2)
			got := staticRouteStore.List()
			if len(got) != 2 {
				t.Errorf("size = %v, want %v", len(got), 2)
			}
		})
	}
}

func TestStaticRouteStore_GetByKey(t *testing.T) {
	staticRouteCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeStaticRouteCRUID: indexFunc})
	resourceStore := common.ResourceStore{
		Indexer:     staticRouteCacheIndexer,
		BindingType: model.StaticRoutesBindingType(),
	}

	staticRouteStore := &StaticRouteStore{ResourceStore: resourceStore}
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
			Scope: &tagScopeStaticRouteCRName,
			Tag:   &tagValueStaticRouteCRName,
		},
		{
			Scope: &tagScopeStaticRouteCRUID,
			Tag:   &tagValueStaticRouteCRUID,
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
			Scope: &tagScopeStaticRouteCRName,
			Tag:   &tagValueStaticRouteCRName,
		},
		{
			Scope: &tagScopeStaticRouteCRUID,
			Tag:   &tagValueStaticRouteCRUID,
		},
	}
	staticRoute1 := model.StaticRoutes{
		DisplayName: &staticrouteName1,
		Id:          &staticrouteID1,
		Tags:        tag1,
	}
	staticRoute2 := model.StaticRoutes{

		DisplayName: &staticrouteName2,
		Id:          &staticrouteID2,
		Tags:        tag2,
	}

	staticRouteStore.Add(staticRoute1)
	staticRouteStore.Add(staticRoute2)
	got := staticRouteStore.GetByKey(staticrouteID2)
	if *got.Id != staticrouteID2 {
		t.Errorf("get id = %v failed", staticrouteID2)
	}

	staticRouteStore.Delete(staticRoute2)
	got = staticRouteStore.GetByKey(staticrouteID2)
	if got != nil {
		t.Errorf("id %v should be deleted", *got.Id)
	}
}

/*
func TestStaticRouteStore_GetByIndex(t *testing.T) {
	staticRouteCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeStaticRouteCRUID: indexFunc})
	resourceStore := common.ResourceStore{
		Indexer:     staticRouteCacheIndexer,
		BindingType: model.StaticRoutesBindingType(),
	}

	staticRouteStore := &StaticRouteStore{ResourceStore: resourceStore}
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
			Scope: &tagScopeStaticRouteCRName,
			Tag:   &tagValueStaticRouteCRName,
		},
		{
			Scope: &tagScopeStaticRouteCRUID,
			Tag:   &tagValueStaticRouteCRUID,
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
			Scope: &tagScopeStaticRouteCRName,
			Tag:   &tagValueStaticRouteCRName,
		},
		{
			Scope: &tagScopeStaticRouteCRUID,
			Tag:   &tagValueStaticRouteCRUID,
		},
	}
	staticRoute1 := model.StaticRoutes{
		DisplayName: &staticrouteName1,
		Id:          &staticrouteID1,
		Tags:        tag1,
	}
	staticRoute2 := model.StaticRoutes{

		DisplayName: &staticrouteName2,
		Id:          &staticrouteID2,
		Tags:        tag2,
	}

	staticRouteStore.Add(staticRoute1)
	staticRouteStore.Add(staticRoute2)
	value := staticRouteStore.ListIndexFuncValues(tagValueStaticRouteCRUID)
	t.Errorf("the value is %d, %v", len(value), value)
	got := staticRouteStore.GetByIndex(tagValueStaticRouteCRUID, string(tagValueStaticRouteCRUID))
	if len(got) != 2 {
		t.Errorf("size = %v, want = %v", len(got), 2)
	}

	staticRouteStore.Delete(staticRoute2)
	got = staticRouteStore.GetByIndex(tagValueStaticRouteCRUID, "test")
	if len(got) != 0 {
		t.Errorf("size = %v, want = %v", len(got), 0)
	}
}
*/
