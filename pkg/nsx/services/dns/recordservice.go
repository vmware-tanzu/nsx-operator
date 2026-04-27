/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

var (
	log               = logger.Log
	_   RouteDNSWrite = (*DNSRecordService)(nil)
)

// DNSRecordService reconciles DNS rows against NSX (store + future HAPI); optional VPCService and DNSZoneMap for zone validation.
type DNSRecordService struct {
	common.Service
	VPCService     common.VPCServiceProvider
	DNSRecordStore *RecordStore
	DNSZoneMap     map[string]string
}

// CreateOrUpdateDNSRecords upserts batch rows into the store and NSX placeholder. Returns (storeMutated, err).
func (s *DNSRecordService) CreateOrUpdateDNSRecords(ctx context.Context, batch *AggregatedDNSEndponts) (bool, error) {
	if batch == nil {
		return false, nil // nothing to do
	}
	updated, err := s.applyDNSUpsertRows(ctx, batch)
	if err != nil {
		return false, err
	}
	if len(updated) == 0 {
		return false, nil // no-op reconcile
	}
	if err = s.applyDNSRecordUpdatesToNSXByZonePath(ctx, updated); err != nil {
		return false, err
	}
	if err := s.DNSRecordStore.Apply(updated); err != nil {
		return false, err
	}
	return true, nil
}

// applyDNSRecordUpdatesToNSXByZonePath groups updates by zone path for NSX (stub). Returns nil on success.
func (s *DNSRecordService) applyDNSRecordUpdatesToNSXByZonePath(ctx context.Context, updates []*Record) error {
	_ = ctx
	if len(updates) == 0 {
		return nil
	}
	byZone := make(map[string][]*Record)
	for _, rec := range updates {
		zk := ""
		if rec.DnsZonePath != nil {
			zk = strings.TrimSpace(*rec.DnsZonePath)
		}
		byZone[zk] = append(byZone[zk], rec)
	}
	for zonePath, group := range byZone {
		if len(group) == 0 {
			continue // skip empty zone bucket
		}
		// TODO: batch upsert/delete DNS records in NSX for this zone path (HAPI).
		_ = zonePath
	}
	return nil
}

// applyDNSUpsertRows builds store mutations for one batch. Returns (records to apply, err).
func (s *DNSRecordService) applyDNSUpsertRows(_ context.Context, batch *AggregatedDNSEndponts) ([]*Record, error) {
	if batch.Owner == nil {
		if len(batch.Rows) > 0 {
			return nil, fmt.Errorf("aggregated DNS batch has rows but Owner is nil")
		}
		return nil, nil // empty batch with no owner
	}
	var desired []*Record
	for _, ep := range batch.Rows {
		if rec := s.buildDNSRecord(batch.Owner, ep); rec != nil {
			desired = append(desired, rec)
		}
	}
	primaryOwned := s.DNSRecordStore.GetByOwnerResourceNamespacedName(batch.Owner.Kind, batch.Owner.GetNamespace(), batch.Owner.GetName())
	byID := make(map[string]*Record)
	for _, e := range primaryOwned {
		if e != nil && e.Id != nil {
			byID[*e.Id] = e
		}
	}
	for _, d := range desired {
		if d == nil || d.Id == nil || d.Path == nil {
			continue
		}
		if _, ok := byID[*d.Id]; ok {
			continue // already in primary owner set
		}
		p := strings.TrimSpace(*d.Path)
		if p == "" {
			continue
		}
		// Merge shared-FQDN rows keyed by path but indexed under effective owner (not batch owner).
		if ex := s.DNSRecordStore.GetByKey(p); ex != nil && ex.Id != nil && *ex.Id == *d.Id {
			byID[*ex.Id] = ex
		}
	}
	existingList := make([]*Record, 0, len(byID))
	for _, e := range byID {
		existingList = append(existingList, e)
	}
	toUpsert, toRemove := compareRecords(desired, existingList)
	out := append([]*Record(nil), toUpsert...)
	for _, rec := range toRemove {
		cp := *rec
		cp.MarkedForDelete = common.Bool(true)
		out = append(out, &cp)
	}
	return out, nil
}

// DeleteDNSRecordByOwnerNN deletes or retags rows for kind/ns/name. Returns (storeMutated, err).
func (s *DNSRecordService) DeleteDNSRecordByOwnerNN(ctx context.Context, kind, namespace, name string) (bool, error) {
	return s.deleteOrUpdateDNSRecordsByOwnerNN(ctx, kind, namespace, name)
}

func gatewayIndexTagFromRecord(rec *Record) string {
	if rec == nil {
		return ""
	}
	return firstTagValue(rec.Tags, common.TagScopeDNSRecordGatewayIndexList)
}

// recordAfterPrimaryDeletePromotion returns the updated *Record when the primary owner is removed but contributors remain.
func (s *DNSRecordService) recordAfterPrimaryDeletePromotion(rec *Record, sortedContribNNKeys []string) (*Record, error) {
	if len(sortedContribNNKeys) == 0 {
		return nil, fmt.Errorf("internal: promotion requires non-empty contributing owners")
	}
	promotedNN := sortedContribNNKeys[0]
	remaining := append([]string(nil), sortedContribNNKeys[1:]...)
	createdFor, ns, name, ok := parseOwnerNNIndexKey(promotedNN)
	if !ok {
		return nil, fmt.Errorf("invalid contributing owner key %q", promotedNN)
	}
	kind := resourceKindFromCreatedForTag(createdFor)
	if kind == "" {
		return nil, fmt.Errorf("unknown created-for tag in contributing owner key %q", promotedNN)
	}
	recs := s.DNSRecordStore.GetByOwnerResourceNamespacedName(kind, ns, name)
	if len(recs) == 0 {
		return nil, fmt.Errorf("promote DNS owner: no store row for new primary %s %s/%s to copy owner identity", kind, ns, name)
	}
	promo := recs[0]
	newOwner, rok := resourceRefFromDNSRecord(promo)
	if !rok {
		return nil, fmt.Errorf("promote DNS owner: existing row for new primary %s %s/%s has incomplete owner metadata", kind, ns, name)
	}
	tags := s.tagsForOwner(newOwner)
	if gw := gatewayIndexTagFromRecord(rec); gw != "" {
		tags = append(tags, modelTag(common.TagScopeDNSRecordGatewayIndexList, gw)) // preserve gateway index list
	}
	if len(remaining) > 0 {
		tags = append(tags, modelTag(common.TagScopeDNSRecordContributingOwners, formatContributingOwnersTag(remaining)))
	}
	out := *rec
	out.Tags = tags
	out.MarkedForDelete = nil
	return &out, nil
}

// recordAfterContributingRemoval removes deletedNNKey from contributing tag; returns (updatedRecord, changed).
func recordAfterContributingRemoval(rec *Record, deletedNNKey string) (*Record, bool) {
	keys := parseContributingOwnersFromRecord(rec)
	var newKeys []string
	changed := false
	for _, k := range keys {
		if k == deletedNNKey {
			changed = true
			continue
		}
		newKeys = append(newKeys, k)
	}
	if !changed {
		return nil, false // key was not in contributing list
	}
	out := *rec
	out.Tags = replaceContributingOwnersInTags(rec.Tags, newKeys)
	out.MarkedForDelete = nil
	return &out, true
}

func dedupeRecordsByPath(recs []*Record) []*Record {
	seen := make(map[string]*Record)
	for _, r := range recs {
		if r == nil || r.Path == nil {
			continue
		}
		p := strings.TrimSpace(*r.Path)
		if p == "" {
			continue
		}
		seen[p] = r
	}
	out := make([]*Record, 0, len(seen))
	for _, r := range seen {
		out = append(out, r)
	}
	return out
}

func (s *DNSRecordService) deleteOrUpdateDNSRecordsByOwnerNN(ctx context.Context, kind, namespace, name string) (bool, error) {
	createdFor := resourceKindToCreatedFor(kind)
	if createdFor == "" {
		return false, nil // kind not tracked in DNS store
	}
	deletedNNKey := dnsRecordOwnerKey(createdFor, dnsRecordOwnerNamespacedNameKey(namespace, name))

	prim := s.DNSRecordStore.GetByOwnerResourceNamespacedName(kind, namespace, name)
	extra := s.DNSRecordStore.ListRecordsReferencingContributingOwner(deletedNNKey)
	all := append([]*Record(nil), prim...)
	all = append(all, extra...)
	all = dedupeRecordsByPath(all)

	var toDelete []*Record
	var toUpdate []*Record

	for _, rec := range all {
		if rec == nil {
			continue
		}
		primaryKey := getDNSRecordOwnerNamespacedName(rec)
		contrib := parseContributingOwnersFromRecord(rec)

		if primaryKey == deletedNNKey {
			if len(contrib) == 0 {
				cp := *rec
				cp.MarkedForDelete = common.Bool(true)
				toDelete = append(toDelete, &cp)
				continue // sole owner: delete row
			}
			ck := append([]string(nil), contrib...)
			slices.Sort(ck)
			upd, err := s.recordAfterPrimaryDeletePromotion(rec, ck)
			if err != nil {
				return false, err
			}
			toUpdate = append(toUpdate, upd)
			continue
		}

		if upd, ok := recordAfterContributingRemoval(rec, deletedNNKey); ok {
			toUpdate = append(toUpdate, upd) // drop this owner from contributing tag
		}
	}

	cacheChanged := len(toDelete) > 0 || len(toUpdate) > 0
	if err := s.deleteDNSRecords(ctx, toDelete); err != nil {
		return false, err
	}
	if len(toUpdate) > 0 {
		if err := s.deleteDNSRecordsInNSX(ctx, toUpdate); err != nil {
			return false, err
		}
		if err := s.DNSRecordStore.Apply(toUpdate); err != nil {
			return false, err
		}
	}
	return cacheChanged, nil
}

// ListReferredGatewayNN returns Gateway NNs referenced by store index.
func (s *DNSRecordService) ListReferredGatewayNN() sets.Set[types.NamespacedName] {
	gatewaySet := sets.New[types.NamespacedName]()
	for elem := range s.DNSRecordStore.ListIndexFuncValues(indexKeyDNSRecordGatewayNN) {
		gwConfig := strings.Split(elem, "/")
		if len(gwConfig) < 2 {
			continue // malformed index value
		}
		gwNamespace, gwName := gwConfig[0], gwConfig[1]
		gatewaySet.Insert(types.NamespacedName{Namespace: gwNamespace, Name: gwName})
	}
	return gatewaySet
}

// DeleteDNSRecords deletes rows in NSX stub then applies MarkedForDelete in the store. Returns err.
func (s *DNSRecordService) DeleteDNSRecords(ctx context.Context, records []*Record) error {
	return s.deleteDNSRecords(ctx, records)
}

func (s *DNSRecordService) deleteDNSRecords(ctx context.Context, records []*Record) error {
	if len(records) == 0 {
		return nil
	}

	toDelete := make([]*Record, len(records))
	for i, rec := range records {
		cp := *rec
		cp.MarkedForDelete = common.Bool(true)
		toDelete[i] = &cp
	}

	if err := s.deleteDNSRecordsInNSX(ctx, toDelete); err != nil {
		return err
	}

	if err := s.DNSRecordStore.Apply(toDelete); err != nil {
		return err
	}

	return nil
}

// TODO: Implement this function to delete DNS record in NSX using HAPI
func (s *DNSRecordService) deleteDNSRecordsInNSX(_ context.Context, _ []*Record) error {
	return nil
}

func (s *DNSRecordService) getDNSZoneFromNSX(_ context.Context, _ string) (*Zone, error) {
	return nil, nil
}

func getCluster(s *DNSRecordService) string {
	return s.NSXConfig.Cluster
}
