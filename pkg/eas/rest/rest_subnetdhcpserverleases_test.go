/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package rest

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	vapierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/vpcs"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/vpcs/subnets/state"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/endpoints/request"
	apirest "k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/utils/ptr"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas/storage"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type restDHCPServerLeasesSubnetsClient struct {
	vpcs.SubnetsClient
	results []model.VpcSubnet
}

func (c *restDHCPServerLeasesSubnetsClient) List(_, _, _ string, _ *string, _, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.VpcSubnetListResult, error) {
	return model.VpcSubnetListResult{Results: c.results}, nil
}

type restDHCPServerLeasesCall struct {
	orgID, projectID, vpcID, subnetID string
	cursor                            *string
}

type restDHCPServerLeasesClient struct {
	state.DhcpServerLeasesClient
	results map[string]model.DhcpLeasesResult
	errs    map[string]error
	calls   []restDHCPServerLeasesCall
}

func (c *restDHCPServerLeasesClient) List(orgID, projectID, vpcID, subnetID string, _ *string, cursor *string, _, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.DhcpLeasesResult, error) {
	c.calls = append(c.calls, restDHCPServerLeasesCall{orgID: orgID, projectID: projectID, vpcID: vpcID, subnetID: subnetID, cursor: cursor})
	return c.results[subnetID], c.errs[subnetID]
}

func newSubnetDHCPServerLeasesREST() (*subnetDHCPServerLeasesStorage, *restDHCPServerLeasesClient) {
	var objects []k8sclient.Object
	var nsxSubnets []model.VpcSubnet
	for _, namespace := range []string{"ns1", "ns2"} {
		uid := "uid-" + namespace
		objects = append(objects, &vpcv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{Name: "same-name", Namespace: namespace, UID: types.UID(uid)},
			Spec:       vpcv1alpha1.SubnetSpec{VPCName: "p1:shared-vpc"},
		})
		nsxSubnets = append(nsxSubnets, model.VpcSubnet{
			Id: ptr.To("subnet-" + namespace),
			Tags: []model.Tag{
				{Scope: ptr.To(common.TagScopeSubnetCRUID), Tag: ptr.To(uid)},
				{Scope: ptr.To(common.TagScopeVMNamespace), Tag: ptr.To(namespace)},
			},
		})
	}
	client := &restDHCPServerLeasesClient{
		results: map[string]model.DhcpLeasesResult{
			"subnet-ns1": {
				Leases:     []model.DhcpLeasePerIP{{MacAddress: ptr.To("00:11:22:33:44:01"), IpAddress: ptr.To("10.0.1.2")}},
				Ipv6Leases: []model.DhcpV6Lease{{Duid: ptr.To("duid-ns1"), IpAddresses: []string{"2001:db8:1::2"}}},
			},
			"subnet-ns2": {
				Leases:     []model.DhcpLeasePerIP{{MacAddress: ptr.To("00:11:22:33:44:02"), IpAddress: ptr.To("10.0.2.2")}},
				Ipv6Leases: []model.DhcpV6Lease{{Duid: ptr.To("duid-ns2"), IpAddresses: []string{"2001:db8:2::2"}}},
			},
		},
		errs: make(map[string]error),
	}
	store := storage.NewSubnetDHCPServerLeasesStorage(&nsx.Client{
		SubnetsClient:                &restDHCPServerLeasesSubnetsClient{results: nsxSubnets},
		SubnetDHCPServerLeasesClient: client,
	}, newTestFakeK8sClient().WithObjects(objects...).Build())
	return NewSubnetDHCPServerLeasesStorage(store), client
}

func TestSubnetDHCPServerLeasesStorage_Metadata(t *testing.T) {
	r := NewSubnetDHCPServerLeasesStorage(&storage.SubnetDHCPServerLeasesStorage{})
	assert.IsType(t, &easv1alpha1.SubnetDHCPServerLeases{}, r.New())
	assert.True(t, r.NamespaceScoped())
	assert.Equal(t, "subnetdhcpserverleases", r.GetSingularName())
	_, canList := interface{}(r).(apirest.Lister)
	assert.False(t, canList, "SubnetDHCPServerLeases only supports Get")
	r.Destroy()
}

func TestSubnetDHCPServerLeasesStorage_Get_CRNameAndNamespace(t *testing.T) {
	r, client := newSubnetDHCPServerLeasesREST()
	for _, tc := range []struct {
		namespace, ipv4Address, ipv6Address string
	}{
		{"ns1", "10.0.1.2", "2001:db8:1::2"},
		{"ns2", "10.0.2.2", "2001:db8:2::2"},
	} {
		object, err := r.Get(request.WithNamespace(context.Background(), tc.namespace), "same-name", &metav1.GetOptions{})
		require.NoError(t, err)
		leases := object.(*easv1alpha1.SubnetDHCPServerLeases)
		assert.Equal(t, "same-name", leases.Name)
		assert.Equal(t, tc.namespace, leases.Namespace)
		require.Len(t, leases.Leases, 1)
		assert.Equal(t, tc.ipv4Address, leases.Leases[0].IPAddress)
		require.Len(t, leases.IPv6Leases, 1)
		assert.Equal(t, "duid-"+tc.namespace, leases.IPv6Leases[0].DUID)
		assert.Equal(t, []string{tc.ipv6Address}, leases.IPv6Leases[0].IPAddresses)
	}
	assert.Equal(t, []restDHCPServerLeasesCall{
		{orgID: "default", projectID: "p1", vpcID: "shared-vpc", subnetID: "subnet-ns1"},
		{orgID: "default", projectID: "p1", vpcID: "shared-vpc", subnetID: "subnet-ns2"},
	}, client.calls)
	for _, tc := range []struct{ namespace, name string }{
		{"ns1", "subnet-ns1"},
		{"ns1", "missing"},
		{"unmapped", "same-name"},
	} {
		_, err := r.Get(request.WithNamespace(context.Background(), tc.namespace), tc.name, nil)
		assert.True(t, k8serrors.IsNotFound(err), "Get requires a Subnet CR in the requested namespace")
	}
}

func TestSubnetDHCPServerLeasesStorage_Get_Errors(t *testing.T) {
	failure := errors.New("DHCP leases unavailable")
	for _, tc := range []struct {
		name    string
		err     error
		matches func(error) bool
	}{
		{"not found", vapierrors.NotFound{}, k8serrors.IsNotFound},
		{"forbidden", vapierrors.Unauthorized{}, k8serrors.IsForbidden},
		{"other backend error", failure, func(err error) bool { return errors.Is(err, failure) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, client := newSubnetDHCPServerLeasesREST()
			client.errs["subnet-ns1"] = tc.err
			_, err := r.Get(request.WithNamespace(context.Background(), "ns1"), "same-name", nil)
			require.Error(t, err)
			assert.True(t, tc.matches(err), "unexpected error: %v", err)
		})
	}
}

func TestSubnetDHCPServerLeasesStorage_Get_Cancelled(t *testing.T) {
	r, client := newSubnetDHCPServerLeasesREST()
	ctx, cancel := context.WithCancel(request.WithNamespace(context.Background(), "ns1"))
	cancel()
	_, err := r.Get(ctx, "same-name", nil)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, client.calls)
}

func TestSubnetDHCPServerLeasesStorage_ConvertToTable(t *testing.T) {
	r := NewSubnetDHCPServerLeasesStorage(&storage.SubnetDHCPServerLeasesStorage{})
	for _, tc := range []struct {
		name       string
		ipv4Leases []easv1alpha1.DHCPLease
		ipv6Leases []easv1alpha1.DHCPv6Lease
	}{
		{name: "no leases"},
		{
			name:       "IPv4 leases",
			ipv4Leases: []easv1alpha1.DHCPLease{{IPAddress: "10.0.0.2"}, {IPAddress: "10.0.0.3"}},
		},
		{
			name:       "IPv6 leases",
			ipv6Leases: []easv1alpha1.DHCPv6Lease{{DUID: "duid-1", IPAddresses: []string{"2001:db8::2", "2001:db8::3"}}},
		},
		{
			name:       "dual stack leases",
			ipv4Leases: []easv1alpha1.DHCPLease{{IPAddress: "10.0.0.2"}},
			ipv6Leases: []easv1alpha1.DHCPv6Lease{{DUID: "duid-1"}, {DUID: "duid-2"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			leases := &easv1alpha1.SubnetDHCPServerLeases{
				ObjectMeta: metav1.ObjectMeta{Name: "subnet-1", Namespace: "ns1"},
				Leases:     tc.ipv4Leases,
				IPv6Leases: tc.ipv6Leases,
			}
			table, err := r.ConvertToTable(context.Background(), leases, nil)
			require.NoError(t, err)
			assert.Equal(t, subnetDHCPServerLeasesColumns, table.ColumnDefinitions)
			require.Len(t, table.Rows, 1)
			assert.Equal(t, []interface{}{"subnet-1", int64(len(tc.ipv4Leases)), int64(len(tc.ipv6Leases))}, table.Rows[0].Cells)
			metadata := table.Rows[0].Object.Object.(*metav1.PartialObjectMetadata)
			assert.Equal(t, "subnet-1", metadata.Name)
			assert.Equal(t, "ns1", metadata.Namespace)
		})
	}
	_, err := r.ConvertToTable(context.Background(), &easv1alpha1.SubnetStatus{}, nil)
	assert.ErrorContains(t, err, "unsupported type")
}
