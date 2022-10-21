package common

import (
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func Test_SecurityPolicyCRUIDScopeIndexFunc(t *testing.T) {
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
			got, err := CRUIDScopeIndexFunc(util.TagScopeSecurityPolicyCRUID, tt.args.obj)
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
			if got := filterTag(util.TagScopeSecurityPolicyCRUID, tt.args.v); !reflect.DeepEqual(got, tt.want) {
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
			got, err := KeyFunc(tt.args.obj)
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
