package common

import (
	"fmt"
	"testing"

	"github.com/openlyinc/pointy"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

func TestService_WrapAttachment(t *testing.T) {
	service := &Service{}
	attachmentId := "attachment-id"
	markedForDelete := true
	profile := "VpcConnectivityProfile"
	attachment := &model.VpcAttachment{
		Id:                     &attachmentId,
		MarkedForDelete:        &markedForDelete,
		VpcConnectivityProfile: &profile,
	}

	tests := []struct {
		name       string
		attachment *model.VpcAttachment
		want       []*data.StructValue
		wantErr    assert.ErrorAssertionFunc
	}{
		{
			name:       "Valid attachment",
			attachment: attachment,
			want: []*data.StructValue{
				{},
			},
			wantErr: assert.NoError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := service.WrapAttachment(tt.attachment)
			if !tt.wantErr(t, err, fmt.Sprintf("WrapAttachment(%v)", tt.attachment)) {
				return
			}
			assert.NotNil(t, got)
			if tt.attachment != nil {
				assert.Equal(t, *tt.attachment.Id, attachmentId)
			}
		})
	}
}

func TestService_WrapLBS(t *testing.T) {
	service := &Service{}
	lbsId := "lbs-id"
	markedForDelete := true
	lbs := &model.LBService{
		Id:              &lbsId,
		MarkedForDelete: &markedForDelete,
	}

	tests := []struct {
		name    string
		lbs     *model.LBService
		want    []*data.StructValue
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "Valid LBService",
			lbs:  lbs,
			want: []*data.StructValue{
				{},
			},
			wantErr: assert.NoError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := service.WrapLBS(tt.lbs)
			if !tt.wantErr(t, err, fmt.Sprintf("WrapLBS(%v)", tt.lbs)) {
				return
			}
			assert.NotNil(t, got)
			if tt.lbs != nil {
				assert.Equal(t, *tt.lbs.Id, lbsId)
			}
		})
	}
}

func TestService_WrapVPC(t *testing.T) {
	service := &Service{}
	vpcId := "vpc-id"
	markedForDelete := true
	vpc := &model.Vpc{
		Id:              &vpcId,
		MarkedForDelete: &markedForDelete,
	}

	tests := []struct {
		name    string
		vpc     *model.Vpc
		want    []*data.StructValue
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "Valid VPC",
			vpc:  vpc,
			want: []*data.StructValue{
				{},
			},
			wantErr: assert.NoError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := service.WrapVPC(tt.vpc)
			if !tt.wantErr(t, err, fmt.Sprintf("WrapVPC(%v)", tt.vpc)) {
				return
			}
			assert.NotNil(t, got)
			if tt.vpc != nil {
				assert.Equal(t, *tt.vpc.Id, vpcId)
			}
		})
	}
}

func TestService_WrapInfra(t *testing.T) {
	service := &Service{}
	children := []*data.StructValue{
		{},
		{},
	}

	tests := []struct {
		name     string
		children []*data.StructValue
		want     *model.Infra
		wantErr  assert.ErrorAssertionFunc
	}{
		{
			name:     "Valid children",
			children: children,
			want: &model.Infra{
				Children:     children,
				ResourceType: pointy.String(ResourceTypeInfra),
			},
			wantErr: assert.NoError,
		},
		{
			name:     "Nil children",
			children: nil,
			want: &model.Infra{
				Children:     nil,
				ResourceType: pointy.String(ResourceTypeInfra),
			},
			wantErr: assert.NoError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := service.WrapInfra(tt.children)
			if !tt.wantErr(t, err, fmt.Sprintf("WrapInfra(%v)", tt.children)) {
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestService_WrapOrgRoot(t *testing.T) {
	service := &Service{}
	children := []*data.StructValue{
		{},
		{},
	}

	tests := []struct {
		name     string
		children []*data.StructValue
		want     *model.OrgRoot
		wantErr  assert.ErrorAssertionFunc
	}{
		{
			name:     "Valid children",
			children: children,
			want: &model.OrgRoot{
				Children:     children,
				ResourceType: pointy.String(ResourceTypeOrgRoot),
			},
			wantErr: assert.NoError,
		},
		{
			name:     "Nil children",
			children: nil,
			want: &model.OrgRoot{
				Children:     nil,
				ResourceType: pointy.String(ResourceTypeOrgRoot),
			},
			wantErr: assert.NoError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := service.WrapOrgRoot(tt.children)
			if !tt.wantErr(t, err, fmt.Sprintf("WrapOrgRoot(%v)", tt.children)) {
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestService_WrapOrg(t *testing.T) {
	service := &Service{}
	org := "org-id"

	var projectChildren []*data.StructValue
	childrenProject, _ := service.WrapProject("nsxtProject", projectChildren)

	tests := []struct {
		name     string
		org      string
		children []*data.StructValue
		wantErr  assert.ErrorAssertionFunc
	}{
		{
			name:     "Valid org and children",
			org:      org,
			children: childrenProject,
			wantErr:  assert.NoError,
		},
		{
			name:     "Nil children",
			org:      org,
			children: nil,
			wantErr:  assert.NoError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := service.WrapOrg(tt.org, tt.children)
			if !tt.wantErr(t, err, fmt.Sprintf("WrapOrg(%v, %v)", tt.org, tt.children)) {
				return
			}
			assert.NotNil(t, got)
			if tt.children != nil {
				assert.Equal(t, len(tt.children), len(got))
			}
		})
	}
}
