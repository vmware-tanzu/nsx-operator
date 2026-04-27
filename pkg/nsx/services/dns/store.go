/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"errors"
	"fmt"
	"slices"
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

// TODO: update to use the NSX DNS record model
func dnsRecordKeyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case *Record:
		if v.Path != nil {
			return *v.Path, nil
		}
		return "", errors.New("DNS record has nil path")
	default:
		return "", errors.New("dnsRecordKeyFunc doesn't support unknown type")
	}
}

func filterTagBy(v []model.Tag, tagScope string) []string {
	res := make([]string, 0)
	for _, tag := range v {
		if tag.Scope != nil && *tag.Scope == tagScope && tag.Tag != nil {
			res = append(res, *tag.Tag)
		}
	}
	return res
}

// dnsRecordCreatedForValue returns the dns_for tag value on rec, or "".
func dnsRecordCreatedForValue(rec *Record) string {
	return firstTagValue(rec.Tags, common.TagScopeDNSRecordFor)
}

func indexDNSRecordByOwnerTypeNN(obj interface{}) ([]string, error) {
	switch v := obj.(type) {
	case *Record:
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
func ownerCreatedForAndNNFromDNSRecord(record *Record) (createdFor, ns, name string, ok bool) {
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
func getDNSRecordOwnerNamespacedName(record *Record) string {
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
func gatewayIndexKeysFromDNSRecord(v *Record) []string {
	raw := firstTagValue(v.Tags, common.TagScopeDNSRecordGatewayIndexList)
	if raw == "" {
		return nil
	}
	seen := sets.New[string]()
	for _, part := range strings.Split(raw, ",") {
		k := strings.TrimSpace(part)
		if k != "" {
			seen.Insert(k)
		}
	}
	if seen.Len() == 0 {
		return nil
	}
	keys := make([]string, 0, seen.Len())
	for k := range seen {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func dnsRecordZonePathFQDNIndexKey(zonePath, fqdnLower, recordType string) string {
	zp := strings.TrimSpace(zonePath)
	fq := strings.TrimSpace(strings.ToLower(fqdnLower))
	rt := strings.ToLower(strings.TrimSpace(recordType))
	return zp + "|" + fq + "|" + rt
}

func indexDNSRecordByZonePathFQDN(obj interface{}) ([]string, error) {
	switch v := obj.(type) {
	case *Record:
		dnsZone := v.DnsZonePath
		if dnsZone == nil || *dnsZone == "" {
			return nil, errors.New("DNS Zone path is not set")
		}
		zp := *dnsZone
		fq := strings.TrimSpace(strings.ToLower(*v.Fqdn))
		rt := strings.TrimSpace(*v.RecordType)
		if fq == "" || rt == "" {
			return []string{}, nil
		}
		return []string{dnsRecordZonePathFQDNIndexKey(zp, fq, rt)}, nil
	default:
		return nil, errors.New("indexDNSRecordByZonePathFQDN doesn't support unknown type")
	}
}

func indexDNSRecordByGatewayNamespacedName(obj interface{}) ([]string, error) {
	switch v := obj.(type) {
	case *Record:
		createdFor := firstTagValue(v.Tags, common.TagScopeDNSRecordFor)
		if createdFor == "" {
			return []string{}, errors.New("indexDNSRecordByGatewayNamespacedName doesn't support unknown type")
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
	indexKeyDNSRecordOwnerTypeNN  = "ownerNamespacedName"
	indexKeyDNSRecordGatewayNN    = "gatewayNamespacedName"
	indexKeyDNSRecordZonePathFQDN = "dnsZonePathFqdn"
)

func (s *RecordStore) Apply(i interface{}) error {
	if i == nil {
		return nil
	}
	records := i.([]*Record)
	for _, rec := range records {
		if rec.MarkedForDelete != nil && *rec.MarkedForDelete { // remove from store
			if err := s.Delete(rec); err != nil {
				return err
			}
		} else { // add or update
			if err := s.Add(rec); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *RecordStore) GetByKey(key string) *Record {
	obj := s.ResourceStore.GetByKey(key)
	r, ok := obj.(*Record)
	if !ok {
		return nil
	}
	return r
}

func (s *RecordStore) GetByIndex(index string, value string) []*Record {
	objs := s.ResourceStore.GetByIndex(index, value)
	out := make([]*Record, 0, len(objs))
	for _, o := range objs {
		out = append(out, o.(*Record))
	}
	return out
}

func (s *RecordStore) GetByOwnerResourceNamespacedName(resourceKind string, namespace, name string) []*Record {
	createdFor := resourceKindToCreatedFor(resourceKind)
	if createdFor == "" {
		return []*Record{}
	}

	primaryKey := dnsRecordOwnerKey(createdFor, dnsRecordOwnerNamespacedNameKey(namespace, name))
	return s.GetByIndex(indexKeyDNSRecordOwnerTypeNN, primaryKey)
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

// ListDNSRecords returns all *Record in the store.
func (s *RecordStore) ListDNSRecords() []*Record {
	objs := s.List()
	out := make([]*Record, 0, len(objs))
	for _, o := range objs {
		if r, ok := o.(*Record); ok && r != nil {
			out = append(out, r)
		}
	}
	return out
}

// ListRecordsReferencingContributingOwner returns rows whose contributing-owners tag contains deletedNNKey.
func (s *RecordStore) ListRecordsReferencingContributingOwner(deletedNNKey string) []*Record {
	if deletedNNKey == "" {
		return nil
	}
	var out []*Record
	for _, rec := range s.ListDNSRecords() {
		for _, k := range parseContributingOwnersFromRecord(rec) {
			if k == deletedNNKey {
				out = append(out, rec)
				break
			}
		}
	}
	return out
}

// BuildDNSRecordStore returns a new RecordStore with default indexers.
func BuildDNSRecordStore() *RecordStore {
	return &RecordStore{
		ResourceStore: common.ResourceStore{
			Indexer: cache.NewIndexer(dnsRecordKeyFunc, cache.Indexers{
				indexKeyDNSRecordOwnerTypeNN:  indexDNSRecordByOwnerTypeNN,
				indexKeyDNSRecordGatewayNN:    indexDNSRecordByGatewayNamespacedName,
				indexKeyDNSRecordZonePathFQDN: indexDNSRecordByZonePathFQDN,
			}),
			BindingType: nil,
		},
	}
}
