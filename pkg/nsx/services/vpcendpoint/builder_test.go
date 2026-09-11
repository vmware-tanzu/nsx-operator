/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package vpcendpoint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func newTestService() *VPCEndpointService {
	return &VPCEndpointService{
		Service: common.Service{
			Client: fake.NewClientBuilder().Build(),
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{Cluster: "k8scl-one:test"},
			},
		},
		VPCEndpointStore: buildVPCEndpointStore(),
	}
}

func TestBuildVPCEndpoint(t *testing.T) {
	service := newTestService()
	obj := &v1alpha1.VPCEndpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vpcep-1",
			Namespace: "ns-1",
			UID:       types.UID("11111111-2222-3333-4444-555555555555"),
		},
		Spec: v1alpha1.VPCEndpointSpec{
			ServiceEndpointName: "proj-1:vpc-1:se-1",
			IPAllocationName:    "ip-alloc-1",
		},
	}
	ipAllocationPath := "/orgs/default/projects/proj-2/vpcs/vpc-2/ip-address-allocations/ipa-1"
	serviceEndpointPath := "/orgs/default/projects/proj-1/vpcs/vpc-1/vpc-service-endpoints/se-1"

	nsxVPCEndpoint, err := service.BuildVPCEndpoint(obj, ipAllocationPath, serviceEndpointPath)
	assert.NoError(t, err)
	assert.NotNil(t, nsxVPCEndpoint.Id)
	assert.NotEmpty(t, *nsxVPCEndpoint.Id)
	assert.Equal(t, ipAllocationPath, *nsxVPCEndpoint.IpAllocationPath)
	assert.Equal(t, serviceEndpointPath, *nsxVPCEndpoint.VpcServiceEndpoint)

	foundName, foundUID := false, false
	for _, tag := range nsxVPCEndpoint.Tags {
		if *tag.Scope == common.TagScopeVPCEndpointCRName {
			assert.Equal(t, "vpcep-1", *tag.Tag)
			foundName = true
		}
		if *tag.Scope == common.TagScopeVPCEndpointCRUID {
			assert.Equal(t, string(obj.UID), *tag.Tag)
			foundUID = true
		}
	}
	assert.True(t, foundName, "expected a tag with scope %s", common.TagScopeVPCEndpointCRName)
	assert.True(t, foundUID, "expected a tag with scope %s", common.TagScopeVPCEndpointCRUID)
}
