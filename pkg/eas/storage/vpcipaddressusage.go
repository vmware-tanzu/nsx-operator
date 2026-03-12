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

// VPCIPAddressUsageStorage implements REST operations for VPCIPAddressUsage.
type VPCIPAddressUsageStorage struct {
	nsxClient  *nsx.Client
	vpcService eas.VPCInfoProvider
}

// NewVPCIPAddressUsageStorage creates a new storage instance.
func NewVPCIPAddressUsageStorage(nsxClient *nsx.Client, vpcService eas.VPCInfoProvider) *VPCIPAddressUsageStorage {
	return &VPCIPAddressUsageStorage{
		nsxClient:  nsxClient,
		vpcService: vpcService,
	}
}

// Get retrieves IP address usage for the VPC associated with the given namespace.
func (s *VPCIPAddressUsageStorage) Get(_ context.Context, namespace, _ string) (*easv1alpha1.VPCIPAddressUsage, error) {
	log := logger.Log
	vpcInfos := s.vpcService.ListVPCInfo(namespace)
	if len(vpcInfos) == 0 {
		return nil, fmt.Errorf("no VPC found for namespace %s", namespace)
	}

	info := vpcInfos[0]
	log.Debug("Fetching VPC IP address usage from NSX", "namespace", namespace, "vpcID", info.VPCID)
	nsxBlocks, err := s.nsxClient.IpAddressUsageClient.Get(info.OrgID, info.ProjectID, info.VPCID)
	if err != nil {
		return nil, fmt.Errorf("failed to get VPC IP address usage from NSX: %w", err)
	}

	return converter.ConvertVpcIpAddressBlocks(&nsxBlocks, info.VPCID, namespace), nil
}

// List retrieves IP address usage for all VPCs associated with the given namespace.
func (s *VPCIPAddressUsageStorage) List(_ context.Context, namespace string) (*easv1alpha1.VPCIPAddressUsageList, error) {
	log := logger.Log
	vpcInfos := s.vpcService.ListVPCInfo(namespace)
	log.Debug("Listing VPC IP address usage", "namespace", namespace, "vpcCount", len(vpcInfos))

	list := &easv1alpha1.VPCIPAddressUsageList{
		TypeMeta: metav1.TypeMeta{
			APIVersion: easv1alpha1.GroupVersion.String(),
			Kind:       "VPCIPAddressUsageList",
		},
	}

	for _, info := range vpcInfos {
		nsxBlocks, err := s.nsxClient.IpAddressUsageClient.Get(info.OrgID, info.ProjectID, info.VPCID)
		if err != nil {
			return nil, fmt.Errorf("failed to get VPC IP address usage for VPC %s: %w", info.VPCID, err)
		}
		usage := converter.ConvertVpcIpAddressBlocks(&nsxBlocks, info.VPCID, namespace)
		list.Items = append(list.Items, *usage)
	}

	return list, nil
}
