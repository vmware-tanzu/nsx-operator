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

// IPBlockUsageStorage implements REST operations for IPBlockUsage.
type IPBlockUsageStorage struct {
	nsxClient  *nsx.Client
	vpcService eas.VPCInfoProvider
}

// NewIPBlockUsageStorage creates a new storage instance.
func NewIPBlockUsageStorage(nsxClient *nsx.Client, vpcService eas.VPCInfoProvider) *IPBlockUsageStorage {
	return &IPBlockUsageStorage{
		nsxClient:  nsxClient,
		vpcService: vpcService,
	}
}

// List retrieves IP block usage for the VPC associated with the given namespace.
func (s *IPBlockUsageStorage) List(_ context.Context, namespace string) (*easv1alpha1.IPBlockUsageList, error) {
	log := logger.Log
	vpcInfos := s.vpcService.ListVPCInfo(namespace)
	log.Debug("Listing IP block usage", "namespace", namespace, "vpcCount", len(vpcInfos))

	if len(vpcInfos) == 0 {
		return &easv1alpha1.IPBlockUsageList{
			TypeMeta: metav1.TypeMeta{
				APIVersion: easv1alpha1.GroupVersion.String(),
				Kind:       "IPBlockUsageList",
			},
		}, nil
	}

	list := &easv1alpha1.IPBlockUsageList{
		TypeMeta: metav1.TypeMeta{
			APIVersion: easv1alpha1.GroupVersion.String(),
			Kind:       "IPBlockUsageList",
		},
	}

	for _, info := range vpcInfos {
		log.Debug("Fetching IP block usage from NSX", "vpcID", info.VPCID)
		nsxList, err := s.nsxClient.VpcIpBlockUsageClient.List(info.OrgID, info.ProjectID, info.VPCID, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get IP block usage for VPC %s: %w", info.VPCID, err)
		}
		items := converter.ConvertIpAddressBlockUsageList(&nsxList, namespace)
		log.Debug("Got IP block usage", "vpcID", info.VPCID, "itemCount", len(items))
		list.Items = append(list.Items, items...)
	}

	return list, nil
}
