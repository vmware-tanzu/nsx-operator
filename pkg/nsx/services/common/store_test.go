package common

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/openlyinc/pointy"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	mp_model "github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
)

func Test_DecrementPageSize(t *testing.T) {
	p := int64(1000)
	p1 := int64(100)
	p2 := int64(0)
	p3 := int64(-10)
	type args struct {
		pageSize *int64
	}
	tests := []struct {
		name string
		args args
	}{
		{"0", args{&p}},
		{"1", args{&p1}},
		{"2", args{&p2}},
		{"3", args{&p3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			DecrementPageSize(tt.args.pageSize)
		})
	}
}

func Test_transError(t *testing.T) {
	ec := int64(60576)
	var tc *bindings.TypeConverter
	patches := gomonkey.ApplyMethod(reflect.TypeOf(tc), "ConvertToGolang",
		func(_ *bindings.TypeConverter, d data.DataValue, b bindings.BindingType) (interface{}, []error) {
			apiError := model.ApiError{ErrorCode: &ec}
			var j interface{} = &apiError
			return j, nil
		})
	defer patches.Reset()

	type args struct {
		err error
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{"1", args{err: errors.ServiceUnavailable{}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := TransError(tt.args.err); (err != nil) != tt.wantErr {
				t.Errorf("transError() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

type fakeQueryClient struct {
}

func (_ *fakeQueryClient) List(_ string, _ *string, _ *string, _ *int64, _ *bool, _ *string) (model.SearchResponse, error) {
	cursor := "2"
	resultCount := int64(2)
	return model.SearchResponse{
		Results: []*data.StructValue{{}},
		Cursor:  &cursor, ResultCount: &resultCount,
	}, nil
}

func (resourceStore *ResourceStore) Apply(i interface{}) error {
	sp := i.(*model.SecurityPolicy)
	for _, rule := range sp.Rules {
		tempRule := rule
		if rule.MarkedForDelete != nil && *rule.MarkedForDelete {
			err := resourceStore.Delete(&tempRule)
			log.V(1).Info("delete rule from store", "rule", tempRule)
			if err != nil {
				return err
			}
		} else {
			err := resourceStore.Add(&tempRule)
			log.V(1).Info("add rule to store", "rule", tempRule)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case *model.Rule:
		return *v.Id, nil
	default:
		return "", nil
	}
}

func indexFunc(obj interface{}) ([]string, error) {
	res := make([]string, 0, 5)
	switch o := obj.(type) {
	case *model.Rule:
		return filterTag(o.Tags), nil
	default:
		return res, nil
	}
}

var filterTag = func(v []model.Tag) []string {
	res := make([]string, 0, 5)
	for _, tag := range v {
		if *tag.Scope == TagValueScopeSecurityPolicyUID {
			res = append(res, *tag.Tag)
		}
	}
	return res
}

func Test_InitializeResourceStore(t *testing.T) {
	config2 := nsx.NewConfig("localhost", "1", "1", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster, _ := nsx.NewCluster(config2)
	rc, _ := cluster.NewRestConnector()

	service := Service{
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
	}

	ruleCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{TagValueScopeSecurityPolicyUID: indexFunc})
	ruleStore := &ResourceStore{
		Indexer:     ruleCacheIndexer,
		BindingType: model.RuleBindingType(),
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
			var j interface{} = m
			return j, nil
		})
	defer patches2.Reset()

	service.InitializeResourceStore(&wg, fatalErrors, ResourceTypeRule, nil, ruleStore)
	assert.Empty(t, fatalErrors)
	assert.Equal(t, []string{"11111"}, ruleStore.ListKeys())
	mTag, mScope := TagScopeNamespace, "11111"
	service.InitializeResourceStore(&wg, fatalErrors, ResourceTypeRule, []model.Tag{{Tag: &mTag, Scope: &mScope}}, ruleStore)
	assert.Empty(t, fatalErrors)
	assert.Equal(t, []string{"11111"}, ruleStore.ListKeys())
}

func TestService_SearchResource(t *testing.T) {
	type args struct {
		resourceTypeValue string
		queryParam        string
		store             Store
		filter            Filter
	}
	tests := []struct {
		name    string
		args    args
		want    uint64
		wantErr bool
	}{
		{
			name: "Policy API with results",
			args: args{
				resourceTypeValue: "testResourceType",
				queryParam:        "testQueryParam",
				store: &fakeStore{
					isPolicyAPI: true,
				},
				filter: nil,
			},
			want:    1,
			wantErr: false,
		},
		{
			name: "MP API with results",
			args: args{
				resourceTypeValue: "testResourceType",
				queryParam:        "testQueryParam",
				store: &fakeStore{
					isPolicyAPI: false,
				},
				filter: nil,
			},
			want:    1,
			wantErr: false,
		},
		{
			name: "Policy API with error",
			args: args{
				resourceTypeValue: "testResourceType",
				queryParam:        "testQueryParam",
				store: &fakeStore{
					isPolicyAPI: true,
					transError:  true,
				},
				filter: nil,
			},
			want:    0,
			wantErr: true,
		},
		{
			name: "MP API with error",
			args: args{
				resourceTypeValue: "testResourceType",
				queryParam:        "testQueryParam",
				store: &fakeStore{
					isPolicyAPI: false,
					transError:  true,
				},
				filter: nil,
			},
			want:    0,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &Service{
				NSXClient: &nsx.Client{
					QueryClient:   &fakeQueryClient{},
					MPQueryClient: &fakeMPQueryClient{},
				},
			}
			got, err := service.SearchResource(tt.args.resourceTypeValue, tt.args.queryParam, tt.args.store, tt.args.filter)
			if (err != nil) != tt.wantErr {
				t.Errorf("SearchResource() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("SearchResource() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_containsTagScope(t *testing.T) {
	tests := []struct {
		name   string
		tags   []model.Tag
		scopes []string
		want   bool
	}{
		{
			name: "Tag with matching scope",
			tags: []model.Tag{
				{Scope: pointy.String("scope1")},
				{Scope: pointy.String("scope2")},
			},
			scopes: []string{"scope1"},
			want:   true,
		},
		{
			name: "Tag without matching scope",
			tags: []model.Tag{
				{Scope: pointy.String("scope1")},
				{Scope: pointy.String("scope2")},
			},
			scopes: []string{"scope3"},
			want:   false,
		},
		{
			name:   "Empty tags",
			tags:   []model.Tag{},
			scopes: []string{"scope1"},
			want:   false,
		},
		{
			name: "Empty scopes",
			tags: []model.Tag{
				{Scope: pointy.String("scope1")},
				{Scope: pointy.String("scope2")},
			},
			scopes: []string{},
			want:   false,
		},
		{
			name: "Nil scope in tag",
			tags: []model.Tag{
				{Scope: nil},
				{Scope: pointy.String("scope2")},
			},
			scopes: []string{"scope1"},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsTagScope(tt.tags, tt.scopes...); got != tt.want {
				t.Errorf("containsTagScope() = %v, want %v", got, tt.want)
			}
		})
	}
}

type fakeStore struct {
	isPolicyAPI bool
	transError  bool
}

func (f *fakeStore) TransResourceToStore(obj *data.StructValue) error {
	if f.transError {
		return fmt.Errorf("transformation error")
	}
	return nil
}

func (f *fakeStore) ListIndexFuncValues(key string) sets.Set[string] {
	return sets.New[string]()
}

func (f *fakeStore) Apply(obj interface{}) error {
	return nil
}

func (f *fakeStore) IsPolicyAPI() bool {
	return f.isPolicyAPI
}

type fakeMPQueryClient struct{}

func (_ *fakeMPQueryClient) List(_ string, _ *string, _ *string, _ *int64, _ *bool, _ *string) (mp_model.SearchResponse, error) {
	cursor := "2"
	resultCount := int64(2)
	return mp_model.SearchResponse{
		Results: []*data.StructValue{{}},
		Cursor:  &cursor, ResultCount: &resultCount,
	}, nil
}

func Test_formatTagParamScope(t *testing.T) {
	tests := []struct {
		name      string
		paramType string
		value     string
		want      string
	}{
		{
			name:      "Simple value",
			paramType: "tags.scope",
			value:     "simpleValue",
			want:      "tags.scope:simpleValue",
		},
		{
			name:      "Value with slash",
			paramType: "tags.scope",
			value:     "value/with/slash",
			want:      "tags.scope:value\\/with\\/slash",
		},
		{
			name:      "Empty value",
			paramType: "tags.scope",
			value:     "",
			want:      "tags.scope:",
		},
		{
			name:      "Value with multiple slashes",
			paramType: "tags.scope",
			value:     "value/with/multiple/slashes",
			want:      "tags.scope:value\\/with\\/multiple\\/slashes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatTagParamScope(tt.paramType, tt.value); got != tt.want {
				t.Errorf("formatTagParamScope() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_formatTagParamTag(t *testing.T) {
	tests := []struct {
		name      string
		paramType string
		value     string
		want      string
	}{
		{
			name:      "Simple value",
			paramType: "tags.tag",
			value:     "simpleValue",
			want:      "tags.tag:simpleValue",
		},
		{
			name:      "Value with colon",
			paramType: "tags.tag",
			value:     "value:with:colon",
			want:      "tags.tag:value\\:with\\:colon",
		},
		{
			name:      "Empty value",
			paramType: "tags.tag",
			value:     "",
			want:      "tags.tag:",
		},
		{
			name:      "Value with multiple colons",
			paramType: "tags.tag",
			value:     "value:with:multiple:colons",
			want:      "tags.tag:value\\:with\\:multiple\\:colons",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatTagParamTag(tt.paramType, tt.value); got != tt.want {
				t.Errorf("formatTagParamTag() = %v, want %v", got, tt.want)
			}
		})
	}
}
