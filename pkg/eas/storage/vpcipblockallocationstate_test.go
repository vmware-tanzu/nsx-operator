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
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type fakeVPCIPBlockAllocationStateClient struct {
	list  func(string, string, string, *string, *bool, *string) (model.IpAddressBlockAllocationStateList, error)
	calls [][]string
}

func (f *fakeVPCIPBlockAllocationStateClient) List(org, project, vpc string, cidr *string, workload *bool, path *string) (model.IpAddressBlockAllocationStateList, error) {
	f.calls = append(f.calls, []string{org, project, vpc})
	return f.list(org, project, vpc, cidr, workload, path)
}

func allocationStateBlock(path string, cidrs ...string) model.IpAddressBlockAllocationState {
	block := model.IpAddressBlockAllocationState{
		IpBlockPath: strPtr(path), DisplayName: strPtr("display-name-is-not-the-ID"),
		Visibility: strPtr("EXTERNAL"), IpAddressType: strPtr("IPV4"),
	}
	for _, cidr := range cidrs {
		block.AllocationStates = append(block.AllocationStates, model.IpAddressBlockAllocationStateForCidr{Cidr: strPtr(cidr)})
	}
	return block
}

func allocationStateVPCProvider() singleVPCProvider {
	return singleVPCProvider{info: common.VPCResourceInfo{OrgID: "default", ProjectID: "project", VPCID: "vpc1"}}
}

func allocationStateCR(name, namespace, allocated string) *vpcv1alpha1.IPAddressAllocation {
	return &vpcv1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Status:     vpcv1alpha1.IPAddressAllocationStatus{AllocationIPs: allocated},
	}
}

func TestVPCIPBlockAllocationStateStorage_Get_IPv4Associations(t *testing.T) {
	specOnly := allocationStateCR("pending-spec", "ns1", "")
	specOnly.Spec.AllocationIPs = "192.0.2.9"
	differentStatus := allocationStateCR("realized-status", "ns1", "192.0.2.11")
	differentStatus.Spec.AllocationIPs = "192.0.2.10"
	objects := []k8sclient.Object{
		allocationStateCR("prefix-allocation", "ns1", "192.0.2.128/27"),
		allocationStateCR("single-ip", "ns1", "192.0.2.7"),
		allocationStateCR("foreign-namespace", "ns2", "192.0.2.8"),
		specOnly, differentStatus,
		allocationStateCR("containing-prefix", "ns1", "192.0.2.0/24"),
		allocationStateCR("duplicate-one", "ns1", "192.0.2.12"),
		allocationStateCR("duplicate-two", "ns1", "192.0.2.12/32"),
		allocationStateCR("invalid-status", "ns1", "invalid-IP"),
	}
	block := allocationStateBlock("/orgs/default/projects/project/infra/ip-blocks/block-id",
		"192.0.2.128/27", "192.0.2.7/32", "192.0.2.8/32", "192.0.2.9/32", "192.0.2.10/32", "192.0.2.11/32", "192.0.2.64/26", "192.0.2.12/32", "invalid-IP")
	block.AllocationStates = append(block.AllocationStates, model.IpAddressBlockAllocationStateForCidr{}, model.IpAddressBlockAllocationStateForCidr{Cidr: strPtr("")})
	backend := &fakeVPCIPBlockAllocationStateClient{list: func(org, project, vpc string, cidr *string, workload *bool, path *string) (model.IpAddressBlockAllocationStateList, error) {
		assert.Nil(t, cidr)
		assert.Nil(t, workload)
		assert.Nil(t, path)
		return model.IpAddressBlockAllocationStateList{Results: []model.IpAddressBlockAllocationState{block}}, nil
	}}
	store := NewVPCIPBlockAllocationStateStorage(&nsx.Client{VPCIPBlockAllocationStateClient: backend},
		singleVPCProvider{info: common.VPCResourceInfo{OrgID: "default", ProjectID: "project", VPCID: "vpc1"}}, newFakeK8sClient(objects...))
	result, err := store.Get(context.Background(), "ns1", "block-id")
	require.NoError(t, err)
	assert.Equal(t, &easv1alpha1.VPCIPBlockAllocationState{
		TypeMeta:      metav1.TypeMeta{APIVersion: easv1alpha1.GroupVersion.String(), Kind: "VPCIPBlockAllocationState"},
		ObjectMeta:    metav1.ObjectMeta{Name: "block-id", Namespace: "ns1"},
		Visibility:    easv1alpha1.External,
		IPAddressType: easv1alpha1.IPv4,
		AllocationStates: []easv1alpha1.VPCIPAddressBlockAllocationStateForCIDR{
			{CIDR: "192.0.2.128/27", VPCIPAddressAllocationName: "prefix-allocation"},
			{CIDR: "192.0.2.7/32", VPCIPAddressAllocationName: "single-ip"},
			{CIDR: "192.0.2.8/32"}, {CIDR: "192.0.2.9/32"}, {CIDR: "192.0.2.10/32"},
			{CIDR: "192.0.2.11/32", VPCIPAddressAllocationName: "realized-status"},
			{CIDR: "192.0.2.64/26"}, {CIDR: "192.0.2.12/32"}, {CIDR: "invalid-IP"},
		},
	}, result)
	assert.Equal(t, [][]string{{"default", "project", "vpc1"}}, backend.calls)
}

func TestVPCIPBlockAllocationStateStorage_Get_IPv6(t *testing.T) {
	block := allocationStateBlock("/infra/ip-blocks/ipv6-block", "2001:db8::7/128", "2001:db8:1234::/64", "2001:db8::8")
	block.IpAddressType, block.Visibility = strPtr("IPV6"), strPtr("PRIVATE")
	backend := &fakeVPCIPBlockAllocationStateClient{list: func(_, _, _ string, _ *string, _ *bool, _ *string) (model.IpAddressBlockAllocationStateList, error) {
		return model.IpAddressBlockAllocationStateList{Results: []model.IpAddressBlockAllocationState{block}}, nil
	}}
	store := NewVPCIPBlockAllocationStateStorage(&nsx.Client{VPCIPBlockAllocationStateClient: backend}, allocationStateVPCProvider(), newFakeK8sClient(
		allocationStateCR("single-v6", "ns1", "2001:0DB8:0000:0000:0000:0000:0000:0007"),
		allocationStateCR("prefix-v6", "ns1", "2001:0DB8:1234:0000::/64"),
		allocationStateCR("bare-backend-ip", "ns1", "2001:db8::8/128"),
	))
	result, err := store.Get(context.Background(), "ns1", ":ipv6-block")
	require.NoError(t, err)
	assert.Equal(t, easv1alpha1.Private, result.Visibility)
	assert.Equal(t, easv1alpha1.IPv6, result.IPAddressType)
	assert.Equal(t, []easv1alpha1.VPCIPAddressBlockAllocationStateForCIDR{
		{CIDR: "2001:db8::7/128", VPCIPAddressAllocationName: "single-v6"},
		{CIDR: "2001:db8:1234::/64", VPCIPAddressAllocationName: "prefix-v6"},
		{CIDR: "2001:db8::8", VPCIPAddressAllocationName: "bare-backend-ip"},
	}, result.AllocationStates)
}

func TestVPCIPBlockAllocationStateStorage_Get_EmptyAllocations(t *testing.T) {
	block := allocationStateBlock("/infra/ip-blocks/empty-block")
	block.IpAddressType, block.Visibility = nil, nil
	backend := &fakeVPCIPBlockAllocationStateClient{list: func(_, _, _ string, _ *string, _ *bool, _ *string) (model.IpAddressBlockAllocationStateList, error) {
		return model.IpAddressBlockAllocationStateList{Results: []model.IpAddressBlockAllocationState{block}}, nil
	}}
	// No Kubernetes client is needed when there are no allocated CIDRs to associate.
	result, err := NewVPCIPBlockAllocationStateStorage(&nsx.Client{VPCIPBlockAllocationStateClient: backend}, allocationStateVPCProvider(), nil).
		Get(context.Background(), "ns1", ":empty-block")
	require.NoError(t, err)
	assert.Empty(t, result.IPAddressType)
	assert.Empty(t, result.Visibility)
	assert.Equal(t, []easv1alpha1.VPCIPAddressBlockAllocationStateForCIDR{}, result.AllocationStates)
}

type allocationStateK8sClient struct {
	k8sclient.Client
	list func(context.Context, k8sclient.ObjectList, ...k8sclient.ListOption) error
}

func (c allocationStateK8sClient) List(ctx context.Context, list k8sclient.ObjectList, opts ...k8sclient.ListOption) error {
	return c.list(ctx, list, opts...)
}

func TestVPCIPBlockAllocationStateStorage_Get_AggregatesVPCs(t *testing.T) {
	entries := []eas.VPCEntry{
		{Info: common.VPCResourceInfo{OrgID: "org1", ProjectID: "project1", VPCID: "shared-id"}},
		{Info: common.VPCResourceInfo{OrgID: "org1", ProjectID: "project1", VPCID: "shared-id"}},
		{Info: common.VPCResourceInfo{OrgID: "org1", ProjectID: "project2", VPCID: "shared-id"}},
		{Info: common.VPCResourceInfo{OrgID: "org2", ProjectID: "project1", VPCID: "shared-id"}},
	}
	backend := &fakeVPCIPBlockAllocationStateClient{list: func(org, project, _ string, _ *string, _ *bool, _ *string) (model.IpAddressBlockAllocationStateList, error) {
		cidrs := []string{"192.0.2.1/32"}
		if project == "project2" {
			cidrs = []string{"192.0.2.1", "192.0.2.2/32"}
		}
		if org == "org2" {
			cidrs = []string{"192.0.2.3/32"}
		}
		return model.IpAddressBlockAllocationStateList{Results: []model.IpAddressBlockAllocationState{allocationStateBlock("/infra/ip-blocks/block-id", cidrs...)}}, nil
	}}
	base := newFakeK8sClient(allocationStateCR("local", "ns1", "192.0.2.1"), allocationStateCR("foreign", "ns2", "192.0.2.2"))
	listCalls := 0
	k8s := allocationStateK8sClient{Client: base, list: func(ctx context.Context, list k8sclient.ObjectList, opts ...k8sclient.ListOption) error {
		listCalls++
		options := (&k8sclient.ListOptions{}).ApplyOptions(opts)
		assert.Equal(t, "ns1", options.Namespace)
		return base.List(ctx, list, opts...)
	}}
	result, err := NewVPCIPBlockAllocationStateStorage(&nsx.Client{VPCIPBlockAllocationStateClient: backend}, multiVPCProvider{entries: entries}, k8s).
		Get(context.Background(), "ns1", ":block-id")
	require.NoError(t, err)
	assert.ElementsMatch(t, []easv1alpha1.VPCIPAddressBlockAllocationStateForCIDR{
		{CIDR: "192.0.2.1/32", VPCIPAddressAllocationName: "local"}, {CIDR: "192.0.2.2/32"}, {CIDR: "192.0.2.3/32"},
	}, result.AllocationStates)
	assert.Equal(t, 1, listCalls)
	assert.Equal(t, [][]string{{"org1", "project1", "shared-id"}, {"org1", "project2", "shared-id"}, {"org2", "project1", "shared-id"}}, backend.calls)
}

func TestVPCIPBlockAllocationStateStorage_Get_NotFound(t *testing.T) {
	for _, tc := range []struct {
		name     string
		request  string
		provider eas.VPCInfoProvider
		blocks   []model.IpAddressBlockAllocationState
	}{
		{name: "no associated VPC", request: "block-id", provider: emptyVPCProvider{}},
		{name: "no block", request: "block-id", provider: allocationStateVPCProvider()},
		{name: "display name is not ID", request: "block-id", provider: allocationStateVPCProvider(), blocks: []model.IpAddressBlockAllocationState{{IpBlockPath: strPtr("/infra/ip-blocks/another-id"), DisplayName: strPtr("block-id")}}},
		{name: "missing path", request: "block-id", provider: allocationStateVPCProvider(), blocks: []model.IpAddressBlockAllocationState{{DisplayName: strPtr("block-id")}}},
		{name: "infra block requires prefix", request: "block-id", provider: allocationStateVPCProvider(), blocks: []model.IpAddressBlockAllocationState{allocationStateBlock("/infra/ip-blocks/block-id")}},
		{name: "project block rejects prefix", request: ":block-id", provider: allocationStateVPCProvider(), blocks: []model.IpAddressBlockAllocationState{allocationStateBlock("/orgs/default/projects/project/infra/ip-blocks/block-id")}},
		{name: "VPC ID is not block ID", request: "vpc1", provider: allocationStateVPCProvider(), blocks: []model.IpAddressBlockAllocationState{allocationStateBlock("/orgs/default/projects/project/infra/ip-blocks/block-id")}},
		{name: "different project", request: "block-id", provider: allocationStateVPCProvider(), blocks: []model.IpAddressBlockAllocationState{allocationStateBlock("/orgs/default/projects/other-project/infra/ip-blocks/block-id")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &fakeVPCIPBlockAllocationStateClient{list: func(_, _, _ string, _ *string, _ *bool, _ *string) (model.IpAddressBlockAllocationStateList, error) {
				return model.IpAddressBlockAllocationStateList{Results: tc.blocks}, nil
			}}
			// A missing block must return before any Kubernetes allocation lookup.
			result, err := NewVPCIPBlockAllocationStateStorage(&nsx.Client{VPCIPBlockAllocationStateClient: backend}, tc.provider, nil).
				Get(context.Background(), "ns1", tc.request)
			assert.Nil(t, result)
			assert.True(t, k8serrors.IsNotFound(err), "got %v", err)
			if tc.name == "no associated VPC" {
				assert.Empty(t, backend.calls)
			}
		})
	}
}

func TestVPCIPBlockAllocationStateStorage_Get_RequiresNamespaceAndName(t *testing.T) {
	for _, tc := range []struct{ namespace, name string }{
		{"", "block-id"}, {"ns1", ""}, {"ns1", ":"},
	} {
		// No SDK or Kubernetes client: invalid scope must fail before fetching
		// allocations, since an empty Kubernetes namespace would list all CRs.
		store := NewVPCIPBlockAllocationStateStorage(nil, allocationStateVPCProvider(), nil)
		result, err := store.Get(context.Background(), tc.namespace, tc.name)
		assert.Nil(t, result)
		assert.True(t, k8serrors.IsNotFound(err), "got %v", err)
	}
}

func TestVPCIPBlockAllocationStateStorage_Get_SelectsBlockScope(t *testing.T) {
	for _, tc := range []struct{ request, cidr, allocation string }{
		{":block-id", "192.0.2.1/32", "infra-allocation"},
		{"block-id", "192.0.2.2/32", "project-allocation"},
	} {
		t.Run(tc.request, func(t *testing.T) {
			backend := &fakeVPCIPBlockAllocationStateClient{list: func(_, _, _ string, _ *string, _ *bool, _ *string) (model.IpAddressBlockAllocationStateList, error) {
				return model.IpAddressBlockAllocationStateList{Results: []model.IpAddressBlockAllocationState{
					allocationStateBlock("/infra/ip-blocks/unrelated-block", "192.0.2.3/32"),
					allocationStateBlock("/infra/ip-blocks/block-id", "192.0.2.1/32"),
					allocationStateBlock("/orgs/default/projects/project/infra/ip-blocks/block-id", "192.0.2.2/32"),
					allocationStateBlock("/orgs/default/projects/other-project/infra/ip-blocks/block-id", "192.0.2.4/32"),
					allocationStateBlock("", "192.0.2.5/32"),
				}}, nil
			}}
			k8s := newFakeK8sClient(allocationStateCR("infra-allocation", "ns1", "192.0.2.1"), allocationStateCR("project-allocation", "ns1", "192.0.2.2"))
			result, err := NewVPCIPBlockAllocationStateStorage(&nsx.Client{VPCIPBlockAllocationStateClient: backend}, allocationStateVPCProvider(), k8s).
				Get(context.Background(), "ns1", tc.request)
			require.NoError(t, err)
			assert.Equal(t, tc.request, result.Name)
			assert.Equal(t, []easv1alpha1.VPCIPAddressBlockAllocationStateForCIDR{{CIDR: tc.cidr, VPCIPAddressAllocationName: tc.allocation}}, result.AllocationStates)
			assert.Equal(t, [][]string{{"default", "project", "vpc1"}}, backend.calls)
		})
	}
}

func TestVPCIPBlockAllocationStateStorage_Get_SkipsVPCNotFound(t *testing.T) {
	backend := &fakeVPCIPBlockAllocationStateClient{list: func(_, _, vpc string, _ *string, _ *bool, _ *string) (model.IpAddressBlockAllocationStateList, error) {
		if vpc == "deleted-vpc" {
			return model.IpAddressBlockAllocationStateList{}, vapierrors.NotFound{}
		}
		return model.IpAddressBlockAllocationStateList{Results: []model.IpAddressBlockAllocationState{allocationStateBlock("/orgs/default/projects/project/infra/ip-blocks/block-id")}}, nil
	}}
	provider := multiVPCProvider{entries: []eas.VPCEntry{
		{Info: common.VPCResourceInfo{OrgID: "default", ProjectID: "project", VPCID: "deleted-vpc"}},
		{Info: common.VPCResourceInfo{OrgID: "default", ProjectID: "project", VPCID: "active-vpc"}},
	}}
	result, err := NewVPCIPBlockAllocationStateStorage(&nsx.Client{VPCIPBlockAllocationStateClient: backend}, provider, nil).
		Get(context.Background(), "ns1", "block-id")
	require.NoError(t, err)
	assert.Equal(t, "block-id", result.Name)
	assert.Len(t, backend.calls, 2)
}

func TestVPCIPBlockAllocationStateStorage_Get_Errors(t *testing.T) {
	backendErr := fmt.Errorf("allocation-state backend unavailable")
	for _, tc := range []struct {
		name string
		err  error
		k8s  bool
	}{
		{name: "all VPCs missing", err: vapierrors.NotFound{}},
		{name: "forbidden", err: vapierrors.Unauthorized{}},
		{name: "backend failure", err: backendErr},
		{name: "Kubernetes allocation list failed", err: backendErr, k8s: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &fakeVPCIPBlockAllocationStateClient{list: func(_, _, _ string, _ *string, _ *bool, _ *string) (model.IpAddressBlockAllocationStateList, error) {
				if !tc.k8s {
					return model.IpAddressBlockAllocationStateList{}, tc.err
				}
				return model.IpAddressBlockAllocationStateList{Results: []model.IpAddressBlockAllocationState{allocationStateBlock("/orgs/default/projects/project/infra/ip-blocks/block-id", "192.0.2.1/32")}}, nil
			}}
			k8s := allocationStateK8sClient{list: func(context.Context, k8sclient.ObjectList, ...k8sclient.ListOption) error { return tc.err }}
			result, err := NewVPCIPBlockAllocationStateStorage(&nsx.Client{VPCIPBlockAllocationStateClient: backend}, allocationStateVPCProvider(), k8s).
				Get(context.Background(), "ns1", "block-id")
			assert.Nil(t, result)
			switch tc.name {
			case "all VPCs missing":
				assert.True(t, k8serrors.IsNotFound(err), "got %v", err)
			case "forbidden":
				assert.True(t, k8serrors.IsForbidden(err), "got %v", err)
			default:
				assert.ErrorIs(t, err, tc.err)
			}
		})
	}
}

func TestVPCIPBlockAllocationStateStorage_Get_ContextCanceled(t *testing.T) {
	for _, stage := range []string{"before request", "during NSX call", "during Kubernetes list"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backend := &fakeVPCIPBlockAllocationStateClient{list: func(_, _, _ string, _ *string, _ *bool, _ *string) (model.IpAddressBlockAllocationStateList, error) {
				if stage == "during NSX call" {
					cancel()
				}
				return model.IpAddressBlockAllocationStateList{Results: []model.IpAddressBlockAllocationState{allocationStateBlock("/orgs/default/projects/project/infra/ip-blocks/block-id", "192.0.2.1/32")}}, nil
			}}
			listCalls := 0
			k8s := allocationStateK8sClient{list: func(context.Context, k8sclient.ObjectList, ...k8sclient.ListOption) error {
				listCalls++
				cancel()
				return nil
			}}
			if stage == "before request" {
				cancel()
			}
			result, err := NewVPCIPBlockAllocationStateStorage(&nsx.Client{VPCIPBlockAllocationStateClient: backend}, allocationStateVPCProvider(), k8s).
				Get(ctx, "ns1", "block-id")
			assert.Nil(t, result)
			assert.ErrorIs(t, err, context.Canceled)
			if stage == "before request" {
				assert.Empty(t, backend.calls)
			}
			if stage != "during Kubernetes list" {
				assert.Zero(t, listCalls)
			}
		})
	}
}
