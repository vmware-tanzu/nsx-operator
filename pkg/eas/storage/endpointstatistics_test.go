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
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/search"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

type endpointStatisticsQueryClient struct {
	search.QueryClient
	pages   map[string]model.SearchResponse
	err     error
	queries []string
	cursors []string
}

func (f *endpointStatisticsQueryClient) List(query string, cursor, _ *string, _ *int64, _ *bool, _ *string) (model.SearchResponse, error) {
	f.queries = append(f.queries, query)
	f.cursors = append(f.cursors, DerefString(cursor))
	return f.pages[DerefString(cursor)], f.err
}

type endpointReadErrorClient struct {
	k8sclient.Client
	err error
}

func (c endpointReadErrorClient) Get(context.Context, k8sclient.ObjectKey, k8sclient.Object, ...k8sclient.GetOption) error {
	return c.err
}

func TestEndpointStatisticsStorage_GetByCRUID(t *testing.T) {
	for _, kind := range []struct {
		name, resourceType, uidScope, endpointPath string
	}{
		{"ServiceEndpoint", "VpcServiceEndpoint", "nsx-op/serviceendpoint_uid", "vpc-service-endpoints"},
		{"VPCEndpoint", "VpcEndpoint", "nsx-op/vpcendpoint_uid", "vpc-endpoints"},
	} {
		t.Run(kind.name, func(t *testing.T) {
			// Literal tag names are taken from PR #1532, rather than an NSX ID
			// derived from the CR name or UID.
			const uid = "cdb706db-ad86-452d-af30-930109e0e1e7"
			path := "/orgs/actual-org/projects/actual-project/vpcs/actual-vpc/" + kind.endpointPath + "/generated-nsx-id"
			resource := func(tagUID, endpointPath string) *data.StructValue {
				endpoint := model.PolicyResource{
					ResourceType: strPtr(kind.resourceType), Path: strPtr(endpointPath),
					Tags: []model.Tag{{Scope: strPtr(kind.uidScope), Tag: strPtr(tagUID)}},
				}
				value, errs := endpoint.GetDataValue__()
				require.Empty(t, errs)
				return value.(*data.StructValue)
			}
			for _, tc := range []struct {
				name                         string
				missingCR, emptyUID          bool
				readErr, searchErr, statsErr error
				pages                        map[string]model.SearchResponse
				wantError                    string
				wantNotFound, wantForbidden  bool
				wantCursors                  []string
			}{
				{
					name:        "resolves UID to generated NSX ID and actual VPC path",
					pages:       map[string]model.SearchResponse{"": {Results: []*data.StructValue{resource("old-cr-uid", path), resource(uid, path)}}},
					wantCursors: []string{""},
				},
				{
					name: "missing CR does not query NSX", missingCR: true, wantNotFound: true,
				},
				{
					name:          "CR read forbidden does not query NSX",
					readErr:       k8serrors.NewForbidden(schema.GroupResource{Group: "crd.nsx.vmware.com", Resource: "endpoints"}, "ep1", fmt.Errorf("denied")),
					wantForbidden: true,
				},
				{
					name: "empty UID does not query NSX", emptyUID: true, wantError: "no UID",
				},
				{
					name: "no NSX result", wantNotFound: true, wantCursors: []string{""},
				},
				{
					name:         "stale UID is not used",
					pages:        map[string]model.SearchResponse{"": {Results: []*data.StructValue{resource("old-cr-uid", path)}}},
					wantNotFound: true, wantCursors: []string{""},
				},
				{
					name:        "UID search follows cursor",
					pages:       map[string]model.SearchResponse{"": {Cursor: strPtr("next")}, "next": {Results: []*data.StructValue{resource(uid, path)}}},
					wantCursors: []string{"", "next"},
				},
				{
					name: "search error", searchErr: fmt.Errorf("search failed"), wantError: "search failed", wantCursors: []string{""},
				},
				{
					name: "search forbidden", searchErr: vapierrors.Unauthorized{}, wantForbidden: true, wantCursors: []string{""},
				},
				{
					name:      "invalid NSX path",
					pages:     map[string]model.SearchResponse{"": {Results: []*data.StructValue{resource(uid, "/invalid/path")}}},
					wantError: "invalid path", wantCursors: []string{""},
				},
				{
					name:     "statistics error",
					pages:    map[string]model.SearchResponse{"": {Results: []*data.StructValue{resource(uid, path)}}},
					statsErr: fmt.Errorf("statistics failed"), wantError: "statistics failed", wantCursors: []string{""},
				},
				{
					name:     "NSX endpoint not realized",
					pages:    map[string]model.SearchResponse{"": {Results: []*data.StructValue{resource(uid, path)}}},
					statsErr: vapierrors.NotFound{}, wantNotFound: true, wantCursors: []string{""},
				},
			} {
				t.Run(tc.name, func(t *testing.T) {
					metadata := metav1.ObjectMeta{Name: "ep1", Namespace: "ns1", UID: types.UID(uid)}
					if tc.emptyUID {
						metadata.UID = ""
					}
					if tc.missingCR {
						metadata.Namespace = "another-namespace"
					}
					var cr k8sclient.Object = &vpcv1alpha1.ServiceEndpoint{ObjectMeta: metadata}
					if kind.name == "VPCEndpoint" {
						cr = &vpcv1alpha1.VPCEndpoint{ObjectMeta: metadata}
					}
					k8sClient := newFakeK8sClient(cr)
					if tc.readErr != nil {
						k8sClient = endpointReadErrorClient{Client: k8sClient, err: tc.readErr}
					}
					queryClient := &endpointStatisticsQueryClient{pages: tc.pages, err: tc.searchErr}
					nsxClient := &nsx.Client{QueryClient: queryClient}
					var result runtime.Object
					var err error
					var statsCalls [][]string
					if kind.name == "ServiceEndpoint" {
						statsClient := &fakeVPCServiceEndpointStatisticsClient{err: tc.statsErr}
						nsxClient.VPCServiceEndpointStatisticsClient = statsClient
						result, err = NewServiceEndpointStatisticsStorage(nsxClient, k8sClient).Get(context.Background(), "ns1", "ep1")
						statsCalls = statsClient.calls
					} else {
						statsClient := &fakeVPCEndpointStatisticsClient{err: tc.statsErr}
						nsxClient.VPCEndpointStatisticsClient = statsClient
						result, err = NewVPCEndpointStatisticsStorage(nsxClient, k8sClient).Get(context.Background(), "ns1", "ep1")
						statsCalls = statsClient.calls
					}
					assert.Equal(t, tc.wantCursors, queryClient.cursors)
					for _, query := range queryClient.queries {
						assert.Equal(t, "resource_type:"+kind.resourceType+" AND tags.scope:nsx-op\\/"+kind.uidScope[len("nsx-op/"):]+" AND tags.tag:"+uid+" AND marked_for_delete:false", query)
					}
					if tc.wantError != "" || tc.wantNotFound || tc.wantForbidden {
						require.Error(t, err)
						assert.Nil(t, result)
						if tc.wantError != "" {
							assert.Contains(t, err.Error(), tc.wantError)
						}
						if tc.wantNotFound {
							assert.True(t, k8serrors.IsNotFound(err))
						}
						if tc.wantForbidden {
							assert.True(t, k8serrors.IsForbidden(err))
						}
						if tc.statsErr == nil {
							assert.Empty(t, statsCalls)
						}
						return
					}
					require.NoError(t, err)
					require.NotNil(t, result)
					assert.Equal(t, [][]string{{"actual-org", "actual-project", "actual-vpc", "generated-nsx-id"}}, statsCalls)
				})
			}
		})
	}
}
