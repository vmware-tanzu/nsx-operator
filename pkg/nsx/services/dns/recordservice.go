/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"context"
	"strings"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

var (
	log = logger.Log
)

// DNSRecordService handles validation and configuration of DNS records.
// DNSRecordStore holds NSX DNS record state; initialize with BuildDNSRecordStore() when constructing the service.
type DNSRecordService struct {
	common.Service
	DNSRecordStore *DNSRecordStore
}

func (s *DNSRecordService) CreateOrUpdateDNSRecords(ctx context.Context, dnsRecord *Record) error {
	// TODO: validate FQDNs against permitted DNS zones and create/update DNS records in NSX.
	updatedRecords, err := s.createOrUpdateDNSRecordsInNSX(ctx, nil)
	if err != nil {
		return err
	}
	s.DNSRecordStore.Apply(updatedRecords)
	return nil
}

func (s *DNSRecordService) DeleteDNSRecordsByOwner(ctx context.Context, kind string, uid string) error {
	dnsRecords := s.DNSRecordStore.GetByOwnerResourceUID(kind, uid)
	return s.deleteDNSRecords(ctx, dnsRecords)
}

func (s *DNSRecordService) DeleteAllDNSRecordsInGateway(ctx context.Context, gwNamespace, gwName string) error {
	dnsRecords := s.DNSRecordStore.GetByIndex(indexKeyDNSRecordNamespacedName, dnsRecordGatewayKey(gwNamespace, gwName))
	return s.deleteDNSRecords(ctx, dnsRecords)
}

func (s *DNSRecordService) DeleteOrphanedDNSRecordsInGateway(ctx context.Context, gwNamespace, gwName string, desiredOwners []*ResourceRef) error {
	dnsRecords := s.DNSRecordStore.GetByIndex(indexKeyDNSRecordNamespacedName, dnsRecordGatewayKey(gwNamespace, gwName))
	if len(dnsRecords) == 0 {
		return nil
	}
	desiredKeys := sets.New[string]()
	for _, o := range desiredOwners {
		createdFor := resourceKindToCreatedFor(o.Kind)
		if createdFor == "" {
			continue
		}
		desiredKeys.Insert(dnsRecordOwnerKey(createdFor, string(o.GetUID())))
	}
	var orphaned []*DNSRecord
	for _, rec := range dnsRecords {
		ownerKey := getDNSRecordOwnerKey(rec)
		if ownerKey == "" || !desiredKeys.Has(ownerKey) {
			orphaned = append(orphaned, rec)
		}
	}
	return s.deleteDNSRecords(ctx, orphaned)
}

func (s *DNSRecordService) ListGatewayNamespacedName() sets.Set[types.NamespacedName] {
	gatewaySet := sets.New[types.NamespacedName]()
	for elem := range s.DNSRecordStore.ListIndexFuncValues(indexKeyDNSRecordNamespacedName) {
		gwConfig := strings.Split(elem, "/")
		if len(gwConfig) < 2 {
			continue
		}
		gwNamespace, gwName := gwConfig[0], gwConfig[1]
		gatewaySet.Insert(types.NamespacedName{Namespace: gwNamespace, Name: gwName})
	}
	return gatewaySet
}

func (s *DNSRecordService) deleteDNSRecords(ctx context.Context, records []*DNSRecord) error {
	if len(records) == 0 {
		return nil
	}

	// Copy each record before setting MarkedForDelete.  GetByIndex returns
	// direct pointers into cache.Indexer; mutating them in place races with
	// concurrent Reconcile goroutines or the GC goroutine that may hold the
	// same pointer.  The copies share the same Id/Tags (read-only after
	// creation), so the store's keyFunc and index functions work correctly on
	// the copies.
	toDelete := make([]*DNSRecord, len(records))
	for i, rec := range records {
		cp := *rec
		cp.MarkedForDelete = common.Bool(true)
		toDelete[i] = &cp
	}

	if err := s.deleteDNSRecordsInNSX(ctx, toDelete); err != nil {
		return err
	}

	s.DNSRecordStore.Apply(toDelete)

	return nil
}

// TODO: Implement this function to create or update DNS record in NSX using HAPI
func (s *DNSRecordService) createOrUpdateDNSRecordsInNSX(ctx context.Context, records []*DNSRecord) ([]*DNSRecord, error) {
	return nil, nil
}

// TODO: Implement this function to delete DNS record in NSX using HAPI
func (s *DNSRecordService) deleteDNSRecordsInNSX(ctx context.Context, records []*DNSRecord) error {
	return nil
}
