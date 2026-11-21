/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package rest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	vapierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/vpcs"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/vpcs/subnets"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/utils/ptr"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas/storage"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type restSubnetStatusSubnetsClient struct {
	vpcs.SubnetsClient
	results []model.VpcSubnet
}

func (f *restSubnetStatusSubnetsClient) List(_, _, _ string, _ *string, _, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.VpcSubnetListResult, error) {
	return model.VpcSubnetListResult{Results: f.results}, nil
}

type restSubnetStatusClient struct {
	subnets.StatusClient
	results   map[string][]model.VpcSubnetStatus
	errs      map[string]error
	subnetIDs []string
}

func (f *restSubnetStatusClient) List(_, _, _, subnetID string) (model.VpcSubnetStatusListResult, error) {
	f.subnetIDs = append(f.subnetIDs, subnetID)
	return model.VpcSubnetStatusListResult{Results: f.results[subnetID]}, f.errs[subnetID]
}

type restSubnetStatusK8sClient struct {
	k8sclient.Client
	listErrors     map[string]error
	listNamespaces []string
}

func (c *restSubnetStatusK8sClient) List(ctx context.Context, list k8sclient.ObjectList, opts ...k8sclient.ListOption) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	options := (&k8sclient.ListOptions{}).ApplyOptions(opts)
	c.listNamespaces = append(c.listNamespaces, options.Namespace)
	if err := c.listErrors[options.Namespace]; err != nil {
		return err
	}
	return c.Client.List(ctx, list, opts...)
}

func newSubnetStatusREST() (*subnetStatusStorage, *restSubnetStatusClient, *restSubnetStatusK8sClient) {
	var objects []k8sclient.Object
	var nsxSubnets []model.VpcSubnet
	statuses := &restSubnetStatusClient{
		results: map[string][]model.VpcSubnetStatus{
			"subnet-ns1": {{GatewayAddress: ptr.To("10.0.1.1"), DhcpRanges: ptr.To("10.0.1.2-10.0.1.30"), IpAddressType: ptr.To("IPV4")}},
			"subnet-ns2": {{GatewayAddress: ptr.To("10.0.2.1"), DhcpRanges: ptr.To("10.0.2.2-10.0.2.30"), IpAddressType: ptr.To("IPV4")}},
		},
		errs: make(map[string]error),
	}
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
	k8s := &restSubnetStatusK8sClient{Client: newTestFakeK8sClient().WithObjects(objects...).Build(), listErrors: make(map[string]error)}
	store := storage.NewSubnetStatusStorage(&nsx.Client{
		SubnetsClient:      &restSubnetStatusSubnetsClient{results: nsxSubnets},
		SubnetStatusClient: statuses,
	}, k8s)
	return NewSubnetStatusStorage(store, fakeVPCInfoProvider{namespaces: []string{"ns1", "ns2"}}), statuses, k8s
}

func TestSubnetStatusStorage_Metadata(t *testing.T) {
	r, _, _ := newSubnetStatusREST()
	assert.IsType(t, &easv1alpha1.SubnetStatus{}, r.New())
	assert.IsType(t, &easv1alpha1.SubnetStatusList{}, r.NewList())
	assert.True(t, r.NamespaceScoped())
	assert.Equal(t, "subnetstatus", r.GetSingularName())
	r.Destroy()
}

func TestSubnetStatusStorage_Get_CRNameAndNamespace(t *testing.T) {
	r, client, _ := newSubnetStatusREST()
	for _, tc := range []struct {
		namespace, gateway string
	}{
		{"ns1", "10.0.1.1"},
		{"ns2", "10.0.2.1"},
	} {
		object, err := r.Get(request.WithNamespace(context.Background(), tc.namespace), "same-name", &metav1.GetOptions{})
		require.NoError(t, err)
		status := object.(*easv1alpha1.SubnetStatus)
		assert.Equal(t, "same-name", status.Name)
		assert.Equal(t, tc.namespace, status.Namespace)
		assert.Equal(t, []string{tc.gateway}, status.GatewayAddresses)
		assert.NotEmpty(t, status.DHCPRanges, "Get includes detailed status fields")
	}
	assert.Equal(t, []string{"subnet-ns1", "subnet-ns2"}, client.subnetIDs)
	for _, tc := range []struct{ namespace, name string }{
		{"ns1", "subnet-ns1"}, // The NSX ID is not an EAS Get parameter.
		{"ns1", "missing"},
		{"unmapped", "same-name"},
		{"", "same-name"},
	} {
		_, err := r.Get(request.WithNamespace(context.Background(), tc.namespace), tc.name, nil)
		assert.True(t, k8serrors.IsNotFound(err))
	}
}

func TestSubnetStatusStorage_Get_BackendError(t *testing.T) {
	r, client, _ := newSubnetStatusREST()
	client.errs["subnet-ns1"] = vapierrors.Unauthorized{}
	_, err := r.Get(request.WithNamespace(context.Background(), "ns1"), "same-name", nil)
	assert.True(t, k8serrors.IsForbidden(err))
}

func TestSubnetStatusStorage_List_NamespaceScope(t *testing.T) {
	for _, tc := range []struct {
		name       string
		ctx        context.Context
		namespaces []string
	}{
		{"ns1", request.WithNamespace(context.Background(), "ns1"), []string{"ns1"}},
		{"ns2", request.WithNamespace(context.Background(), "ns2"), []string{"ns2"}},
		{"all without namespace", context.Background(), []string{"ns1", "ns2"}},
		{"all with empty namespace", request.WithNamespace(context.Background(), ""), []string{"ns1", "ns2"}},
		{"empty namespace", request.WithNamespace(context.Background(), "unmapped"), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, client, k8s := newSubnetStatusREST()
			object, err := r.List(tc.ctx, nil)
			require.NoError(t, err)
			list := object.(*easv1alpha1.SubnetStatusList)
			assert.Equal(t, "SubnetStatusList", list.Kind)
			assert.Equal(t, easv1alpha1.GroupVersion.String(), list.APIVersion)
			assert.NotNil(t, list.Items)
			require.Len(t, list.Items, len(tc.namespaces))
			var expectedIDs []string
			for i, namespace := range tc.namespaces {
				assert.Equal(t, "same-name", list.Items[i].Name)
				assert.Equal(t, namespace, list.Items[i].Namespace)
				assert.NotEmpty(t, list.Items[i].GatewayAddresses)
				assert.Empty(t, list.Items[i].DHCPRanges, "List returns summary fields")
				assert.Empty(t, list.Items[i].IPAddressTypes)
				expectedIDs = append(expectedIDs, "subnet-"+namespace)
			}
			assert.Equal(t, expectedIDs, client.subnetIDs)
			if len(tc.namespaces) != 0 {
				assert.Equal(t, tc.namespaces, k8s.listNamespaces)
			} else {
				assert.Equal(t, []string{"unmapped"}, k8s.listNamespaces)
			}
		})
	}
}

func TestSubnetStatusStorage_List_Errors(t *testing.T) {
	r, _, k8s := newSubnetStatusREST()
	failure := errors.New("failed to list CRs")
	k8s.listErrors["ns2"] = failure
	_, err := r.List(request.WithNamespace(context.Background(), "ns2"), nil)
	assert.ErrorIs(t, err, failure)
	object, err := r.List(context.Background(), nil)
	require.NoError(t, err, "all-namespace List skips failed namespaces consistently with other EAS APIs")
	list := object.(*easv1alpha1.SubnetStatusList)
	require.Len(t, list.Items, 1)
	assert.Equal(t, "ns1", list.Items[0].Namespace)
}

func TestSubnetStatusStorage_List_EmptyAndCancelled(t *testing.T) {
	r := NewSubnetStatusStorage(storage.NewSubnetStatusStorage(&nsx.Client{}, newTestFakeK8sClient().Build()), fakeVPCInfoProvider{})
	object, err := r.List(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, object.(*easv1alpha1.SubnetStatusList).Items)

	r, client, _ := newSubnetStatusREST()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	object, err = r.List(ctx, nil)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, object)
	assert.Empty(t, client.subnetIDs)
}

func TestSubnetStatusStorage_ConvertToTable(t *testing.T) {
	r, _, _ := newSubnetStatusREST()
	status := easv1alpha1.SubnetStatus{
		ObjectMeta:       metav1.ObjectMeta{Name: "same-name", Namespace: "ns1"},
		GatewayAddresses: []string{"10.0.1.1", "2001:db8::1"},
	}
	table, err := r.ConvertToTable(context.Background(), &status, nil)
	require.NoError(t, err)
	assert.Equal(t, subnetStatusColumns, table.ColumnDefinitions)
	require.Len(t, table.Rows, 1)
	assert.Equal(t, []interface{}{"same-name", "10.0.1.1,2001:db8::1"}, table.Rows[0].Cells)
	metadata := table.Rows[0].Object.Object.(*metav1.PartialObjectMetadata)
	assert.Equal(t, "same-name", metadata.Name)
	assert.Equal(t, "ns1", metadata.Namespace)

	status.GatewayAddresses = []string{strings.Repeat("a", maxColLen+1)}
	list := &easv1alpha1.SubnetStatusList{Items: []easv1alpha1.SubnetStatus{status}}
	table, err = r.ConvertToTable(context.Background(), list, nil)
	require.NoError(t, err)
	require.Len(t, table.Rows, 1)
	assert.Equal(t, strings.Repeat("a", maxColLen-3)+"...", table.Rows[0].Cells[1])
	table, err = r.ConvertToTable(context.Background(), &easv1alpha1.SubnetStatusList{}, nil)
	require.NoError(t, err)
	assert.Empty(t, table.Rows)
	_, err = r.ConvertToTable(context.Background(), &easv1alpha1.SubnetIPPools{}, nil)
	assert.ErrorContains(t, err, "unsupported type")
}

func tableAfterJSONSerialization(t *testing.T, table *metav1.Table) *metav1.Table {
	t.Helper()
	data, err := json.Marshal(table)
	require.NoError(t, err)
	decoded := &metav1.Table{}
	require.NoError(t, json.Unmarshal(data, decoded))
	for i := range decoded.Rows {
		row := &decoded.Rows[i]
		require.NotEmpty(t, row.Object.Raw)
		// kubectl decodes the embedded JSON before extracting namespace metadata.
		row.Object.Object, err = runtime.Decode(unstructured.UnstructuredJSONScheme, row.Object.Raw)
		require.NoError(t, err)
	}
	return decoded
}

func TestSubnetStatusStorage_ConvertToTable_JSONNamespaceMetadata(t *testing.T) {
	statuses := []easv1alpha1.SubnetStatus{
		{ObjectMeta: metav1.ObjectMeta{Name: "same-name", Namespace: "ns1"}, GatewayAddresses: []string{"10.0.1.1", "2001:db8:1::1"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "same-name", Namespace: "ns2"}, GatewayAddresses: []string{"10.0.2.1"}},
	}
	for _, tc := range []struct {
		name   string
		object runtime.Object
		rows   []easv1alpha1.SubnetStatus
	}{
		{"single", &statuses[0], statuses[:1]},
		{"list", &easv1alpha1.SubnetStatusList{Items: statuses}, statuses},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &subnetStatusStorage{}
			table, err := r.ConvertToTable(context.Background(), tc.object, nil)
			require.NoError(t, err)
			require.Len(t, table.ColumnDefinitions, 2)
			for _, column := range table.ColumnDefinitions {
				assert.Zero(t, column.Priority, "both columns are available in normal and wide output")
			}
			table = tableAfterJSONSerialization(t, table)
			require.Len(t, table.Rows, len(tc.rows))
			for i, expected := range tc.rows {
				row := table.Rows[i]
				require.Len(t, row.Cells, len(table.ColumnDefinitions), "namespace must not add a display cell")
				assert.Equal(t, []interface{}{expected.Name, strings.Join(expected.GatewayAddresses, ",")}, row.Cells)
				assert.Equal(t, metav1.SchemeGroupVersion.WithKind("PartialObjectMetadata"), row.Object.Object.GetObjectKind().GroupVersionKind())
				metadata, err := meta.Accessor(row.Object.Object)
				require.NoError(t, err)
				assert.Equal(t, expected.Name, metadata.GetName())
				assert.Equal(t, expected.Namespace, metadata.GetNamespace(), "kubectl can extract the namespace after JSON decoding")
			}
		})
	}
}
