package securitypolicy

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

func Test_IndexFunc(t *testing.T) {
	mId, mTag, mScope := "11111", "11111", "nsx-op/security_policy_cr_uid"
	m := model.Group{
		Id:   &mId,
		Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
	}
	s := model.SecurityPolicy{
		Id:   &mId,
		Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
	}
	r := model.Rule{
		Id:   &mId,
		Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
	}
	t.Run("1", func(t *testing.T) {
		got, _ := indexFunc(s)
		if !reflect.DeepEqual(got, []string{"11111"}) {
			t.Errorf("securityPolicyCRUIDScopeIndexFunc() = %v, want %v", got, model.Tag{Tag: &mTag, Scope: &mScope})
		}
	})
	t.Run("2", func(t *testing.T) {
		got, _ := indexFunc(m)
		if !reflect.DeepEqual(got, []string{"11111"}) {
			t.Errorf("securityPolicyCRUIDScopeIndexFunc() = %v, want %v", got, model.Tag{Tag: &mTag, Scope: &mScope})
		}
	})
	t.Run("3", func(t *testing.T) {
		got, _ := indexFunc(r)
		if !reflect.DeepEqual(got, []string{"11111"}) {
			t.Errorf("securityPolicyCRUIDScopeIndexFunc() = %v, want %v", got, model.Tag{Tag: &mTag, Scope: &mScope})
		}
	})
}

func Test_filterTag(t *testing.T) {
	mTag, mScope := "11111", "nsx-op/security_policy_cr_uid"
	mTag2, mScope2 := "11111", "nsx"
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
		{"1", args{v: tags, res: res}, []string{"11111"}},
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
	Id := "11111"
	g := model.Group{Id: &Id}
	s := model.SecurityPolicy{Id: &Id}
	r := model.Rule{Id: &Id}
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
	t.Run("3", func(t *testing.T) {
		got, _ := keyFunc(r)
		if got != "11111" {
			t.Errorf("keyFunc() = %v, want %v", got, "11111")
		}
	})
}

func Test_InitializeRuleStore(t *testing.T) {
	config2 := nsx.NewConfig("localhost", "1", "1", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster, _ := nsx.NewCluster(config2)
	rc, _ := cluster.NewRestConnector()

	service := SecurityPolicyService{
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
	ruleCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeSecurityPolicyCRUID: indexFunc})
	ruleStore := &RuleStore{ResourceStore: common.ResourceStore{
		Indexer:     ruleCacheIndexer,
		BindingType: model.RuleBindingType(),
	}}

	wg := sync.WaitGroup{}
	fatalErrors := make(chan error)
	wg.Add(3)

	var tc *bindings.TypeConverter
	patches2 := gomonkey.ApplyMethod(reflect.TypeOf(tc), "ConvertToGolang",
		func(_ *bindings.TypeConverter, d data.DataValue, b bindings.BindingType) (interface{}, []error) {
			mId, mTag, mScope := "11111", "11111", "11111"
			m := model.Rule{
				Id:   &mId,
				Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
			}
			var j interface{} = m
			return j, nil
		})
	defer patches2.Reset()

	service.InitializeResourceStore(&wg, fatalErrors, ResourceTypeRule, ruleStore)
}

func Test_InitializeGroupStore(t *testing.T) {
	config2 := nsx.NewConfig("localhost", "1", "1", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster, _ := nsx.NewCluster(config2)
	rc, _ := cluster.NewRestConnector()

	service := SecurityPolicyService{
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
	groupCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeSecurityPolicyCRUID: indexFunc})
	groupStore := &GroupStore{ResourceStore: common.ResourceStore{
		Indexer:     groupCacheIndexer,
		BindingType: model.GroupBindingType(),
	}}

	wg := sync.WaitGroup{}
	fatalErrors := make(chan error)
	wg.Add(3)

	var tc *bindings.TypeConverter
	patches2 := gomonkey.ApplyMethod(reflect.TypeOf(tc), "ConvertToGolang",
		func(_ *bindings.TypeConverter, d data.DataValue, b bindings.BindingType) (interface{}, []error) {
			mId, mTag, mScope := "11111", "11111", "11111"
			m := model.Group{
				Id:   &mId,
				Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
			}
			var j interface{} = m
			return j, nil
		})
	defer patches2.Reset()

	service.InitializeResourceStore(&wg, fatalErrors, ResourceTypeGroup, groupStore)
}

func Test_InitializeSecurityPolicyStore(t *testing.T) {
	config2 := nsx.NewConfig("localhost", "1", "1", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster, _ := nsx.NewCluster(config2)
	rc, _ := cluster.NewRestConnector()

	service := SecurityPolicyService{
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
	securityPolicyCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeSecurityPolicyCRUID: indexFunc})
	securityPolicyStore := &SecurityPolicyStore{ResourceStore: common.ResourceStore{
		Indexer:     securityPolicyCacheIndexer,
		BindingType: model.SecurityPolicyBindingType(),
	}}

	wg := sync.WaitGroup{}
	fatalErrors := make(chan error)
	wg.Add(3)

	var tc *bindings.TypeConverter
	patches2 := gomonkey.ApplyMethod(reflect.TypeOf(tc), "ConvertToGolang",
		func(_ *bindings.TypeConverter, d data.DataValue, b bindings.BindingType) (interface{}, []error) {
			mId, mTag, mScope := "11111", "11111", "11111"
			m := model.SecurityPolicy{
				Id:   &mId,
				Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
			}
			var j interface{} = m
			return j, nil
		})
	defer patches2.Reset()

	service.InitializeResourceStore(&wg, fatalErrors, ResourceTypeSecurityPolicy, securityPolicyStore)
}

func TestSecurityPolicyStore_Operate(t *testing.T) {
	securityPolicyCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeSecurityPolicyCRUID: indexFunc})
	resourceStore := common.ResourceStore{
		Indexer:     securityPolicyCacheIndexer,
		BindingType: model.SecurityPolicyBindingType(),
	}
	securityPolicyStore := &SecurityPolicyStore{ResourceStore: resourceStore}
	type args struct {
		i interface{}
	}
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{"1", args{i: &model.SecurityPolicy{Id: String("1")}}, assert.NoError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, securityPolicyStore.Operate(tt.args.i), fmt.Sprintf("Operate(%v)", tt.args.i))
		})
	}
}

func TestRuleStore_Operate(t *testing.T) {
	ruleCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeSecurityPolicyCRUID: indexFunc})
	resourceStore := common.ResourceStore{
		Indexer:     ruleCacheIndexer,
		BindingType: model.RuleBindingType(),
	}
	ruleStore := &RuleStore{ResourceStore: resourceStore}
	type args struct {
		i interface{}
	}
	sp := model.SecurityPolicy{
		DisplayName:    &spName,
		Id:             &spID,
		Scope:          []string{"/infra/domains/k8scl-one/groups/sp_uidA_scope"},
		SequenceNumber: &seq0,
		Rules: []model.Rule{
			{
				DisplayName:       &ruleNameWithPodSelector00,
				Id:                &ruleIDPort000,
				DestinationGroups: []string{"ANY"},
				Direction:         &nsxDirectionIn,
				Scope:             []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_scope"},
				SequenceNumber:    &seq0,
				Services:          []string{"ANY"},
				SourceGroups:      []string{"/infra/domains/k8scl-one/groups/sp_uidA_0_src"},
				Action:            &nsxActionAllow,
				Tags:              basicTags,
			},
			{
				DisplayName:       &ruleNameWithNsSelector00,
				Id:                &ruleIDPort100,
				DestinationGroups: []string{"ANY"},
				Direction:         &nsxDirectionIn,
				Scope:             []string{"ANY"},
				SequenceNumber:    &seq1,
				Services:          []string{"ANY"},
				SourceGroups:      []string{"/infra/domains/k8scl-one/groups/sp_uidA_1_src"},
				Action:            &nsxActionAllow,
				Tags:              basicTags,
			},
		},
	}
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{"1", args{i: &sp}, assert.NoError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, ruleStore.Operate(tt.args.i), fmt.Sprintf("Operate(%v)", tt.args.i))
		})
	}
}

func TestGroupStore_Operator(t *testing.T) {
	groupCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeSecurityPolicyCRUID: indexFunc})
	resourceStore := common.ResourceStore{
		Indexer:     groupCacheIndexer,
		BindingType: model.GroupBindingType(),
	}
	groupStore := &GroupStore{ResourceStore: resourceStore}
	type args struct {
		i interface{}
	}
	groups := []model.Group{{Id: String("1")}, {Id: String("2")}}
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{"1", args{i: &groups}, assert.NoError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, groupStore.Operate(tt.args.i), fmt.Sprintf("Operate(%v)", tt.args.i))
		})
	}
}