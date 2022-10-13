package securitypolicy

import (
	"reflect"
	"sync"
	"testing"

	"github.com/agiledragon/gomonkey"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func Test_queryTagCondition(t *testing.T) {
	config2 := nsx.NewConfig("1.1.1.1", "1", "1", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster, _ := nsx.NewCluster(config2)
	rc, _ := cluster.NewRestConnector()
	service = &SecurityPolicyService{
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
		},
	}
	type args struct {
		service *SecurityPolicyService
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{"1", args{service: service}, "tags.scope:nsx-op\\/cluster AND tags.tag:k8scl-one\\:test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := queryTagCondition(tt.args.service); got != tt.want {
				t.Errorf("queryTagCondition() = %v, want %v", got, tt.want)
			}
		})
	}
}

type fakeQueryClient struct {
}

func (qIface *fakeQueryClient) List(queryParam string, cursorParam *string, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.SearchResponse, error) {
	cursor := "2"
	resultCount := int64(2)
	return model.SearchResponse{
		Results: []*data.StructValue{&data.StructValue{}},
		Cursor:  &cursor, ResultCount: &resultCount,
	}, nil
}

func keyFunc2(obj interface{}) (string, error) {
	return "", nil
}

func Test_queryGroup(t *testing.T) {
	config := nsx.NewConfig("1.1.1.1", "1", "1", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster, _ := nsx.NewCluster(config)
	rc, _ := cluster.NewRestConnector()
	service = &SecurityPolicyService{
		Service: common.Service{
			NSXClient: &nsx.Client{
				QueryClient:   &fakeQueryClient{},
				RestConnector: rc,
			},
		},
		GroupStore: cache.NewIndexer(keyFunc2,
			cache.Indexers{
				util.TagScopeSecurityPolicyCRUID: common.IndexFunc(util.TagScopeSecurityPolicyCRUID),
			}),
	}

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
			var j interface{} = &m
			return j, nil
		})
	defer patches2.Reset()

	patches3 := gomonkey.ApplyFunc(queryTagCondition, func(service *SecurityPolicyService) string {
		return ""
	})
	defer patches3.Reset()

	queryGroup(service, &wg, fatalErrors)
}

func Test_querySecurityPolicy(t *testing.T) {
	config := nsx.NewConfig("1.1.1.1", "1", "1", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster, _ := nsx.NewCluster(config)
	rc, _ := cluster.NewRestConnector()
	service = &SecurityPolicyService{
		Service: common.Service{
			NSXClient: &nsx.Client{
				QueryClient:   &fakeQueryClient{},
				RestConnector: rc,
			},
		},
		SecurityPolicyStore: cache.NewIndexer(keyFunc2,
			cache.Indexers{
				util.TagScopeSecurityPolicyCRUID: common.IndexFunc(util.TagScopeSecurityPolicyCRUID),
			}),
	}

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
			var j interface{} = &m
			return j, nil
		})
	defer patches2.Reset()

	patches3 := gomonkey.ApplyFunc(queryTagCondition, func(service *SecurityPolicyService) string {
		return ""
	})
	defer patches3.Reset()

	querySecurityPolicy(service, &wg, fatalErrors)
}

func Test_queryRule(t *testing.T) {
	config := nsx.NewConfig("1.1.1.1", "1", "1", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster, _ := nsx.NewCluster(config)
	rc, _ := cluster.NewRestConnector()
	service = &SecurityPolicyService{
		Service: common.Service{
			NSXClient: &nsx.Client{
				QueryClient:   &fakeQueryClient{},
				RestConnector: rc,
			},
		},
		RuleStore: cache.NewIndexer(keyFunc2,
			cache.Indexers{
				util.TagScopeSecurityPolicyCRUID: common.IndexFunc(util.TagScopeSecurityPolicyCRUID),
			}),
	}

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
			var j interface{} = &m
			return j, nil
		})
	defer patches2.Reset()

	patches3 := gomonkey.ApplyFunc(queryTagCondition, func(service *SecurityPolicyService) string {
		return ""
	})
	defer patches3.Reset()

	queryRule(service, &wg, fatalErrors)
}

func TestListSecurityPolicyID(t *testing.T) {
	groupStore := cache.NewIndexer(common.KeyFunc, cache.Indexers{util.TagScopeSecurityPolicyCRUID: common.IndexFunc(util.TagScopeSecurityPolicyCRUID)})
	policyStore := cache.NewIndexer(common.KeyFunc, cache.Indexers{util.TagScopeSecurityPolicyCRUID: common.IndexFunc(util.TagScopeSecurityPolicyCRUID)})
	ruleStore := cache.NewIndexer(common.KeyFunc, cache.Indexers{util.TagScopeSecurityPolicyCRUID: common.IndexFunc(util.TagScopeSecurityPolicyCRUID)})

	group := model.Group{}
	scope := "nsx-op/security_policy_cr_uid"
	uuid := "111111111"
	id := "1234"
	group.Id = &id
	group.UniqueId = &uuid

	group.Tags = []model.Tag{{Scope: &scope, Tag: &id}}
	groupStore.Add(group)

	id1 := "4567"
	uuid1 := "111111112"
	group1 := model.Group{}
	group1.Id = &id1
	group1.UniqueId = &uuid1
	group1.Tags = []model.Tag{{Scope: &scope, Tag: &id1}}
	groupStore.Add(group1)

	policy := model.SecurityPolicy{}
	id2 := "1235"
	policy.Id = &id2
	policy.UniqueId = &uuid
	policy.Tags = []model.Tag{{Scope: &scope, Tag: &id2}}
	policyStore.Add(policy)

	type fields struct {
		NSXClient           *nsx.Client
		GroupStore          cache.Indexer
		SecurityPolicyStore cache.Indexer
		RuleStore           cache.Indexer
	}
	field := fields{NSXClient: nil, GroupStore: groupStore, SecurityPolicyStore: policyStore, RuleStore: ruleStore}

	tests := []struct {
		name    string
		fields  fields
		want    sets.String
		wantErr bool
	}{
		{
			name:    "test",
			fields:  field,
			wantErr: false,
		},
	}

	tests[0].want = sets.NewString()
	tests[0].want.Insert(id)
	tests[0].want.Insert(id1)
	tests[0].want.Insert(id2)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &SecurityPolicyService{
				Service:             common.Service{NSXClient: tt.fields.NSXClient},
				GroupStore:          tt.fields.GroupStore,
				SecurityPolicyStore: tt.fields.SecurityPolicyStore,
				RuleStore:           tt.fields.RuleStore,
			}
			got := service.ListSecurityPolicyID()
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SecurityPolicyService.ListSecurityPolicyID() = %v, want %v", got, tt.want)
			}
		})
	}
}
