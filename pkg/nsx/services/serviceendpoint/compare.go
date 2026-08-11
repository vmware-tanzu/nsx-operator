/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package serviceendpoint

import (
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type ServiceEndpoint model.VpcServiceEndpoint

type Comparable = common.Comparable

func (se *ServiceEndpoint) Key() string {
	return *se.Id
}

func (se *ServiceEndpoint) Value() data.DataValue {
	if se == nil {
		return nil
	}
	s := &ServiceEndpoint{Id: se.Id, DisplayName: se.DisplayName, Tags: se.Tags, ServiceEndpointIp: se.ServiceEndpointIp, ServiceEndpointIpType: se.ServiceEndpointIpType}
	dataValue, _ := ComparableToServiceEndpoint(s).GetDataValue__()
	return dataValue
}

func ServiceEndpointToComparable(se *model.VpcServiceEndpoint) Comparable {
	return (*ServiceEndpoint)(se)
}

func ComparableToServiceEndpoint(se Comparable) *model.VpcServiceEndpoint {
	return (*model.VpcServiceEndpoint)(se.(*ServiceEndpoint))
}
