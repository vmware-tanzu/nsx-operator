/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package serviceendpoint

import (
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func (service *ServiceEndpointService) BuildServiceEndpoint(obj *v1alpha1.ServiceEndpoint) *model.VpcServiceEndpoint {
	tags := service.buildServiceEndpointTags(obj)
	serviceEndpointType := model.VpcServiceEndpoint_SERVICE_ENDPOINT_IP_TYPE_WORKLOAD
	return &model.VpcServiceEndpoint{
		Id:                    String(service.BuildServiceEndpointID(obj)),
		DisplayName:           String(service.buildServiceEndpointName(obj)),
		Tags:                  tags,
		ServiceEndpointIp:     String(obj.Spec.ServiceEndpointIP),
		ServiceEndpointIpType: &serviceEndpointType,
	}
}

func (service *ServiceEndpointService) BuildServiceEndpointID(obj metav1.Object) string {
	return common.BuildUniqueIDWithRandomUUID(obj, util.GenerateIDByObject, service.serviceEndpointIdExists)
}

func (service *ServiceEndpointService) buildServiceEndpointName(obj metav1.Object) string {
	return util.GenerateTruncName(common.MaxNameLength, obj.GetName(), "", "", "", "")
}

func (service *ServiceEndpointService) buildServiceEndpointTags(obj metav1.Object) []model.Tag {
	return util.BuildBasicTags(service.NSXConfig.Cluster, obj, service.GetNamespaceUID(obj.GetNamespace()))
}
