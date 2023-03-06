package nsxserviceaccount

import (
	"reflect"
	"testing"

	mpmodel "github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

func Test_indexFunc(t *testing.T) {
	mId, mTag, mScope := "11111", "11111", "nsx-op/nsx_service_account_uid"
	ccp := model.ClusterControlPlane{
		Id:   &mId,
		Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
	}
	pi := mpmodel.PrincipalIdentity{
		Id:   &mId,
		Tags: []mpmodel.Tag{{Tag: &mTag, Scope: &mScope}},
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
		{"1", args{obj: ccp}, []string{"11111"}, false},
		{"2", args{obj: pi}, []string{"11111"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := indexFunc(tt.args.obj)
			if (err != nil) != tt.wantErr {
				t.Errorf("CRUIDScopeIndexFunc() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CRUIDScopeIndexFunc() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_keyFunc(t *testing.T) {
	Id := "11111"
	ccp := model.ClusterControlPlane{Id: &Id}
	pi := mpmodel.PrincipalIdentity{Name: &Id}
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
		{"1", args{obj: ccp}, Id, false},
		{"2", args{obj: pi}, Id, false},
		{"0", args{obj: o}, "", true},
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
