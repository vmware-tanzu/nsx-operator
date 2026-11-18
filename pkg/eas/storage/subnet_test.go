/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package storage

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	vapierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func subnetOwnershipTags(name, uid, namespace string) []model.Tag {
	return []model.Tag{
		{Scope: strPtr(common.TagScopeSubnetCRName), Tag: strPtr(name)},
		{Scope: strPtr(common.TagScopeSubnetCRUID), Tag: strPtr(uid)},
		{Scope: strPtr(common.TagScopeVMNamespace), Tag: strPtr(namespace)},
	}
}

func getSubnetEAS(ctx context.Context, dhcp bool, c *nsx.Client, k8s k8sclient.Client, namespace, name string) (metav1.Object, error) {
	if dhcp {
		return NewSubnetDHCPStatsStorage(c, k8s).Get(ctx, namespace, name)
	}
	pools, err := NewSubnetIPPoolsStorage(c, k8s).Get(ctx, namespace, name)
	if err != nil {
		return nil, err
	}
	if len(pools.Items) != 1 {
		return nil, fmt.Errorf("expected one pool in ownership test, got %d", len(pools.Items))
	}
	return &pools.Items[0], nil
}

func TestSubnetEAS_SharedVPCSameCRName(t *testing.T) {
	for _, dhcp := range []bool{false, true} {
		for target := 0; target < 3; target++ {
			t.Run(fmt.Sprintf("DHCP=%t/namespace=%d", dhcp, target), func(t *testing.T) {
				var objects []k8sclient.Object
				var subnets []model.VpcSubnet
				// Put the requested namespace last to reproduce the original first-match bug.
				for offset := 1; offset <= 3; offset++ {
					i := (target + offset) % 3
					namespace, uid := fmt.Sprintf("ns-%d", i), fmt.Sprintf("uid-%d", i)
					objects = append(objects, &vpcv1alpha1.Subnet{
						ObjectMeta: metav1.ObjectMeta{Name: "shared-name", Namespace: namespace, UID: types.UID(uid)},
						Spec:       vpcv1alpha1.SubnetSpec{VPCName: "p1:shared-vpc"},
					})
					mode := "DHCP_DEACTIVATED"
					if dhcp {
						mode = "DHCP_SERVER"
					}
					subnets = append(subnets, model.VpcSubnet{
						Id: strPtr(fmt.Sprintf("subnet-%d", i)), Tags: subnetOwnershipTags("shared-name", uid, namespace),
						SubnetDhcpConfig: &model.SubnetDhcpConfig{Mode: strPtr(mode)},
					})
				}
				pools, stats := &fakeIPPoolClient{result: model.IpAddressPoolListResult{Results: []model.IpAddressPool{{}}}}, &fakeDHCPStatsClient{}
				c := &nsx.Client{SubnetsClient: &fakeSubnetsClient{results: model.VpcSubnetListResult{Results: subnets}}, IPPoolClient: pools, DhcpServerConfigStatsClient: stats}
				namespace := fmt.Sprintf("ns-%d", target)
				result, err := getSubnetEAS(context.Background(), dhcp, c, newFakeK8sClient(objects...), namespace, "shared-name")
				require.NoError(t, err)
				require.Equal(t, namespace, result.GetNamespace())
				require.Equal(t, "shared-name", result.GetName())
				calledIDs := append(pools.subnetIDs, stats.subnetIDs...)
				require.Equal(t, []string{fmt.Sprintf("subnet-%d", target)}, calledIDs)
			})
		}
	}
}

func TestSubnetEAS_RejectsUnownedSubnets(t *testing.T) {
	for _, dhcp := range []bool{false, true} {
		for _, tc := range []struct {
			name string
			tags []model.Tag
		}{
			{name: "same name in another namespace", tags: subnetOwnershipTags("sub1", "other-uid", "ns2")},
			{name: "matching UID but wrong namespace", tags: subnetOwnershipTags("sub1", "current-uid", "ns2")},
			{name: "stale UID after CR recreation", tags: subnetOwnershipTags("sub1", "old-uid", "ns1")},
			{name: "missing UID", tags: subnetOwnershipTags("sub1", "", "ns1")},
			{name: "missing namespace", tags: subnetOwnershipTags("sub1", "current-uid", "")},
			{name: "name tag only", tags: []model.Tag{{Scope: strPtr(common.TagScopeSubnetCRName), Tag: strPtr("sub1")}}},
			{name: "no tags"},
			{name: "nil tag fields", tags: []model.Tag{{}, {Scope: strPtr(common.TagScopeSubnetCRUID)}, {Tag: strPtr("ns1")}}},
		} {
			t.Run(fmt.Sprintf("DHCP=%t/%s", dhcp, tc.name), func(t *testing.T) {
				cr := &vpcv1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "ns1", UID: "current-uid"}, Spec: vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"}}
				c := &nsx.Client{SubnetsClient: &fakeSubnetsClient{results: model.VpcSubnetListResult{
					Results: []model.VpcSubnet{{Id: strPtr("sub1"), Tags: tc.tags}},
				}}}
				// No downstream clients: rejected subnets must never be queried for data.
				_, err := getSubnetEAS(context.Background(), dhcp, c, newFakeK8sClient(cr), "ns1", "sub1")
				require.True(t, k8serrors.IsNotFound(err), "got %v", err)
			})
		}
	}
}

func TestSubnetEAS_PaginationAndOwnershipBeforeDHCPMode(t *testing.T) {
	for _, dhcp := range []bool{false, true} {
		t.Run(fmt.Sprintf("DHCP=%t", dhcp), func(t *testing.T) {
			cr := &vpcv1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "ns1", UID: "current-uid"}, Spec: vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"}}
			mode, otherMode := "DHCP_DEACTIVATED", "DHCP_SERVER"
			if dhcp {
				mode, otherMode = otherMode, mode
			}
			calls := 0
			list := &fakeSubnetsClient{list: func(orgID, projectID, vpcID string, cursor *string) (model.VpcSubnetListResult, error) {
				require.Equal(t, []string{"default", "p1", "vpc1"}, []string{orgID, projectID, vpcID})
				calls++
				if calls == 1 {
					require.Nil(t, cursor)
					return model.VpcSubnetListResult{Cursor: strPtr("page-2"), Results: []model.VpcSubnet{{
						Id: strPtr("other-subnet"), Tags: subnetOwnershipTags("sub1", "other-uid", "ns2"),
						SubnetDhcpConfig: &model.SubnetDhcpConfig{Mode: strPtr(otherMode)},
					}}}, nil
				}
				require.Equal(t, 2, calls)
				require.Equal(t, "page-2", DerefString(cursor))
				return model.VpcSubnetListResult{Results: []model.VpcSubnet{{
					Id: strPtr("owned-subnet"), Tags: subnetOwnershipTags("sub1", "current-uid", "ns1"),
					SubnetDhcpConfig: &model.SubnetDhcpConfig{Mode: strPtr(mode)},
				}}}, nil
			}}
			pools, stats := &fakeIPPoolClient{result: model.IpAddressPoolListResult{Results: []model.IpAddressPool{{}}}}, &fakeDHCPStatsClient{}
			c := &nsx.Client{SubnetsClient: list, IPPoolClient: pools, DhcpServerConfigStatsClient: stats}
			_, err := getSubnetEAS(context.Background(), dhcp, c, newFakeK8sClient(cr), "ns1", "sub1")
			require.NoError(t, err)
			require.Equal(t, 2, calls)
			require.Equal(t, []string{"owned-subnet"}, append(pools.subnetIDs, stats.subnetIDs...))
		})
	}
}

func TestSubnetEAS_EmptyUID(t *testing.T) {
	for _, dhcp := range []bool{false, true} {
		t.Run(fmt.Sprintf("DHCP=%t", dhcp), func(t *testing.T) {
			cr := &vpcv1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "ns1"}, Spec: vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"}}
			_, err := getSubnetEAS(context.Background(), dhcp, &nsx.Client{}, newFakeK8sClient(cr), "ns1", "sub1")
			require.ErrorContains(t, err, "empty UID")
		})
	}
}

func TestResolveSubnet_AmbiguousOwnership(t *testing.T) {
	cr := &vpcv1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "ns1", UID: "current-uid"}, Spec: vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"}}
	calls := 0
	c := &nsx.Client{SubnetsClient: &fakeSubnetsClient{list: func(_, _, _ string, cursor *string) (model.VpcSubnetListResult, error) {
		calls++
		page := model.VpcSubnetListResult{Results: []model.VpcSubnet{{Id: strPtr(fmt.Sprintf("subnet-%d", calls)), Tags: subnetOwnershipTags("sub1", "current-uid", "ns1")}}}
		if cursor == nil {
			page.Cursor = strPtr("page-2")
		}
		return page, nil
	}}}
	_, _, err := resolveSubnet(context.Background(), newFakeK8sClient(cr), c, "ns1", "sub1", "subnetippools")
	require.True(t, k8serrors.IsInternalError(err), "got %v", err)
	require.ErrorContains(t, err, "multiple NSX subnets")
	require.Equal(t, 2, calls)
}

func TestResolveSubnet_PaginationErrors(t *testing.T) {
	for _, repeatedCursor := range []bool{false, true} {
		t.Run(fmt.Sprintf("repeatedCursor=%t", repeatedCursor), func(t *testing.T) {
			cr := &vpcv1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "ns1", UID: "current-uid"}, Spec: vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"}}
			calls := 0
			c := &nsx.Client{SubnetsClient: &fakeSubnetsClient{list: func(_, _, _ string, cursor *string) (model.VpcSubnetListResult, error) {
				calls++
				if cursor != nil && !repeatedCursor {
					return model.VpcSubnetListResult{}, fmt.Errorf("second page unavailable")
				}
				return model.VpcSubnetListResult{Cursor: strPtr("page-2"), Results: []model.VpcSubnet{{
					Id: strPtr("owned-subnet"), Tags: subnetOwnershipTags("sub1", "current-uid", "ns1"),
				}}}, nil
			}}}
			_, _, err := resolveSubnet(context.Background(), newFakeK8sClient(cr), c, "ns1", "sub1", "subnetippools")
			if repeatedCursor {
				require.ErrorContains(t, err, "repeated NSX subnet list cursor")
			} else {
				require.ErrorContains(t, err, "second page unavailable")
			}
			require.Equal(t, 2, calls)
		})
	}
}

func TestSubnetEAS_PreCreatedSharedSubnet(t *testing.T) {
	for _, dhcp := range []bool{false, true} {
		for _, namespace := range []string{"ns-1", "ns-2", "ns-3"} {
			t.Run(fmt.Sprintf("DHCP=%t/namespace=%s", dhcp, namespace), func(t *testing.T) {
				cr := &vpcv1alpha1.Subnet{
					ObjectMeta: metav1.ObjectMeta{Name: "cr-alias", Namespace: namespace, UID: types.UID(namespace + "-uid"),
						Annotations: map[string]string{common.AnnotationAssociatedResource: "p1:vpc1:precreated-subnet"}},
					Spec: vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"},
				}
				mode := "DHCP_DEACTIVATED"
				if dhcp {
					mode = "DHCP_SERVER"
				}
				pools := &fakeIPPoolClient{result: model.IpAddressPoolListResult{
					Results: []model.IpAddressPool{{IpAddressType: strPtr("IPV4")}},
				}}
				stats := &fakeDHCPStatsClient{}
				c := &nsx.Client{SubnetsClient: &fakeSubnetsClient{
					get: func(orgID, projectID, vpcID, subnetID string) (model.VpcSubnet, error) {
						require.Equal(t, []string{"default", "p1", "vpc1", "precreated-subnet"}, []string{orgID, projectID, vpcID, subnetID})
						return model.VpcSubnet{Id: strPtr(subnetID), SubnetDhcpConfig: &model.SubnetDhcpConfig{Mode: strPtr(mode)}}, nil
					},
					list: func(_, _, _ string, _ *string) (model.VpcSubnetListResult, error) {
						t.Fatal("shared resources must use their explicit association")
						return model.VpcSubnetListResult{}, nil
					},
				}, IPPoolClient: pools, DhcpServerConfigStatsClient: stats}
				result, err := getSubnetEAS(context.Background(), dhcp, c, newFakeK8sClient(cr), namespace, cr.Name)
				require.NoError(t, err)
				require.Equal(t, cr.Name, result.GetName())
				require.Equal(t, namespace, result.GetNamespace())
				require.Equal(t, []string{"precreated-subnet"}, append(pools.subnetIDs, stats.subnetIDs...))
			})
		}
	}
}

func TestResolveSubnet_InvalidSharedAssociation(t *testing.T) {
	for _, association := range []string{
		"", "p1:vpc1", "p1:vpc1:subnet:extra", ":vpc1:subnet", "p1::subnet", "p1:vpc1:",
		"other:vpc1:subnet", "p1:other:subnet", "p1:vpc1:a/b", "p1:vpc1:a\\b", "p1:vpc1:..",
		"p1:vpc1:.", "p1:vpc1:%2e%2e", "p1:vpc1:subnet?x", "p1:vpc1:subnet#x", "p1:vpc1: subnet",
	} {
		t.Run(association, func(t *testing.T) {
			cr := &vpcv1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: "ns1", UID: "uid",
					Annotations: map[string]string{common.AnnotationAssociatedResource: association}},
				Spec: vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"},
			}
			// Invalid associations must be rejected before calling NSX.
			_, _, err := resolveSubnet(context.Background(), newFakeK8sClient(cr), &nsx.Client{}, "ns1", cr.Name, "subnetstatuses")
			require.Error(t, err)
		})
	}
}

func TestResolveSubnet_SharedErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		subnet    model.VpcSubnet
		err       error
		isError   func(error) bool
		wantError string
	}{
		{name: "not found", err: vapierrors.NotFound{}, isError: k8serrors.IsNotFound},
		{name: "forbidden", err: vapierrors.Unauthorized{}, isError: k8serrors.IsForbidden},
		{name: "backend error", err: fmt.Errorf("backend unavailable"), wantError: "backend unavailable"},
		{name: "missing ID", wantError: "does not match the association"},
		{name: "wrong ID", subnet: model.VpcSubnet{Id: strPtr("other-subnet")}, wantError: "does not match the association"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cr := &vpcv1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: "ns1", UID: "uid",
					Annotations: map[string]string{common.AnnotationAssociatedResource: "p1:vpc1:subnet"}},
				Spec: vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"},
			}
			c := &nsx.Client{SubnetsClient: &fakeSubnetsClient{get: func(_, _, _, _ string) (model.VpcSubnet, error) {
				return tc.subnet, tc.err
			}}}
			_, _, err := resolveSubnet(context.Background(), newFakeK8sClient(cr), c, "ns1", cr.Name, "subnetstatuses")
			require.Error(t, err)
			if tc.isError != nil {
				require.True(t, tc.isError(err), "got %v", err)
			} else {
				require.ErrorContains(t, err, tc.wantError)
			}
		})
	}
}

func TestResolveSubnet_SharedFlagDoesNotBypassOwnership(t *testing.T) {
	cr := &vpcv1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "subnet", Namespace: "ns1", UID: "uid"},
		Spec:       vpcv1alpha1.SubnetSpec{VPCName: "p1:vpc1"},
		Status:     vpcv1alpha1.SubnetStatus{Shared: true},
	}
	c := &nsx.Client{SubnetsClient: &fakeSubnetsClient{results: model.VpcSubnetListResult{Results: []model.VpcSubnet{{Id: strPtr("subnet")}}}}}
	_, _, err := resolveSubnet(context.Background(), newFakeK8sClient(cr), c, "ns1", cr.Name, "subnetstatuses")
	require.True(t, k8serrors.IsNotFound(err), "got %v", err)
}

func TestResolveSubnet_SharedDefaultProject(t *testing.T) {
	cr := &vpcv1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "alias", Namespace: "ns1", UID: "uid",
			Annotations: map[string]string{common.AnnotationAssociatedResource: "default:vpc1:subnet"}},
		Spec: vpcv1alpha1.SubnetSpec{VPCName: ":vpc1"},
	}
	c := &nsx.Client{SubnetsClient: &fakeSubnetsClient{get: func(orgID, projectID, vpcID, subnetID string) (model.VpcSubnet, error) {
		require.Equal(t, []string{"default", "default", "vpc1", "subnet"}, []string{orgID, projectID, vpcID, subnetID})
		return model.VpcSubnet{Id: strPtr(subnetID)}, nil
	}}}
	subnet, info, err := resolveSubnet(context.Background(), newFakeK8sClient(cr), c, "ns1", cr.Name, "subnetstatuses")
	require.NoError(t, err)
	require.Equal(t, "default", info.ProjectID)
	require.Equal(t, "subnet", *subnet.Id)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = resolveSharedSubnet(ctx, &nsx.Client{}, cr, info, "subnetstatuses")
	require.ErrorIs(t, err, context.Canceled)
}
