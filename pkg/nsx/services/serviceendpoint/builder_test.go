/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package serviceendpoint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func newTestService() *ServiceEndpointService {
	return &ServiceEndpointService{
		Service: common.Service{
			Client: fake.NewClientBuilder().Build(),
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{Cluster: "k8scl-one:test"},
			},
		},
		ServiceEndpointStore: buildServiceEndpointStore(),
	}
}

func TestBuildServiceEndpoint(t *testing.T) {
	service := newTestService()
	obj := &v1alpha1.ServiceEndpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "se-1",
			Namespace: "ns-1",
			UID:       types.UID("11111111-2222-3333-4444-555555555555"),
		},
		Spec: v1alpha1.ServiceEndpointSpec{
			ServiceEndpointIP: "10.0.0.10",
		},
	}

	nsxServiceEndpoint, err := service.BuildServiceEndpoint(obj)
	assert.NoError(t, err)
	assert.NotNil(t, nsxServiceEndpoint.Id)
	assert.NotEmpty(t, *nsxServiceEndpoint.Id)
	assert.Equal(t, "10.0.0.10", *nsxServiceEndpoint.ServiceEndpointIp)
	assert.Equal(t, model.VpcServiceEndpoint_SERVICE_ENDPOINT_IP_TYPE_WORKLOAD, *nsxServiceEndpoint.ServiceEndpointIpType)

	foundName, foundUID := false, false
	for _, tag := range nsxServiceEndpoint.Tags {
		if *tag.Scope == common.TagScopeServiceEndpointCRName {
			assert.Equal(t, "se-1", *tag.Tag)
			foundName = true
		}
		if *tag.Scope == common.TagScopeServiceEndpointCRUID {
			assert.Equal(t, string(obj.UID), *tag.Tag)
			foundUID = true
		}
	}
	assert.True(t, foundName, "expected a tag with scope %s", common.TagScopeServiceEndpointCRName)
	assert.True(t, foundUID, "expected a tag with scope %s", common.TagScopeServiceEndpointCRUID)
}
