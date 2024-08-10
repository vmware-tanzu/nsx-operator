/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package common

import (
	"reflect"
	"testing"

	mpmodel "github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

func TestConvertMPTagsToTags(t *testing.T) {
	scope, tag := "scope1", "tag1"
	type args struct {
		mpTags []mpmodel.Tag
	}
	tests := []struct {
		name string
		args args
		want []model.Tag
	}{
		{
			name: "nil",
			args: args{
				mpTags: nil,
			},
			want: []model.Tag{},
		},
		{
			name: "standard",
			args: args{
				mpTags: []mpmodel.Tag{{
					Scope: &scope,
					Tag:   &tag,
				}},
			},
			want: []model.Tag{{
				Scope: &scope,
				Tag:   &tag,
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ConvertMPTagsToTags(tt.args.mpTags); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ConvertMPTagsToTags() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConvertTagsToMPTags(t *testing.T) {
	scope, tag := "scope1", "tag1"
	type args struct {
		tags []model.Tag
	}
	tests := []struct {
		name string
		args args
		want []mpmodel.Tag
	}{
		{
			name: "nil",
			args: args{
				tags: nil,
			},
			want: []mpmodel.Tag{},
		},
		{
			name: "standard",
			args: args{
				tags: []model.Tag{{
					Scope: &scope,
					Tag:   &tag,
				}},
			},
			want: []mpmodel.Tag{{
				Scope: &scope,
				Tag:   &tag,
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ConvertTagsToMPTags(tt.args.tags); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ConvertTagsToMPTags() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQueryTagCondition(t *testing.T) {
	type args struct {
		resourceType string
		cluster      string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "CCP-dummyCluster",
			args: args{
				resourceType: ResourceTypeClusterControlPlane,
				cluster:      "dummyCluster:a:b",
			},
			want: "resource_type:clustercontrolplane AND tags.scope:nsx-op\\/cluster AND tags.tag:dummyCluster\\:a\\:b",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := QueryTagCondition(tt.args.resourceType, tt.args.cluster); got != tt.want {
				t.Errorf("QueryTagCondition() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseVPCResourcePath(t *testing.T) {
	type args struct {
		nsxResourcePath string
	}
	tests := []struct {
		name    string
		args    args
		want    VPCResourceInfo
		wantErr bool
	}{
		{
			name: "SubnetPort Path",
			args: args{
				nsxResourcePath: "/orgs/org1/projects/proj1/vpcs/vpc1/subnets/subnet1/ports/port1",
			},
			want: VPCResourceInfo{
				OrgID:             "org1",
				ProjectID:         "proj1",
				VPCID:             "vpc1",
				ParentID:          "subnet1",
				ID:                "port1",
				PrivateIpv4Blocks: nil,
			},
			wantErr: false,
		},
		{
			name: "VPC Path",
			args: args{
				nsxResourcePath: "/orgs/org1/projects/proj1/vpcs/vpc1",
			},
			want: VPCResourceInfo{
				OrgID:             "org1",
				ProjectID:         "proj1",
				VPCID:             "vpc1",
				ParentID:          "proj1",
				ID:                "vpc1",
				PrivateIpv4Blocks: nil,
			},
			wantErr: false,
		},
		{
			name: "Invalid Path",
			args: args{
				nsxResourcePath: "/abc/def",
			},
			want:    VPCResourceInfo{},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseVPCResourcePath(tt.args.nsxResourcePath)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseVPCResourcePath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(tt.want, got) {
				t.Errorf("ParseVPCResourcePath() got = %v, want %v", got, tt.want)
			}
		})
	}
}
