package services

import (
	"reflect"
	"sync"
	"testing"

	"github.com/agiledragon/gomonkey"
	"github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func Test_securityPolicyCRUIDScopeIndexFunc(t *testing.T) {
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
	type args struct {
		obj interface{}
	}
	tests := []struct {
		name    string
		args    args
		want    []string
		wantErr bool
	}{
		{"1", args{obj: m}, []string{"11111"}, false},
		{"2", args{obj: s}, []string{"11111"}, false},
		{"3", args{obj: r}, []string{"11111"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := securityPolicyCRUIDScopeIndexFunc(tt.args.obj)
			if (err != nil) != tt.wantErr {
				t.Errorf("securityPolicyCRUIDScopeIndexFunc() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("securityPolicyCRUIDScopeIndexFunc() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_appendTag(t *testing.T) {
	mTag, mScope := "11111", "nsx-op/security_policy_cr_uid"
	mTag2, mScope2 := "11111", "nsx"
	tags := []model.Tag{{Scope: &mScope, Tag: &mTag}}
	tags2 := []model.Tag{{Scope: &mScope2, Tag: &mTag2}}
	res := []string{}
	res2 := []string{}
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
			if got := appendTag(tt.args.v, tt.args.res); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("appendTag() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_namespaceIndexFunc(t *testing.T) {
	mId, mTag, mScope := "11111", "11111", "nsx-op/namespace"
	m := model.Group{
		Id:   &mId,
		Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
	}
	type args struct {
		obj interface{}
	}
	tests := []struct {
		name    string
		args    args
		want    []string
		wantErr bool
	}{
		{"1", args{obj: m}, []string{"11111"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := namespaceIndexFunc(tt.args.obj)
			if (err != nil) != tt.wantErr {
				t.Errorf("namespaceIndexFunc() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("namespaceIndexFunc() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_keyFunc(t *testing.T) {
	Id := "11111"
	g := model.Group{Id: &Id}
	s := model.SecurityPolicy{Id: &Id}
	r := model.Rule{Id: &Id}
	o := model.UserInfo{}
	type args struct {
		obj interface{}
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{"1", args{obj: g}, Id, false},
		{"2", args{obj: s}, Id, false},
		{"3", args{obj: r}, Id, false},
		{"4", args{obj: o}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := keyFunc(tt.args.obj)
			if (err != nil) != tt.wantErr {
				t.Errorf("keyFunc() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("keyFunc() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_queryTagCondition(t *testing.T) {
	config2 := nsx.NewConfig("1.1.1.1", "1", "1", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster, _ := nsx.NewCluster(config2)
	rc, _ := cluster.NewRestConnector()
	service = &SecurityPolicyService{
		NSXClient: &nsx.Client{
			QueryClient:   &fakeQueryClient{},
			RestConnector: rc,
			NsxConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "k8scl-one:test",
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
		NSXClient: &nsx.Client{
			QueryClient:   &fakeQueryClient{},
			RestConnector: rc,
		},
		GroupStore: cache.NewIndexer(keyFunc2,
			cache.Indexers{
				util.TagScopeNamespace:           namespaceIndexFunc,
				util.TagScopeSecurityPolicyCRUID: securityPolicyCRUIDScopeIndexFunc,
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
		NSXClient: &nsx.Client{
			QueryClient:   &fakeQueryClient{},
			RestConnector: rc,
		},
		SecurityPolicyStore: cache.NewIndexer(keyFunc2,
			cache.Indexers{
				util.TagScopeNamespace:           namespaceIndexFunc,
				util.TagScopeSecurityPolicyCRUID: securityPolicyCRUIDScopeIndexFunc,
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
		NSXClient: &nsx.Client{
			QueryClient:   &fakeQueryClient{},
			RestConnector: rc,
		},
		RuleStore: cache.NewIndexer(keyFunc2,
			cache.Indexers{
				util.TagScopeNamespace:           namespaceIndexFunc,
				util.TagScopeSecurityPolicyCRUID: securityPolicyCRUIDScopeIndexFunc,
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

func Test_decrementPageSize(t *testing.T) {
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
			decrementPageSize(tt.args.pageSize)
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
			if err := transError(tt.args.err); (err != nil) != tt.wantErr {
				t.Errorf("transError() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
