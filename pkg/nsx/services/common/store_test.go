package common

import (
	"reflect"
	"sync"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
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

func (resourceStore *ResourceStore) Operate(i interface{}) error {
	sp := i.(*model.SecurityPolicy)
	for _, rule := range sp.Rules {
		if rule.MarkedForDelete != nil && *rule.MarkedForDelete {
			err := resourceStore.Delete(rule)
			log.V(1).Info("delete rule from store", "rule", rule)
			if err != nil {
				return err
			}
		} else {
			err := resourceStore.Add(rule)
			log.V(1).Info("add rule to store", "rule", rule)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case model.Rule:
		return *v.Id, nil
	default:
		return "", nil
	}
}

func indexFunc(obj interface{}) ([]string, error) {
	res := make([]string, 0, 5)
	switch o := obj.(type) {
	case model.Rule:
		return filterTag(o.Tags), nil
	default:
		return res, nil
	}
}

var filterTag = func(v []model.Tag) []string {
	res := make([]string, 0, 5)
	for _, tag := range v {
		if *tag.Scope == TagScopeSecurityPolicyCRUID {
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

	ruleCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{TagScopeSecurityPolicyCRUID: indexFunc})
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
