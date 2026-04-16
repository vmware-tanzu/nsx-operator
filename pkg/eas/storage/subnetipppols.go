/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package storage

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas/converter"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

// SubnetIPPoolsStorage implements REST operations for SubnetIPPools.
type SubnetIPPoolsStorage struct {
	nsxClient  *nsx.Client
	vpcService eas.VPCInfoProvider
}

// NewSubnetIPPoolsStorage creates a new storage instance.
func NewSubnetIPPoolsStorage(nsxClient *nsx.Client, vpcService eas.VPCInfoProvider) *SubnetIPPoolsStorage {
	return &SubnetIPPoolsStorage{
		nsxClient:  nsxClient,
		vpcService: vpcService,
	}
}

// Get retrieves IP pools for a specific subnet.
func (s *SubnetIPPoolsStorage) Get(_ context.Context, namespace, subnetID string) (*easv1alpha1.SubnetIPPools, error) {
	log := logger.Log
	vpcInfos := s.vpcService.ListVPCInfo(namespace)
	if len(vpcInfos) == 0 {
		return nil, fmt.Errorf("no VPC found for namespace %s", namespace)
	}

	info := vpcInfos[0]
	log.Debug("Fetching subnet IP pools from NSX", "namespace", namespace, "subnetID", subnetID, "vpcID", info.VPCID)
	nsxPools, err := s.nsxClient.IPPoolClient.List(info.OrgID, info.ProjectID, info.VPCID, subnetID,
		nil, nil, nil, nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get subnet IP pools from NSX: %w", err)
	}
	log.Debug("Got subnet IP pools", "namespace", namespace, "subnetID", subnetID, "poolCount", len(nsxPools.Results))

	result := &easv1alpha1.SubnetIPPools{
		TypeMeta: metav1.TypeMeta{
			APIVersion: easv1alpha1.GroupVersion.String(),
			Kind:       "SubnetIPPools",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      subnetID,
			Namespace: namespace,
		},
	}

	// SubnetIPPools matches eas.nsx.vmware.com_subnetippools: checkOverlapWithExistingPools, ipAddressType, poolUsage.
	if len(nsxPools.Results) > 0 {
		p0 := nsxPools.Results[0]
		if p0.CheckOverlapWithExistingPools != nil {
			result.CheckOverlapWithExistingPools = *p0.CheckOverlapWithExistingPools
		}
		if p0.IpAddressType != nil {
			result.IPAddressType = *p0.IpAddressType
		}
		var agg easv1alpha1.PoolUsage
		hadUsage := false
		for _, pool := range nsxPools.Results {
			if pool.PoolUsage == nil {
				continue
			}
			hadUsage = true
			pu := pool.PoolUsage
			agg.TotalIPs += converter.DerefInt64(pu.TotalIps)
			agg.AvailableIPs += converter.DerefInt64(pu.AvailableIps)
			agg.AllocatedIPAllocations += converter.DerefInt64(pu.AllocatedIpAllocations)
			agg.RequestedIPAllocations += converter.DerefInt64(pu.RequestedIpAllocations)
		}
		if hadUsage {
			result.PoolUsage = &agg
		}
	}

	return result, nil
}

// List retrieves IP pools for all subnets in the VPC associated with the namespace.
func (s *SubnetIPPoolsStorage) List(ctx context.Context, namespace string) (*easv1alpha1.SubnetIPPoolsList, error) {
	log := logger.Log
	vpcInfos := s.vpcService.ListVPCInfo(namespace)
	log.Debug("Listing subnet IP pools", "namespace", namespace, "vpcCount", len(vpcInfos))

	list := &easv1alpha1.SubnetIPPoolsList{
		TypeMeta: metav1.TypeMeta{APIVersion: easv1alpha1.GroupVersion.String(), Kind: "SubnetIPPoolsList"},
	}
	for _, info := range vpcInfos {
		subnets, err := s.nsxClient.SubnetsClient.List(info.OrgID, info.ProjectID, info.VPCID,
			nil, nil, nil, nil, nil, nil)
		if err != nil {
			log.Warn("Failed to list subnets from NSX", "vpcID", info.VPCID, "error", err)
			continue
		}
		log.Debug("Listed subnets for VPC", "vpcID", info.VPCID, "subnetCount", len(subnets.Results))
		for _, subnet := range subnets.Results {
			if subnet.Id == nil {
				continue
			}
			// SubnetIPPools applies to non-DHCP_SERVER subnets (DHCP deactivated/default mode).
			if subnet.SubnetDhcpConfig != nil && subnet.SubnetDhcpConfig.Mode != nil && *subnet.SubnetDhcpConfig.Mode == "DHCP_SERVER" {
				continue
			}
			item, err := s.Get(ctx, namespace, *subnet.Id)
			if err != nil {
				log.Warn("Failed to get IP pools for subnet", "subnetID", *subnet.Id, "error", err)
				continue
			}
			list.Items = append(list.Items, *item)
		}
	}
	log.Debug("Subnet IP pools list complete", "namespace", namespace, "totalItems", len(list.Items))
	return list, nil
}
