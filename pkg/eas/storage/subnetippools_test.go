/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	nsxcommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestSubnetIPPoolsStorage_Get_SubnetCRNotFound(t *testing.T) {
	// No Subnet CR in k8s → error about missing CR.
	s := NewSubnetIPPoolsStorage(&nsx.Client{}, newFakeK8sClient())
	_, err := s.Get(context.Background(), "ns1", "sub1")
	require.Error(t, err)
	assert.True(t, k8serrors.IsNotFound(err))
}
func TestSubnetIPPoolsStorage_Get_EmptyVPCName(t *testing.T) {
	// Subnet CR exists but spec.vpcName is empty → error.
	subnetCR := &vpcv1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "ns1", UID: "sub1-uid"},
		// VPCName intentionally empty
	}
	s := NewSubnetIPPoolsStorage(&nsx.Client{}, newFakeK8sClient(subnetCR))
	_, err := s.Get(context.Background(), "ns1", "sub1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty spec.vpcName")
}
func TestSubnetIPPoolsStorage_Get_SubnetNotFound(t *testing.T) {
	subnetID := "subnet-x"
	subnets := model.VpcSubnetListResult{
		Results: []model.VpcSubnet{{Id: &subnetID}},
	}
	c := &nsx.Client{}
	c.SubnetsClient = &fakeSubnetsClient{results: subnets}
	subnetCR := &vpcv1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "ns1", UID: "sub1-uid"},
		Spec:       vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"},
	}
	s := NewSubnetIPPoolsStorage(c, newFakeK8sClient(subnetCR))
	_, err := s.Get(context.Background(), "ns1", "sub1")
	require.Error(t, err)
	assert.True(t, k8serrors.IsNotFound(err))
}
func TestSubnetIPPoolsStorage_Get_OK(t *testing.T) {
	subnetID := "subnet-1"
	crName := "sub1"
	subnets := model.VpcSubnetListResult{
		Results: []model.VpcSubnet{
			{
				Id:               &subnetID,
				Tags:             subnetOwnershipTags(crName, "sub1-uid", "ns1"),
				SubnetDhcpConfig: &model.SubnetDhcpConfig{Mode: ptr.To("DHCP_DEACTIVATED")},
			},
		},
	}
	c := &nsx.Client{}
	c.SubnetsClient = &fakeSubnetsClient{results: subnets}
	c.IPPoolClient = &fakeIPPoolClient{
		result: model.IpAddressPoolListResult{
			Results: []model.IpAddressPool{{}},
		},
	}
	subnetCR := &vpcv1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "ns1", UID: "sub1-uid"},
		Spec:       vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"},
	}
	s := NewSubnetIPPoolsStorage(c, newFakeK8sClient(subnetCR))
	result, err := s.Get(context.Background(), "ns1", "sub1")
	require.NoError(t, err)
	assert.Equal(t, 1, len(result.Items))
	assert.Equal(t, "sub1", result.Items[0].Name)
}
func TestSubnetIPPoolsStorage_Get_WithPoolData(t *testing.T) {
	subnetID := "subnet-1"
	crName := "sub1"
	subnets := model.VpcSubnetListResult{
		Results: []model.VpcSubnet{
			{Id: &subnetID, Tags: subnetOwnershipTags(crName, "sub1-uid", "ns1")},
		},
	}
	ipType := "IPV4"
	ipv6Type := "IPV6"
	total := int64(256)
	avail := int64(200)
	alloc := int64(50)
	req := int64(55)
	pools := model.IpAddressPoolListResult{
		Results: []model.IpAddressPool{
			{
				IpAddressType: &ipType,
				PoolUsage: &model.PolicyPoolUsage{
					TotalIps:               &total,
					AvailableIps:           &avail,
					AllocatedIpAllocations: &alloc,
					RequestedIpAllocations: &req,
				},
			},
			{IpAddressType: &ipv6Type},
		},
	}
	c := &nsx.Client{}
	c.SubnetsClient = &fakeSubnetsClient{results: subnets}
	c.IPPoolClient = &fakeIPPoolClient{result: pools}
	subnetCR := &vpcv1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "ns1", UID: "sub1-uid"},
		Spec:       vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"},
	}
	s := NewSubnetIPPoolsStorage(c, newFakeK8sClient(subnetCR))
	result, err := s.Get(context.Background(), "ns1", "sub1")
	require.NoError(t, err)
	require.Equal(t, 2, len(result.Items))
	assert.Equal(t, "IPv6", result.Items[1].IPAddressType)
	item := result.Items[0]
	assert.Equal(t, "IPv4", item.IPAddressType)
	require.NotNil(t, item.PoolUsage)
	assert.Equal(t, int64(256), item.PoolUsage.TotalIPs)
	assert.Equal(t, int64(200), item.PoolUsage.AvailableIPs)
	assert.Equal(t, int64(50), item.PoolUsage.AllocatedIPAllocations)
	assert.Equal(t, int64(55), item.PoolUsage.RequestedIPAllocations)
}
func TestSubnetIPPoolsStorage_ReservedIPs(t *testing.T) {
	for _, tc := range []struct {
		name     string
		ipType   string
		reserved *string
	}{
		{name: "omitted", ipType: "IPV4"},
		{name: "zero", ipType: "IPV4", reserved: ptr.To("0")},
		{name: "IPv4", ipType: "IPV4", reserved: ptr.To("16")},
		{name: "IPv6 exceeds int64", ipType: "IPV6", reserved: ptr.To("18446744073709551616")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &nsx.Client{IPPoolClient: &fakeIPPoolClient{result: model.IpAddressPoolListResult{
				Results: []model.IpAddressPool{{
					IpAddressType: &tc.ipType,
					PoolUsage:     &model.PolicyPoolUsage{ReservedIps: tc.reserved},
				}},
			}}}
			s := NewSubnetIPPoolsStorage(c, nil)
			result, err := s.fetchIPPools("ns1", "subnet-1", "sub1", nsxcommon.VPCResourceInfo{
				OrgID: "default", ProjectID: "p1", VPCID: "vpc1",
			})
			require.NoError(t, err)
			require.Len(t, result.Items, 1)
			require.NotNil(t, result.Items[0].PoolUsage)
			encoded, err := json.Marshal(result.Items[0].PoolUsage)
			require.NoError(t, err)
			var usage map[string]interface{}
			require.NoError(t, json.Unmarshal(encoded, &usage))
			if tc.reserved == nil {
				assert.Empty(t, result.Items[0].PoolUsage.ReservedIPs)
				assert.NotContains(t, usage, "reservedIPs")
			} else {
				assert.Equal(t, *tc.reserved, result.Items[0].PoolUsage.ReservedIPs)
				assert.Equal(t, *tc.reserved, usage["reservedIPs"])
			}
		})
	}
}

func TestSubnetIPPoolsStorage_Get_FetchPoolError(t *testing.T) {
	subnetID := "subnet-1"
	crName := "sub1"
	subnets := model.VpcSubnetListResult{
		Results: []model.VpcSubnet{
			{Id: &subnetID, Tags: subnetOwnershipTags(crName, "sub1-uid", "ns1")},
		},
	}
	c := &nsx.Client{}
	c.SubnetsClient = &fakeSubnetsClient{results: subnets}
	c.IPPoolClient = &fakeIPPoolClient{err: fmt.Errorf("pool error")}
	subnetCR := &vpcv1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "ns1", UID: "sub1-uid"},
		Spec:       vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"},
	}
	s := NewSubnetIPPoolsStorage(c, newFakeK8sClient(subnetCR))
	_, err := s.Get(context.Background(), "ns1", "sub1")
	require.Error(t, err)
}
func TestSubnetIPPoolsStorage_Get_SubnetsError(t *testing.T) {
	c := &nsx.Client{}
	c.SubnetsClient = &fakeSubnetsClient{err: fmt.Errorf("list subnets error")}
	subnetCR := &vpcv1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "ns1", UID: "sub1-uid"},
		Spec:       vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"},
	}
	s := NewSubnetIPPoolsStorage(c, newFakeK8sClient(subnetCR))
	_, err := s.Get(context.Background(), "ns1", "sub1")
	require.Error(t, err)
}
func TestSubnetIPPoolsStorage_Get_RejectsIDFallback(t *testing.T) {
	// An NSX ID equal to the CR name must not bypass ownership checks.
	subnetID := "pool-subnet-id"
	subnets := model.VpcSubnetListResult{
		Results: []model.VpcSubnet{
			{Id: &subnetID}, // No tags, no DHCP mode → non-DHCP_SERVER
		},
	}
	c := &nsx.Client{}
	c.SubnetsClient = &fakeSubnetsClient{results: subnets}
	c.IPPoolClient = &fakeIPPoolClient{}
	subnetCR := &vpcv1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: subnetID, Namespace: "ns1", UID: "sub1-uid"},
		Spec:       vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"},
	}
	s := NewSubnetIPPoolsStorage(c, newFakeK8sClient(subnetCR))
	_, err := s.Get(context.Background(), "ns1", subnetID)
	require.Error(t, err)
	assert.True(t, k8serrors.IsNotFound(err))
}
func TestSubnetIPPoolsStorage_Get_DHCPServerMode(t *testing.T) {
	for _, tc := range []struct {
		name     string
		advanced *model.SubnetAdvancedConfig
	}{
		{name: "missing advanced config"},
		{name: "missing static allocation config", advanced: &model.SubnetAdvancedConfig{}},
		{
			name: "unset static allocation enabled",
			advanced: &model.SubnetAdvancedConfig{
				StaticIpAllocation: &model.StaticIpAllocation{},
			},
		},
		{
			name: "disabled static allocation",
			advanced: &model.SubnetAdvancedConfig{
				StaticIpAllocation: &model.StaticIpAllocation{Enabled: ptr.To(false)},
			},
		},
		{
			name: "pool ranges without enabled static allocation",
			advanced: &model.SubnetAdvancedConfig{
				StaticIpAllocation: &model.StaticIpAllocation{PoolRanges: []string{"172.26.2.4-172.26.2.6"}},
			},
		},
		{
			name: "pool ranges with disabled static allocation",
			advanced: &model.SubnetAdvancedConfig{
				StaticIpAllocation: &model.StaticIpAllocation{
					Enabled: ptr.To(false), PoolRanges: []string{"172.26.2.4-172.26.2.6"},
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pools := &fakeIPPoolClient{result: model.IpAddressPoolListResult{
				Results: []model.IpAddressPool{{IpAddressType: ptr.To("IPV4")}},
			}}
			c := &nsx.Client{
				SubnetsClient: &fakeSubnetsClient{results: model.VpcSubnetListResult{
					Results: []model.VpcSubnet{{
						Id:               ptr.To("subnet-1"),
						Tags:             subnetOwnershipTags("sub1", "sub1-uid", "ns1"),
						SubnetDhcpConfig: &model.SubnetDhcpConfig{Mode: ptr.To("DHCP_SERVER")},
						AdvancedConfig:   tc.advanced,
					}},
				}},
				IPPoolClient: pools,
			}
			subnetCR := &vpcv1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "ns1", UID: "sub1-uid"},
				Spec:       vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"},
			}

			result, err := NewSubnetIPPoolsStorage(c, newFakeK8sClient(subnetCR)).Get(context.Background(), "ns1", "sub1")

			require.EqualError(t, err, "SubnetIPPools ns1/sub1: subnet DHCP mode is DHCP_SERVER, use SubnetDHCPServerStats instead")
			assert.Nil(t, result)
			assert.Empty(t, pools.subnetIDs, "DHCP-only Subnets must be rejected before querying IP pools")
		})
	}
}

func TestSubnetIPPoolsStorage_Get_MixedSubnet(t *testing.T) {
	for _, tc := range []struct {
		name       string
		poolRanges []string
	}{
		{name: "explicit static pool ranges", poolRanges: []string{"172.26.2.4-172.26.2.6", "172.26.2.10"}},
		{name: "default static pool ranges"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pools := &fakeIPPoolClient{result: model.IpAddressPoolListResult{
				Results: []model.IpAddressPool{{
					IpAddressType: ptr.To("IPV4"),
					PoolUsage: &model.PolicyPoolUsage{
						TotalIps:               ptr.To(int64(4)),
						AvailableIps:           ptr.To(int64(2)),
						AllocatedIpAllocations: ptr.To(int64(1)),
						RequestedIpAllocations: ptr.To(int64(1)),
						ReservedIps:            ptr.To("1"),
					},
				}},
			}}
			c := &nsx.Client{
				SubnetsClient: &fakeSubnetsClient{results: model.VpcSubnetListResult{
					Results: []model.VpcSubnet{{
						Id:               ptr.To("mixed-subnet-nsx-id"),
						Tags:             subnetOwnershipTags("mixed-subnet", "mixed-uid", "ns1"),
						SubnetDhcpConfig: &model.SubnetDhcpConfig{Mode: ptr.To("DHCP_SERVER")},
						AdvancedConfig: &model.SubnetAdvancedConfig{
							StaticIpAllocation: &model.StaticIpAllocation{Enabled: ptr.To(true), PoolRanges: tc.poolRanges},
						},
					}},
				}},
				IPPoolClient: pools,
			}
			subnetCR := &vpcv1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{Name: "mixed-subnet", Namespace: "ns1", UID: "mixed-uid"},
				Spec:       vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"},
			}

			result, err := NewSubnetIPPoolsStorage(c, newFakeK8sClient(subnetCR)).Get(context.Background(), "ns1", "mixed-subnet")

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, "SubnetIPPoolsList", result.Kind)
			assert.Equal(t, "eas.nsx.vmware.com/v1alpha1", result.APIVersion)
			require.Len(t, result.Items, 1)
			item := result.Items[0]
			assert.Equal(t, "SubnetIPPools", item.Kind)
			assert.Equal(t, result.APIVersion, item.APIVersion)
			assert.Equal(t, "mixed-subnet", item.Name)
			assert.Equal(t, "ns1", item.Namespace)
			assert.Equal(t, "IPv4", item.IPAddressType)
			require.NotNil(t, item.PoolUsage)
			assert.Equal(t, int64(4), item.PoolUsage.TotalIPs)
			assert.Equal(t, int64(2), item.PoolUsage.AvailableIPs)
			assert.Equal(t, int64(1), item.PoolUsage.AllocatedIPAllocations)
			assert.Equal(t, int64(1), item.PoolUsage.RequestedIPAllocations)
			assert.Equal(t, "1", item.PoolUsage.ReservedIPs)
			assert.Equal(t, []string{"mixed-subnet-nsx-id"}, pools.subnetIDs)
		})
	}
}
