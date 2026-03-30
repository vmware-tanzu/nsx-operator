/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// newSvc creates a DNSRecordService with a fresh store seeded with the given records.
func newSvc(recs ...*DNSRecord) *DNSRecordService {
	store := BuildDNSRecordStore()
	for _, r := range recs {
		_ = store.Apply([]*DNSRecord{r})
	}
	return &DNSRecordService{DNSRecordStore: store}
}

func Test_CreateOrUpdateDNSRecords(t *testing.T) {
	assert.NoError(t, newSvc().CreateOrUpdateDNSRecords(context.Background(), &Record{}))
}

func Test_DeleteDNSRecordsByOwner(t *testing.T) {
	t.Run("empty store is no-op", func(t *testing.T) {
		assert.NoError(t, newSvc().DeleteDNSRecordsByOwner(context.Background(), ResourceKindGateway, "uid-1"))
	})
	t.Run("removes matching record", func(t *testing.T) {
		rec := makeRecordWithGatewayOwnerTags("rec-1", "gw-uid-1", "ns1", "gw1")
		svc := newSvc(rec)
		require.Len(t, svc.DNSRecordStore.GetByOwnerResourceUID(ResourceKindGateway, "gw-uid-1"), 1)
		assert.NoError(t, svc.DeleteDNSRecordsByOwner(context.Background(), ResourceKindGateway, "gw-uid-1"))
		assert.Empty(t, svc.DNSRecordStore.GetByOwnerResourceUID(ResourceKindGateway, "gw-uid-1"))
	})
}

func Test_DeleteAllDNSRecordsInGateway(t *testing.T) {
	t.Run("empty store is no-op", func(t *testing.T) {
		assert.NoError(t, newSvc().DeleteAllDNSRecordsInGateway(context.Background(), "ns1", "gw1"))
	})
	t.Run("removes all records for gateway", func(t *testing.T) {
		rec := makeRecordWithGatewayOwnerTags("rec-1", "gw-uid-1", "ns1", "gw1")
		svc := newSvc(rec)
		require.Len(t, svc.DNSRecordStore.GetByIndex(indexKeyDNSRecordNamespacedName, "ns1/gw1"), 1)
		assert.NoError(t, svc.DeleteAllDNSRecordsInGateway(context.Background(), "ns1", "gw1"))
		assert.Empty(t, svc.DNSRecordStore.GetByIndex(indexKeyDNSRecordNamespacedName, "ns1/gw1"))
	})
}

func Test_DeleteOrphanedDNSRecordsInGateway(t *testing.T) {
	tests := []struct {
		name       string
		storeRec   *DNSRecord
		owners     []*ResourceRef
		surviveKey string // if non-empty, record with this key must remain in store
		removeKey  string // if non-empty, record with this key must be gone
	}{
		{
			name:     "empty store is no-op",
			storeRec: nil,
		},
		{
			name:       "desired owner keeps record",
			storeRec:   makeRecordWithGatewayOwnerTags("rec-1", "gw-uid-1", "ns1", "gw1"),
			owners:     []*ResourceRef{{Kind: ResourceKindGateway, Object: &metav1.ObjectMeta{UID: "gw-uid-1"}}},
			surviveKey: "rec-1",
		},
		{
			name:      "unmatched owner removes orphaned record",
			storeRec:  makeRecordWithGatewayOwnerTags("rec-old", "old-uid", "ns1", "gw1"),
			owners:    []*ResourceRef{{Kind: ResourceKindGateway, Object: &metav1.ObjectMeta{UID: "new-uid"}}},
			removeKey: "rec-old",
		},
		{
			name:      "nil desired owners deletes all records",
			storeRec:  makeRecordWithGatewayOwnerTags("rec-1", "gw-uid-1", "ns1", "gw1"),
			owners:    nil,
			removeKey: "rec-1",
		},
		{
			name:      "unknown owner kind is treated as orphan",
			storeRec:  makeRecordWithGatewayOwnerTags("rec-1", "gw-uid-1", "ns1", "gw1"),
			owners:    []*ResourceRef{{Kind: "Unknown", Object: &metav1.ObjectMeta{UID: "gw-uid-1"}}},
			removeKey: "rec-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var svc *DNSRecordService
			if tt.storeRec != nil {
				svc = newSvc(tt.storeRec)
			} else {
				svc = newSvc()
			}
			err := svc.DeleteOrphanedDNSRecordsInGateway(context.Background(), "ns1", "gw1", tt.owners)
			assert.NoError(t, err)
			if tt.surviveKey != "" {
				assert.NotNil(t, svc.DNSRecordStore.GetByKey(tt.surviveKey), "record %s must survive", tt.surviveKey)
			}
			if tt.removeKey != "" {
				assert.Nil(t, svc.DNSRecordStore.GetByKey(tt.removeKey), "record %s must be removed", tt.removeKey)
			}
		})
	}
}

// Test_deleteDNSRecords_CopyOnWrite verifies that deleteDNSRecords does NOT mutate the original
// *DNSRecord pointers retrieved from the store, which is the race-condition fix.
func Test_deleteDNSRecords_CopyOnWrite(t *testing.T) {
	rec := makeRecordWithGatewayOwnerTags("rec-1", "gw-uid-1", "ns1", "gw1")
	svc := newSvc(rec)

	// Capture the live pointer stored in the index before deletion.
	before := svc.DNSRecordStore.GetByIndex(indexKeyDNSRecordNamespacedName, "ns1/gw1")
	require.Len(t, before, 1)
	original := before[0]
	require.Nil(t, original.MarkedForDelete, "precondition: MarkedForDelete must be nil")

	require.NoError(t, svc.deleteDNSRecords(context.Background(), before))

	// The original pointer and the slice element must NOT have been mutated.
	assert.Nil(t, original.MarkedForDelete, "original pointer must not be mutated (copy-on-write)")
	assert.Nil(t, before[0].MarkedForDelete, "retrieved slice element must not be mutated")
	// The record must have been removed from the store via the copy.
	assert.Nil(t, svc.DNSRecordStore.GetByKey("rec-1"), "record must be removed from store")
}

func Test_deleteDNSRecords_EmptyIsNoop(t *testing.T) {
	assert.NoError(t, newSvc().deleteDNSRecords(context.Background(), nil))
	assert.NoError(t, newSvc().deleteDNSRecords(context.Background(), []*DNSRecord{}))
}
