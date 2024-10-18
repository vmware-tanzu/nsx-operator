package vpc

import (
	"fmt"
	"testing"

	"github.com/openlyinc/pointy"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

func TestVPCService_WrapHierarchyVPC(t *testing.T) {
	type args struct {
		org         string
		nsxtProject string
		vpc         *model.Vpc
		lbs         *model.LBService
		attachment  *model.VpcAttachment
	}
	tests := []struct {
		name         string
		args         args
		want         *model.OrgRoot
		wantChildren int
		wantErr      assert.ErrorAssertionFunc
	}{
		{
			name: "test",
			args: args{
				org:         "testorg",
				nsxtProject: "testproject",
				vpc:         &model.Vpc{},
				lbs:         &model.LBService{},
				attachment:  &model.VpcAttachment{},
			},
			want:         &model.OrgRoot{ResourceType: pointy.String("OrgRoot")},
			wantChildren: 1,
			wantErr:      assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &VPCService{}
			got, err := s.WrapHierarchyVPC(tt.args.org, tt.args.nsxtProject, tt.args.vpc, tt.args.lbs, tt.args.attachment)
			if !tt.wantErr(t, err, fmt.Sprintf("WrapHierarchyVPC(%v, %v, %v, %v)", tt.args.org, tt.args.nsxtProject, tt.args.vpc, tt.args.lbs)) {
				return
			}
			assert.Equalf(t, tt.wantChildren, len(got.Children), "WrapHierarchyVPC children count")
			got.Children = nil
			assert.Equalf(t, tt.want, got, "WrapHierarchyVPC(%v, %v, %v, %v)", tt.args.org, tt.args.nsxtProject, tt.args.vpc, tt.args.lbs)
		})
	}
}
