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
			obj:  &model.DnsRecord{Path: &path},
			want: path,
		},
		{
			name:    "nil path returns error",
			obj:     &model.DnsRecord{},
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

func TestDNSRecordRecordNameLower_table(t *testing.T) {
	fqdn := "A.EXAMPLE.COM"
	tests := []struct {
		name string
		rec  *model.DnsRecord
		want string
	}{
		{"nil record", nil, ""},
		{"nil fqdn field", &model.DnsRecord{}, ""},
		{"valid fqdn lowercased", &model.DnsRecord{RecordName: &fqdn}, "a.example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, dnsRecordRecordNameLower(tc.rec))
		})
	}
}

func TestDeleteMultipleObjects_table(t *testing.T) {
	t.Run("nil records skipped without panic", func(t *testing.T) {
		store := BuildDNSRecordStore()
		store.DeleteMultipleObjects([]*model.DnsRecord{nil, nil})
	})

	t.Run("valid record removed from store", func(t *testing.T) {
		store := BuildDNSRecordStore()
		path := "/orgs/o/projects/p/dns-records/r1"
		rec := &model.DnsRecord{Path: servicecommon.String(path)}
		require.NoError(t, store.Add(rec))
		require.NotNil(t, store.GetByKey(path))

		store.DeleteMultipleObjects([]*model.DnsRecord{nil, rec})
		require.Nil(t, store.GetByKey(path))
	})
}

func TestApply_addAndDeleteBranches(t *testing.T) {
	store := BuildDNSRecordStore()
	path := "/orgs/o/projects/p/dns-records/r2"
	rec := &model.DnsRecord{Path: servicecommon.String(path)}

	require.NoError(t, store.Apply([]*model.DnsRecord{rec}))
	require.NotNil(t, store.GetByKey(path))

	cp := *rec
	cp.MarkedForDelete = servicecommon.Bool(true)
	require.NoError(t, store.Apply([]*model.DnsRecord{&cp}))
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
		{"indexDNSRecordByZonePathRecordName", indexDNSRecordByZonePathRecordName},
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
	rt := model.DnsRecord_RECORD_TYPE_A
	fqdn := "a.example.com"
	zp := "/z1"
	tests := []struct {
		name    string
		rec     *model.DnsRecord
		wantLen int
	}{
		{
			name:    "nil ZonePath returns empty",
			rec:     &model.DnsRecord{RecordType: &rt, RecordName: &fqdn},
			wantLen: 0,
		},
		{
			name:    "nil RecordType returns empty",
			rec:     &model.DnsRecord{ZonePath: &zp, RecordName: &fqdn},
			wantLen: 0,
		},
		{
			name:    "empty fqdn returns empty",
			rec:     &model.DnsRecord{ZonePath: &zp, RecordType: &rt},
			wantLen: 0,
		},
		{
			name:    "valid record returns one index key",
			rec:     &model.DnsRecord{ZonePath: &zp, RecordType: &rt, RecordName: &fqdn},
			wantLen: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			keys, err := indexDNSRecordByZonePathRecordName(tc.rec)
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
