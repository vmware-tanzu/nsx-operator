/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package vpcendpoint

import (
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type VPCEndpoint model.VpcEndpoint

type Comparable = common.Comparable

func (ve *VPCEndpoint) Key() string {
	return *ve.Id
}

func (ve *VPCEndpoint) Value() data.DataValue {
	if ve == nil {
		return nil
	}
	s := &VPCEndpoint{Id: ve.Id, DisplayName: ve.DisplayName, Tags: ve.Tags, IpAllocationPath: ve.IpAllocationPath, VpcServiceEndpoint: ve.VpcServiceEndpoint}
	dataValue, _ := ComparableToVPCEndpoint(s).GetDataValue__()
	return dataValue
}

func VPCEndpointToComparable(ve *model.VpcEndpoint) Comparable {
	return (*VPCEndpoint)(ve)
}

func ComparableToVPCEndpoint(ve Comparable) *model.VpcEndpoint {
	return (*model.VpcEndpoint)(ve.(*VPCEndpoint))
}
