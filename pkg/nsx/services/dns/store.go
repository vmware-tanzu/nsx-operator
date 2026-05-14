/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"errors"
	"fmt"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// RecordStore holds in-memory NSX DNS Record rows and indexes.
type RecordStore struct {
	common.ResourceStore
}

func dnsRecordKeyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case *model.ProjectDnsRecord:
		if v.Path != nil {
			return *v.Path, nil
		}
		return "", errors.New("DNS record has nil path")
	default:
		return "", errors.New("dnsRecordKeyFunc doesn't support unknown type")
	}
}

func filterTagBy(v []model.Tag, tagScope string) []string {
	var res []string
	for _, tag := range v {
		if tag.Scope != nil && *tag.Scope == tagScope && tag.Tag != nil {
			res = append(res, *tag.Tag)
		}
	}
	return res
}

// dnsRecordCreatedForValue returns the dns_for tag value on rec, or "".
func dnsRecordCreatedForValue(rec *model.ProjectDnsRecord) string {
	return firstTagValue(rec.Tags, common.TagScopeDNSRecordFor)
}

func indexDNSRecordByOwnerTypeNN(obj interface{}) ([]string, error) {
	switch v := obj.(type) {
	case *model.ProjectDnsRecord:
		ownerKey := getDNSRecordOwnerNamespacedName(v)
		if ownerKey == "" {
			return []string{}, nil
		}
		return []string{ownerKey}, nil
	default:
		return nil, errors.New("indexDNSRecordByOwnerTypeNN doesn't support unknown type")
	}
}

// ownerCreatedForAndNNFromDNSRecord returns (dns_for, owner ns, owner name, ok) from rec tags.
func ownerCreatedForAndNNFromDNSRecord(record *model.ProjectDnsRecord) (createdFor, ns, name string, ok bool) {
	createdFor = dnsRecordCreatedForValue(record)
	if createdFor == "" {
		return "", "", "", false
	}
	ns = firstTagValue(record.Tags, common.TagScopeDNSRecordOwnerNamespace)
	name = firstTagValue(record.Tags, common.TagScopeDNSRecordOwnerName)
	if ns == "" || name == "" {
		return "", "", "", false
	}
	return createdFor, ns, name, true
}

// GroupRecordsByResourceKind groups owner NNs by ResourceRef.Kind (skips unknown dns_for).
func (s *RecordStore) GroupRecordsByResourceKind() map[string]sets.Set[types.NamespacedName] {
	ownerKindKeys := s.ListIndexFuncValues(indexKeyDNSRecordOwnerTypeNN)
	ownersByKind := make(map[string]sets.Set[types.NamespacedName])
	for key := range ownerKindKeys {
		p := strings.Split(key, "/")
		if len(p) < 3 {
			continue // bad index key shape
		}
		createdForTag, ns, name := p[0], p[1], p[2]
		resourceKind := resourceKindFromCreatedForTag(createdForTag)
		if resourceKind == "" {
			continue
		}
		nn := types.NamespacedName{Namespace: ns, Name: name}
		if ownersByKind[resourceKind] == nil {
			ownersByKind[resourceKind] = sets.New[types.NamespacedName]()
		}
		ownersByKind[resourceKind].Insert(nn)
	}
	return ownersByKind
}

// getDNSRecordOwnerNamespacedName returns owner index key "createdFor/ns/name", or "" if tags incomplete.
func getDNSRecordOwnerNamespacedName(record *model.ProjectDnsRecord) string {
	createdFor, ns, name, ok := ownerCreatedForAndNNFromDNSRecord(record)
	if !ok {
		return ""
	}
	return dnsRecordOwnerKey(createdFor, dnsRecordOwnerNamespacedNameKey(ns, name))
}

func firstTagValue(tags []model.Tag, scope string) string {
	v := filterTagBy(tags, scope)
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

// gatewayIndexKeysFromDNSRecord returns parent-gateway label keys for Route rows (comma-separated in tag).
func gatewayIndexKeysFromDNSRecord(v *model.ProjectDnsRecord) []string {
	raw := gatewayIndexTagFromRecord(v)
	if raw == "" {
		return nil
	}
	return parseGateways(raw)
}

func parseGateways(raw string) []string {
	plainGws := decompressString(raw)
	seen := sets.New[string]()
	for _, part := range strings.Split(plainGws, ",") {
		k := strings.TrimSpace(part)
		if k != "" {
			seen.Insert(k)
		}
	}
	if seen.Len() == 0 {
		return nil
	}
	return sets.List(seen)
}

func dnsRecordZonePathRecordNameIndexKey(zonePath, recordNameLower, recordType string) string {
	zp := strings.TrimSpace(zonePath)
	rn := strings.TrimSpace(strings.ToLower(recordNameLower))
	rt := strings.ToLower(strings.TrimSpace(recordType))
	return zp + "|" + rn + "|" + rt
}

func dnsRecordRecordNameLower(rec *model.ProjectDnsRecord) string {
	if rec == nil || rec.RecordName == nil {
		return ""
	}
	return strings.TrimSpace(strings.ToLower(*rec.RecordName))
}

func indexDNSRecordByZonePathRecordName(obj interface{}) ([]string, error) {
	switch v := obj.(type) {
	case *model.ProjectDnsRecord:
		dnsZone := v.ZonePath
		if dnsZone == nil || *dnsZone == "" {
			return []string{}, nil // no zone path; skip this index. This is for security purpose, should not happen in the runtime.
		}
		zp := *dnsZone
		if v.RecordType == nil {
			return []string{}, nil
		}
		rn := dnsRecordRecordNameLower(v)
		rt := strings.TrimSpace(*v.RecordType)
		if rn == "" || rt == "" { // This is for security purpose, should not happen in the runtime
			return []string{}, nil
		}
		return []string{dnsRecordZonePathRecordNameIndexKey(zp, rn, rt)}, nil
	default:
		return nil, errors.New("indexDNSRecordByZonePathRecordName doesn't support unknown type")
	}
}

func indexDNSRecordByGatewayNamespacedName(obj interface{}) ([]string, error) {
	switch v := obj.(type) {
	case *model.ProjectDnsRecord:
		createdFor := firstTagValue(v.Tags, common.TagScopeDNSRecordFor)
		if createdFor == "" {
			return []string{}, nil // record has no dns_for tag; not indexed by gateway
		}
		switch createdFor {
		case common.TagValueDNSRecordForHTTPRoute,
			common.TagValueDNSRecordForGRPCRoute,
			common.TagValueDNSRecordForTLSRoute:
			keys := gatewayIndexKeysFromDNSRecord(v)
			if len(keys) == 0 {
				return []string{}, nil
			}
			return keys, nil // route kinds only
		default:
			return []string{}, nil // no gateway index for this kind
		}
	default:
		return nil, errors.New("indexDNSRecordByGatewayNamespacedName doesn't support unknown type")
	}
}

// dnsRecordOwnerKey is "createdFor/namespace/name" for owner/contributing indexes.
func dnsRecordOwnerKey(createdFor, id string) string {
	return fmt.Sprintf("%s/%s", createdFor, id)
}

func dnsRecordOwnerNamespacedNameKey(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}

const (
	indexKeyDNSRecordOwnerTypeNN        = "ownerNamespacedName"
	indexKeyDNSRecordGatewayNN          = "gatewayNamespacedName"
	indexKeyDNSRecordZonePathRecordName = "dnsZonePathRecordName"
	indexKeyDNSRecordZonePath           = "dnsZonePath"
	indexKeyDNSRecordContributingOwner  = "contributingOwner"
)

func (s *RecordStore) Apply(i interface{}) error {
	records := i.([]*model.ProjectDnsRecord)
	for _, rec := range records {
		if rec.MarkedForDelete != nil && *rec.MarkedForDelete {
			if err := s.Delete(rec); err != nil {
				return err
			}
		} else {
			if err := s.Add(rec); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *RecordStore) GetByKey(key string) *model.ProjectDnsRecord {
	obj := s.ResourceStore.GetByKey(key)
	r, ok := obj.(*model.ProjectDnsRecord)
	if !ok {
		return nil
	}
	return r
}

func (s *RecordStore) GetByIndex(index string, value string) []*model.ProjectDnsRecord {
	objs := s.ResourceStore.GetByIndex(index, value)
	out := make([]*model.ProjectDnsRecord, 0, len(objs))
	for _, o := range objs {
		out = append(out, o.(*model.ProjectDnsRecord))
	}
	return out
}

// resourceKindFromCreatedForTag maps dns_for tag value to ResourceRef.Kind; "" if unknown.
func resourceKindFromCreatedForTag(createdFor string) string {
	switch createdFor {
	case common.TagValueDNSRecordForHTTPRoute:
		return ResourceKindHTTPRoute
	case common.TagValueDNSRecordForGRPCRoute:
		return ResourceKindGRPCRoute
	case common.TagValueDNSRecordForTLSRoute:
		return ResourceKindTLSRoute
	case common.TagValueDNSRecordForGateway:
		return ResourceKindGateway
	case common.TagValueDNSRecordForService:
		return ResourceKindService
	default:
		return ""
	}
}

// resourceKindToCreatedFor maps ResourceRef.Kind to dns_for tag value; "" if unsupported.
func resourceKindToCreatedFor(kind string) string {
	switch kind {
	case ResourceKindGateway:
		return common.TagValueDNSRecordForGateway
	case ResourceKindHTTPRoute:
		return common.TagValueDNSRecordForHTTPRoute
	case ResourceKindGRPCRoute:
		return common.TagValueDNSRecordForGRPCRoute
	case ResourceKindTLSRoute:
		return common.TagValueDNSRecordForTLSRoute
	case ResourceKindService:
		return common.TagValueDNSRecordForService
	default:
		return ""
	}
}

// DeleteMultipleObjects removes records from the in-memory store (used after NSX deletion succeeds).
func (s *RecordStore) DeleteMultipleObjects(records []*model.ProjectDnsRecord) {
	for _, rec := range records {
		if rec == nil {
			continue
		}
		if err := s.Delete(rec); err != nil {
			log.Error(err, "failed to delete DNS record from store", "path", rec.Path)
		}
	}
}

// GetByOwnerResourceNamespacedName returns all DNS records whose primary owner matches kind/namespace/name.
func (s *RecordStore) GetByOwnerResourceNamespacedName(kind, namespace, name string) []*model.ProjectDnsRecord {
	createdFor := resourceKindToCreatedFor(kind)
	if createdFor == "" {
		return nil
	}
	key := dnsRecordOwnerKey(createdFor, dnsRecordOwnerNamespacedNameKey(namespace, name))
	return s.GetByIndex(indexKeyDNSRecordOwnerTypeNN, key)
}

// ListZonePaths returns all distinct zone paths present across stored DNS records.
func (s *RecordStore) ListZonePaths() sets.Set[string] {
	vals := s.ListIndexFuncValues(indexKeyDNSRecordZonePath)
	result := sets.New[string]()
	for k := range vals {
		if k != "" {
			result.Insert(k)
		}
	}
	return result
}

func (s *RecordStore) ListDNSRecords() []*model.ProjectDnsRecord {
	objs := s.List()
	out := make([]*model.ProjectDnsRecord, 0, len(objs))
	for _, o := range objs {
		if r, ok := o.(*model.ProjectDnsRecord); ok && r != nil {
			out = append(out, r)
		}
	}
	return out
}

// ListRecordsReferencingContributingOwner returns rows whose contributing-owners tag contains contribNNKey.
// It uses the contributingOwner index for O(1) lookup instead of a full table scan.
func (s *RecordStore) ListRecordsReferencingContributingOwner(contribNNKey string) []*model.ProjectDnsRecord {
	return s.GetByIndex(indexKeyDNSRecordContributingOwner, contribNNKey)
}

func indexDNSRecordByZonePath(obj interface{}) ([]string, error) {
	switch v := obj.(type) {
	case *model.ProjectDnsRecord:
		if v.ZonePath == nil || *v.ZonePath == "" {
			return []string{}, nil
		}
		return []string{*v.ZonePath}, nil
	default:
		return nil, errors.New("indexDNSRecordByZonePath doesn't support unknown type")
	}
}

// indexDNSRecordByContributingOwner expands the comma-separated TagScopeDNSRecordContributingOwners
// tag into one index entry per contributing-owner key, enabling O(1) reverse lookup.
func indexDNSRecordByContributingOwner(obj interface{}) ([]string, error) {
	switch v := obj.(type) {
	case *model.ProjectDnsRecord:
		keys := parseContributingOwnersFromRecord(v)
		if len(keys) == 0 {
			return []string{}, nil
		}
		return keys, nil
	default:
		return nil, errors.New("indexDNSRecordByContributingOwner doesn't support unknown type")
	}
}

// BuildDNSRecordStore returns a new RecordStore with default indexers.
func BuildDNSRecordStore() *RecordStore {
	return &RecordStore{
		ResourceStore: common.ResourceStore{
			Indexer: cache.NewIndexer(dnsRecordKeyFunc, cache.Indexers{
				indexKeyDNSRecordOwnerTypeNN:        indexDNSRecordByOwnerTypeNN,
				indexKeyDNSRecordGatewayNN:          indexDNSRecordByGatewayNamespacedName,
				indexKeyDNSRecordZonePathRecordName: indexDNSRecordByZonePathRecordName,
				indexKeyDNSRecordZonePath:           indexDNSRecordByZonePath,
				indexKeyDNSRecordContributingOwner:  indexDNSRecordByContributingOwner,
			}),
			BindingType: model.ProjectDnsRecordBindingType(),
		},
	}
}
