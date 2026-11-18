/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package storage

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

const subnetStatusResource = "subnetstatuses"

// SubnetStatusStorage reads realized status for namespaced Subnet CRs.
type SubnetStatusStorage struct {
	nsxClient *nsx.Client
	k8sClient k8sclient.Client
}

func NewSubnetStatusStorage(nsxClient *nsx.Client, k8sClient k8sclient.Client) *SubnetStatusStorage {
	return &SubnetStatusStorage{nsxClient: nsxClient, k8sClient: k8sClient}
}

// Get retrieves status by Subnet CR name. UID and namespace tags identify the
// corresponding NSX subnet even when namespaces share a VPC and CR names.
func (s *SubnetStatusStorage) Get(ctx context.Context, namespace, name string) (*easv1alpha1.SubnetStatus, error) {
	subnet, info, err := resolveSubnet(ctx, s.k8sClient, s.nsxClient, namespace, name, subnetStatusResource)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	status, err := s.nsxClient.SubnetStatusClient.List(info.OrgID, info.ProjectID, info.VPCID, *subnet.Id)
	if err != nil {
		return nil, HandleEASError(err, subnetStatusResource, name, fmt.Errorf("failed to get subnet status from NSX: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return ConvertVpcSubnetStatus(status.Results, name, namespace), nil
}

// List returns gateway summaries for the namespace's realized Subnet CRs.
// A CR without a VPC or a corresponding NSX resource is not realized yet.
func (s *SubnetStatusStorage) List(ctx context.Context, namespace string) (*easv1alpha1.SubnetStatusList, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	subnets := &vpcv1alpha1.SubnetList{}
	if err := s.k8sClient.List(ctx, subnets, k8sclient.InNamespace(namespace)); err != nil {
		return nil, HandleEASError(err, subnetStatusResource, "", fmt.Errorf("failed to list Subnet CRs in namespace %s: %w", namespace, err))
	}
	list := &easv1alpha1.SubnetStatusList{
		TypeMeta: metav1.TypeMeta{APIVersion: easv1alpha1.GroupVersion.String(), Kind: "SubnetStatusList"},
		Items:    make([]easv1alpha1.SubnetStatus, 0, len(subnets.Items)),
	}
	for _, subnet := range subnets.Items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if subnet.Spec.VPCName == "" || subnet.UID == "" {
			continue
		}
		status, err := s.Get(ctx, namespace, subnet.Name)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				continue
			}
			return nil, err
		}
		list.Items = append(list.Items, easv1alpha1.SubnetStatus{
			TypeMeta:         status.TypeMeta,
			ObjectMeta:       status.ObjectMeta,
			GatewayAddresses: status.GatewayAddresses,
		})
	}
	return list, nil
}

// ConvertVpcSubnetStatus aggregates all address-family status records into one
// response, preserving optional fields and the Subnet CR's name and namespace.
func ConvertVpcSubnetStatus(statuses []model.VpcSubnetStatus, name, namespace string) *easv1alpha1.SubnetStatus {
	result := &easv1alpha1.SubnetStatus{
		TypeMeta:   metav1.TypeMeta{APIVersion: easv1alpha1.GroupVersion.String(), Kind: "SubnetStatus"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
	for _, status := range statuses {
		result.DHCPRanges = appendSubnetStatusValue(result.DHCPRanges, status.DhcpRanges)
		result.DHCPReservedRanges = appendSubnetStatusValue(result.DHCPReservedRanges, status.DhcpReservedRanges)
		result.GatewayAddresses = appendSubnetStatusValue(result.GatewayAddresses, status.GatewayAddress)
		result.StaticIPPoolRanges = appendSubnetStatusValue(result.StaticIPPoolRanges, status.StaticIpPoolRange)
		result.UnmanagedIPs = appendSubnetStatusValue(result.UnmanagedIPs, status.UnmanagedIps)
		switch strings.ToUpper(DerefString(status.IpAddressType)) {
		case model.VpcSubnetStatus_IP_ADDRESS_TYPE_IPV4:
			result.IPAddressTypes = appendSubnetStatusIPType(result.IPAddressTypes, easv1alpha1.IPv4)
		case model.VpcSubnetStatus_IP_ADDRESS_TYPE_IPV6:
			result.IPAddressTypes = appendSubnetStatusIPType(result.IPAddressTypes, easv1alpha1.IPv6)
		case model.VpcSubnetStatus_IP_ADDRESS_TYPE_IPV4_IPV6:
			result.IPAddressTypes = appendSubnetStatusIPType(result.IPAddressTypes, easv1alpha1.IPv4)
			result.IPAddressTypes = appendSubnetStatusIPType(result.IPAddressTypes, easv1alpha1.IPv6)
		}
		if vlan := status.VlanExtension; vlan != nil {
			if result.VLANExtension == nil {
				result.VLANExtension = &easv1alpha1.VLANExtension{}
			}
			if vlan.VlanId != nil {
				result.VLANExtension.VLANID = int32(*vlan.VlanId)
			}
			if vlan.VpcGatewayConnectionEnable != nil {
				result.VLANExtension.VPCGatewayConnectionEnable = *vlan.VpcGatewayConnectionEnable
			}
		}
	}
	return result
}

func appendSubnetStatusValue(values []string, value *string) []string {
	if value == nil || *value == "" || slices.Contains(values, *value) {
		return values
	}
	return append(values, *value)
}

func appendSubnetStatusIPType(values []easv1alpha1.IPAddressType, value easv1alpha1.IPAddressType) []easv1alpha1.IPAddressType {
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}
