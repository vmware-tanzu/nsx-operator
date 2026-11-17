/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/mock/gomock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	dnsrecmocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/dnsrecordsclient"
	orgrootmocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/orgrootclient"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
)

func TestDNSRecordPatchFailurePreservesCacheAndRetries(t *testing.T) {
	for _, operation := range []string{"create", "delete"} {
		t.Run(operation, func(t *testing.T) {
			ctx := context.Background()
			store := BuildDNSRecordStore()
			env := newTestDNSRecordService(t, store)
			owner := &ResourceRef{
				Kind:   ResourceKindDNSRecord,
				Object: &metav1.ObjectMeta{Namespace: "ns", Name: "record"},
			}
			ep := extdns.NewEndpoint("api.example.com", extdns.RecordTypeA, "192.0.2.1")
			row := NewEndpointRow(ep, testDNSZonePathT, "api")
			batch := NewOwnerScopedAggregatedRouteDNS(owner, []EndpointRow{*row})
			registerBodiesForBatch(t, env, batch)
			if operation == "delete" {
				_, err := env.CreateOrUpdateRecords(ctx, batch)
				require.NoError(t, err)
			}

			patchClient := orgrootmocks.NewMockOrgRootClient(gomock.NewController(t))
			gomock.InOrder(
				patchClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(errors.New("NSX unavailable")),
				patchClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil),
			)
			env.NSXClient.OrgRootClient = patchClient
			sync := func() (bool, error) {
				if operation == "delete" {
					return env.DeleteRecordByOwnerNN(ctx, owner.Kind, owner.GetNamespace(), owner.GetName())
				}
				return env.CreateOrUpdateRecords(ctx, batch)
			}

			before := store.ListDNSRecords()
			changed, err := sync()
			require.ErrorContains(t, err, "NSX unavailable")
			require.False(t, changed)
			require.Equal(t, before, store.ListDNSRecords(), "failed PATCH must not change the cache")

			changed, err = sync()
			require.NoError(t, err)
			require.True(t, changed, "retry must perform the failed operation")
			if operation == "delete" {
				require.Empty(t, store.ListDNSRecords())
			} else {
				require.Len(t, store.ListDNSRecords(), 1)
			}
		})
	}
}

func TestDNSRecordGetFailureOnlyAppliesConfirmedRecords(t *testing.T) {
	env := newTestDNSRecordService(t, BuildDNSRecordStore())
	record := func(id string) *model.DnsRecord {
		return &model.DnsRecord{
			Id:           servicecommon.String(id),
			Path:         servicecommon.String("/orgs/org1/projects/proj1/dns-records/" + id),
			RecordName:   servicecommon.String(id),
			RecordType:   servicecommon.String("A"),
			ZonePath:     servicecommon.String(testDNSZonePathT),
			RecordValues: []string{"192.0.2.1"},
		}
	}
	failed, confirmed := record("failed"), record("confirmed")
	getClient := dnsrecmocks.NewMockDnsRecordsClient(gomock.NewController(t))
	getClient.EXPECT().Get("org1", "proj1", "failed").Return(model.DnsRecord{}, errors.New("NSX GET unavailable"))
	getClient.EXPECT().Get("org1", "proj1", "confirmed").Return(*confirmed, nil)
	env.NSXClient.DnsRecordsClient = getClient

	applied, err := env.syncDnsRecordsInNSX(context.Background(), []*model.DnsRecord{failed, confirmed}, nil)
	require.ErrorContains(t, err, "NSX GET unavailable")
	require.Equal(t, []*model.DnsRecord{confirmed}, applied)
}

func TestDNSRecordApexIDDoesNotCollideWithHostname(t *testing.T) {
	for _, recordType := range []string{"A", "AAAA"} {
		t.Run(recordType, func(t *testing.T) {
			_, apexPath, _ := getRecordIDAndPathAndType("@", recordType, testDNSZonePathT)
			paths := map[string]bool{apexPath: true}
			for _, name := range []string{"apex", "_apex", "__apex"} {
				_, hostPath, _ := getRecordIDAndPathAndType(name, recordType, testDNSZonePathT)
				require.False(t, paths[hostPath], "record %q must have a distinct NSX path", name)
				paths[hostPath] = true
			}
		})
	}
}

func TestDNSRecordIDDoesNotCollideAcrossNamesAndZones(t *testing.T) {
	ordinaryID, _, _ := getRecordIDAndPathAndType("api", "A", testDNSZonePathT)
	apexID, _, _ := getRecordIDAndPathAndType("@", "A", testDNSZonePathT)
	require.NotEqual(t, ordinaryID, apexID)
	require.Equal(t, "dns_", ordinaryID[:4])

	zoneWithUnderscore := "/orgs/org1/projects/proj1/dns-services/ds1/zones/zone_1"
	zoneInAnotherService := "/orgs/org1/projects/proj1/dns-services/ds2/zones/zone-t"
	for _, recordType := range []string{"A", "AAAA"} {
		t.Run(recordType, func(t *testing.T) {
			paths := make(map[string]bool)
			for _, tc := range []struct{ name, zone string }{
				{"a.b", testDNSZonePathT},
				{"a_b", testDNSZonePathT},
				{"a__b", testDNSZonePathT},
				{"a", zoneWithUnderscore},
				{"a_zone", testDNSZonePathT},
				{"a.b", zoneInAnotherService},
				{"api", testDNSZonePathT},
				{"api", zoneInAnotherService},
			} {
				_, path, _ := getRecordIDAndPathAndType(tc.name, recordType, tc.zone)
				require.False(t, paths[path], "record %q in %q shares an NSX path", tc.name, tc.zone)
				paths[path] = true
			}
		})
	}
}

func TestCompareRecordsDetectsIdentityChange(t *testing.T) {
	_, newPath, _ := getRecordIDAndPathAndType("api", "A", testDNSZonePathT)
	_, existingPath, _ := getRecordIDAndPathAndType("web", "A", testDNSZonePathT)
	record := func(path string) *model.DnsRecord {
		return &model.DnsRecord{
			Path: servicecommon.String(path), RecordName: servicecommon.String("api"),
			RecordType: servicecommon.String("A"), ZonePath: servicecommon.String(testDNSZonePathT),
		}
	}
	toUpsert, toRemove := compareRecords([]*model.DnsRecord{record(newPath)}, []*model.DnsRecord{record(existingPath)})
	require.Len(t, toUpsert, 1)
	require.Len(t, toRemove, 1)
	require.Equal(t, newPath, *toUpsert[0].Path)
	require.Equal(t, existingPath, *toRemove[0].Path)
}

func TestSharedRecordKeepsIdentityAndOwners(t *testing.T) {
	for _, reconcileContributor := range []bool{false, true} {
		for _, deletePrimaryFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("contributor=%t/primaryDeletedFirst=%t", reconcileContributor, deletePrimaryFirst), func(t *testing.T) {
				ctx := context.Background()
				store := BuildDNSRecordStore()
				env := newTestDNSRecordService(t, store)
				primary := &ResourceRef{Kind: ResourceKindHTTPRoute, Object: &metav1.ObjectMeta{Namespace: "ns", Name: "route-a"}}
				contributor := &ResourceRef{Kind: ResourceKindHTTPRoute, Object: &metav1.ObjectMeta{Namespace: "ns", Name: "route-b"}}
				ep := extdns.NewEndpoint("api.example.com", extdns.RecordTypeA, "192.0.2.1")
				ep.WithLabel(EndpointLabelParentGateway, "ns/gateway")
				row := NewEndpointRow(ep, testDNSZonePathT, "api")
				row.contributingOwnerKeys = []string{ownerNNIndexKeyForResourceRef(contributor)}
				existing, err := env.BuildDnsRecord(primary, *row)
				require.NoError(t, err)
				existingID, existingPath := *existing.Id, *existing.Path
				require.NoError(t, store.Apply([]*model.DnsRecord{existing}))

				// Force an update so the mocked NSX GET returns the updated existing record.
				ep.RecordTTL = extdns.TTL(600)
				updated := *existing
				updated.Ttl = servicecommon.Int64(600)
				env.bodies[existingID] = &updated
				owner := primary
				if reconcileContributor {
					owner = contributor
				}
				batch := NewOwnerScopedAggregatedRouteDNS(owner, []EndpointRow{*row})
				// Updates and deletion of the existing record must use the same logical lock.
				logicalKey := getLockKey(testDNSZonePathT, "api", "A")
				require.Contains(t, env.getInvolvedRecordLockKeys(batch), logicalKey)
				require.Contains(t, env.getInvolvedRecordLockKeysForOwner(owner.Kind, "ns", owner.GetName()), logicalKey)
				changed, err := env.CreateOrUpdateRecords(ctx, batch)
				require.NoError(t, err)
				require.True(t, changed)
				records := store.ListDNSRecords()
				require.Len(t, records, 1)
				require.Equal(t, existingPath, *records[0].Path)
				require.Equal(t, ownerNNIndexKeyForResourceRef(primary), getDNSRecordOwnerNamespacedName(records[0]))
				require.Equal(t, []string{ownerNNIndexKeyForResourceRef(contributor)}, parseContributingOwnersFromRecord(records[0]))

				first, last := contributor, primary
				if deletePrimaryFirst {
					first, last = primary, contributor
				}
				requireNoErrDeleteDNS(ctx, t, env, first.Kind, first.GetNamespace(), first.GetName())
				records = store.ListDNSRecords()
				require.Len(t, records, 1)
				require.Equal(t, existingPath, *records[0].Path)
				require.Equal(t, ownerNNIndexKeyForResourceRef(last), getDNSRecordOwnerNamespacedName(records[0]))
				require.Empty(t, parseContributingOwnersFromRecord(records[0]))
				requireNoErrDeleteDNS(ctx, t, env, last.Kind, last.GetNamespace(), last.GetName())
				require.Empty(t, store.ListDNSRecords())
			})
		}
	}
}

func TestNewContributorSharesRecordIdentity(t *testing.T) {
	ctx := context.Background()
	store := BuildDNSRecordStore()
	env := newTestDNSRecordService(t, store)
	primary := &ResourceRef{Kind: ResourceKindHTTPRoute, Object: &metav1.ObjectMeta{Namespace: "ns", Name: "route-a"}}
	contributor := &ResourceRef{Kind: ResourceKindHTTPRoute, Object: &metav1.ObjectMeta{Namespace: "ns", Name: "route-b"}}
	ep := extdns.NewEndpoint("api.example.com", extdns.RecordTypeA, "192.0.2.1")
	ep.WithLabel(EndpointLabelParentGateway, "ns/gateway")
	row := NewEndpointRow(ep, testDNSZonePathT, "api")
	existing, err := env.BuildDnsRecord(primary, *row)
	require.NoError(t, err)
	existingID, existingPath := *existing.Id, *existing.Path
	require.NoError(t, store.Apply([]*model.DnsRecord{existing}))

	contribution := *row
	contribution.effectiveOwner = primary
	contribution.contributingOwnerKeys = []string{ownerNNIndexKeyForResourceRef(contributor)}
	updated, err := env.BuildDnsRecord(contributor, contribution)
	require.NoError(t, err)
	env.bodies[existingID] = updated

	batch := NewOwnerScopedAggregatedRouteDNS(contributor, []EndpointRow{*row})
	logicalKey := getLockKey(testDNSZonePathT, "api", "A")
	require.Contains(t, env.getInvolvedRecordLockKeysForOwner(primary.Kind, "ns", primary.GetName()), logicalKey)
	require.Contains(t, env.getInvolvedRecordLockKeys(batch), logicalKey)
	changed, err := env.CreateOrUpdateRecords(ctx, batch)
	require.NoError(t, err)
	require.True(t, changed)
	records := store.ListDNSRecords()
	require.Len(t, records, 1)
	require.Equal(t, existingPath, *records[0].Path)
	require.Equal(t, ownerNNIndexKeyForResourceRef(primary), getDNSRecordOwnerNamespacedName(records[0]))
	require.Equal(t, []string{ownerNNIndexKeyForResourceRef(contributor)}, parseContributingOwnersFromRecord(records[0]))
}

func TestPrimaryDeletionSerializesContributorReconcile(t *testing.T) {
	ctx := context.Background()
	store := BuildDNSRecordStore()
	env := newTestDNSRecordService(t, store)
	primary := &ResourceRef{Kind: ResourceKindHTTPRoute, Object: &metav1.ObjectMeta{Namespace: "ns", Name: "route-a"}}
	contributor := &ResourceRef{Kind: ResourceKindHTTPRoute, Object: &metav1.ObjectMeta{Namespace: "ns", Name: "route-b"}}
	ep := extdns.NewEndpoint("api.example.com", extdns.RecordTypeA, "192.0.2.1")
	ep.WithLabel(EndpointLabelParentGateway, "ns/gateway")
	row := NewEndpointRow(ep, testDNSZonePathT, "api")
	row.contributingOwnerKeys = []string{ownerNNIndexKeyForResourceRef(contributor)}
	existing, err := env.BuildDnsRecord(primary, *row)
	require.NoError(t, err)
	existingID, existingPath := *existing.Id, *existing.Path
	require.NoError(t, store.Apply([]*model.DnsRecord{existing}))
	promoted, err := env.recordAfterPrimaryDeletePromotion(existing, row.contributingOwnerKeys)
	require.NoError(t, err)
	env.bodies[existingID] = promoted

	patchStarted := make(chan struct{})
	releasePatch := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releasePatch) }) })
	patchClient := orgrootmocks.NewMockOrgRootClient(gomock.NewController(t))
	patchClient.EXPECT().Patch(gomock.Any(), gomock.Any()).DoAndReturn(func(_ model.OrgRoot, _ *bool) error {
		close(patchStarted)
		<-releasePatch
		return nil
	}).Times(1)
	env.NSXClient.OrgRootClient = patchClient
	deleteDone := make(chan error, 1)
	go func() {
		_, err := env.DeleteRecordByOwnerNN(ctx, primary.Kind, primary.GetNamespace(), primary.GetName())
		deleteDone <- err
	}()
	select {
	case <-patchStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("primary deletion did not reach the NSX patch")
	}

	reconcileStarted := make(chan struct{})
	reconcileDone := make(chan error, 1)
	go func() {
		close(reconcileStarted)
		_, err := env.CreateOrUpdateRecords(ctx, NewOwnerScopedAggregatedRouteDNS(contributor, []EndpointRow{*row}))
		reconcileDone <- err
	}()
	<-reconcileStarted
	select {
	case err := <-reconcileDone:
		t.Fatalf("contributor reconciled before the primary deletion finished: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(releasePatch) })
	require.NoError(t, <-deleteDone)
	require.NoError(t, <-reconcileDone)
	records := store.ListDNSRecords()
	require.Len(t, records, 1)
	require.Equal(t, existingPath, *records[0].Path)
	require.Equal(t, ownerNNIndexKeyForResourceRef(contributor), getDNSRecordOwnerNamespacedName(records[0]))
	require.Empty(t, parseContributingOwnersFromRecord(records[0]))
}

func TestDNSRecordIDCaseInsensitive(t *testing.T) {
	upperID, upperPath, _ := getRecordIDAndPathAndType("API", "A", testDNSZonePathT)
	lowerID, lowerPath, _ := getRecordIDAndPathAndType("api", "A", testDNSZonePathT)
	require.Equal(t, lowerID, upperID)
	require.Equal(t, lowerPath, upperPath)
}
