package mediator

import (
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

func TestServiceMediator_GetOrgProject(t *testing.T) {
	vpcService := &vpc.VPCService{}
	vs := &ServiceMediator{
		SecurityPolicyService: nil,
		VPCService:            vpcService,
	}

	patches := gomonkey.ApplyMethod(reflect.TypeOf(vpcService), "GetVPCsByNamespace", func(_ *vpc.VPCService, ns string) []model.Vpc {
		return []model.Vpc{{Path: common.String("/orgs/default/projects/project-1/vpcs/vpc-1")}}
	})
	defer patches.Reset()

	got := vs.ListVPCInfo("ns")[0]
	want := common.VPCResourceInfo{OrgID: "default", ProjectID: "project-1", VPCID: "vpc-1", ID: "vpc-1", ParentID: "project-1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GetOrgProject() = %v, want %v", got, want)
	}
}
