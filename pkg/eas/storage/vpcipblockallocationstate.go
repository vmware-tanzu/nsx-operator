/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package storage

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

const vpcIPBlockAllocationStateResource = "vpcipblockallocationstates"

// VPCIPBlockAllocationStateStorage exposes Get-only allocation state for an
// IPBlock consumed by the namespace's VPCs.
type VPCIPBlockAllocationStateStorage struct {
	nsxClient   *nsx.Client
	vpcProvider eas.VPCInfoProvider
	k8sClient   k8sclient.Client
}

func NewVPCIPBlockAllocationStateStorage(nsxClient *nsx.Client, provider eas.VPCInfoProvider, k8sClient k8sclient.Client) *VPCIPBlockAllocationStateStorage {
	return &VPCIPBlockAllocationStateStorage{nsxClient: nsxClient, vpcProvider: provider, k8sClient: k8sClient}
}

// Get accepts ":<ipBlockID>" for an infra IPBlock and "<ipBlockID>" for a
// project IPBlock used by the namespace's VPCs, independently of display name.
// The VPC endpoint restricts returned allocations to those associated VPCs.
func (s *VPCIPBlockAllocationStateStorage) Get(ctx context.Context, namespace, name string) (*easv1alpha1.VPCIPBlockAllocationState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if namespace == "" || strings.TrimPrefix(name, ":") == "" {
		return nil, k8serrors.NewNotFound(schema.GroupResource{Group: easv1alpha1.GroupVersion.Group, Resource: vpcIPBlockAllocationStateResource}, name)
	}
	block, err := s.getBlock(ctx, namespace, name)
	if err != nil {
		return nil, err
	}
	result := &easv1alpha1.VPCIPBlockAllocationState{
		TypeMeta:         metav1.TypeMeta{APIVersion: easv1alpha1.GroupVersion.String(), Kind: "VPCIPBlockAllocationState"},
		ObjectMeta:       metav1.ObjectMeta{Name: name, Namespace: namespace},
		Visibility:       toIPAddressVisibility(DerefString(block.Visibility)),
		AllocationStates: make([]easv1alpha1.VPCIPAddressBlockAllocationStateForCIDR, 0),
	}
	if block.IpAddressType != nil {
		result.IPAddressType = toIPAddressType(*block.IpAddressType)
	}
	seenCIDRs := make(map[string]struct{})
	for _, state := range block.AllocationStates {
		cidr := DerefString(state.Cidr)
		if strings.TrimSpace(cidr) == "" {
			continue
		}
		key := canonicalAllocationCIDR(cidr)
		if key == "" {
			key = cidr
		}
		if _, seen := seenCIDRs[key]; seen {
			continue
		}
		seenCIDRs[key] = struct{}{}
		result.AllocationStates = append(result.AllocationStates, easv1alpha1.VPCIPAddressBlockAllocationStateForCIDR{CIDR: cidr})
	}
	if len(result.AllocationStates) == 0 {
		return result, nil
	}
	allocations := &vpcv1alpha1.IPAddressAllocationList{}
	if err := s.k8sClient.List(ctx, allocations, k8sclient.InNamespace(namespace)); err != nil {
		return nil, HandleEASError(err, vpcIPBlockAllocationStateResource, name, fmt.Errorf("failed to list IPAddressAllocation CRs in namespace %s: %w", namespace, err))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	allocationNames := make(map[string]string)
	for _, allocation := range allocations.Items {
		// Status records the realized allocation; spec alone can describe a
		// request that has not succeeded and must not establish ownership.
		key := canonicalAllocationCIDR(allocation.Status.AllocationIPs)
		if key == "" {
			continue
		}
		if previous, exists := allocationNames[key]; exists && previous != allocation.Name {
			// An overlapping allocation is ambiguous. Do not choose a CR name
			// based on list order when the CIDR alone cannot identify it.
			allocationNames[key] = ""
		} else if !exists {
			allocationNames[key] = allocation.Name
		}
	}
	for i := range result.AllocationStates {
		state := &result.AllocationStates[i]
		state.VPCIPAddressAllocationName = allocationNames[canonicalAllocationCIDR(state.CIDR)]
	}
	return result, nil
}

func (s *VPCIPBlockAllocationStateStorage) getBlock(ctx context.Context, namespace, name string) (*model.IpAddressBlockAllocationState, error) {
	var match *model.IpAddressBlockAllocationState
	seenVPCs := make(map[[3]string]struct{})
	for _, entry := range s.vpcProvider.ListVPCInfo(namespace) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info := entry.Info
		if info.OrgID == "" || info.ProjectID == "" || info.VPCID == "" {
			continue
		}
		key := [3]string{info.OrgID, info.ProjectID, info.VPCID}
		if _, seen := seenVPCs[key]; seen {
			continue
		}
		seenVPCs[key] = struct{}{}
		blockPath := fmt.Sprintf("/orgs/%s/projects/%s/infra/ip-blocks/%s", info.OrgID, info.ProjectID, name)
		if strings.HasPrefix(name, ":") {
			blockPath = "/infra/ip-blocks/" + strings.TrimPrefix(name, ":")
		}
		// Both block scopes are queried through the VPC endpoint. Select only
		// the requested block's exact path from the returned list.
		blocks, err := s.nsxClient.VPCIPBlockAllocationStateClient.List(info.OrgID, info.ProjectID, info.VPCID, nil, nil, nil)
		if err != nil {
			err = HandleEASError(err, vpcIPBlockAllocationStateResource, name, fmt.Errorf("failed to get IPBlock allocation state for VPC %s: %w", info.VPCID, err))
			if k8serrors.IsNotFound(err) {
				continue
			}
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, block := range blocks.Results {
			path := DerefString(block.IpBlockPath)
			if path != blockPath {
				continue
			}
			if match == nil {
				match = &model.IpAddressBlockAllocationState{IpBlockPath: block.IpBlockPath}
			} else if DerefString(match.IpBlockPath) != path {
				return nil, k8serrors.NewInternalError(fmt.Errorf("multiple IPBlocks match ID %q in namespace %s", name, namespace))
			}
			if block.Visibility != nil {
				match.Visibility = block.Visibility
			}
			if block.IpAddressType != nil {
				match.IpAddressType = block.IpAddressType
			}
			match.AllocationStates = append(match.AllocationStates, block.AllocationStates...)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if match == nil {
		return nil, k8serrors.NewNotFound(schema.GroupResource{Group: easv1alpha1.GroupVersion.Group, Resource: vpcIPBlockAllocationStateResource}, name)
	}
	return match, nil
}

// canonicalAllocationCIDR compares exact address allocations, accepting a bare
// address as a host prefix and equivalent IPv6 spellings as the same CIDR.
func canonicalAllocationCIDR(value string) string {
	value = strings.TrimSpace(value)
	if prefix, err := netip.ParsePrefix(value); err == nil {
		return prefix.Masked().String()
	}
	if address, err := netip.ParseAddr(value); err == nil {
		return netip.PrefixFrom(address, address.BitLen()).String()
	}
	return ""
}
