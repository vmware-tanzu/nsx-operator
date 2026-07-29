/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

type fakeVPCEndpointStatisticsClient struct {
	result model.VpcEndpointStatistics
	err    error
	calls  [][]string
}

func (f *fakeVPCEndpointStatisticsClient) Get(orgID, projectID, vpcID, endpointID string) (model.VpcEndpointStatistics, error) {
	f.calls = append(f.calls, []string{orgID, projectID, vpcID, endpointID})
	return f.result, f.err
}

func TestVPCEndpointStatisticsStorage_ConvertStatistics(t *testing.T) {
	store := &VPCEndpointStatisticsStorage{}
	empty := store.convertStatistics(nil, "ep1", "ns1")
	assert.Equal(t, "ep1", empty.Name)
	assert.Equal(t, "ns1", empty.Namespace)
	assert.Zero(t, empty.RX.TotalPackets)
	assert.Zero(t, empty.TX.TotalPackets)
	stats := store.convertStatistics(&model.VpcEndpointStatistics{
		Rx: &model.ServiceGatewayCounterCommon{DroppedPackets: int64Ptr(1), TotalBytes: int64Ptr(20), TotalPackets: int64Ptr(3)},
		Tx: &model.ServiceGatewayCounterCommon{DroppedPackets: int64Ptr(4), TotalBytes: int64Ptr(50), TotalPackets: int64Ptr(6)},
	}, "ep1", "ns1")
	assert.Equal(t, int64(1), stats.RX.DroppedPackets)
	assert.Equal(t, int64(20), stats.RX.TotalBytes)
	assert.Equal(t, int64(3), stats.RX.TotalPackets)
	assert.Equal(t, int64(4), stats.TX.DroppedPackets)
	assert.Equal(t, int64(50), stats.TX.TotalBytes)
	assert.Equal(t, int64(6), stats.TX.TotalPackets)
}
