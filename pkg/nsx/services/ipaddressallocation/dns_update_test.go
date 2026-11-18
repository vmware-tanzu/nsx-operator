/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package ipaddressallocation

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/mock/gomock"

	mocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/ipaddressallocation"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestPrepareExistingAllocationDNSUpdate(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		existing, desired, wantPatch *string
		enabled                      bool
	}{
		{name: "add", desired: String("new.example"), wantPatch: String("new.example"), enabled: true},
		{name: "change", existing: String("old.example"), desired: String("new.example"), wantPatch: String("new.example"), enabled: true},
		{name: "remove", existing: String("old.example"), wantPatch: String(""), enabled: true},
		{name: "empty existing hostname", existing: String(""), enabled: true},
		{name: "disabled feature", existing: String("old.example")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			existing := &model.VpcIpAddressAllocation{AllocationIps: String("192.0.2.4"), AllocationIp: String("192.0.2.4"), DnsHostname: tc.existing}
			desired := &model.VpcIpAddressAllocation{DnsHostname: tc.desired}
			prepareExistingAllocationUpdate(desired, existing, tc.enabled)
			require.Equal(t, existing.AllocationIps, desired.AllocationIps)
			require.Equal(t, existing.AllocationIp, desired.AllocationIp)
			require.Equal(t, tc.wantPatch, desired.DnsHostname)
		})
	}
}

func TestIPAddressAllocationPatchErrorIsIgnoredWhenGetSucceeds(t *testing.T) {
	controller := gomock.NewController(t)
	client := mocks.NewMockIPAddressAllocationClient(controller)
	service := &IPAddressAllocationService{Service: common.Service{NSXClient: &nsx.Client{IPAddressAllocationClient: client}}, ipAddressAllocationStore: buildIPAddressAllocationStore()}
	allocation := &model.VpcIpAddressAllocation{Id: String("allocation")}
	patchErr := errors.New("patch failed")
	client.EXPECT().Patch("o", "p", "v", "allocation", *allocation).Return(patchErr)
	client.EXPECT().Get("o", "p", "v", "allocation").Return(model.VpcIpAddressAllocation{Id: String("allocation"), AllocationIps: String("192.0.2.4")}, nil)
	err := service.Apply(allocation, []common.VPCResourceInfo{{OrgID: "o", ProjectID: "p", VPCID: "v"}})
	require.NoError(t, err)
}

func TestIPAddressAllocationRejectsUnappliedDNSHostname(t *testing.T) {
	controller := gomock.NewController(t)
	client := mocks.NewMockIPAddressAllocationClient(controller)
	service := &IPAddressAllocationService{Service: common.Service{NSXClient: &nsx.Client{IPAddressAllocationClient: client}}, ipAddressAllocationStore: buildIPAddressAllocationStore()}
	allocation := &model.VpcIpAddressAllocation{Id: String("allocation"), DnsHostname: String("requested")}
	client.EXPECT().Patch("o", "p", "v", "allocation", *allocation).Return(errors.New("patch failed"))
	client.EXPECT().Get("o", "p", "v", "allocation").Return(model.VpcIpAddressAllocation{Id: String("allocation"), AllocationIps: String("192.0.2.4"), DnsHostname: String("old")}, nil)
	err := service.Apply(allocation, []common.VPCResourceInfo{{OrgID: "o", ProjectID: "p", VPCID: "v"}})
	require.ErrorContains(t, err, "DNS hostname does not match")
}
