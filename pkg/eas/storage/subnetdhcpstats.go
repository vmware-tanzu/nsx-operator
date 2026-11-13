/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package storage

import (
	"context"
	"fmt"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	nsxcommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// SubnetDHCPStatsStorage implements REST operations for SubnetDHCPServerStats.
// Only Get is supported; List is not.
type SubnetDHCPStatsStorage struct {
	nsxClient *nsx.Client
	k8sClient k8sclient.Client
}

// NewSubnetDHCPStatsStorage creates a new storage instance.
func NewSubnetDHCPStatsStorage(nsxClient *nsx.Client, k8sClient k8sclient.Client) *SubnetDHCPStatsStorage {
	return &SubnetDHCPStatsStorage{
		nsxClient: nsxClient,
		k8sClient: k8sClient,
	}
}

// Get retrieves SubnetDHCPServerStats for the namespaced Subnet CR identified by name.
// The NSX subnet must match both the CR UID and its namespace ownership tag.
func (s *SubnetDHCPStatsStorage) Get(ctx context.Context, namespace, name string) (*easv1alpha1.SubnetDHCPServerStats, error) {
	subnet, info, err := resolveSubnet(ctx, s.k8sClient, s.nsxClient, namespace, name, "subnetdhcpserverstats")
	if err != nil {
		return nil, err
	}
	if subnet.SubnetDhcpConfig == nil || subnet.SubnetDhcpConfig.Mode == nil || *subnet.SubnetDhcpConfig.Mode != "DHCP_SERVER" {
		mode := "unknown"
		if subnet.SubnetDhcpConfig != nil && subnet.SubnetDhcpConfig.Mode != nil {
			mode = *subnet.SubnetDhcpConfig.Mode
		}
		return nil, fmt.Errorf("SubnetDHCPServerStats %s/%s: subnet DHCP mode is %s, not DHCP_SERVER", namespace, name, mode)
	}
	return s.fetchStats(namespace, *subnet.Id, name, info)
}

// fetchStats calls NSX for DHCP stats of a specific NSX subnet and returns the result
// with metadata.name set to name (the Subnet CR name).
func (s *SubnetDHCPStatsStorage) fetchStats(namespace, nsxSubnetID, name string, info nsxcommon.VPCResourceInfo) (*easv1alpha1.SubnetDHCPServerStats, error) {
	nsxStats, err := s.nsxClient.DhcpServerConfigStatsClient.Get(
		info.OrgID, info.ProjectID, info.VPCID, nsxSubnetID,
		nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		return nil, HandleEASError(err, "subnetdhcpserverstats", name, fmt.Errorf("failed to get DHCP server config stats from NSX: %w", err))
	}
	return ConvertDhcpServerStatistics(&nsxStats, name, namespace), nil
}

// ConvertDhcpServerStatistics converts NSX DhcpServerStatistics to K8s SubnetDHCPServerStats
// (fields must match eas.nsx.vmware.com_subnetdhcpserverstats).
func ConvertDhcpServerStatistics(nsxStats *model.DhcpServerStatistics, name, namespace string) *easv1alpha1.SubnetDHCPServerStats {
	stats := &easv1alpha1.SubnetDHCPServerStats{
		TypeMeta: metav1.TypeMeta{
			APIVersion: easv1alpha1.GroupVersion.String(),
			Kind:       "SubnetDHCPServerStats",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	if nsxStats == nil {
		return stats
	}
	for _, p := range nsxStats.IpPoolStats {
		stats.IPPoolStats = append(stats.IPPoolStats, easv1alpha1.DHCPIPPoolUsage{
			AllocatedNumber:     DerefInt64(p.AllocatedNumber),
			AllocatedPercentage: DerefInt64(p.AllocatedPercentage),
			ConsumedNumber:      DerefInt64(p.ConsumedNumber),
			PoolSize:            DerefInt64(p.PoolSize),
		})
	}
	return stats
}
