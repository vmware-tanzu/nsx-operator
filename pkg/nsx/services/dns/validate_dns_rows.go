/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"fmt"
	"slices"
	"strings"

	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
)

// validateEndpointRowConflict returns a row for ep in zone, or err on FQDN conflict; nil,nil if no adopt match.
func (s *DNSRecordService) validateEndpointRowConflict(zonePath string, ep *extdns.Endpoint, projectPath string, recordName string, owner *ResourceRef) (*EndpointRow, error) {
	if owner == nil {
		return NewEndpointRow(ep, zonePath, projectPath, recordName), nil
	}
	fqdn := strings.ToLower(ep.DNSName)
	recTypeForIdx := strings.ToLower(strings.TrimSpace(ep.RecordType))
	idxKey := dnsRecordZonePathFQDNIndexKey(zonePath, fqdn, recTypeForIdx)
	recs := s.DNSRecordStore.GetByIndex(indexKeyDNSRecordZonePathFQDN, idxKey)
	if len(recs) == 0 {
		return NewEndpointRow(ep, zonePath, projectPath, recordName), nil // no existing row in zone
	}

	createdFor := resourceKindToCreatedFor(owner.Kind)
	if createdFor == "" {
		return nil, fmt.Errorf("unsupported resource kind for DNS record")
	}

	currentNNKey := ownerNNIndexKeyForResourceRef(owner)

	for _, rec := range recs {
		if getDNSRecordOwnerNamespacedName(rec) == currentNNKey {
			return NewEndpointRow(ep, zonePath, projectPath, recordName), nil // same owner, no conflict
		}
		if *rec.RecordType != ep.RecordType {
			continue
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
		row := NewEndpointRow(ep, zonePath, projectPath, recordName)
		primaryNN := primaryOwnerNNIndexKeyFromRecord(rec)
		row.effectiveOwner = effectiveOwner
		row.contributingOwnerKeys = mergeContributingOwnerKeys(parseContributingOwnersFromRecord(rec), currentNNKey, primaryNN)
		return row, nil
	}
	return nil, nil
}
