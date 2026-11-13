/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package storage

import (
	"context"
	"fmt"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	nsxcommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// resolveSubnet resolves a namespaced Subnet CR to its NSX subnet. Names alone
// cannot identify ownership because multiple namespaces can share the same VPC.
func resolveSubnet(ctx context.Context, k8sClient k8sclient.Client, nsxClient *nsx.Client, namespace, name, resource string) (model.VpcSubnet, nsxcommon.VPCResourceInfo, error) {
	var info nsxcommon.VPCResourceInfo
	subnetCR := &vpcv1alpha1.Subnet{}
	if err := k8sClient.Get(ctx, k8sclient.ObjectKey{Namespace: namespace, Name: name}, subnetCR); err != nil {
		return model.VpcSubnet{}, info, HandleEASError(err, resource, name, fmt.Errorf("failed to get subnet CR %s/%s: %w", namespace, name, err))
	}
	if subnetCR.Spec.VPCName == "" {
		return model.VpcSubnet{}, info, fmt.Errorf("subnet CR %s/%s has empty spec.vpcName", namespace, name)
	}
	if subnetCR.UID == "" {
		return model.VpcSubnet{}, info, fmt.Errorf("subnet CR %s/%s has empty UID", namespace, name)
	}
	info.OrgID, info.ProjectID, info.VPCID = parseSubnetVPCName(subnetCR.Spec.VPCName)
	var match *model.VpcSubnet
	var cursor *string
	seenCursors := make(map[string]struct{})
	for {
		if err := ctx.Err(); err != nil {
			return model.VpcSubnet{}, info, err
		}
		page, err := nsxClient.SubnetsClient.List(info.OrgID, info.ProjectID, info.VPCID, cursor,
			nil, nil, nil, nil, nil, nil)
		if err != nil {
			return model.VpcSubnet{}, info, HandleEASError(err, resource, name, fmt.Errorf("failed to list subnets from NSX: %w", err))
		}
		for _, subnet := range page.Results {
			if subnet.Id == nil || *subnet.Id == "" ||
				nsxTagValue(subnet.Tags, nsxcommon.TagScopeSubnetCRUID) != string(subnetCR.UID) ||
				nsxTagValue(subnet.Tags, nsxcommon.TagScopeVMNamespace) != namespace {
				continue
			}
			if match != nil && *match.Id != *subnet.Id {
				return model.VpcSubnet{}, info, k8serrors.NewInternalError(fmt.Errorf("multiple NSX subnets match subnet CR %s/%s UID %s", namespace, name, subnetCR.UID))
			}
			match = &subnet
		}
		if page.Cursor == nil || *page.Cursor == "" {
			break
		}
		if _, seen := seenCursors[*page.Cursor]; seen {
			return model.VpcSubnet{}, info, fmt.Errorf("repeated NSX subnet list cursor for subnet CR %s/%s", namespace, name)
		}
		seenCursors[*page.Cursor] = struct{}{}
		cursor = page.Cursor
	}
	if match == nil {
		return model.VpcSubnet{}, info, k8serrors.NewNotFound(schema.GroupResource{Group: easv1alpha1.GroupVersion.Group, Resource: resource}, name)
	}
	return *match, info, nil
}

// nsxTagValue returns the tag value for the given scope from an NSX tags slice.
func nsxTagValue(tags []model.Tag, scope string) string {
	for _, tag := range tags {
		if tag.Scope != nil && *tag.Scope == scope && tag.Tag != nil {
			return *tag.Tag
		}
	}
	return ""
}
