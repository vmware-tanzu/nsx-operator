/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package rest

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	vapierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/utils/ptr"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas/storage"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type serviceProfileVPCProvider struct {
	namespaces []string
	entries    map[string][]eas.VPCEntry
}

func (p serviceProfileVPCProvider) ListVPCInfo(namespace string) []eas.VPCEntry {
	return p.entries[namespace]
}

func (p serviceProfileVPCProvider) ListAllVPCNamespaces() []string { return p.namespaces }

type restServiceProfilesClient struct {
	projects.VpcServiceProfilesClient
	profiles map[string][]model.VpcServiceProfile
	errs     map[string]error
	getCalls []string
}

func (f *restServiceProfilesClient) Get(orgID, projectID, name string) (model.VpcServiceProfile, error) {
	key := orgID + "/" + projectID
	f.getCalls = append(f.getCalls, key+"/"+name)
	if err := f.errs[key]; err != nil {
		return model.VpcServiceProfile{}, err
	}
	for _, profile := range f.profiles[key] {
		if ptr.Deref(profile.Id, "") == name {
			return profile, nil
		}
	}
	return model.VpcServiceProfile{}, vapierrors.NotFound{}
}

type restServiceProfileVPCClient struct {
	projects.VpcsClient
	vpcs map[string]model.Vpc
}

func (f *restServiceProfileVPCClient) Get(orgID, projectID, vpcID string) (model.Vpc, error) {
	vpc, exists := f.vpcs[orgID+"/"+projectID+"/"+vpcID]
	if !exists {
		return model.Vpc{}, vapierrors.NotFound{}
	}
	return vpc, nil
}

func newServiceProfileREST() (*vpcServiceProfileStorage, *restServiceProfilesClient) {
	provider := serviceProfileVPCProvider{
		namespaces: []string{"ns1", "ns2"},
		entries: map[string][]eas.VPCEntry{
			"ns1": {{Info: common.VPCResourceInfo{OrgID: "org", ProjectID: "p1", VPCID: "shared-vpc"}}},
			"ns2": {{Info: common.VPCResourceInfo{OrgID: "org", ProjectID: "p2", VPCID: "shared-vpc"}}},
		},
	}
	client := &restServiceProfilesClient{
		profiles: map[string][]model.VpcServiceProfile{
			"org/p1": {{Id: ptr.To("profile-p1"), Description: ptr.To("profile in p1"), IsDefault: ptr.To(true)}},
			"org/p2": {{Id: ptr.To("profile-p2"), Description: ptr.To("profile in p2")}},
		},
		errs: make(map[string]error),
	}
	vpcs := &restServiceProfileVPCClient{vpcs: map[string]model.Vpc{
		"org/p1/shared-vpc": {VpcServiceProfile: ptr.To("/orgs/org/projects/p1/vpc-service-profiles/profile-p1")},
		"org/p2/shared-vpc": {VpcServiceProfile: ptr.To("/orgs/org/projects/p2/vpc-service-profiles/profile-p2")},
	}}
	store := storage.NewVPCServiceProfileStorage(&nsx.Client{VPCClient: vpcs, VpcServiceProfileClient: client}, provider)
	return NewVPCServiceProfileStorage(store, provider), client
}

func TestVPCServiceProfileStorage_Metadata(t *testing.T) {
	r, _ := newServiceProfileREST()
	assert.IsType(t, &easv1alpha1.VPCServiceProfile{}, r.New())
	assert.IsType(t, &easv1alpha1.VPCServiceProfileList{}, r.NewList())
	assert.True(t, r.NamespaceScoped())
	assert.Equal(t, "vpcserviceprofile", r.GetSingularName())
	r.Destroy()
}

func TestVPCServiceProfileStorage_Get_NamespaceIsolation(t *testing.T) {
	r, _ := newServiceProfileREST()
	for _, tc := range []struct {
		namespace, description string
	}{
		{"ns1", "profile in p1"},
		{"ns2", "profile in p2"},
	} {
		ctx := request.WithNamespace(context.Background(), tc.namespace)
		object, err := r.Get(ctx, "shared-vpc", &metav1.GetOptions{})
		require.NoError(t, err)
		profile := object.(*easv1alpha1.VPCServiceProfile)
		assert.Equal(t, "shared-vpc", profile.Name)
		assert.Equal(t, tc.namespace, profile.Namespace)
		assert.Equal(t, tc.description, profile.Description)
	}
	_, err := r.Get(request.WithNamespace(context.Background(), "unmapped"), "shared-vpc", nil)
	assert.True(t, k8serrors.IsNotFound(err))
	_, err = r.Get(request.WithNamespace(context.Background(), "ns1"), "missing", nil)
	assert.True(t, k8serrors.IsNotFound(err))
	_, err = r.Get(request.WithNamespace(context.Background(), "ns1"), "profile-p1", nil)
	assert.True(t, k8serrors.IsNotFound(err), "the profile ID is not an EAS Get parameter")
}

func TestVPCServiceProfileStorage_List_NamespaceScope(t *testing.T) {
	for _, tc := range []struct {
		name      string
		ctx       context.Context
		wantNS    []string
		wantCalls []string
	}{
		{"ns1", request.WithNamespace(context.Background(), "ns1"), []string{"ns1"}, []string{"org/p1/profile-p1"}},
		{"ns2", request.WithNamespace(context.Background(), "ns2"), []string{"ns2"}, []string{"org/p2/profile-p2"}},
		{"all without namespace", context.Background(), []string{"ns1", "ns2"}, []string{"org/p1/profile-p1", "org/p2/profile-p2"}},
		{"all with empty namespace", request.WithNamespace(context.Background(), ""), []string{"ns1", "ns2"}, []string{"org/p1/profile-p1", "org/p2/profile-p2"}},
		{"unmapped namespace", request.WithNamespace(context.Background(), "unmapped"), nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, client := newServiceProfileREST()
			object, err := r.List(tc.ctx, nil)
			require.NoError(t, err)
			list := object.(*easv1alpha1.VPCServiceProfileList)
			assert.Equal(t, "VPCServiceProfileList", list.Kind)
			assert.NotNil(t, list.Items)
			require.Len(t, list.Items, len(tc.wantNS))
			for i, namespace := range tc.wantNS {
				assert.Equal(t, namespace, list.Items[i].Namespace)
				assert.Equal(t, "shared-vpc", list.Items[i].Name)
			}
			assert.Equal(t, tc.wantCalls, client.getCalls)
		})
	}
}

func TestVPCServiceProfileStorage_List_Errors(t *testing.T) {
	r, client := newServiceProfileREST()
	client.errs["org/p2"] = vapierrors.Unauthorized{}
	_, err := r.List(request.WithNamespace(context.Background(), "ns2"), nil)
	assert.True(t, k8serrors.IsForbidden(err))
	object, err := r.List(context.Background(), nil)
	require.NoError(t, err, "cross-namespace reads follow the existing EAS behavior of skipping failed namespaces")
	list := object.(*easv1alpha1.VPCServiceProfileList)
	require.Len(t, list.Items, 1)
	assert.Equal(t, "ns1", list.Items[0].Namespace)
}

func TestVPCServiceProfileStorage_List_EmptyAndCancelled(t *testing.T) {
	r := NewVPCServiceProfileStorage(
		storage.NewVPCServiceProfileStorage(&nsx.Client{}, fakeVPCInfoProvider{}), fakeVPCInfoProvider{},
	)
	object, err := r.List(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, object.(*easv1alpha1.VPCServiceProfileList).Items)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	object, err = r.List(ctx, nil)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, object)
	r, client := newServiceProfileREST()
	object, err = r.List(ctx, nil)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, object)
	assert.Empty(t, client.getCalls)
}

func TestVPCServiceProfileStorage_ConvertToTable(t *testing.T) {
	r, _ := newServiceProfileREST()
	profile := easv1alpha1.VPCServiceProfile{
		ObjectMeta:         metav1.ObjectMeta{Name: "profile", Namespace: "ns1"},
		IsDefault:          true,
		ServiceSubnetCIDRs: []string{"10.10.0.0/28", "2001:db8::/64"},
	}
	table, err := r.ConvertToTable(context.Background(), &profile, nil)
	require.NoError(t, err)
	assert.Equal(t, vpcServiceProfileColumns, table.ColumnDefinitions)
	require.Len(t, table.Rows, 1)
	assert.Equal(t, []interface{}{"profile", true, "10.10.0.0/28,2001:db8::/64"}, table.Rows[0].Cells)
	metadata := table.Rows[0].Object.Object.(*metav1.PartialObjectMetadata)
	assert.Equal(t, "profile", metadata.Name)
	assert.Equal(t, "ns1", metadata.Namespace)
	profile.ServiceSubnetCIDRs = []string{strings.Repeat("a", maxColLen+1)}
	list := &easv1alpha1.VPCServiceProfileList{Items: []easv1alpha1.VPCServiceProfile{profile}}
	table, err = r.ConvertToTable(context.Background(), list, nil)
	require.NoError(t, err)
	require.Len(t, table.Rows, 1)
	assert.Equal(t, strings.Repeat("a", maxColLen-3)+"...", table.Rows[0].Cells[2])
	table, err = r.ConvertToTable(context.Background(), &easv1alpha1.VPCServiceProfileList{}, nil)
	require.NoError(t, err)
	assert.Empty(t, table.Rows)
	_, err = r.ConvertToTable(context.Background(), &easv1alpha1.IPBlockUsage{}, nil)
	require.ErrorContains(t, err, "unsupported type")
}
