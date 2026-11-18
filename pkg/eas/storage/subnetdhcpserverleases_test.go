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
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type fakeSubnetDHCPLeasesClient struct {
	t       *testing.T
	list    func(subnetID string, cursor *string) (model.DhcpLeasesResult, error)
	calls   [][]string
	cursors []string
}

func (f *fakeSubnetDHCPLeasesClient) List(org, project, vpc, subnetID string, address, cursor *string, conflicts, deleted *bool, fields *string, pageSize *int64, ascending *bool, sortBy *string) (model.DhcpLeasesResult, error) {
	f.calls = append(f.calls, []string{org, project, vpc, subnetID})
	f.cursors = append(f.cursors, DerefString(cursor))
	assert.Nil(f.t, address)
	assert.Nil(f.t, conflicts)
	assert.Nil(f.t, deleted)
	assert.Nil(f.t, fields)
	assert.Nil(f.t, pageSize)
	assert.Nil(f.t, ascending)
	assert.Nil(f.t, sortBy)
	return f.list(subnetID, cursor)
}

func dhcpLeaseNSXClient(leases *fakeSubnetDHCPLeasesClient) *nsx.Client {
	return &nsx.Client{
		SubnetsClient: &fakeSubnetsClient{results: model.VpcSubnetListResult{Results: []model.VpcSubnet{{
			Id: strPtr("nsx-subnet"), Tags: subnetOwnershipTags("sub1", "uid1", "ns1"),
		}}}},
		SubnetDHCPServerLeasesClient: leases,
	}
}

func TestConvertDhcpServerLeases(t *testing.T) {
	leases := &model.DhcpLeasesResult{
		Leases: []model.DhcpLeasePerIP{{
			MacAddress: strPtr("00:50:56:ab:cd:ef"), IpAddress: strPtr("192.0.2.7"), Subnet: strPtr("192.0.2.0"),
			LeaseTime: strPtr("3600"), StartTime: strPtr("2026-11-18 10:00:00"), ExpireTime: strPtr("2026-11-18 11:00:00"),
		}},
		Ipv6Leases: []model.DhcpV6Lease{{
			Duid: strPtr("00:01:00:01:ab:cd:ef:01"), IaType: strPtr("IA_PD"), Iaid: int64Ptr(0x123456789),
			IpAddresses: []string{"2001:db8:1::/64", "2001:db8:2::/64"},
			LeaseTime:   strPtr("7200"), StartTime: strPtr("raw-start-time"), ExpireTime: strPtr("raw-expire-time"),
		}},
	}
	result := ConvertDhcpServerLeases(leases, "subnet-cr", "ns1")
	assert.Equal(t, &easv1alpha1.SubnetDHCPServerLeases{
		TypeMeta:   metav1.TypeMeta{APIVersion: easv1alpha1.GroupVersion.String(), Kind: "SubnetDHCPServerLeases"},
		ObjectMeta: metav1.ObjectMeta{Name: "subnet-cr", Namespace: "ns1"},
		Leases: []easv1alpha1.DHCPLease{{
			MACAddress: "00:50:56:ab:cd:ef", IPAddress: "192.0.2.7", Subnet: "192.0.2.0", LeaseTime: "3600",
			StartTime: "2026-11-18 10:00:00", ExpireTime: "2026-11-18 11:00:00",
		}},
		IPv6Leases: []easv1alpha1.DHCPv6Lease{{
			DUID: "00:01:00:01:ab:cd:ef:01", IAType: "IA_PD", IAID: 0x123456789,
			IPAddresses: []string{"2001:db8:1::/64", "2001:db8:2::/64"},
			LeaseTime:   "7200", StartTime: "raw-start-time", ExpireTime: "raw-expire-time",
		}},
	}, result)
	leases.Ipv6Leases[0].IpAddresses[0] = "changed-source-address"
	assert.Equal(t, "2001:db8:1::/64", result.IPv6Leases[0].IPAddresses[0])
}

func TestConvertDhcpServerLeases_OptionalFields(t *testing.T) {
	for _, source := range []*model.DhcpLeasesResult{nil, {}} {
		result := ConvertDhcpServerLeases(source, "sub1", "ns1")
		require.NotNil(t, result)
		assert.Equal(t, metav1.TypeMeta{APIVersion: easv1alpha1.GroupVersion.String(), Kind: "SubnetDHCPServerLeases"}, result.TypeMeta)
		assert.Equal(t, metav1.ObjectMeta{Name: "sub1", Namespace: "ns1"}, result.ObjectMeta)
		assert.Empty(t, result.Leases)
		assert.Empty(t, result.IPv6Leases)
	}
	result := ConvertDhcpServerLeases(&model.DhcpLeasesResult{
		Leases:     []model.DhcpLeasePerIP{{}, {MacAddress: strPtr(""), IpAddress: strPtr(""), Subnet: strPtr(""), LeaseTime: strPtr(""), StartTime: strPtr(""), ExpireTime: strPtr("")}},
		Ipv6Leases: []model.DhcpV6Lease{{}, {Duid: strPtr(""), IaType: strPtr(""), Iaid: int64Ptr(0), LeaseTime: strPtr(""), StartTime: strPtr(""), ExpireTime: strPtr("")}},
	}, "sub1", "ns1")
	assert.Equal(t, []easv1alpha1.DHCPLease{{}, {}}, result.Leases)
	assert.Equal(t, []easv1alpha1.DHCPv6Lease{{}, {}}, result.IPv6Leases)
}

func TestSubnetDHCPServerLeasesStorage_Get_SharedVPCIPv6Only(t *testing.T) {
	for target := range 3 {
		t.Run(fmt.Sprintf("ns-%d", target+1), func(t *testing.T) {
			var objects []k8sclient.Object
			var subnets []model.VpcSubnet
			// The requested CR's subnet is last, behind two identical CR names.
			for offset := 1; offset <= 3; offset++ {
				i := (target + offset) % 3
				namespace, uid := fmt.Sprintf("ns-%d", i+1), fmt.Sprintf("uid-%d", i+1)
				objects = append(objects, &vpcv1alpha1.Subnet{
					ObjectMeta: metav1.ObjectMeta{Name: "same-name", Namespace: namespace, UID: types.UID(uid)},
					Spec:       vpcv1alpha1.SubnetSpec{VPCName: "project:shared-vpc"},
				})
				subnets = append(subnets, model.VpcSubnet{
					Id: strPtr(fmt.Sprintf("nsx-subnet-%d", i+1)), Tags: subnetOwnershipTags("same-name", uid, namespace),
					// No IPv4 DHCP config: IPv6-only DHCP must remain queryable.
					SubnetDhcpv6Config: &model.SubnetDhcpv6Config{Mode: strPtr("DHCP_SERVER")},
				})
			}
			backend := &fakeSubnetDHCPLeasesClient{t: t, list: func(id string, cursor *string) (model.DhcpLeasesResult, error) {
				assert.Nil(t, cursor)
				return model.DhcpLeasesResult{Ipv6Leases: []model.DhcpV6Lease{{Duid: strPtr(id)}}}, nil
			}}
			client := &nsx.Client{SubnetsClient: &fakeSubnetsClient{results: model.VpcSubnetListResult{Results: subnets}}, SubnetDHCPServerLeasesClient: backend}
			namespace, id := fmt.Sprintf("ns-%d", target+1), fmt.Sprintf("nsx-subnet-%d", target+1)
			result, err := NewSubnetDHCPServerLeasesStorage(client, newFakeK8sClient(objects...)).Get(context.Background(), namespace, "same-name")
			require.NoError(t, err)
			assert.Equal(t, "same-name", result.Name)
			assert.Equal(t, namespace, result.Namespace)
			assert.Equal(t, []easv1alpha1.DHCPv6Lease{{DUID: id}}, result.IPv6Leases)
			assert.Equal(t, [][]string{{"default", "project", "shared-vpc", id}}, backend.calls)
		})
	}
}

func TestSubnetDHCPServerLeasesStorage_Get_PrecreatedSharedSubnet(t *testing.T) {
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
			}, nil
		},
		list: func(_, _, _ string, _ *string) (model.VpcSubnetListResult, error) {
			t.Fatal("shared lease lookup must use the annotated resource instead of managed subnet listing")
			return model.VpcSubnetListResult{}, nil
		},
	}
	backend := &fakeSubnetDHCPLeasesClient{t: t, list: func(_ string, cursor *string) (model.DhcpLeasesResult, error) {
		assert.Nil(t, cursor)
		return model.DhcpLeasesResult{
			Leases: []model.DhcpLeasePerIP{{
				MacAddress: strPtr("00:50:56:ab:cd:ef"), IpAddress: strPtr("192.0.2.7"), Subnet: strPtr("192.0.2.0"),
				LeaseTime: strPtr("3600"), StartTime: strPtr("ipv4-start"), ExpireTime: strPtr("ipv4-expire"),
			}},
			Ipv6Leases: []model.DhcpV6Lease{{
				Duid: strPtr("duid-shared"), IaType: strPtr("IA_NA"), Iaid: int64Ptr(42), IpAddresses: []string{"2001:db8::7"},
				LeaseTime: strPtr("7200"), StartTime: strPtr("ipv6-start"), ExpireTime: strPtr("ipv6-expire"),
			}},
		}, nil
	}}
	store := NewSubnetDHCPServerLeasesStorage(&nsx.Client{SubnetsClient: subnets, SubnetDHCPServerLeasesClient: backend}, newFakeK8sClient(objects...))
	for _, namespace := range []string{"ns1", "ns2"} {
		result, err := store.Get(context.Background(), namespace, "shared-alias")
		require.NoError(t, err)
		assert.Equal(t, &easv1alpha1.SubnetDHCPServerLeases{
			TypeMeta:   metav1.TypeMeta{APIVersion: easv1alpha1.GroupVersion.String(), Kind: "SubnetDHCPServerLeases"},
			ObjectMeta: metav1.ObjectMeta{Name: "shared-alias", Namespace: namespace},
			Leases: []easv1alpha1.DHCPLease{{
				MACAddress: "00:50:56:ab:cd:ef", IPAddress: "192.0.2.7", Subnet: "192.0.2.0", LeaseTime: "3600",
				StartTime: "ipv4-start", ExpireTime: "ipv4-expire",
			}},
			IPv6Leases: []easv1alpha1.DHCPv6Lease{{
				DUID: "duid-shared", IAType: "IA_NA", IAID: 42, IPAddresses: []string{"2001:db8::7"}, LeaseTime: "7200",
				StartTime: "ipv6-start", ExpireTime: "ipv6-expire",
			}},
		}, result)
	}
	wantCalls := [][]string{
		{"default", "project", "shared-vpc", "precreated-source"},
		{"default", "project", "shared-vpc", "precreated-source"},
	}
	assert.Equal(t, wantCalls, subnetGetCalls)
	assert.Equal(t, wantCalls, backend.calls)
}

func TestSubnetDHCPServerLeasesStorage_Get_RejectsMissingOrForeignCR(t *testing.T) {
	for _, tc := range []struct {
		name    string
		missing bool
		tags    []model.Tag
	}{
		{name: "missing CR", missing: true},
		{name: "stale UID", tags: subnetOwnershipTags("sub1", "old-uid", "ns1")},
		{name: "foreign namespace", tags: subnetOwnershipTags("sub1", "uid1", "ns2")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var objects []k8sclient.Object
			if !tc.missing {
				objects = append(objects, subnetStatusCR("sub1", "ns1", "uid1"))
			}
			backend := &fakeSubnetDHCPLeasesClient{t: t}
			client := &nsx.Client{
				SubnetsClient:                &fakeSubnetsClient{results: model.VpcSubnetListResult{Results: []model.VpcSubnet{{Id: strPtr("sub1"), Tags: tc.tags}}}},
				SubnetDHCPServerLeasesClient: backend,
			}
			result, err := NewSubnetDHCPServerLeasesStorage(client, newFakeK8sClient(objects...)).Get(context.Background(), "ns1", "sub1")
			assert.Nil(t, result)
			assert.True(t, k8serrors.IsNotFound(err), "got %v", err)
			assert.Empty(t, backend.calls)
		})
	}
}

func TestSubnetDHCPServerLeasesStorage_Get_AllLeasePages(t *testing.T) {
	backend := &fakeSubnetDHCPLeasesClient{t: t, list: func(_ string, cursor *string) (model.DhcpLeasesResult, error) {
		switch DerefString(cursor) {
		case "":
			return model.DhcpLeasesResult{Cursor: strPtr("page-2"), Leases: []model.DhcpLeasePerIP{{IpAddress: strPtr("192.0.2.1")}}}, nil
		case "page-2":
			return model.DhcpLeasesResult{Cursor: strPtr("page-3"), Ipv6Leases: []model.DhcpV6Lease{{Duid: strPtr("duid-1"), IpAddresses: []string{"2001:db8::1", "2001:db8::2"}}}}, nil
		case "page-3":
			return model.DhcpLeasesResult{Cursor: strPtr(""), Leases: []model.DhcpLeasePerIP{{IpAddress: strPtr("192.0.2.2")}}, Ipv6Leases: []model.DhcpV6Lease{{Duid: strPtr("duid-2")}}}, nil
		default:
			t.Fatalf("unexpected lease cursor %q", DerefString(cursor))
			return model.DhcpLeasesResult{}, nil
		}
	}}
	store := NewSubnetDHCPServerLeasesStorage(dhcpLeaseNSXClient(backend), newFakeK8sClient(subnetStatusCR("sub1", "ns1", "uid1")))
	result, err := store.Get(context.Background(), "ns1", "sub1")
	require.NoError(t, err)
	assert.Equal(t, []easv1alpha1.DHCPLease{{IPAddress: "192.0.2.1"}, {IPAddress: "192.0.2.2"}}, result.Leases)
	assert.Equal(t, []easv1alpha1.DHCPv6Lease{{DUID: "duid-1", IPAddresses: []string{"2001:db8::1", "2001:db8::2"}}, {DUID: "duid-2"}}, result.IPv6Leases)
	assert.Equal(t, []string{"", "page-2", "page-3"}, backend.cursors)
	assert.Equal(t, [][]string{
		{"default", "project", "shared-vpc", "nsx-subnet"}, {"default", "project", "shared-vpc", "nsx-subnet"}, {"default", "project", "shared-vpc", "nsx-subnet"},
	}, backend.calls)
}

func TestSubnetDHCPServerLeasesStorage_Get_RepeatedCursor(t *testing.T) {
	backend := &fakeSubnetDHCPLeasesClient{t: t, list: func(_ string, _ *string) (model.DhcpLeasesResult, error) {
		return model.DhcpLeasesResult{Cursor: strPtr("repeated"), Leases: []model.DhcpLeasePerIP{{IpAddress: strPtr("192.0.2.1")}}}, nil
	}}
	result, err := NewSubnetDHCPServerLeasesStorage(dhcpLeaseNSXClient(backend), newFakeK8sClient(subnetStatusCR("sub1", "ns1", "uid1"))).
		Get(context.Background(), "ns1", "sub1")
	assert.Nil(t, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cursor")
	assert.Len(t, backend.calls, 2)
}

func TestSubnetDHCPServerLeasesStorage_Get_BackendErrors(t *testing.T) {
	backendErr := fmt.Errorf("DHCP leases backend unavailable")
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "not found", err: vapierrors.NotFound{}},
		{name: "forbidden", err: vapierrors.Unauthorized{}},
		{name: "backend failure", err: backendErr},
	} {
		for _, laterPage := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/laterPage=%t", tc.name, laterPage), func(t *testing.T) {
				backend := &fakeSubnetDHCPLeasesClient{t: t, list: func(_ string, cursor *string) (model.DhcpLeasesResult, error) {
					if laterPage && cursor == nil {
						return model.DhcpLeasesResult{Cursor: strPtr("page-2"), Leases: []model.DhcpLeasePerIP{{IpAddress: strPtr("192.0.2.1")}}}, nil
					}
					return model.DhcpLeasesResult{}, tc.err
				}}
				result, err := NewSubnetDHCPServerLeasesStorage(dhcpLeaseNSXClient(backend), newFakeK8sClient(subnetStatusCR("sub1", "ns1", "uid1"))).
					Get(context.Background(), "ns1", "sub1")
				assert.Nil(t, result, "a later page error must not return partial leases")
				switch tc.name {
				case "not found":
					assert.True(t, k8serrors.IsNotFound(err), "got %v", err)
				case "forbidden":
					assert.True(t, k8serrors.IsForbidden(err), "got %v", err)
				default:
					assert.ErrorIs(t, err, backendErr)
				}
				if laterPage {
					assert.Equal(t, []string{"", "page-2"}, backend.cursors)
				}
			})
		}
	}
}

func TestSubnetDHCPServerLeasesStorage_Get_ContextCanceled(t *testing.T) {
	for _, stage := range []string{"before request", "during subnet lookup", "during lease lookup"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backend := &fakeSubnetDHCPLeasesClient{t: t, list: func(_ string, _ *string) (model.DhcpLeasesResult, error) {
				cancel()
				return model.DhcpLeasesResult{Cursor: strPtr("page-2"), Leases: []model.DhcpLeasePerIP{{IpAddress: strPtr("192.0.2.1")}}}, nil
			}}
			client := dhcpLeaseNSXClient(backend)
			if stage == "before request" {
				cancel()
			}
			if stage == "during subnet lookup" {
				client.SubnetsClient = &fakeSubnetsClient{list: func(_, _, _ string, _ *string) (model.VpcSubnetListResult, error) {
					cancel()
					return model.VpcSubnetListResult{Results: []model.VpcSubnet{{Id: strPtr("nsx-subnet"), Tags: subnetOwnershipTags("sub1", "uid1", "ns1")}}}, nil
				}}
			}
			result, err := NewSubnetDHCPServerLeasesStorage(client, newFakeK8sClient(subnetStatusCR("sub1", "ns1", "uid1"))).Get(ctx, "ns1", "sub1")
			assert.Nil(t, result)
			assert.ErrorIs(t, err, context.Canceled)
			if stage == "during lease lookup" {
				assert.Len(t, backend.calls, 1)
			} else {
				assert.Empty(t, backend.calls)
			}
		})
	}
}
