/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"errors"
	"fmt"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// DNSRecordStore is a store for DNS record config (NSX DNSRecord resources).
type DNSRecordStore struct {
	common.ResourceStore
}

// TODO: update to use the NSX DNS record model
func dnsRecordKeyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case *DNSRecord:
		if v.Id != nil {
			return *v.Id, nil
		}
		return "", errors.New("DNSRecord has nil Id")
	default:
		return "", errors.New("dnsRecordKeyFunc doesn't support unknown type")
	}
}

func filterTagBy(v []model.Tag, tagScope string) []string {
	res := make([]string, 0, 2)
	for _, tag := range v {
		if tag.Scope != nil && *tag.Scope == tagScope && tag.Tag != nil {
			res = append(res, *tag.Tag)
		}
	}
	return res
}

// indexDNSRecordByOwnerUID indexes DNSRecord by owner UID (gateway or listenerset UID from tags).
func indexDNSRecordByOwnerUID(obj interface{}) ([]string, error) {
	switch v := obj.(type) {
	case *DNSRecord:
		ownerKey := getDNSRecordOwnerKey(v)
		if ownerKey == "" {
			return []string{}, nil
		}
		return []string{ownerKey}, nil
	default:
		return nil, errors.New("indexDNSRecordByOwnerUID doesn't support unknown type")
	}
}

func getDNSRecordOwnerKey(record *DNSRecord) string {
	createdFors := filterTagBy(record.Tags, common.TagScopeDNSRecordFor)
	if len(createdFors) == 0 {
		return ""
	}
	createdFor := createdFors[0]
	var uids []string
	switch createdFor {
	case common.TagValueDNSRecordForGateway:
		uids = filterTagBy(record.Tags, common.TagScopeGatewayUID)
	case common.TagValueDNSRecordForListenerSet:
		uids = filterTagBy(record.Tags, common.TagScopeListenerSetUID)
	case common.TagValueDNSRecordForService:
		uids = filterTagBy(record.Tags, common.TagScopeServiceUID)
	}
	if len(uids) == 0 {
		return ""
	}
	return dnsRecordOwnerKey(createdFor, uids[0])
}

func indexDNSRecordByNamespacedName(obj interface{}) ([]string, error) {
	switch v := obj.(type) {
	case *DNSRecord:
		createdFors := filterTagBy(v.Tags, common.TagScopeDNSRecordFor)
		if len(createdFors) == 0 {
			return []string{}, nil
		}
		createdFor := createdFors[0]
		switch createdFor {
		case common.TagValueDNSRecordForGateway:
			fallthrough
		case common.TagValueDNSRecordForListenerSet:
			gwNames := filterTagBy(v.Tags, common.TagScopeGatewayName)
			gwNamespaces := filterTagBy(v.Tags, common.TagScopeGatewayNamespace)
			if len(gwNames) == 0 || len(gwNamespaces) == 0 {
				return []string{}, nil
			}
			return []string{dnsRecordGatewayKey(gwNamespaces[0], gwNames[0])}, nil
		default:
			return []string{}, nil
		}
	default:
		return nil, errors.New("indexDNSRecordByOwnerUID doesn't support unknown type")
	}
}

func dnsRecordOwnerKey(createdFor string, ownerUID string) string {
	return fmt.Sprintf("%s/%s", createdFor, ownerUID)
}

func dnsRecordGatewayKey(gwNamespace, gwName string) string {
	return fmt.Sprintf("%s/%s", gwNamespace, gwName)
}

const (
	indexKeyDNSRecordOwnerUID       = "ownerUID"
	indexKeyDNSRecordNamespacedName = "gatewayNamespacedName"
)

func (s *DNSRecordStore) Apply(i interface{}) error {
	if i == nil {
		return nil
	}
	records := i.([]*DNSRecord)
	for _, rec := range records {
		if rec.MarkedForDelete != nil && *rec.MarkedForDelete {
			err := s.Delete(rec)
			if err != nil {
				return err
			}
			log.V(1).Info("delete DNSRecord from store", "id", ptrStr(rec.Id))
		} else {
			err := s.Add(rec)
			if err != nil {
				return err
			}
			log.V(1).Info("add DNSRecord to store", "id", ptrStr(rec.Id))
		}
	}

	return nil
}

func (s *DNSRecordStore) GetByKey(key string) *DNSRecord {
	obj := s.ResourceStore.GetByKey(key)
	if obj != nil {
		return obj.(*DNSRecord)
	}
	return nil
}

func (s *DNSRecordStore) GetByIndex(index string, value string) []*DNSRecord {
	objs := s.ResourceStore.GetByIndex(index, value)
	out := make([]*DNSRecord, 0, len(objs))
	for _, o := range objs {
		out = append(out, o.(*DNSRecord))
	}
	return out
}

func (s *DNSRecordStore) GetByOwnerResourceUID(resourceKind string, uid string) []*DNSRecord {
	createdFor := resourceKindToCreatedFor(resourceKind)
	if createdFor == "" {
		return []*DNSRecord{}
	}

	return s.GetByIndex(indexKeyDNSRecordOwnerUID, dnsRecordOwnerKey(createdFor, uid))
}

// resourceKindToCreatedFor maps ResourceRef.Kind to TagValueDNSRecordFor*.
func resourceKindToCreatedFor(kind string) string {
	switch kind {
	case ResourceKindGateway:
		return common.TagValueDNSRecordForGateway
	case ResourceKindListenerSet:
		return common.TagValueDNSRecordForListenerSet
	case ResourceKindService:
		return common.TagValueDNSRecordForService
	default:
		return ""
	}
}

func ptrStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// BuildDNSRecordStore returns a new DNSRecordStore with default indexers.
// BindingType is nil; replace when SDK provides model.DNSRecord and use model.DNSRecordBindingType() if needed for TransResourceToStore.
func BuildDNSRecordStore() *DNSRecordStore {
	return &DNSRecordStore{
		ResourceStore: common.ResourceStore{
			Indexer: cache.NewIndexer(dnsRecordKeyFunc, cache.Indexers{
				indexKeyDNSRecordOwnerUID:       indexDNSRecordByOwnerUID,
				indexKeyDNSRecordNamespacedName: indexDNSRecordByNamespacedName,
			}),
			BindingType: nil,
		},
	}
}
