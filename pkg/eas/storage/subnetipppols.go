/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package storage

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	nsxcommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// SubnetIPPoolsStorage implements REST operations for SubnetIPPools.
// Only Get is supported; List is not.
type SubnetIPPoolsStorage struct {
	nsxClient *nsx.Client
	k8sClient k8sclient.Client
}

// NewSubnetIPPoolsStorage creates a new storage instance.
func NewSubnetIPPoolsStorage(nsxClient *nsx.Client, k8sClient k8sclient.Client) *SubnetIPPoolsStorage {
	return &SubnetIPPoolsStorage{
		nsxClient: nsxClient,
		k8sClient: k8sClient,
	}
}

// Get retrieves SubnetIPPools for the namespaced Subnet CR identified by name.
// The NSX subnet must match both the CR UID and its namespace ownership tag.
func (s *SubnetIPPoolsStorage) Get(ctx context.Context, namespace, name string) (*easv1alpha1.SubnetIPPools, error) {
	subnet, info, err := resolveSubnet(ctx, s.k8sClient, s.nsxClient, namespace, name, "subnetippools")
	if err != nil {
		return nil, err
	}
	if subnet.SubnetDhcpConfig != nil && subnet.SubnetDhcpConfig.Mode != nil && *subnet.SubnetDhcpConfig.Mode == "DHCP_SERVER" {
		return nil, fmt.Errorf("SubnetIPPools %s/%s: subnet DHCP mode is DHCP_SERVER, use SubnetDHCPServerStats instead", namespace, name)
	}
	return s.fetchIPPools(namespace, *subnet.Id, name, info)
}

// fetchIPPools calls NSX for the IP pools of a specific NSX subnet and returns the result
// with metadata.name set to name (the Subnet CR name).
func (s *SubnetIPPoolsStorage) fetchIPPools(namespace, nsxSubnetID, name string, info nsxcommon.VPCResourceInfo) (*easv1alpha1.SubnetIPPools, error) {
	log := logger.Log
	nsxPools, err := s.nsxClient.IPPoolClient.List(info.OrgID, info.ProjectID, info.VPCID, nsxSubnetID,
		nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		return nil, HandleEASError(err, "subnetippools", name, fmt.Errorf("failed to get subnet IP pools from NSX: %w", err))
	}
	log.Debug("Got subnet IP pools", "subnetID", nsxSubnetID, "name", name, "poolCount", len(nsxPools.Results))

	result := &easv1alpha1.SubnetIPPools{
		TypeMeta: metav1.TypeMeta{
			APIVersion: easv1alpha1.GroupVersion.String(),
			Kind:       "SubnetIPPools",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	if len(nsxPools.Results) > 0 {
		p0 := nsxPools.Results[0]
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
			agg.TotalIPs += DerefInt64(pu.TotalIps)
			agg.AvailableIPs += DerefInt64(pu.AvailableIps)
			agg.AllocatedIPAllocations += DerefInt64(pu.AllocatedIpAllocations)
			agg.RequestedIPAllocations += DerefInt64(pu.RequestedIpAllocations)
		}
		if hadUsage {
			result.PoolUsage = &agg
		}
	}

	return result, nil
}

// parseSubnetVPCName parses the spec.vpcName field of a Subnet CR.
//
// The operator sets spec.vpcName via GetVPCFullID:
//   - Non-default NSX project: "projectID:vpcID"
//   - Default NSX project:     ":vpcID"  (empty project prefix)
//   - Infra-scoped (no project): "vpcID" (no colon)
//
// orgID always defaults to "default".
// An empty project prefix (":vpcID") means the NSX default project,
// whose path segment is "default".
func parseSubnetVPCName(vpcName string) (orgID, projectID, vpcID string) {
	orgID = "default"
	if idx := strings.IndexByte(vpcName, ':'); idx >= 0 {
		projectID = vpcName[:idx]
		if projectID == "" {
			projectID = "default" // empty prefix = NSX default project
		}
		vpcID = vpcName[idx+1:]
	} else {
		vpcID = vpcName
	}
	return
}
