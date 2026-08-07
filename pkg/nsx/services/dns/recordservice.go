/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/retry"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	log                   = logger.Log
	_   DNSRecordProvider = (*DNSRecordService)(nil)
)

// NSX Policy path: /orgs/{org}/projects/{project}/dns-records/{record-id}
var dnsRecordPathRe = regexp.MustCompile(`^/orgs/([^/]+)/projects/([^/]+)/dns-records/([^/]+)$`)

// recordAccessArbitrator ensures that concurrent goroutines cannot perform operations
// on the same record ID within a zone simultaneously. It avoids the memory leaks associated with
// storing a sync.Mutex per record ID indefinitely.
type recordAccessArbitrator struct {
	mutex       sync.Mutex
	cond        *sync.Cond
	busyRecords map[string]bool
}

var recordArbitrator = func() *recordAccessArbitrator {
	a := &recordAccessArbitrator{
		busyRecords: make(map[string]bool),
	}
	a.cond = sync.NewCond(&a.mutex)
	return a
}()

func getLockKey(zonePath, recordID, recType string) string {
	return zonePath + "/" + recordID + "/" + recType
}

func (a *recordAccessArbitrator) lock(lockKey string) {
	a.cond.L.Lock()
	defer a.cond.L.Unlock()
	for a.busyRecords[lockKey] {
		a.cond.Wait()
	}
	a.busyRecords[lockKey] = true
}

func (a *recordAccessArbitrator) unlock(lockKey string) {
	a.cond.L.Lock()
	defer a.cond.L.Unlock()
	delete(a.busyRecords, lockKey)
	a.cond.Broadcast()
}

// dnsZoneCache is a thread-safe zone path → DNS domain name cache.
type dnsZoneCache struct {
	mu sync.RWMutex
	m  map[string]string
}

func newDNSZoneCache() *dnsZoneCache {
	return &dnsZoneCache{m: make(map[string]string)}
}

// NewDNSZoneCacheFromMap creates a dnsZoneCache pre-populated from m.
func NewDNSZoneCacheFromMap(m map[string]string) *dnsZoneCache {
	c := newDNSZoneCache()
	for k, v := range m {
		c.m[k] = v
	}
	return c
}

func (c *dnsZoneCache) get(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.m[key]
	return v, ok
}

func (c *dnsZoneCache) set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key] = value
}

// DNSRecordService reconciles DNS rows against NSX (store + future HAPI); optional VPCService and DNSZoneMap for zone validation.
type DNSRecordService struct {
	common.Service
	VPCService       common.VPCServiceProvider
	DNSRecordStore   *RecordStore
	DNSZoneMap       *dnsZoneCache
	DnsRecordBuilder *common.PolicyTreeBuilder[*model.DnsRecord]
}

// getInvolvedRecordLockKeys returns the unique policy paths and lock keys for all records involved in this batch.
// This includes the records the batch *wants* to manage, as well as the records the owner *currently* owns.
func (s *DNSRecordService) getInvolvedRecordLockKeys(batch *AggregatedDNSEndpoints) (lockKeys []string) {
	involvedLockKeys := make(map[string]struct{})

	if batch.Owner != nil {
		for _, row := range batch.Rows {
			recID, _, recType := getRecordIDAndPathAndType(row.nsxRecordName, row.RecordType, row.zonePath)
			involvedLockKeys[getLockKey(row.zonePath, recID, recType)] = struct{}{}
		}

		_, ownedRecs := s.collectRecordsByOwner(batch.Owner.Kind, batch.Owner.GetNamespace(), batch.Owner.GetName())
		for _, rec := range ownedRecs {
			if rec.Id != nil && rec.ZonePath != nil && rec.RecordType != nil {
				involvedLockKeys[getLockKey(*rec.ZonePath, *rec.Id, *rec.RecordType)] = struct{}{}
			}
		}
	}

	for k := range involvedLockKeys {
		lockKeys = append(lockKeys, k)
	}
	slices.Sort(lockKeys)

	return lockKeys
}

// CreateOrUpdateRecords upserts batch rows into the store and NSX placeholder. Returns (storeMutated, err).
func (s *DNSRecordService) CreateOrUpdateRecords(ctx context.Context, batch *AggregatedDNSEndpoints) (bool, error) {
	sortedRecordIDs := s.getInvolvedRecordLockKeys(batch)

	for _, id := range sortedRecordIDs {
		recordArbitrator.lock(id)
	}
	defer func() {
		// unlock in reverse order
		for i := len(sortedRecordIDs) - 1; i >= 0; i-- {
			recordArbitrator.unlock(sortedRecordIDs[i])
		}
	}()

	toUpsert, toRemove, err := s.applyDNSUpsertRows(batch)
	if err != nil {
		return false, err
	}

	if len(toUpsert) == 0 && len(toRemove) == 0 {
		return false, nil
	}

	toApply, syncErr := s.syncDnsRecordsInNSX(ctx, toUpsert, toRemove)

	// Apply whichever records were successfully processed to keep the local store in sync even on
	// partial realization failures; this prevents re-sending already-realized records next reconcile.
	if len(toApply) > 0 {
		if applyErr := s.DNSRecordStore.Apply(toApply); applyErr != nil {
			return false, applyErr
		}
	}
	return len(toApply) > 0, syncErr
}

// syncDnsRecordsInNSX sends upserts then deletes via OrgRoot hierarchy patch. For each upsert it checks
// realization on the patched record path (like Subnet), then GETs the record from NSX for the store; on realization
// failure it deletes the record via Policy API.
func (s *DNSRecordService) syncDnsRecordsInNSX(ctx context.Context, toUpsert, toRemove []*model.DnsRecord) ([]*model.DnsRecord, error) {
	removeOps := make([]*model.DnsRecord, 0, len(toRemove))
	for _, rec := range toRemove {
		cp := *rec
		cp.MarkedForDelete = common.Bool(true)
		removeOps = append(removeOps, &cp)
	}

	// Merge ops, ensuring deletes process first or aren't duplicated if upserting same path
	batch := append(append([]*model.DnsRecord(nil), toUpsert...), removeOps...)
	batch = dedupeRecordsByPath(batch) // In case of overlap, the last one in batch wins

	if len(batch) == 0 {
		return nil, nil
	}
	log.Info("Patching DnsRecord batch on NSX", "upsert", len(toUpsert), "remove", len(toRemove))
	if err := s.DnsRecordBuilder.PagingUpdateResources(ctx, batch, common.DefaultHAPIChildrenCount, s.NSXClient, nil); err != nil {
		return nil, err
	}
	if len(toUpsert) == 0 {
		return removeOps, nil
	}
	refreshed := make([]*model.DnsRecord, 0, len(toUpsert))
	var realizeErrs []error
	for _, rec := range toUpsert {
		orgID, projectID, recordID, perr := parseDnsRecordPolicyPath(*rec.Path)
		if perr != nil {
			log.Error(perr, "Failed to parse DnsRecord path, skipping record", "path", *rec.Path)
			realizeErrs = append(realizeErrs, perr)
			continue
		}

		var live model.DnsRecord
		var gerr error
		err := retry.OnError(util.NSXTRealizeRetry, func(err error) bool {
			if err == nil {
				return false
			}
			// 500087 is checked and handled at the top level retry loop
			if nsxApiErr, ok := err.(*nsxutil.NSXApiError); ok {
				if nsxApiErr.ErrorCode != nil && *nsxApiErr.ErrorCode == 500090 {
					// 500090 is returned when the object is requested before it is fully indexed/available
					return true
				}
			}
			return false
		}, func() error {
			live, gerr = s.NSXClient.DnsRecordsClient.Get(orgID, projectID, recordID)
			gerr = nsxutil.TransNSXApiError(gerr)
			// Return immediately if it's 500090 so the retry catches it
			return gerr
		})

		if err != nil {
			log.Error(err, "Failed to get realized DnsRecord from NSX", "Id", recordID)
			realizeErrs = append(realizeErrs, fmt.Errorf("failed to get record %s from NSX after realization: %w", recordID, err))
			continue
		}
		log.Debug("DnsRecord realized and refreshed", "Id", recordID)
		lc := live
		refreshed = append(refreshed, &lc)
	}
	toApply := append(append([]*model.DnsRecord(nil), refreshed...), removeOps...)
	if len(realizeErrs) > 0 {
		log.Error(errors.Join(realizeErrs...), "Some DnsRecords failed realization",
			"failed", len(realizeErrs), "succeeded", len(refreshed))
		return toApply, errors.Join(realizeErrs...)
	}
	return toApply, nil
}

// validateEndpointRowConflict returns a row for ep in zone, or an error on FQDN conflict.
func (s *DNSRecordService) validateEndpointRowConflict(zonePath string, ep *extdns.Endpoint, recordName string, owner *ResourceRef) (*EndpointRow, error) {
	createdFor := resourceKindToCreatedFor(owner.Kind)
	if createdFor == "" {
		return nil, fmt.Errorf("unsupported resource kind %q for DNS record", owner.Kind)
	}
	fqdn := strings.ToLower(ep.DNSName)
	recTypeForIdx := strings.ToLower(strings.TrimSpace(ep.RecordType))
	idxKey := dnsRecordZonePathRecordNameIndexKey(zonePath, recordName, recTypeForIdx)
	recs := s.DNSRecordStore.GetByIndex(indexKeyDNSRecordZonePathRecordName, idxKey)
	log.Debug("Checking DNS record conflict", "fqdn", fqdn, "zone", zonePath, "type", recTypeForIdx, "existingCount", len(recs))
	currentNNKey := ownerNNIndexKeyForResourceRef(owner)
	if len(recs) == 0 {
		return NewEndpointRow(ep, zonePath, recordName), nil
	}

	// Always prefer the newest record for the index key if multiple exist somehow (which shouldn't happen)
	rec := recs[0]
	for _, r := range recs {
		if r.MarkedForDelete == nil || !*r.MarkedForDelete {
			rec = r
			break
		}
	}

	if getDNSRecordOwnerNamespacedName(rec) == currentNNKey {
		row := NewEndpointRow(ep, zonePath, recordName)
		row.contributingOwnerKeys = parseContributingOwnersFromRecord(rec)
		return row, nil
	}
	extRecValues := sortedCopyStrings(rec.RecordValues)
	newRecValues := sortedCopyStrings(ep.Targets)
	if !slices.Equal(extRecValues, newRecValues) {
		err := fmt.Errorf("FQDN %s is configured with different values in DNS zone %s", fqdn, zonePath)
		log.Error(err, "FQDN targets conflict with existing record", "resource", getDNSRecordOwnerNamespacedName(rec))
		return nil, err
	}
	effectiveOwner, ok := resourceRefFromDNSRecord(rec)
	if !ok {
		err := fmt.Errorf("FQDN %s has an existing DNS record with incomplete owner metadata in DNS zone %s", fqdn, zonePath)
		log.Error(err, "cannot adopt shared DNS record")
		return nil, err
	}
	primaryNN := getDNSRecordOwnerNamespacedName(rec)
	log.Info("Adopting shared DNS record", "fqdn", fqdn, "zone", zonePath,
		"effectiveOwner", primaryNN, "currentOwner", currentNNKey)
	row := NewEndpointRow(ep, zonePath, recordName)
	row.effectiveOwner = effectiveOwner
	existingContributions := parseContributingOwnersFromRecord(rec)
	row.contributingOwnerKeys = mergeContributingOwnerKeys(existingContributions, currentNNKey, primaryNN)
	return row, nil
}

// classifyOwnerRemoval handles the three-way decision for a record that references deletedOwnerKey
// (either as primary or contributing owner) and appends to toDelete or toUpdate accordingly:
//   - primary owner, no contributors → mark for delete
//   - primary owner, has contributors → promote first contributor, append promoted record to toUpdate
//   - contributing owner → drop key from contributing tag, append updated record to toUpdate
func (s *DNSRecordService) classifyOwnerRemoval(rec *model.DnsRecord, deletedOwnerKey string, toDelete, toUpdate *[]*model.DnsRecord) error {
	contribs := parseContributingOwnersFromRecord(rec) // already sorted
	if getDNSRecordOwnerNamespacedName(rec) == deletedOwnerKey {
		if len(contribs) == 0 {
			cp := *rec
			cp.MarkedForDelete = common.Bool(true)
			*toDelete = append(*toDelete, &cp)
			return nil
		}
		upd, err := s.recordAfterPrimaryDeletePromotion(rec, contribs)
		if err != nil {
			return err
		}
		*toUpdate = append(*toUpdate, upd)
		return nil
	}
	if upd, ok := recordAfterContributingRemoval(rec, deletedOwnerKey); ok {
		*toUpdate = append(*toUpdate, upd)
	}
	return nil
}

// applyDNSUpsertRows computes upserts and removals for a reconcile batch:
//   - desired rows: build the target record, compare with store, enqueue update if changed.
//   - stale rows: records once owned by batch.Owner that are no longer in the desired set
//     are either deleted (sole owner) or re-tagged (promote/remove-contributor).
func (s *DNSRecordService) applyDNSUpsertRows(batch *AggregatedDNSEndpoints) ([]*model.DnsRecord, []*model.DnsRecord, error) {
	if batch.Owner == nil {
		if len(batch.Rows) > 0 {
			return nil, nil, fmt.Errorf("aggregated DNS batch has rows but Owner is nil")
		}
		return nil, nil, nil
	}
	log.Debug("Computing DNS record diff", "kind", batch.Owner.Kind,
		"namespace", batch.Owner.GetNamespace(), "name", batch.Owner.GetName(), "rows", len(batch.Rows))

	desiredRecs := make([]*model.DnsRecord, 0, len(batch.Rows))
	for _, row := range batch.Rows {
		// Re-evaluate the row against the current store state while under the local lock
		// to prevent overwriting existing shared records if the initial validation used a stale cache.
		freshRow, err := s.validateEndpointRowConflict(row.zonePath, row.Endpoint, row.nsxRecordName, batch.Owner)
		if err != nil {
			log.Error(err, "conflict detected during upsert, skipping row", "fqdn", row.Endpoint.DNSName)
			return nil, nil, &DNSZoneValidationError{Msg: "DNS endpoint validation failed for FQDN is taken by other resource", Cause: err}
		}
		if rec := s.BuildDnsRecord(batch.Owner, *freshRow); rec != nil {
			desiredRecs = append(desiredRecs, rec)
		}
	}

	// Collect all records where batch.Owner is primary or contributing.
	owner := batch.Owner
	ownerNNKey, ownedRecs := s.collectRecordsByOwner(owner.Kind, owner.GetNamespace(), owner.GetName())

	// compareRecords: new/content-changed records go to toUpsert; stale go to staleRecs.
	toUpsert, staleRecs := compareRecords(desiredRecs, ownedRecs)

	var toRemove []*model.DnsRecord
	for _, rec := range staleRecs {
		if err := s.classifyOwnerRemoval(rec, ownerNNKey, &toRemove, &toUpsert); err != nil {
			return nil, nil, err
		}
	}

	log.Debug("DNS record diff ready", "owner", ownerNNKey, "toUpsert", len(toUpsert), "toRemove", len(toRemove))
	return toUpsert, toRemove, nil
}

func (s *DNSRecordService) collectRecordsByOwner(ownerKind, ownerNamespace, ownerName string) (string, []*model.DnsRecord) {
	createdFor := resourceKindToCreatedFor(ownerKind)
	if createdFor == "" {
		return "", nil
	}
	ownerNNKey := dnsRecordOwnerKey(createdFor, dnsRecordOwnerNamespacedNameKey(ownerNamespace, ownerName))
	primRecs := s.DNSRecordStore.GetByOwnerResourceNamespacedName(ownerKind, ownerNamespace, ownerName)
	contribRecs := s.DNSRecordStore.ListRecordsReferencingContributingOwner(ownerNNKey)
	return ownerNNKey, dedupeRecordsByPath(slices.Concat(primRecs, contribRecs))
}

// DeleteRecordByOwnerNN deletes or retags rows for kind/ns/name. Returns (storeMutated, err).
func (s *DNSRecordService) DeleteRecordByOwnerNN(ctx context.Context, kind, namespace, name string) (bool, error) {
	// calculate distinct record IDs to lock to prevent overlapping calculations and patches
	recordIDsToLock := make(map[string]struct{})
	_, ownedRecs := s.collectRecordsByOwner(kind, namespace, name)
	for _, rec := range ownedRecs {
		if rec.Id != nil && rec.ZonePath != nil && rec.RecordType != nil {
			recordIDsToLock[getLockKey(*rec.ZonePath, *rec.Id, *rec.RecordType)] = struct{}{}
		}
	}

	// acquire locks in a deterministic order to prevent deadlocks
	var sortedRecordIDs []string
	for id := range recordIDsToLock {
		sortedRecordIDs = append(sortedRecordIDs, id)
	}
	slices.Sort(sortedRecordIDs)

	for _, id := range sortedRecordIDs {
		recordArbitrator.lock(id)
	}
	defer func() {
		// unlock in reverse order
		for i := len(sortedRecordIDs) - 1; i >= 0; i-- {
			recordArbitrator.unlock(sortedRecordIDs[i])
		}
	}()

	toUpdate, toDelete, err := s.calculateRecordsForDeletion(kind, namespace, name)
	if err != nil {
		return false, err
	}
	log.Debug("Classified DNS record removals", "kind", kind, "namespace", namespace, "name", name,
		"toDelete", len(toDelete), "toUpdate", len(toUpdate))

	cacheChanged := len(toDelete) > 0 || len(toUpdate) > 0
	if !cacheChanged {
		return false, nil
	}

	toApply, syncErr := s.syncDnsRecordsInNSX(ctx, toUpdate, toDelete)

	if len(toApply) > 0 {
		if applyErr := s.DNSRecordStore.Apply(toApply); applyErr != nil {
			return false, applyErr
		}
	}
	return len(toApply) > 0, syncErr
}

func (s *DNSRecordService) calculateRecordsForDeletion(kind, namespace, name string) (toUpdate []*model.DnsRecord, toDelete []*model.DnsRecord, err error) {
	deletedNNKey, all := s.collectRecordsByOwner(kind, namespace, name)
	if deletedNNKey == "" || len(all) == 0 {
		log.Debug("No owned DNS records found, skipping delete", "kind", kind, "namespace", namespace, "name", name)
		return nil, nil, nil
	}
	log.Info("Deleting DNS records for owner", "kind", kind, "namespace", namespace, "name", name, "count", len(all))

	for _, rec := range all {
		if err = s.classifyOwnerRemoval(rec, deletedNNKey, &toDelete, &toUpdate); err != nil {
			return nil, nil, err
		}
	}

	return toUpdate, toDelete, nil
}

func gatewayIndexTagFromRecord(rec *model.DnsRecord) string {
	return firstTagValue(rec.Tags, common.TagScopeDNSRecordGatewayIndexList)
}

// recordAfterPrimaryDeletePromotion returns the updated record when the primary owner is removed but contributors remain.
func (s *DNSRecordService) recordAfterPrimaryDeletePromotion(rec *model.DnsRecord, sortedContribNNKeys []string) (*model.DnsRecord, error) {
	promotedNN := sortedContribNNKeys[0]
	createdFor, ns, name, ok := parseOwnerNNIndexKey(promotedNN)
	if !ok {
		return nil, fmt.Errorf("invalid contributing owner key %q", promotedNN)
	}
	kind := resourceKindFromCreatedForTag(createdFor)
	if kind == "" {
		return nil, fmt.Errorf("unknown created-for tag in contributing owner key %q", promotedNN)
	}

	newOwner := &ResourceRef{Kind: kind, Object: &metav1.ObjectMeta{Namespace: ns, Name: name}}
	out := *rec
	var remaining []string
	if len(sortedContribNNKeys) > 1 {
		remaining = append(remaining, sortedContribNNKeys[1:]...)
	}
	out.Tags = appendGatewayAndContributionTags(s.tagsForOwner(newOwner), gatewayIndexTagFromRecord(rec), remaining)
	out.MarkedForDelete = nil
	return &out, nil
}

// recordAfterContributingRemoval removes deletedNNKey from the contributing tag; returns (updatedRecord, changed).
func recordAfterContributingRemoval(rec *model.DnsRecord, deletedNNKey string) (*model.DnsRecord, bool) {
	keys := parseContributingOwnersFromRecord(rec)
	if !slices.Contains(keys, deletedNNKey) {
		return nil, false
	}
	newContribKeys := slices.DeleteFunc(keys, func(k string) bool { return k == deletedNNKey })
	out := *rec
	out.Tags = replaceContributingOwnersInTags(rec.Tags, newContribKeys)
	out.MarkedForDelete = nil
	return &out, true
}

func dedupeRecordsByPath(recs []*model.DnsRecord) []*model.DnsRecord {
	seen := make(map[string]*model.DnsRecord)
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
	out := make([]*model.DnsRecord, 0, len(seen))
	for _, r := range seen {
		out = append(out, r)
	}
	return out
}

// ListRecordOwnerResource returns owner namespaced names grouped by ResourceRef.Kind from the in-memory DNS record store.
func (s *DNSRecordService) ListRecordOwnerResource() map[string]sets.Set[types.NamespacedName] {
	return s.DNSRecordStore.GroupRecordsByResourceKind()
}

// ListReferredGatewayNN returns Gateway NNs referenced by store index.
func (s *DNSRecordService) ListReferredGatewayNN() sets.Set[types.NamespacedName] {
	gatewaySet := sets.New[types.NamespacedName]()
	for elem := range s.DNSRecordStore.ListIndexFuncValues(indexKeyDNSRecordGatewayNN) {
		gwConfig := strings.Split(elem, "/")
		if len(gwConfig) < 2 {
			continue
		}
		gwNamespace, gwName := gwConfig[0], gwConfig[1]
		gatewaySet.Insert(types.NamespacedName{Namespace: gwNamespace, Name: gwName})
	}
	return gatewaySet
}

func getCluster(s *DNSRecordService) string {
	return s.NSXConfig.Cluster
}

// parseDnsRecordPolicyPath splits a Policy DnsRecord path into org, project, and record id.
func parseDnsRecordPolicyPath(path string) (orgID, projectID, recordID string, err error) {
	p := strings.TrimSpace(path)
	if p == "" {
		return "", "", "", fmt.Errorf("empty DnsRecord path")
	}
	matches := dnsRecordPathRe.FindStringSubmatch(p)
	if len(matches) != 4 {
		return "", "", "", fmt.Errorf("invalid DnsRecord path %q: expected /orgs/{org}/projects/{project}/dns-records/{record-id}", path)
	}
	orgID, projectID, recordID = matches[1], matches[2], matches[3]
	if strings.TrimSpace(recordID) == "" {
		return "", "", "", fmt.Errorf("empty record id in DnsRecord path %q", path)
	}
	return orgID, projectID, recordID, nil
}

func (s *DNSRecordService) deleteDnsRecordOnNSX(live *model.DnsRecord) error {
	orgID, projectID, recordID, err := parseDnsRecordPolicyPath(*live.Path)
	if err != nil {
		return err
	}
	log.Info("Deleting DnsRecord from NSX", "Id", recordID)
	err = s.NSXClient.DnsRecordsClient.Delete(orgID, projectID, recordID)
	return nsxutil.TransNSXApiError(err)
}
