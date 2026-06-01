/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package storage

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestConvertDhcpServerStatistics_Nil(t *testing.T) {
	out := ConvertDhcpServerStatistics(nil, "sub1", "ns1")
	require.NotNil(t, out)
	assert.Equal(t, "sub1", out.Name)
	assert.Empty(t, out.IPPoolStats)
}

func TestConvertDhcpServerStatistics_WithPools(t *testing.T) {
	nsx := &model.DhcpServerStatistics{
		IpPoolStats: []model.DhcpIpPoolUsage{
			{
				AllocatedPercentage: int64Ptr(10),
				PoolSize:            int64Ptr(100),
			},
		},
	}
	out := ConvertDhcpServerStatistics(nsx, "sub1", "ns1")
	require.Len(t, out.IPPoolStats, 1)
	assert.Equal(t, int64(10), out.IPPoolStats[0].AllocatedPercentage)
	assert.Equal(t, int64(100), out.IPPoolStats[0].PoolSize)
}
func TestSubnetDHCPStatsStorage_Get_SubnetCRNotFound(t *testing.T) {
	// No Subnet CR in k8s → error about missing CR.
	s := NewSubnetDHCPStatsStorage(&nsx.Client{}, newFakeK8sClient())
	_, err := s.Get(context.Background(), "ns1", "sub1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "subnet CR")
}
func TestSubnetDHCPStatsStorage_Get_EmptyVPCName(t *testing.T) {
	// Subnet CR exists but spec.vpcName is empty → error.
	subnetCR := &vpcv1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "ns1"},
		// VPCName intentionally empty
	}
	s := NewSubnetDHCPStatsStorage(&nsx.Client{}, newFakeK8sClient(subnetCR))
	_, err := s.Get(context.Background(), "ns1", "sub1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty spec.vpcName")
}
func TestSubnetDHCPStatsStorage_Get_SubnetNotFound(t *testing.T) {
	subnetID := "subnet-x"
	subnets := model.VpcSubnetListResult{
		Results: []model.VpcSubnet{{Id: &subnetID}}, // no matching tag
	}
	c := &nsx.Client{}
	c.SubnetsClient = &fakeSubnetsClient{results: subnets}
	subnetCR := &vpcv1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "ns1"},
		Spec:       vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"},
	}
	s := NewSubnetDHCPStatsStorage(c, newFakeK8sClient(subnetCR))
	_, err := s.Get(context.Background(), "ns1", "sub1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
func TestSubnetDHCPStatsStorage_Get_OK(t *testing.T) {
	subnetID := "subnet-1"
	crName := "sub1"
	scope := common.TagScopeSubnetCRName
	dhcpMode := "DHCP_SERVER"
	subnets := model.VpcSubnetListResult{
		Results: []model.VpcSubnet{
			{
				Id:               &subnetID,
				Tags:             []model.Tag{{Scope: &scope, Tag: &crName}},
				SubnetDhcpConfig: &model.SubnetDhcpConfig{Mode: &dhcpMode},
			},
		},
	}
	c := &nsx.Client{}
	c.SubnetsClient = &fakeSubnetsClient{results: subnets}
	c.DhcpServerConfigStatsClient = &fakeDHCPStatsClient{}
	subnetCR := &vpcv1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "ns1"},
		Spec:       vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"},
	}
	s := NewSubnetDHCPStatsStorage(c, newFakeK8sClient(subnetCR))
	result, err := s.Get(context.Background(), "ns1", "sub1")
	require.NoError(t, err)
	assert.Equal(t, "sub1", result.Name)
}
func TestSubnetDHCPStatsStorage_Get_FetchStatsError(t *testing.T) {
	subnetID := "subnet-1"
	crName := "sub1"
	scope := common.TagScopeSubnetCRName
	dhcpMode := "DHCP_SERVER"
	subnets := model.VpcSubnetListResult{
		Results: []model.VpcSubnet{
			{
				Id:               &subnetID,
				Tags:             []model.Tag{{Scope: &scope, Tag: &crName}},
				SubnetDhcpConfig: &model.SubnetDhcpConfig{Mode: &dhcpMode},
			},
		},
	}
	c := &nsx.Client{}
	c.SubnetsClient = &fakeSubnetsClient{results: subnets}
	c.DhcpServerConfigStatsClient = &fakeDHCPStatsClient{err: fmt.Errorf("dhcp error")}
	subnetCR := &vpcv1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "ns1"},
		Spec:       vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"},
	}
	s := NewSubnetDHCPStatsStorage(c, newFakeK8sClient(subnetCR))
	_, err := s.Get(context.Background(), "ns1", "sub1")
	require.Error(t, err)
}
func TestSubnetDHCPStatsStorage_Get_SubnetsError(t *testing.T) {
	c := &nsx.Client{}
	c.SubnetsClient = &fakeSubnetsClient{err: fmt.Errorf("list subnets error")}
	subnetCR := &vpcv1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "ns1"},
		Spec:       vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"},
	}
	s := NewSubnetDHCPStatsStorage(c, newFakeK8sClient(subnetCR))
	_, err := s.Get(context.Background(), "ns1", "sub1")
	require.Error(t, err)
}
func TestSubnetDHCPStatsStorage_Get_WithIDFallback(t *testing.T) {
	// NSX subnet has no nsx-op/subnet_name tag → matched by NSX subnet ID directly.
	subnetID := "dhcp-subnet-id"
	dhcpMode := "DHCP_SERVER"
	subnets := model.VpcSubnetListResult{
		Results: []model.VpcSubnet{
			{
				Id:               &subnetID,
				SubnetDhcpConfig: &model.SubnetDhcpConfig{Mode: &dhcpMode},
				// No tags → falls back to subnet ID as name
			},
		},
	}
	c := &nsx.Client{}
	c.SubnetsClient = &fakeSubnetsClient{results: subnets}
	c.DhcpServerConfigStatsClient = &fakeDHCPStatsClient{}
	subnetCR := &vpcv1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: subnetID, Namespace: "ns1"},
		Spec:       vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"},
	}
	s := NewSubnetDHCPStatsStorage(c, newFakeK8sClient(subnetCR))
	result, err := s.Get(context.Background(), "ns1", subnetID)
	require.NoError(t, err)
	assert.Equal(t, subnetID, result.Name)
}
func TestSubnetDHCPStatsStorage_Get_SubnetNilID(t *testing.T) {
	subnets := model.VpcSubnetListResult{
		Results: []model.VpcSubnet{{Id: nil}},
	}
	c := &nsx.Client{}
	c.SubnetsClient = &fakeSubnetsClient{results: subnets}
	subnetCR := &vpcv1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "anything", Namespace: "ns1"},
		Spec:       vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"},
	}
	s := NewSubnetDHCPStatsStorage(c, newFakeK8sClient(subnetCR))
	_, err := s.Get(context.Background(), "ns1", "anything")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
func TestSubnetDHCPStatsStorage_Get_WrongDHCPMode(t *testing.T) {
	// Subnet is found by name but its DHCP mode is not DHCP_SERVER.
	subnetID := "subnet-1"
	crName := "sub1"
	scope := common.TagScopeSubnetCRName
	dhcpMode := "DHCP_DEACTIVATED"
	subnets := model.VpcSubnetListResult{
		Results: []model.VpcSubnet{
			{
				Id:               &subnetID,
				Tags:             []model.Tag{{Scope: &scope, Tag: &crName}},
				SubnetDhcpConfig: &model.SubnetDhcpConfig{Mode: &dhcpMode},
			},
		},
	}
	c := &nsx.Client{}
	c.SubnetsClient = &fakeSubnetsClient{results: subnets}
	subnetCR := &vpcv1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "ns1"},
		Spec:       vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"},
	}
	s := NewSubnetDHCPStatsStorage(c, newFakeK8sClient(subnetCR))
	_, err := s.Get(context.Background(), "ns1", "sub1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DHCP_DEACTIVATED")
	assert.Contains(t, err.Error(), "not DHCP_SERVER")
}
func TestSubnetDHCPStatsStorage_Get_NilDHCPConfig(t *testing.T) {
	// Subnet is found by name but SubnetDhcpConfig is nil (no DHCP mode set).
	subnetID := "subnet-1"
	crName := "sub1"
	scope := common.TagScopeSubnetCRName
	subnets := model.VpcSubnetListResult{
		Results: []model.VpcSubnet{
			{
				Id:               &subnetID,
				Tags:             []model.Tag{{Scope: &scope, Tag: &crName}},
				SubnetDhcpConfig: nil, // nil config → mode "unknown"
			},
		},
	}
	c := &nsx.Client{}
	c.SubnetsClient = &fakeSubnetsClient{results: subnets}
	subnetCR := &vpcv1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "ns1"},
		Spec:       vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"},
	}
	s := NewSubnetDHCPStatsStorage(c, newFakeK8sClient(subnetCR))
	_, err := s.Get(context.Background(), "ns1", "sub1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown")
	assert.Contains(t, err.Error(), "not DHCP_SERVER")
}
