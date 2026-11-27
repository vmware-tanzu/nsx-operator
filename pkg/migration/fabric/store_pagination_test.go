/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package fabric

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/search"

	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

type pageLimitQuery struct {
	search.QueryClient
	cursors []string
	sizes   []int64
}

func (q *pageLimitQuery) List(_ string, cursor, _ *string, size *int64, _ *bool, _ *string) (model.SearchResponse, error) {
	current := ""
	if cursor != nil {
		current = *cursor
	}
	q.cursors = append(q.cursors, current)
	q.sizes = append(q.sizes, *size)
	if len(q.cursors) == 1 {
		next := "opaque-next"
		return model.SearchResponse{Cursor: &next}, nil
	}
	if len(q.cursors) == 2 {
		return model.SearchResponse{}, nsxutil.PageMaxError{Desc: "page size rejected"}
	}
	return model.SearchResponse{}, nil
}

func TestInitializeResourceStoreRetriesRejectedPage(t *testing.T) {
	query := &pageLimitQuery{}
	err := initializeResourceStore(context.Background(), query, "cluster", TGW, "TransitGateway", func(string, *data.StructValue) error { return nil })
	require.NoError(t, err)
	require.Equal(t, []string{"", "opaque-next", "opaque-next"}, query.cursors)
	require.Len(t, query.sizes, 3)
	require.Less(t, query.sizes[2], query.sizes[1])
}

func TestRefreshStoreRepeatsDiscovery(t *testing.T) {
	query := &searchClient{}
	service, err := NewFabricService(FabricServiceClients{QueryClient: query, Cluster: "cluster", TransitGatewaysClient: &gatewayClient{}, IPBlocksClient: &ipBlocksClient{}, ProfilesClient: &profilesClient{}, VPCsClient: &vpcsClient{}, AttachmentsClient: &attachmentsClient{}, RealizedEntitiesClient: realizedClient{}})
	require.NoError(t, err)
	require.NoError(t, service.refreshStoreLocked(context.Background(), TGW))
	require.NoError(t, service.refreshStoreLocked(context.Background(), TGW))
	require.Len(t, query.calls, 4)
}
