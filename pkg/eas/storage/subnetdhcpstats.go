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

// SubnetDHCPStatsStorage implements REST operations for SubnetDHCPServerStats.
type SubnetDHCPStatsStorage struct {
	nsxClient  *nsx.Client
	vpcService eas.VPCInfoProvider
}

// NewSubnetDHCPStatsStorage creates a new storage instance.
func NewSubnetDHCPStatsStorage(nsxClient *nsx.Client, vpcService eas.VPCInfoProvider) *SubnetDHCPStatsStorage {
	return &SubnetDHCPStatsStorage{
		nsxClient:  nsxClient,
		vpcService: vpcService,
	}
}

// Get retrieves DHCP server config stats for a specific subnet.
func (s *SubnetDHCPStatsStorage) Get(_ context.Context, namespace, subnetID string) (*easv1alpha1.SubnetDHCPServerStats, error) {
	log := logger.Log
	vpcInfos := s.vpcService.ListVPCInfo(namespace)
	if len(vpcInfos) == 0 {
		return nil, fmt.Errorf("no VPC found for namespace %s", namespace)
	}

	info := vpcInfos[0]
	log.Debug("Fetching DHCP stats from NSX", "namespace", namespace, "subnetID", subnetID, "vpcID", info.VPCID)
	nsxStats, err := s.nsxClient.DhcpServerConfigStatsClient.Get(
		info.OrgID, info.ProjectID, info.VPCID, subnetID,
		nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get DHCP server config stats from NSX: %w", err)
	}

	return converter.ConvertDhcpServerStatistics(&nsxStats, subnetID, namespace), nil
}

// List retrieves DHCP stats for all DHCP_SERVER mode subnets in the VPC.
func (s *SubnetDHCPStatsStorage) List(ctx context.Context, namespace string) (*easv1alpha1.SubnetDHCPServerStatsList, error) {
	log := logger.Log
	vpcInfos := s.vpcService.ListVPCInfo(namespace)
	log.Debug("Listing DHCP stats", "namespace", namespace, "vpcCount", len(vpcInfos))

	list := &easv1alpha1.SubnetDHCPServerStatsList{
		TypeMeta: metav1.TypeMeta{APIVersion: easv1alpha1.GroupVersion.String(), Kind: "SubnetDHCPServerStatsList"},
	}
	for _, info := range vpcInfos {
		subnets, err := s.nsxClient.SubnetsClient.List(info.OrgID, info.ProjectID, info.VPCID,
			nil, nil, nil, nil, nil, nil)
		if err != nil {
			log.Warn("Failed to list subnets from NSX", "vpcID", info.VPCID, "error", err)
			continue
		}
		for _, subnet := range subnets.Results {
			if subnet.Id == nil {
				continue
			}
			// Only query DHCP stats for subnets with DHCP_SERVER mode.
			if subnet.SubnetDhcpConfig == nil || subnet.SubnetDhcpConfig.Mode == nil || *subnet.SubnetDhcpConfig.Mode != "DHCP_SERVER" {
				continue
			}
			log.Debug("Found DHCP subnet", "subnetID", *subnet.Id, "vpcID", info.VPCID)
			item, err := s.Get(ctx, namespace, *subnet.Id)
			if err != nil {
				log.Warn("Failed to get DHCP stats for subnet", "subnetID", *subnet.Id, "error", err)
				continue
			}
			list.Items = append(list.Items, *item)
		}
	}
	log.Debug("DHCP stats list complete", "namespace", namespace, "totalItems", len(list.Items))
	return list, nil
}
