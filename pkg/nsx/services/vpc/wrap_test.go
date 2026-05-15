package vpc

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.openly.dev/pointy"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func TestVPCService_WrapHierarchyVPC(t *testing.T) {
	type args struct {
		org              string
		nsxtProject      string
		vpc              *model.Vpc
		lbServiceIPAlloc *model.VpcIpAddressAllocation
		lbs              *model.LBService
		attachment       *model.VpcAttachment
	}
	tests := []struct {
		name            string
		args            args
		want            *model.OrgRoot
		wantOrgChildren int // OrgRoot always has 1 child (the Org wrapper)
		wantVPCChildren int // VPC.Children count after wrapping
		wantErr         assert.ErrorAssertionFunc
	}{
		{
			name: "without lbServiceIPAlloc",
			args: args{
				org:              "testorg",
				nsxtProject:      "testproject",
				vpc:              &model.Vpc{},
				lbServiceIPAlloc: nil,
				lbs:              &model.LBService{},
				attachment:       &model.VpcAttachment{},
			},
			want:            &model.OrgRoot{ResourceType: pointy.String("OrgRoot")},
			wantOrgChildren: 1,
			wantVPCChildren: 2, // LBS + Attachment
			wantErr:         assert.NoError,
		},
		{
			name: "with lbServiceIPAlloc",
			args: args{
				org:              "testorg",
				nsxtProject:      "testproject",
				vpc:              &model.Vpc{},
				lbServiceIPAlloc: &model.VpcIpAddressAllocation{Id: common.String(common.LBServiceIPAllocationID)},
				lbs:              &model.LBService{},
				attachment:       &model.VpcAttachment{},
			},
			want:            &model.OrgRoot{ResourceType: pointy.String("OrgRoot")},
			wantOrgChildren: 1,
			wantVPCChildren: 3, // IPAlloc + LBS + Attachment
			wantErr:         assert.NoError,
		},
		{
			name: "nil lbs and nil attachment",
			args: args{
				org:              "testorg",
				nsxtProject:      "testproject",
				vpc:              &model.Vpc{},
				lbServiceIPAlloc: nil,
				lbs:              nil,
				attachment:       nil,
			},
			want:            &model.OrgRoot{ResourceType: pointy.String("OrgRoot")},
			wantOrgChildren: 1,
			wantVPCChildren: 0,
			wantErr:         assert.NoError,
		},
		{
			name: "only lbServiceIPAlloc, no lbs or attachment",
			args: args{
				org:              "testorg",
				nsxtProject:      "testproject",
				vpc:              &model.Vpc{},
				lbServiceIPAlloc: &model.VpcIpAddressAllocation{Id: common.String(common.LBServiceIPAllocationID)},
				lbs:              nil,
				attachment:       nil,
			},
			want:            &model.OrgRoot{ResourceType: util.Ptr("OrgRoot")},
			wantOrgChildren: 1,
			wantVPCChildren: 1, // only IPAlloc
			wantErr:         assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &VPCService{}
			got, err := s.WrapHierarchyVPC(tt.args.org, tt.args.nsxtProject, tt.args.vpc, tt.args.lbServiceIPAlloc, tt.args.lbs, tt.args.attachment)
			if !tt.wantErr(t, err, fmt.Sprintf("WrapHierarchyVPC(%v, %v, %v, %v, %v)", tt.args.org, tt.args.nsxtProject, tt.args.vpc, tt.args.lbServiceIPAlloc, tt.args.lbs)) {
				return
			}
			require.NotNil(t, got)
			assert.Equalf(t, tt.wantOrgChildren, len(got.Children), "OrgRoot children count")
			assert.Equalf(t, tt.wantVPCChildren, len(tt.args.vpc.Children), "VPC children count")
			got.Children = nil
			assert.Equalf(t, tt.want, got, "WrapHierarchyVPC(%v, %v, %v, %v, %v)", tt.args.org, tt.args.nsxtProject, tt.args.vpc, tt.args.lbServiceIPAlloc, tt.args.lbs)
		})
	}
}
