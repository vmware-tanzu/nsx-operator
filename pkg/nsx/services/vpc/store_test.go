package vpc

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
		Results: []*data.StructValue{{}},
		Cursor:  &cursor, ResultCount: &resultCount,
	}, nil
}

func Test_IndexFunc(t *testing.T) {
	mId, mTag, mScope := "test_id", "test_tag", "nsx-op/namespace_uid"
	v := &model.Vpc{
		Id:   &mId,
		Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
	}
	t.Run("1", func(t *testing.T) {
		got, _ := indexFunc(v)
		if !reflect.DeepEqual(got, []string{"test_tag"}) {
			t.Errorf("NSCRUIDScopeIndexFunc() = %v, want %v", got, model.Tag{Tag: &mTag, Scope: &mScope})
		}
	})
}

func Test_filterTag(t *testing.T) {
	mTag, mScope := "test_tag", "nsx-op/namespace_uid"
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
	v := &model.Vpc{Id: &Id}
	t.Run("1", func(t *testing.T) {
		got, _ := keyFunc(v)
		if got != "test_id" {
			t.Errorf("keyFunc() = %v, want %v", got, "test_id")
		}
	})
}

func Test_InitializeVPCStore(t *testing.T) {
	config2 := nsx.NewConfig("localhost", "1", "1", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster, _ := nsx.NewCluster(config2)
	rc, _ := cluster.NewRestConnector()
	vpcCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{})
	vpcStore := &VPCStore{ResourceStore: common.ResourceStore{
		Indexer:     vpcCacheIndexer,
		BindingType: model.VpcBindingType(),
	}}

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
		VpcStore: vpcStore,
	}

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

	service.InitializeResourceStore(&wg, fatalErrors, common.ResourceTypeVpc, nil, vpcStore)
	assert.Empty(t, fatalErrors)
	assert.Equal(t, sets.New[string](), vpcStore.ListIndexFuncValues(common.TagScopeNamespaceUID))
}

func TestVPCStore_CRUDResource(t *testing.T) {
	vpcCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{})
	resourceStore := common.ResourceStore{
		Indexer:     vpcCacheIndexer,
		BindingType: model.VpcBindingType(),
	}
	vpcStore := &VPCStore{ResourceStore: resourceStore}
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
			tt.wantErr(t, vpcStore.Apply(tt.args.i), fmt.Sprintf("CRUDResource(%v)", tt.args.i))
		})
	}
}

func TestVPCStore_CRUDResource_List(t *testing.T) {
	vpcCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{})
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
	}
	vpc1 := model.Vpc{

		DisplayName:        &vpcName1,
		Id:                 &vpcID1,
		Tags:               tag1,
		IpAddressType:      &IPv4Type,
		PrivateIpv4Blocks:  []string{"1.1.1.0/24"},
		ExternalIpv4Blocks: []string{"2.2.2.0/24"},
	}
	vpc2 := model.Vpc{

		DisplayName:        &vpcName2,
		Id:                 &vpcID2,
		Tags:               tag2,
		IpAddressType:      &IPv4Type,
		PrivateIpv4Blocks:  []string{"3.3.3.0/24"},
		ExternalIpv4Blocks: []string{"4.4.4.0/24"},
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
			vpcStore.Apply(&vpc1)
			vpcStore.Apply(&vpc2)
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

func TestRuleStore_GetByKey(t *testing.T) {
	vpcRuleCacheIndexer := cache.NewIndexer(keyFuncAVI, nil)
	resourceStore := common.ResourceStore{
		Indexer:     vpcRuleCacheIndexer,
		BindingType: model.RuleBindingType(),
	}
	ruleStore := &AviRuleStore{ResourceStore: resourceStore}
	service := &VPCService{
		Service: common.Service{NSXClient: nil},
	}
	service.RuleStore = ruleStore

	path1 := "/org/default/project/project_1/vpcs/vpc1/security-policies/default-section/rules/rule1"
	path2 := "/org/default/project/project_1/vpcs/vpc2/security-policies/default-section/rules/rule1"
	rule1 := model.Rule{
		Path: &path1,
	}
	rule2 := model.Rule{
		Path: &path2,
	}
	ruleStore.Add(&rule1)

	rule := ruleStore.GetByKey(path1)
	assert.Equal(t, rule.Path, rule1.Path)

	rule = ruleStore.GetByKey(path2)
	assert.True(t, rule == nil)

	ruleStore.Add(&rule2)
	rule = ruleStore.GetByKey(path2)
	assert.Equal(t, rule.Path, rule2.Path)
}

func TestGroupStore_GetByKey(t *testing.T) {
	groupCacheIndexer := cache.NewIndexer(keyFuncAVI, nil)
	resourceStore := common.ResourceStore{
		Indexer:     groupCacheIndexer,
		BindingType: model.GroupBindingType(),
	}
	groupStore := &AviGroupStore{ResourceStore: resourceStore}
	service := &VPCService{
		Service: common.Service{NSXClient: nil},
	}
	service.GroupStore = groupStore

	path1 := "/org/default/project/project_1/vpcs/vpc1/groups/group1"
	path2 := "/org/default/project/project_1/vpcs/vpc2/groups/group2"
	group1 := model.Group{
		Path: &path1,
	}
	group2 := model.Group{
		Path: &path2,
	}
	groupStore.Add(&group1)

	group := groupStore.GetByKey(path1)
	assert.Equal(t, group.Path, group1.Path)

	group = groupStore.GetByKey(path2)
	assert.True(t, group == nil)

	groupStore.Add(&group2)
	group = groupStore.GetByKey(path2)
	assert.Equal(t, group.Path, group2.Path)
}

func TestSecurityPolicyStore_GetByKey(t *testing.T) {
	spCacheIndexer := cache.NewIndexer(keyFuncAVI, nil)
	resourceStore := common.ResourceStore{
		Indexer:     spCacheIndexer,
		BindingType: model.SecurityPolicyBindingType(),
	}
	spStore := &AviSecurityPolicyStore{ResourceStore: resourceStore}
	service := &VPCService{
		Service: common.Service{NSXClient: nil},
	}
	service.SecurityPolicyStore = spStore

	path1 := "/org/default/project/project_1/vpcs/vpc1/security-policies/default-section"
	path2 := "/org/default/project/project_1/vpcs/vpc2/security-policies/default-section"
	sp1 := model.SecurityPolicy{
		Path: &path1,
	}
	sp2 := model.SecurityPolicy{
		Path: &path2,
	}
	spStore.Add(&sp1)

	sp := spStore.GetByKey(path1)
	assert.Equal(t, sp.Path, sp1.Path)

	sp = spStore.GetByKey(path2)
	assert.True(t, sp == nil)

	spStore.Add(&sp2)
	sp = spStore.GetByKey(path2)
	assert.Equal(t, sp.Path, sp2.Path)
}
