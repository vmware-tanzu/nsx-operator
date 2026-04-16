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
	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
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

// Get retrieves DHCP server config stats for the DHCP_SERVER subnet identified by name.
// name must be the K8s Subnet CR name.  The Subnet CR's spec.vpcName is read to
// determine the NSX VPC (format: "projectID:vpcID" for project-scoped, "vpcID" for
// infra-scoped).  NSX subnets in that VPC are listed and the one whose
// nsx-op/subnet_name tag (or ID as fallback) matches name is selected.
// The returned object always has metadata.name equal to the input name.
func (s *SubnetDHCPStatsStorage) Get(ctx context.Context, namespace, name string) (*easv1alpha1.SubnetDHCPServerStats, error) {
	log := logger.Log

	// Resolve VPC info from the Subnet CR's spec.vpcName.
	subnetCR := &vpcv1alpha1.Subnet{}
	if err := s.k8sClient.Get(ctx, k8sclient.ObjectKey{Namespace: namespace, Name: name}, subnetCR); err != nil {
		return nil, fmt.Errorf("subnet CR %s/%s not found: %w", namespace, name, err)
	}
	if subnetCR.Spec.VPCName == "" {
		return nil, fmt.Errorf("subnet CR %s/%s has empty spec.vpcName", namespace, name)
	}

	orgID, projectID, vpcID := parseSubnetVPCName(subnetCR.Spec.VPCName)
	log.Debug("Fetching DHCP stats by name", "namespace", namespace, "name", name,
		"projectID", projectID, "vpcID", vpcID)

	subnets, err := s.nsxClient.SubnetsClient.List(orgID, projectID, vpcID,
		nil, nil, nil, nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list subnets from NSX: %w", err)
	}

	for _, subnet := range subnets.Results {
		if subnet.Id == nil {
			continue
		}
		crName := nsxTagValue(subnet.Tags, nsxcommon.TagScopeSubnetCRName)
		if crName == "" {
			crName = *subnet.Id
		}
		if crName != name {
			continue
		}
		// Subnet matched by name — check DHCP mode.
		if subnet.SubnetDhcpConfig == nil || subnet.SubnetDhcpConfig.Mode == nil || *subnet.SubnetDhcpConfig.Mode != "DHCP_SERVER" {
			mode := "unknown"
			if subnet.SubnetDhcpConfig != nil && subnet.SubnetDhcpConfig.Mode != nil {
				mode = *subnet.SubnetDhcpConfig.Mode
			}
			return nil, fmt.Errorf("SubnetDHCPServerStats %s/%s: subnet DHCP mode is %s, not DHCP_SERVER", namespace, name, mode)
		}
		info := nsxcommon.VPCResourceInfo{OrgID: orgID, ProjectID: projectID, VPCID: vpcID}
		return s.fetchStats(namespace, *subnet.Id, name, info)
	}

	return nil, fmt.Errorf("SubnetDHCPServerStats %s/%s not found", namespace, name)
}

// fetchStats calls NSX for DHCP stats of a specific NSX subnet and returns the result
// with metadata.name set to name (the Subnet CR name).
func (s *SubnetDHCPStatsStorage) fetchStats(namespace, nsxSubnetID, name string, info nsxcommon.VPCResourceInfo) (*easv1alpha1.SubnetDHCPServerStats, error) {
	nsxStats, err := s.nsxClient.DhcpServerConfigStatsClient.Get(
		info.OrgID, info.ProjectID, info.VPCID, nsxSubnetID,
		nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get DHCP server config stats from NSX: %w", err)
	}
	return ConvertDhcpServerStatistics(&nsxStats, name, namespace), nil
}

// nsxTagValue returns the tag value for the given scope from an NSX tags slice.
func nsxTagValue(tags []model.Tag, scope string) string {
	for _, t := range tags {
		if t.Scope != nil && *t.Scope == scope && t.Tag != nil {
			return *t.Tag
		}
	}
	return ""
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
