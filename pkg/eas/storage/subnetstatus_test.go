/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package storage

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	vapierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type fakeSubnetStatusClient struct {
	list  func(orgID, projectID, vpcID, subnetID string) (model.VpcSubnetStatusListResult, error)
	calls [][]string
}

func (f *fakeSubnetStatusClient) List(orgID, projectID, vpcID, subnetID string) (model.VpcSubnetStatusListResult, error) {
	f.calls = append(f.calls, []string{orgID, projectID, vpcID, subnetID})
	return f.list(orgID, projectID, vpcID, subnetID)
}

func subnetStatusCR(name, namespace, uid string) *vpcv1alpha1.Subnet {
	return &vpcv1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, UID: types.UID(uid)},
		Spec:       vpcv1alpha1.SubnetSpec{VPCName: "project:shared-vpc"},
	}
}

func TestConvertVpcSubnetStatus(t *testing.T) {
	ipv4 := model.VpcSubnetStatus{
		DhcpRanges:         strPtr("10.0.0.10-10.0.0.19,10.0.0.30-10.0.0.39"),
		DhcpReservedRanges: strPtr("10.0.0.15-10.0.0.16"),
		GatewayAddress:     strPtr("10.0.0.1"),
		IpAddressType:      strPtr("IPV4"),
		StaticIpPoolRange:  strPtr("10.0.0.40-10.0.0.49"),
		UnmanagedIps:       strPtr("10.0.0.50"),
	}
	ipv6 := model.VpcSubnetStatus{
		DhcpRanges:         strPtr("2001:db8::10-2001:db8::19"),
		DhcpReservedRanges: strPtr("2001:db8::15-2001:db8::16"),
		GatewayAddress:     strPtr("2001:db8::1"),
		IpAddressType:      strPtr("ipv6"),
		StaticIpPoolRange:  strPtr("2001:db8::40-2001:db8::49"),
		UnmanagedIps:       strPtr("2001:db8::50"),
		VlanExtension: &model.VpcSubnetVlanExtensionStatus{
			VlanId: int64Ptr(321), VpcGatewayConnectionEnable: ptr.To(true),
		},
	}
	result := ConvertVpcSubnetStatus([]model.VpcSubnetStatus{ipv4, ipv6, ipv4, {IpAddressType: strPtr("IPV4_IPV6")}}, "cr-name", "ns1")
	assert.Equal(t, &easv1alpha1.SubnetStatus{
		TypeMeta:           metav1.TypeMeta{APIVersion: easv1alpha1.GroupVersion.String(), Kind: "SubnetStatus"},
		ObjectMeta:         metav1.ObjectMeta{Name: "cr-name", Namespace: "ns1"},
		DHCPRanges:         []string{*ipv4.DhcpRanges, *ipv6.DhcpRanges},
		DHCPReservedRanges: []string{*ipv4.DhcpReservedRanges, *ipv6.DhcpReservedRanges},
		GatewayAddresses:   []string{*ipv4.GatewayAddress, *ipv6.GatewayAddress},
		IPAddressTypes:     []easv1alpha1.IPAddressType{easv1alpha1.IPv4, easv1alpha1.IPv6},
		StaticIPPoolRanges: []string{*ipv4.StaticIpPoolRange, *ipv6.StaticIpPoolRange},
		UnmanagedIPs:       []string{*ipv4.UnmanagedIps, *ipv6.UnmanagedIps},
		VLANExtension:      &easv1alpha1.VLANExtension{VLANID: 321, VPCGatewayConnectionEnable: true},
	}, result)
}

func TestConvertVpcSubnetStatus_OptionalFieldsAndAddressFamilies(t *testing.T) {
	for _, tc := range []struct {
		name     string
		statuses []model.VpcSubnetStatus
		wantIPs  []easv1alpha1.IPAddressType
	}{
		{name: "no realized records"},
		{name: "all fields nil", statuses: []model.VpcSubnetStatus{{}}},
		{name: "empty fields", statuses: []model.VpcSubnetStatus{{
			DhcpRanges: strPtr(""), DhcpReservedRanges: strPtr(""), GatewayAddress: strPtr(""),
			IpAddressType: strPtr(""), StaticIpPoolRange: strPtr(""), UnmanagedIps: strPtr(""),
		}}},
		{name: "unknown IP type", statuses: []model.VpcSubnetStatus{{IpAddressType: strPtr("UNKNOWN")}}},
		{name: "IPv6 only", statuses: []model.VpcSubnetStatus{{IpAddressType: strPtr("IPV6")}}, wantIPs: []easv1alpha1.IPAddressType{easv1alpha1.IPv6}},
		{name: "dual stack record", statuses: []model.VpcSubnetStatus{{IpAddressType: strPtr("IPV4_IPV6")}}, wantIPs: []easv1alpha1.IPAddressType{easv1alpha1.IPv4, easv1alpha1.IPv6}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := ConvertVpcSubnetStatus(tc.statuses, "sub1", "ns1")
			assert.Equal(t, &easv1alpha1.SubnetStatus{
				TypeMeta:       metav1.TypeMeta{APIVersion: easv1alpha1.GroupVersion.String(), Kind: "SubnetStatus"},
				ObjectMeta:     metav1.ObjectMeta{Name: "sub1", Namespace: "ns1"},
				IPAddressTypes: tc.wantIPs,
			}, result)
		})
	}
}

func TestConvertVpcSubnetStatus_VLANOptionalFields(t *testing.T) {
	for _, tc := range []struct {
		name string
		vlan *model.VpcSubnetVlanExtensionStatus
		want *easv1alpha1.VLANExtension
	}{
		{name: "absent"},
		{name: "empty", vlan: &model.VpcSubnetVlanExtensionStatus{}, want: &easv1alpha1.VLANExtension{}},
		{name: "VLAN ID only", vlan: &model.VpcSubnetVlanExtensionStatus{VlanId: int64Ptr(42)}, want: &easv1alpha1.VLANExtension{VLANID: 42}},
		{name: "gateway enabled only", vlan: &model.VpcSubnetVlanExtensionStatus{VpcGatewayConnectionEnable: ptr.To(true)}, want: &easv1alpha1.VLANExtension{VPCGatewayConnectionEnable: true}},
		{name: "gateway explicitly disabled", vlan: &model.VpcSubnetVlanExtensionStatus{VlanId: int64Ptr(42), VpcGatewayConnectionEnable: ptr.To(false)}, want: &easv1alpha1.VLANExtension{VLANID: 42}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := ConvertVpcSubnetStatus([]model.VpcSubnetStatus{{VlanExtension: tc.vlan}, {}}, "sub1", "ns1")
			assert.Equal(t, tc.want, result.VLANExtension)
		})
	}
}

func TestConvertVpcSubnetStatus_MergesVLANFamilies(t *testing.T) {
	for _, tc := range []struct {
		name  string
		later *model.VpcSubnetVlanExtensionStatus
		want  easv1alpha1.VLANExtension
	}{
		{name: "empty IPv6 extension", later: &model.VpcSubnetVlanExtensionStatus{}, want: easv1alpha1.VLANExtension{VLANID: 42, VPCGatewayConnectionEnable: true}},
		{name: "updated ID only", later: &model.VpcSubnetVlanExtensionStatus{VlanId: int64Ptr(43)}, want: easv1alpha1.VLANExtension{VLANID: 43, VPCGatewayConnectionEnable: true}},
		{name: "explicitly disabled gateway", later: &model.VpcSubnetVlanExtensionStatus{VpcGatewayConnectionEnable: ptr.To(false)}, want: easv1alpha1.VLANExtension{VLANID: 42}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := ConvertVpcSubnetStatus([]model.VpcSubnetStatus{
				{IpAddressType: strPtr("IPV4"), VlanExtension: &model.VpcSubnetVlanExtensionStatus{VlanId: int64Ptr(42), VpcGatewayConnectionEnable: ptr.To(true)}},
				{IpAddressType: strPtr("IPV6"), VlanExtension: tc.later},
			}, "sub1", "ns1")
			assert.Equal(t, &tc.want, result.VLANExtension)
		})
	}
}

func TestSubnetStatusStorage_Get_SharedVPCSameCRName(t *testing.T) {
	for target := range 3 {
		t.Run(fmt.Sprintf("ns-%d", target+1), func(t *testing.T) {
			var objects []k8sclient.Object
			var subnets []model.VpcSubnet
			// The requested CR's NSX subnet is last, so selecting the first name match fails.
			for offset := 1; offset <= 3; offset++ {
				i := (target + offset) % 3
				namespace, uid := fmt.Sprintf("ns-%d", i+1), fmt.Sprintf("uid-%d", i+1)
				objects = append(objects, subnetStatusCR("same-name", namespace, uid))
				subnets = append(subnets, model.VpcSubnet{
					Id: strPtr(fmt.Sprintf("nsx-subnet-%d", i+1)), Tags: subnetOwnershipTags("same-name", uid, namespace),
				})
			}
			statusClient := &fakeSubnetStatusClient{list: func(_, _, _, subnetID string) (model.VpcSubnetStatusListResult, error) {
				return model.VpcSubnetStatusListResult{Results: []model.VpcSubnetStatus{{GatewayAddress: strPtr(subnetID)}}}, nil
			}}
			client := &nsx.Client{
				SubnetsClient: &fakeSubnetsClient{results: model.VpcSubnetListResult{Results: subnets}}, SubnetStatusClient: statusClient,
			}
			namespace, subnetID := fmt.Sprintf("ns-%d", target+1), fmt.Sprintf("nsx-subnet-%d", target+1)
			result, err := NewSubnetStatusStorage(client, newFakeK8sClient(objects...)).Get(context.Background(), namespace, "same-name")
			require.NoError(t, err)
			assert.Equal(t, "same-name", result.Name)
			assert.Equal(t, namespace, result.Namespace)
			assert.Equal(t, []string{subnetID}, result.GatewayAddresses)
			assert.Equal(t, [][]string{{"default", "project", "shared-vpc", subnetID}}, statusClient.calls)
		})
	}
}

func TestSubnetStatusStorage_Get_PrecreatedSharedSubnet(t *testing.T) {
	var objects []k8sclient.Object
	for _, namespace := range []string{"ns1", "ns2"} {
		cr := subnetStatusCR("shared-alias", namespace, "uid-"+namespace)
		cr.Annotations = map[string]string{common.AnnotationAssociatedResource: "project:shared-vpc:precreated-source"}
		objects = append(objects, cr)
	}
	var subnetGetCalls [][]string
	subnets := &fakeSubnetsClient{
		get: func(orgID, projectID, vpcID, subnetID string) (model.VpcSubnet, error) {
			subnetGetCalls = append(subnetGetCalls, []string{orgID, projectID, vpcID, subnetID})
			return model.VpcSubnet{
				Id: strPtr("precreated-source"), Path: strPtr("/orgs/default/projects/project/vpcs/shared-vpc/subnets/precreated-source"),
				// Pre-created shared subnets do not have per-CR ownership tags.
			}, nil
		},
		list: func(_, _, _ string, _ *string) (model.VpcSubnetListResult, error) {
			t.Fatal("an associated Subnet CR must resolve its explicit source rather than list managed resources")
			return model.VpcSubnetListResult{}, nil
		},
	}
	statusClient := &fakeSubnetStatusClient{list: func(_, _, _, _ string) (model.VpcSubnetStatusListResult, error) {
		return model.VpcSubnetStatusListResult{Results: []model.VpcSubnetStatus{{
			GatewayAddress: strPtr("192.0.2.1/26"), IpAddressType: strPtr("IPV4"),
			DhcpRanges: strPtr("192.0.2.10-192.0.2.19"), DhcpReservedRanges: strPtr("192.0.2.20"),
			StaticIpPoolRange: strPtr("192.0.2.30-192.0.2.39"), UnmanagedIps: strPtr("192.0.2.40"),
		}}}, nil
	}}
	store := NewSubnetStatusStorage(&nsx.Client{SubnetsClient: subnets, SubnetStatusClient: statusClient}, newFakeK8sClient(objects...))
	for _, namespace := range []string{"ns1", "ns2"} {
		result, err := store.Get(context.Background(), namespace, "shared-alias")
		require.NoError(t, err)
		assert.Equal(t, &easv1alpha1.SubnetStatus{
			TypeMeta:           metav1.TypeMeta{APIVersion: easv1alpha1.GroupVersion.String(), Kind: "SubnetStatus"},
			ObjectMeta:         metav1.ObjectMeta{Name: "shared-alias", Namespace: namespace},
			GatewayAddresses:   []string{"192.0.2.1/26"},
			IPAddressTypes:     []easv1alpha1.IPAddressType{easv1alpha1.IPv4},
			DHCPRanges:         []string{"192.0.2.10-192.0.2.19"},
			DHCPReservedRanges: []string{"192.0.2.20"},
			StaticIPPoolRanges: []string{"192.0.2.30-192.0.2.39"},
			UnmanagedIPs:       []string{"192.0.2.40"},
		}, result)
	}
	wantCalls := [][]string{
		{"default", "project", "shared-vpc", "precreated-source"},
		{"default", "project", "shared-vpc", "precreated-source"},
	}
	assert.Equal(t, wantCalls, subnetGetCalls)
	assert.Equal(t, wantCalls, statusClient.calls)
}

func TestSubnetStatusStorage_List_PrecreatedAndManagedSubnets(t *testing.T) {
	sharedCR := subnetStatusCR("shared-alias", "ns1", "shared-uid")
	sharedCR.Annotations = map[string]string{common.AnnotationAssociatedResource: "project:shared-vpc:precreated-source"}
	foreignCR := subnetStatusCR("shared-alias", "ns2", "foreign-uid")
	foreignCR.Annotations = map[string]string{common.AnnotationAssociatedResource: "project:shared-vpc:foreign-source"}
	var subnetGetIDs []string
	subnets := &fakeSubnetsClient{
		get: func(_, _, _, subnetID string) (model.VpcSubnet, error) {
			subnetGetIDs = append(subnetGetIDs, subnetID)
			return model.VpcSubnet{
				Id: strPtr("precreated-source"), Path: strPtr("/orgs/default/projects/project/vpcs/shared-vpc/subnets/precreated-source"),
			}, nil
		},
		results: model.VpcSubnetListResult{Results: []model.VpcSubnet{{
			Id: strPtr("managed-source"), Tags: subnetOwnershipTags("managed-cr", "managed-uid", "ns1"),
		}}},
	}
	statusClient := &fakeSubnetStatusClient{list: func(_, _, _, subnetID string) (model.VpcSubnetStatusListResult, error) {
		gateway := "192.0.2.1/26"
		if subnetID == "managed-source" {
			gateway = "198.51.100.1/26"
		}
		return model.VpcSubnetStatusListResult{Results: []model.VpcSubnetStatus{{
			GatewayAddress: strPtr(gateway), IpAddressType: strPtr("IPV4"), DhcpRanges: strPtr("192.0.2.10-192.0.2.19"),
		}}}, nil
	}}
	store := NewSubnetStatusStorage(&nsx.Client{SubnetsClient: subnets, SubnetStatusClient: statusClient},
		newFakeK8sClient(sharedCR, foreignCR, subnetStatusCR("managed-cr", "ns1", "managed-uid")))
	result, err := store.List(context.Background(), "ns1")
	require.NoError(t, err)
	assert.ElementsMatch(t, []easv1alpha1.SubnetStatus{
		{TypeMeta: metav1.TypeMeta{APIVersion: easv1alpha1.GroupVersion.String(), Kind: "SubnetStatus"},
			ObjectMeta: metav1.ObjectMeta{Name: "shared-alias", Namespace: "ns1"}, GatewayAddresses: []string{"192.0.2.1/26"}},
		{TypeMeta: metav1.TypeMeta{APIVersion: easv1alpha1.GroupVersion.String(), Kind: "SubnetStatus"},
			ObjectMeta: metav1.ObjectMeta{Name: "managed-cr", Namespace: "ns1"}, GatewayAddresses: []string{"198.51.100.1/26"}},
	}, result.Items)
	assert.Equal(t, []string{"precreated-source"}, subnetGetIDs)
	assert.ElementsMatch(t, [][]string{
		{"default", "project", "shared-vpc", "precreated-source"},
		{"default", "project", "shared-vpc", "managed-source"},
	}, statusClient.calls)
}

func TestSubnetStatusStorage_Get_RejectsMissingOrForeignCR(t *testing.T) {
	for _, tc := range []struct {
		name    string
		missing bool
		tags    []model.Tag
	}{
		{name: "missing CR", missing: true},
		{name: "foreign UID", tags: subnetOwnershipTags("sub1", "stale-uid", "ns1")},
		{name: "foreign namespace", tags: subnetOwnershipTags("sub1", "uid1", "ns2")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var objects []k8sclient.Object
			if !tc.missing {
				objects = append(objects, subnetStatusCR("sub1", "ns1", "uid1"))
			}
			statusClient := &fakeSubnetStatusClient{}
			client := &nsx.Client{
				SubnetsClient:      &fakeSubnetsClient{results: model.VpcSubnetListResult{Results: []model.VpcSubnet{{Id: strPtr("sub1"), Tags: tc.tags}}}},
				SubnetStatusClient: statusClient,
			}
			result, err := NewSubnetStatusStorage(client, newFakeK8sClient(objects...)).Get(context.Background(), "ns1", "sub1")
			require.True(t, k8serrors.IsNotFound(err), "got %v", err)
			assert.Nil(t, result)
			assert.Empty(t, statusClient.calls)
		})
	}
}

func TestSubnetStatusStorage_Get_NSXErrors(t *testing.T) {
	backendErr := fmt.Errorf("status backend unavailable")
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "not found", err: vapierrors.NotFound{}},
		{name: "forbidden", err: vapierrors.Unauthorized{}},
		{name: "backend error", err: backendErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			statusClient := &fakeSubnetStatusClient{list: func(_, _, _, _ string) (model.VpcSubnetStatusListResult, error) {
				return model.VpcSubnetStatusListResult{}, tc.err
			}}
			client := &nsx.Client{
				SubnetsClient: &fakeSubnetsClient{results: model.VpcSubnetListResult{Results: []model.VpcSubnet{{
					Id: strPtr("nsx-id"), Tags: subnetOwnershipTags("sub1", "uid1", "ns1"),
				}}}}, SubnetStatusClient: statusClient,
			}
			result, err := NewSubnetStatusStorage(client, newFakeK8sClient(subnetStatusCR("sub1", "ns1", "uid1"))).Get(context.Background(), "ns1", "sub1")
			require.Error(t, err)
			assert.Nil(t, result)
			switch tc.name {
			case "not found":
				assert.True(t, k8serrors.IsNotFound(err), "got %v", err)
			case "forbidden":
				assert.True(t, k8serrors.IsForbidden(err), "got %v", err)
			default:
				assert.ErrorIs(t, err, backendErr)
			}
			assert.Equal(t, [][]string{{"default", "project", "shared-vpc", "nsx-id"}}, statusClient.calls)
		})
	}
}

func TestSubnetStatusStorage_List_GatewaySummaries(t *testing.T) {
	objects := []k8sclient.Object{
		subnetStatusCR("dual-stack", "ns1", "dual-uid"), subnetStatusCR("l2-only", "ns1", "l2-uid"),
		subnetStatusCR("no-nsx-resource", "ns1", "orphan-uid"), subnetStatusCR("deleted-status", "ns1", "deleted-uid"),
		subnetStatusCR("no-uid", "ns1", ""), subnetStatusCR("dual-stack", "ns2", "other-uid"),
		&vpcv1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "no-vpc", Namespace: "ns1", UID: "pending-uid"}},
	}
	var subnets []model.VpcSubnet
	for _, object := range objects[:4] {
		subnets = append(subnets, model.VpcSubnet{
			Id: strPtr(object.GetName()), Tags: subnetOwnershipTags(object.GetName(), string(object.GetUID()), "ns1"),
		})
	}
	// The CR exists, but its NSX resource has not been realized yet.
	subnets = append(subnets[:2], subnets[3:]...)
	statusClient := &fakeSubnetStatusClient{list: func(_, _, _, id string) (model.VpcSubnetStatusListResult, error) {
		switch id {
		case "deleted-status":
			return model.VpcSubnetStatusListResult{}, vapierrors.NotFound{}
		case "l2-only":
			return model.VpcSubnetStatusListResult{Results: []model.VpcSubnetStatus{{}}}, nil
		default:
			return model.VpcSubnetStatusListResult{Results: []model.VpcSubnetStatus{
				{GatewayAddress: strPtr("10.0.0.1"), IpAddressType: strPtr("IPV4"), DhcpRanges: strPtr("10.0.0.10-10.0.0.20"),
					DhcpReservedRanges: strPtr("10.0.0.15"), StaticIpPoolRange: strPtr("10.0.0.30-10.0.0.40"),
					UnmanagedIps: strPtr("10.0.0.50"), VlanExtension: &model.VpcSubnetVlanExtensionStatus{VlanId: int64Ptr(42)}},
				{GatewayAddress: strPtr("2001:db8::1"), IpAddressType: strPtr("IPV6")},
			}}, nil
		}
	}}
	client := &nsx.Client{SubnetsClient: &fakeSubnetsClient{results: model.VpcSubnetListResult{Results: subnets}}, SubnetStatusClient: statusClient}
	result, err := NewSubnetStatusStorage(client, newFakeK8sClient(objects...)).List(context.Background(), "ns1")
	require.NoError(t, err)
	assert.Equal(t, "SubnetStatusList", result.Kind)
	assert.Equal(t, easv1alpha1.GroupVersion.String(), result.APIVersion)
	assert.ElementsMatch(t, []easv1alpha1.SubnetStatus{
		{TypeMeta: metav1.TypeMeta{APIVersion: easv1alpha1.GroupVersion.String(), Kind: "SubnetStatus"},
			ObjectMeta: metav1.ObjectMeta{Name: "dual-stack", Namespace: "ns1"}, GatewayAddresses: []string{"10.0.0.1", "2001:db8::1"}},
		{TypeMeta: metav1.TypeMeta{APIVersion: easv1alpha1.GroupVersion.String(), Kind: "SubnetStatus"},
			ObjectMeta: metav1.ObjectMeta{Name: "l2-only", Namespace: "ns1"}},
	}, result.Items)
	assert.ElementsMatch(t, [][]string{
		{"default", "project", "shared-vpc", "dual-stack"}, {"default", "project", "shared-vpc", "l2-only"},
		{"default", "project", "shared-vpc", "deleted-status"},
	}, statusClient.calls)
}

type subnetStatusK8sListClient struct {
	k8sclient.Client
	list func(context.Context, k8sclient.ObjectList, ...k8sclient.ListOption) error
}

func (c subnetStatusK8sListClient) List(ctx context.Context, list k8sclient.ObjectList, opts ...k8sclient.ListOption) error {
	return c.list(ctx, list, opts...)
}

func TestSubnetStatusStorage_List_PropagatesErrors(t *testing.T) {
	for _, stage := range []string{"Kubernetes list", "NSX subnet list", "NSX status"} {
		t.Run(stage, func(t *testing.T) {
			backendErr := fmt.Errorf("%s failed", stage)
			k8s := newFakeK8sClient(subnetStatusCR("sub1", "ns1", "uid1"))
			subnets := &fakeSubnetsClient{results: model.VpcSubnetListResult{Results: []model.VpcSubnet{{
				Id: strPtr("nsx-id"), Tags: subnetOwnershipTags("sub1", "uid1", "ns1"),
			}}}}
			statusClient := &fakeSubnetStatusClient{list: func(_, _, _, _ string) (model.VpcSubnetStatusListResult, error) {
				return model.VpcSubnetStatusListResult{}, backendErr
			}}
			if stage == "Kubernetes list" {
				k8s = subnetStatusK8sListClient{Client: k8s, list: func(context.Context, k8sclient.ObjectList, ...k8sclient.ListOption) error { return backendErr }}
			}
			if stage == "NSX subnet list" {
				subnets.err = backendErr
			}
			result, err := NewSubnetStatusStorage(&nsx.Client{SubnetsClient: subnets, SubnetStatusClient: statusClient}, k8s).List(context.Background(), "ns1")
			assert.Nil(t, result)
			assert.ErrorIs(t, err, backendErr)
			if stage != "NSX status" {
				assert.Empty(t, statusClient.calls)
			}
		})
	}
}

func TestSubnetStatusStorage_ContextCanceled(t *testing.T) {
	for _, stage := range []string{"before request", "during CR list", "during NSX subnet lookup", "during NSX status"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			k8s := newFakeK8sClient(subnetStatusCR("sub1", "ns1", "uid1"))
			subnets := &fakeSubnetsClient{list: func(_, _, _ string, _ *string) (model.VpcSubnetListResult, error) {
				if stage == "during NSX subnet lookup" {
					cancel()
				}
				return model.VpcSubnetListResult{Results: []model.VpcSubnet{{Id: strPtr("nsx-id"), Tags: subnetOwnershipTags("sub1", "uid1", "ns1")}}}, nil
			}}
			statusClient := &fakeSubnetStatusClient{list: func(_, _, _, _ string) (model.VpcSubnetStatusListResult, error) {
				cancel()
				return model.VpcSubnetStatusListResult{}, nil
			}}
			if stage == "before request" {
				cancel()
			}
			if stage == "during CR list" {
				base := k8s
				k8s = subnetStatusK8sListClient{Client: base, list: func(ctx context.Context, list k8sclient.ObjectList, opts ...k8sclient.ListOption) error {
					err := base.List(ctx, list, opts...)
					cancel()
					return err
				}}
			}
			store := NewSubnetStatusStorage(&nsx.Client{SubnetsClient: subnets, SubnetStatusClient: statusClient}, k8s)
			if stage == "before request" {
				_, err := store.Get(ctx, "ns1", "sub1")
				assert.ErrorIs(t, err, context.Canceled)
			}
			_, err := store.List(ctx, "ns1")
			assert.ErrorIs(t, err, context.Canceled)
			if stage != "during NSX status" {
				assert.Empty(t, statusClient.calls)
			}
		})
	}
}
