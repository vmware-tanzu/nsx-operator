/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package common

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	mpmodel "github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
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
				OrgID:      "org1",
				ProjectID:  "proj1",
				VPCID:      "vpc1",
				ParentID:   "subnet1",
				ID:         "port1",
				PrivateIps: nil,
			},
			wantErr: false,
		},
		{
			name: "Subnet Path",
			args: args{
				nsxResourcePath: "/orgs/org1/projects/proj1/vpcs/vpc1/subnets/subnet1",
			},
			want: VPCResourceInfo{
				OrgID:      "org1",
				ProjectID:  "proj1",
				VPCID:      "vpc1",
				ParentID:   "vpc1",
				ID:         "subnet1",
				PrivateIps: nil,
			},
			wantErr: false,
		},
		{
			name: "VPC Path",
			args: args{
				nsxResourcePath: "/orgs/org1/projects/proj1/vpcs/vpc1",
			},
			want: VPCResourceInfo{
				OrgID:      "org1",
				ProjectID:  "proj1",
				VPCID:      "vpc1",
				ParentID:   "proj1",
				ID:         "vpc1",
				PrivateIps: nil,
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

func TestIsDefaultNetworkConfigCR(t *testing.T) {
	testCRD1 := v1alpha1.VPCNetworkConfiguration{}
	testCRD1.Name = "test-1"
	testCRD2 := v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				AnnotationDefaultNetworkConfig: "invalid",
			},
		},
	}
	testCRD2.Name = "test-2"
	testCRD3 := v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				AnnotationDefaultNetworkConfig: "true",
			},
		},
	}
	testCRD3.Name = "test-3"
	assert.Equal(t, IsDefaultNetworkConfigCR(&testCRD1), false)
	assert.Equal(t, IsDefaultNetworkConfigCR(&testCRD2), false)
	assert.Equal(t, IsDefaultNetworkConfigCR(&testCRD3), true)
}

func TestNsxProjectPathToId(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		org       string
		project   string
		expectErr string
	}{
		{"Valid project path", "/orgs/default/projects/nsx_operator_e2e_test", "default", "nsx_operator_e2e_test", ""},
		{"Invalid project path", "", "", "", "invalid NSX project path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, p, err := NSXProjectPathToId(tt.path)
			if tt.expectErr != "" {
				assert.ErrorContains(t, err, tt.expectErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.org, o)
			assert.Equal(t, tt.project, p)
		})
	}
}

func TestExtractSubnetPath(t *testing.T) {
	tests := []struct {
		name              string
		sharedSubnetPath  string
		expectedSubnet    string
		expectedProject   string
		expectedVPC       string
		expectedOrg       string
		expectedErrString string
	}{
		{
			name:             "Valid subnet path",
			sharedSubnetPath: "/orgs/default/projects/proj-1/vpcs/vpc-1/subnets/subnet-1",
			expectedSubnet:   "subnet-1",
			expectedProject:  "proj-1",
			expectedVPC:      "vpc-1",
			expectedOrg:      "default",
		},
		{
			name:              "Invalid subnet path",
			sharedSubnetPath:  "invalid-path",
			expectedErrString: "invalid subnet path format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orgName, projectName, vpcName, subnetName, err := ExtractSubnetPath(tt.sharedSubnetPath)

			if tt.expectedErrString != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErrString)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedOrg, orgName)
				assert.Equal(t, tt.expectedProject, projectName)
				assert.Equal(t, tt.expectedVPC, vpcName)
				assert.Equal(t, tt.expectedSubnet, subnetName)
			}
		})
	}
}

func TestGetSubnetPathFromAssociatedResource(t *testing.T) {
	path, err := GetSubnetPathFromAssociatedResource("project-1:ns-1:subnet-1")
	assert.Nil(t, err)
	assert.Equal(t, "/orgs/default/projects/project-1/vpcs/ns-1/subnets/subnet-1", path)

	_, err = GetSubnetPathFromAssociatedResource("invalid-annotation")
	assert.ErrorContains(t, err, "failed to parse associated resource annotation")
}

func TestConvertSubnetPathToAssociatedResource(t *testing.T) {
	tests := []struct {
		name              string
		sharedSubnetPath  string
		expectedResource  string
		expectedErrString string
	}{
		{
			name:             "Valid subnet path",
			sharedSubnetPath: "/orgs/default/projects/proj-1/vpcs/vpc-1/subnets/subnet-1",
			expectedResource: "proj-1%vpc-1%subnet-1",
		},
		{
			name:              "Invalid subnet path",
			sharedSubnetPath:  "invalid-path",
			expectedErrString: "invalid subnet path format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource, err := ConvertSubnetPathToAssociatedResource(tt.sharedSubnetPath)

			if tt.expectedErrString != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErrString)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedResource, resource)
			}
		})
	}
}
