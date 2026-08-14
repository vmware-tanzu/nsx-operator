/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/mock/gomock"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	dnszonemocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/dnszonesclient"
	searchmocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/searchclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestInitializeDNSRecordServiceRestoresPaginatedSearch(t *testing.T) {
	ctrl := gomock.NewController(t)
	query := searchmocks.NewMockQueryClient(ctrl)
	zones := dnszonemocks.NewMockZonesClient(ctrl)
	tags := []model.Tag{
		{Scope: common.String(common.TagScopeCluster), Tag: common.String("unit-test")},
		{Scope: common.String(common.TagScopeDNSRecordFor), Tag: common.String(common.TagValueDNSRecordForDNSRecord)},
		{Scope: common.String(common.TagScopeDNSRecordOwnerNamespace), Tag: common.String("ns1")},
		{Scope: common.String(common.TagScopeDNSRecordOwnerName), Tag: common.String("rec1")},
	}
	record := func(id string) *data.StructValue {
		return dnsRecordStructValue(t, model.DnsRecord{
			Id: common.String(id), Path: common.String("/orgs/org1/projects/proj1/dns-records/" + id),
			ZonePath: common.String(testDNSZonePathT), Tags: tags,
		})
	}
	cursor, count := "1", int64(2)
	search := `resource_type:DnsRecord AND tags.scope:nsx-op\/cluster AND tags.tag:unit-test AND marked_for_delete:false`
	gomock.InOrder(
		query.EXPECT().List(search, nil, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(model.SearchResponse{Results: []*data.StructValue{record("record1")}, Cursor: &cursor, ResultCount: &count}, nil),
		query.EXPECT().List(search, &cursor, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(model.SearchResponse{Results: []*data.StructValue{record("record2")}, ResultCount: &count}, nil),
	)
	zones.EXPECT().Get("org1", "proj1", "ds1", "zone-t").
		Return(model.DnsZone{DnsDomainName: common.String("example.com")}, nil)
	cfg := &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "unit-test"}}
	svc, err := InitializeDNSRecordService(common.Service{
		NSXConfig: cfg,
		NSXClient: &nsx.Client{QueryClient: query, DnsZoneClient: zones, NsxConfig: cfg},
	}, nil)
	require.NoError(t, err)
	require.Len(t, svc.DNSRecordStore.ListDNSRecords(), 2)
	require.Len(t, svc.DNSRecordStore.GetByOwnerResourceNamespacedName(ResourceKindDNSRecord, "ns1", "rec1"), 2)
	domain, found := svc.DNSZoneMap.get(testDNSZonePathT)
	require.True(t, found)
	require.Equal(t, "example.com", domain)
}

func TestInitializeDNSRecordServiceFailsOnSearchError(t *testing.T) {
	for _, failOnPage := range []int{1, 2} {
		t.Run(map[int]string{1: "first page", 2: "later page"}[failOnPage], func(t *testing.T) {
			ctrl := gomock.NewController(t)
			query := searchmocks.NewMockQueryClient(ctrl)
			searchErr := errors.New("NSX search unavailable")
			first := query.EXPECT().List(gomock.Any(), nil, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any())
			if failOnPage == 1 {
				first.Return(model.SearchResponse{}, searchErr)
			} else {
				cursor, count := "1", int64(2)
				first.Return(model.SearchResponse{Cursor: &cursor, ResultCount: &count}, nil)
				query.EXPECT().List(gomock.Any(), &cursor, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					Return(model.SearchResponse{}, searchErr).After(first)
			}
			cfg := &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "unit-test"}}
			_, err := InitializeDNSRecordService(common.Service{
				NSXConfig: cfg,
				NSXClient: &nsx.Client{QueryClient: query, NsxConfig: cfg},
			}, nil)
			require.ErrorIs(t, err, searchErr)
		})
	}
}
