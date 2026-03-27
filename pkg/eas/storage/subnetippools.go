/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
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

	if len(nsxPools.Results) > 0 {
		pool := nsxPools.Results[0]
		result.Spec = easv1alpha1.SubnetIPPoolSpec{
			DisplayName: converter.DerefString(pool.DisplayName),
			ID:          converter.DerefString(pool.Id),
			Path:        converter.DerefString(pool.Path),
		}
		if pool.PoolUsage != nil {
			result.Spec.PoolUsage = &easv1alpha1.PoolUsage{
				TotalIps:               converter.DerefInt64(pool.PoolUsage.TotalIps),
				AvailableIps:           converter.DerefInt64(pool.PoolUsage.AvailableIps),
				AllocatedIpAllocations: converter.DerefInt64(pool.PoolUsage.AllocatedIpAllocations),
				RequestedIpAllocations: converter.DerefInt64(pool.PoolUsage.RequestedIpAllocations),
			}
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
