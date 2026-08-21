/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package vpcendpoint

import (
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func (service *VPCEndpointService) BuildVPCEndpoint(obj *v1alpha1.VPCEndpoint, ipAllocationPath, serviceEndpointPath string) (*model.VpcEndpoint, error) {
	tags := service.buildVPCEndpointTags(obj)
	vpcEndpoint := &model.VpcEndpoint{
		Id:                 String(service.BuildVPCEndpointID(obj)),
		DisplayName:        String(service.buildVPCEndpointName(obj)),
		Tags:               tags,
		IpAllocationPath:   String(ipAllocationPath),
		VpcServiceEndpoint: String(serviceEndpointPath),
	}
	return vpcEndpoint, nil
}

func (service *VPCEndpointService) BuildVPCEndpointID(obj metav1.Object) string {
	return common.BuildUniqueIDWithRandomUUID(obj, util.GenerateIDByObject, service.vpcEndpointIdExists)
}

func (service *VPCEndpointService) buildVPCEndpointName(obj metav1.Object) string {
	return util.GenerateTruncName(common.MaxNameLength, obj.GetName(), "", "", "", "")
}

func (service *VPCEndpointService) buildVPCEndpointTags(obj metav1.Object) []model.Tag {
	return util.BuildBasicTags(service.NSXConfig.Cluster, obj, service.GetNamespaceUID(obj.GetNamespace()))
}
