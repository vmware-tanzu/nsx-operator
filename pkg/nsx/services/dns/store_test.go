/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestDNSRecordKeyFunc_table(t *testing.T) {
	path := "/orgs/o/projects/p/dns-records/r1"
	tests := []struct {
		name    string
		obj     interface{}
		want    string
		wantErr bool
	}{
		{
			name: "valid path",
			obj:  &model.ProjectDnsRecord{Path: &path},
			want: path,
		},
		{
			name:    "nil path returns error",
			obj:     &model.ProjectDnsRecord{},
			wantErr: true,
		},
		{
			name:    "unknown type returns error",
			obj:     "not-a-record",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := dnsRecordKeyFunc(tc.obj)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestResourceKindToCreatedFor_table(t *testing.T) {
	tests := []struct {
		kind string
		want string
	}{
		{ResourceKindGateway, servicecommon.TagValueDNSRecordForGateway},
		{ResourceKindHTTPRoute, servicecommon.TagValueDNSRecordForHTTPRoute},
		{ResourceKindGRPCRoute, servicecommon.TagValueDNSRecordForGRPCRoute},
		{ResourceKindTLSRoute, servicecommon.TagValueDNSRecordForTLSRoute},
		{ResourceKindService, servicecommon.TagValueDNSRecordForService},
		{"UnknownKind", ""},
		{"", ""},
	}
	for _, tc := range tests {
		t.Run(tc.kind+"->"+tc.want, func(t *testing.T) {
			require.Equal(t, tc.want, resourceKindToCreatedFor(tc.kind))
		})
	}
}

func TestDNSRecordFQDNLower_table(t *testing.T) {
	fqdn := "A.EXAMPLE.COM"
	tests := []struct {
		name string
		rec  *model.ProjectDnsRecord
		want string
	}{
		{"nil record", nil, ""},
		{"nil fqdn field", &model.ProjectDnsRecord{}, ""},
		{"valid fqdn lowercased", &model.ProjectDnsRecord{Fqdn: &fqdn}, "a.example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, dnsRecordFQDNLower(tc.rec))
		})
	}
}

func TestDeleteMultipleObjects_table(t *testing.T) {
	t.Run("nil records skipped without panic", func(t *testing.T) {
		store := BuildDNSRecordStore()
		store.DeleteMultipleObjects([]*model.ProjectDnsRecord{nil, nil})
	})

	t.Run("valid record removed from store", func(t *testing.T) {
		store := BuildDNSRecordStore()
		path := "/orgs/o/projects/p/dns-records/r1"
		rec := &model.ProjectDnsRecord{Path: servicecommon.String(path)}
		require.NoError(t, store.Add(rec))
		require.NotNil(t, store.GetByKey(path))

		store.DeleteMultipleObjects([]*model.ProjectDnsRecord{nil, rec})
		require.Nil(t, store.GetByKey(path))
	})
}

func TestApply_addAndDeleteBranches(t *testing.T) {
	store := BuildDNSRecordStore()
	path := "/orgs/o/projects/p/dns-records/r2"
	rec := &model.ProjectDnsRecord{Path: servicecommon.String(path)}

	require.NoError(t, store.Apply([]*model.ProjectDnsRecord{rec}))
	require.NotNil(t, store.GetByKey(path))

	cp := *rec
	cp.MarkedForDelete = servicecommon.Bool(true)
	require.NoError(t, store.Apply([]*model.ProjectDnsRecord{&cp}))
	require.Nil(t, store.GetByKey(path))
}

func TestIndexFunctions_unsupported_type(t *testing.T) {
	unsupported := "not-a-record"

	tests := []struct {
		name string
		fn   func(interface{}) ([]string, error)
	}{
		{"indexDNSRecordByOwnerTypeNN", indexDNSRecordByOwnerTypeNN},
		{"indexDNSRecordByGatewayNamespacedName", indexDNSRecordByGatewayNamespacedName},
		{"indexDNSRecordByZonePathFQDN", indexDNSRecordByZonePathFQDN},
		{"indexDNSRecordByZonePath", indexDNSRecordByZonePath},
		{"indexDNSRecordByContributingOwner", indexDNSRecordByContributingOwner},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.fn(unsupported)
			require.Error(t, err)
		})
	}
}

func TestIndexDNSRecordByZonePathFQDN_earlyReturn_table(t *testing.T) {
	rt := model.ProjectDnsRecord_RECORD_TYPE_A
	fqdn := "a.example.com"
	zp := "/z1"
	tests := []struct {
		name    string
		rec     *model.ProjectDnsRecord
		wantLen int
	}{
		{
			name:    "nil ZonePath returns empty",
			rec:     &model.ProjectDnsRecord{RecordType: &rt, Fqdn: &fqdn},
			wantLen: 0,
		},
		{
			name:    "nil RecordType returns empty",
			rec:     &model.ProjectDnsRecord{ZonePath: &zp, Fqdn: &fqdn},
			wantLen: 0,
		},
		{
			name:    "empty fqdn returns empty",
			rec:     &model.ProjectDnsRecord{ZonePath: &zp, RecordType: &rt},
			wantLen: 0,
		},
		{
			name:    "valid record returns one index key",
			rec:     &model.ProjectDnsRecord{ZonePath: &zp, RecordType: &rt, Fqdn: &fqdn},
			wantLen: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			keys, err := indexDNSRecordByZonePathFQDN(tc.rec)
			require.NoError(t, err)
			require.Len(t, keys, tc.wantLen)
		})
	}
}

func TestGetByOwnerResourceNamespacedName_unknownKind(t *testing.T) {
	store := BuildDNSRecordStore()
	result := store.GetByOwnerResourceNamespacedName("UnknownKind", "ns", "name")
	require.Nil(t, result)
}
