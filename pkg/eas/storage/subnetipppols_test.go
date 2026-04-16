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

func TestSubnetIPPoolsStorage_Get_SubnetCRNotFound(t *testing.T) {
	// No Subnet CR in k8s → error about missing CR.
	s := NewSubnetIPPoolsStorage(&nsx.Client{}, newFakeK8sClient())
	_, err := s.Get(context.Background(), "ns1", "sub1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "subnet CR")
}
func TestSubnetIPPoolsStorage_Get_EmptyVPCName(t *testing.T) {
	// Subnet CR exists but spec.vpcName is empty → error.
	subnetCR := &vpcv1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "ns1"},
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
		ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "ns1"},
		Spec:       vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"},
	}
	s := NewSubnetIPPoolsStorage(c, newFakeK8sClient(subnetCR))
	_, err := s.Get(context.Background(), "ns1", "sub1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
func TestSubnetIPPoolsStorage_Get_OK(t *testing.T) {
	subnetID := "subnet-1"
	crName := "sub1"
	scope := common.TagScopeSubnetCRName
	subnets := model.VpcSubnetListResult{
		Results: []model.VpcSubnet{
			{Id: &subnetID, Tags: []model.Tag{{Scope: &scope, Tag: &crName}}},
		},
	}
	c := &nsx.Client{}
	c.SubnetsClient = &fakeSubnetsClient{results: subnets}
	c.IPPoolClient = &fakeIPPoolClient{}
	subnetCR := &vpcv1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "ns1"},
		Spec:       vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"},
	}
	s := NewSubnetIPPoolsStorage(c, newFakeK8sClient(subnetCR))
	result, err := s.Get(context.Background(), "ns1", "sub1")
	require.NoError(t, err)
	assert.Equal(t, "sub1", result.Name)
}
func TestSubnetIPPoolsStorage_Get_WithPoolData(t *testing.T) {
	subnetID := "subnet-1"
	crName := "sub1"
	scope := common.TagScopeSubnetCRName
	subnets := model.VpcSubnetListResult{
		Results: []model.VpcSubnet{
			{Id: &subnetID, Tags: []model.Tag{{Scope: &scope, Tag: &crName}}},
		},
	}
	ipType := "IPv4"
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
		},
	}
	c := &nsx.Client{}
	c.SubnetsClient = &fakeSubnetsClient{results: subnets}
	c.IPPoolClient = &fakeIPPoolClient{result: pools}
	subnetCR := &vpcv1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "ns1"},
		Spec:       vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"},
	}
	s := NewSubnetIPPoolsStorage(c, newFakeK8sClient(subnetCR))
	result, err := s.Get(context.Background(), "ns1", "sub1")
	require.NoError(t, err)
	assert.Equal(t, "IPv4", result.IPAddressType)
	require.NotNil(t, result.PoolUsage)
	assert.Equal(t, int64(256), result.PoolUsage.TotalIPs)
	assert.Equal(t, int64(200), result.PoolUsage.AvailableIPs)
	assert.Equal(t, int64(50), result.PoolUsage.AllocatedIPAllocations)
	assert.Equal(t, int64(55), result.PoolUsage.RequestedIPAllocations)
}
func TestSubnetIPPoolsStorage_Get_FetchPoolError(t *testing.T) {
	subnetID := "subnet-1"
	crName := "sub1"
	scope := common.TagScopeSubnetCRName
	subnets := model.VpcSubnetListResult{
		Results: []model.VpcSubnet{
			{Id: &subnetID, Tags: []model.Tag{{Scope: &scope, Tag: &crName}}},
		},
	}
	c := &nsx.Client{}
	c.SubnetsClient = &fakeSubnetsClient{results: subnets}
	c.IPPoolClient = &fakeIPPoolClient{err: fmt.Errorf("pool error")}
	subnetCR := &vpcv1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "ns1"},
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
		ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "ns1"},
		Spec:       vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"},
	}
	s := NewSubnetIPPoolsStorage(c, newFakeK8sClient(subnetCR))
	_, err := s.Get(context.Background(), "ns1", "sub1")
	require.Error(t, err)
}
func TestSubnetIPPoolsStorage_Get_WithIDFallback(t *testing.T) {
	// NSX subnet has no nsx-op/subnet_name tag → matched by NSX subnet ID directly.
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
		ObjectMeta: metav1.ObjectMeta{Name: subnetID, Namespace: "ns1"},
		Spec:       vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"},
	}
	s := NewSubnetIPPoolsStorage(c, newFakeK8sClient(subnetCR))
	result, err := s.Get(context.Background(), "ns1", subnetID)
	require.NoError(t, err)
	assert.Equal(t, subnetID, result.Name)
}
func TestSubnetIPPoolsStorage_Get_DHCPServerMode(t *testing.T) {
	// Subnet is found by name but mode is DHCP_SERVER → use SubnetDHCPServerStats instead.
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
	subnetCR := &vpcv1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "ns1"},
		Spec:       vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"},
	}
	s := NewSubnetIPPoolsStorage(c, newFakeK8sClient(subnetCR))
	_, err := s.Get(context.Background(), "ns1", "sub1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DHCP_SERVER")
	assert.Contains(t, err.Error(), "SubnetDHCPServerStats")
}
