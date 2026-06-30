package vpc

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func TestVPCService_WrapHierarchyVPC(t *testing.T) {
	type args struct {
		org               string
		nsxtProject       string
		vpc               *model.Vpc
		lbServiceIPAllocs []*model.VpcIpAddressAllocation
		lbs               *model.LBService
		attachment        *model.VpcAttachment
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
			name: "no lbServiceIPAllocs (nil slice)",
			args: args{
				org:               "testorg",
				nsxtProject:       "testproject",
				vpc:               &model.Vpc{},
				lbServiceIPAllocs: nil,
				lbs:               &model.LBService{},
				attachment:        &model.VpcAttachment{},
			},
			want:            &model.OrgRoot{ResourceType: util.Ptr("OrgRoot")},
			wantOrgChildren: 1,
			wantVPCChildren: 2, // LBS + Attachment
			wantErr:         assert.NoError,
		},
		{
			name: "single IPv4 lbServiceIPAlloc",
			args: args{
				org:         "testorg",
				nsxtProject: "testproject",
				vpc:         &model.Vpc{},
				lbServiceIPAllocs: []*model.VpcIpAddressAllocation{
					{Id: common.String(common.LBServiceIPAllocationID)},
				},
				lbs:        &model.LBService{},
				attachment: &model.VpcAttachment{},
			},
			want:            &model.OrgRoot{ResourceType: util.Ptr("OrgRoot")},
			wantOrgChildren: 1,
			wantVPCChildren: 3, // IPv4 IPAlloc + LBS + Attachment
			wantErr:         assert.NoError,
		},
		{
			name: "dual-stack lbServiceIPAllocs (IPv4 + IPv6)",
			args: args{
				org:         "testorg",
				nsxtProject: "testproject",
				vpc:         &model.Vpc{},
				lbServiceIPAllocs: []*model.VpcIpAddressAllocation{
					{Id: common.String(common.LBServiceIPAllocationID)},
					{Id: common.String(common.LBServiceIPAllocationIDV6)},
				},
				lbs:        &model.LBService{},
				attachment: &model.VpcAttachment{},
			},
			want:            &model.OrgRoot{ResourceType: util.Ptr("OrgRoot")},
			wantOrgChildren: 1,
			wantVPCChildren: 4, // IPv4 IPAlloc + IPv6 IPAlloc + LBS + Attachment
			wantErr:         assert.NoError,
		},
		{
			name: "nil lbs and nil attachment, no allocs",
			args: args{
				org:               "testorg",
				nsxtProject:       "testproject",
				vpc:               &model.Vpc{},
				lbServiceIPAllocs: nil,
				lbs:               nil,
				attachment:        nil,
			},
			want:            &model.OrgRoot{ResourceType: util.Ptr("OrgRoot")},
			wantOrgChildren: 1,
			wantVPCChildren: 0,
			wantErr:         assert.NoError,
		},
		{
			name: "only IPv4 lbServiceIPAlloc, no lbs or attachment",
			args: args{
				org:         "testorg",
				nsxtProject: "testproject",
				vpc:         &model.Vpc{},
				lbServiceIPAllocs: []*model.VpcIpAddressAllocation{
					{Id: common.String(common.LBServiceIPAllocationID)},
				},
				lbs:        nil,
				attachment: nil,
			},
			want:            &model.OrgRoot{ResourceType: util.Ptr("OrgRoot")},
			wantOrgChildren: 1,
			wantVPCChildren: 1, // only IPv4 IPAlloc
			wantErr:         assert.NoError,
		},
		{
			name: "only IPv6 lbServiceIPAlloc, no lbs or attachment",
			args: args{
				org:         "testorg",
				nsxtProject: "testproject",
				vpc:         &model.Vpc{},
				lbServiceIPAllocs: []*model.VpcIpAddressAllocation{
					{Id: common.String(common.LBServiceIPAllocationIDV6)},
				},
				lbs:        nil,
				attachment: nil,
			},
			want:            &model.OrgRoot{ResourceType: util.Ptr("OrgRoot")},
			wantOrgChildren: 1,
			wantVPCChildren: 1, // only IPv6 IPAlloc
			wantErr:         assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &VPCService{}
			got, err := s.WrapHierarchyVPC(tt.args.org, tt.args.nsxtProject, tt.args.vpc, tt.args.lbServiceIPAllocs, tt.args.lbs, tt.args.attachment)
			if !tt.wantErr(t, err, fmt.Sprintf("WrapHierarchyVPC(%v, %v, %v, %v, %v)", tt.args.org, tt.args.nsxtProject, tt.args.vpc, tt.args.lbServiceIPAllocs, tt.args.lbs)) {
				return
			}
			require.NotNil(t, got)
			assert.Equalf(t, tt.wantOrgChildren, len(got.Children), "OrgRoot children count")
			assert.Equalf(t, tt.wantVPCChildren, len(tt.args.vpc.Children), "VPC children count")
			got.Children = nil
			assert.Equalf(t, tt.want, got, "WrapHierarchyVPC(%v, %v, %v, %v, %v)", tt.args.org, tt.args.nsxtProject, tt.args.vpc, tt.args.lbServiceIPAllocs, tt.args.lbs)
		})
	}
}
