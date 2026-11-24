/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package storage

import (
	"context"
	"fmt"
	"slices"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

const subnetDHCPServerLeasesResource = "subnetdhcpserverleases"

// SubnetDHCPServerLeasesStorage reads DHCP leases for a namespaced Subnet CR.
// Only Get is supported.
type SubnetDHCPServerLeasesStorage struct {
	nsxClient *nsx.Client
	k8sClient k8sclient.Client
}

func NewSubnetDHCPServerLeasesStorage(nsxClient *nsx.Client, k8sClient k8sclient.Client) *SubnetDHCPServerLeasesStorage {
	return &SubnetDHCPServerLeasesStorage{nsxClient: nsxClient, k8sClient: k8sClient}
}

// Get resolves the Subnet CR by name and ownership tags, then collects all IPv4
// and IPv6 lease pages for its corresponding NSX subnet.
func (s *SubnetDHCPServerLeasesStorage) Get(ctx context.Context, namespace, name string) (*easv1alpha1.SubnetDHCPServerLeases, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	subnet, info, err := resolveSubnet(ctx, s.k8sClient, s.nsxClient, namespace, name, subnetDHCPServerLeasesResource)
	if err != nil {
		return nil, err
	}
	// Let NSX handle DHCP mode validation, including DHCPv6 modes inherited
	// from a service profile when the subnet has no explicit IPv4 config.
	leases := &model.DhcpLeasesResult{}
	var cursor *string
	seenCursors := make(map[string]struct{})
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, err := s.nsxClient.SubnetDHCPServerLeasesClient.List(
			info.OrgID, info.ProjectID, info.VPCID, *subnet.Id,
			nil, cursor, nil, nil, nil, nil, nil, nil)
		if err != nil {
			return nil, HandleEASError(err, subnetDHCPServerLeasesResource, name, fmt.Errorf("failed to get DHCP server leases from NSX: %w", err))
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		leases.Leases = append(leases.Leases, page.Leases...)
		leases.Ipv6Leases = append(leases.Ipv6Leases, page.Ipv6Leases...)
		if page.Cursor == nil || *page.Cursor == "" {
			break
		}
		if _, seen := seenCursors[*page.Cursor]; seen {
			return nil, fmt.Errorf("repeated NSX DHCP server lease cursor for Subnet CR %s/%s", namespace, name)
		}
		seenCursors[*page.Cursor] = struct{}{}
		cursor = page.Cursor
	}
	return ConvertDhcpServerLeases(leases, name, namespace), nil
}

// ConvertDhcpServerLeases maps lease fields without changing the address,
// duration, identity association type or timestamp formats supplied by NSX.
func ConvertDhcpServerLeases(leases *model.DhcpLeasesResult, name, namespace string) *easv1alpha1.SubnetDHCPServerLeases {
	result := &easv1alpha1.SubnetDHCPServerLeases{
		TypeMeta:   metav1.TypeMeta{APIVersion: easv1alpha1.GroupVersion.String(), Kind: "SubnetDHCPServerLeases"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
	if leases == nil {
		return result
	}
	for _, lease := range leases.Leases {
		result.Leases = append(result.Leases, easv1alpha1.DHCPLease{
			MACAddress: DerefString(lease.MacAddress),
			IPAddress:  DerefString(lease.IpAddress),
			Subnet:     DerefString(lease.Subnet),
			LeaseTime:  DerefString(lease.LeaseTime),
			StartTime:  DerefString(lease.StartTime),
			ExpireTime: DerefString(lease.ExpireTime),
		})
	}
	for _, lease := range leases.Ipv6Leases {
		result.IPv6Leases = append(result.IPv6Leases, easv1alpha1.DHCPv6Lease{
			DUID:        DerefString(lease.Duid),
			IAType:      DerefString(lease.IaType),
			IAID:        DerefInt64(lease.Iaid),
			IPAddresses: slices.Clone(lease.IpAddresses),
			LeaseTime:   DerefString(lease.LeaseTime),
			StartTime:   DerefString(lease.StartTime),
			ExpireTime:  DerefString(lease.ExpireTime),
		})
	}
	return result
}
