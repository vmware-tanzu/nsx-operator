/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"slices"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// dnsRecordComparable adapts model.DnsRecord for common.CompareResource(s) (see ipaddressallocation/compare.go).
type dnsRecordComparable model.DnsRecord

func (d *dnsRecordComparable) Key() string {
	if d == nil {
		return ""
	}
	rec := (*model.DnsRecord)(d)
	if rec.Path == nil {
		return ""
	}
	return *rec.Path
}

func (d *dnsRecordComparable) Value() data.DataValue {
	if d == nil {
		return nil
	}
	rec := (*model.DnsRecord)(d)
	s := &model.DnsRecord{
		RecordName:   rec.RecordName,
		RecordType:   rec.RecordType,
		ZonePath:     rec.ZonePath,
		Ttl:          rec.Ttl,
		RecordValues: sortedCopyStrings(rec.RecordValues),
		Tags:         sortedNormalizedTagsForCompare(rec.Tags),
	}
	dataValue, _ := s.GetDataValue__()
	return dataValue
}

// sortedNormalizedTagsForCompare returns a copy of tags sorted by (scope, value) with the
// contributing-owners tag value normalised to its canonical comma-separated sorted form.
// This ensures Value() comparisons are order-independent regardless of tag insertion order.
func sortedNormalizedTagsForCompare(tags []model.Tag) []model.Tag {
	out := append([]model.Tag{}, tags...)
	slices.SortFunc(out, func(a, b model.Tag) int {
		as, bs := "", ""
		if a.Scope != nil {
			as = *a.Scope
		}
		if b.Scope != nil {
			bs = *b.Scope
		}
		if c := strings.Compare(as, bs); c != 0 {
			return c
		}
		av, bv := "", ""
		if a.Tag != nil {
			av = *a.Tag
		}
		if b.Tag != nil {
			bv = *b.Tag
		}
		return strings.Compare(av, bv)
	})
	return out
}

func comparableToDnsRecord(c common.Comparable) *model.DnsRecord {
	if c == nil {
		return nil
	}
	dc, ok := c.(*dnsRecordComparable)
	if !ok {
		return nil
	}
	out := model.DnsRecord(*dc)
	return &out
}

// compareRecords returns (toUpsert, toRemove) for reconcile; caller marks toRemove copies deleted.
func compareRecords(desired, existing []*model.DnsRecord) (toUpsert []*model.DnsRecord, toRemove []*model.DnsRecord) {
	existingByPath := make(map[string]*model.DnsRecord)
	existingComp := make([]common.Comparable, 0, len(existing))
	for _, e := range existing {
		if e == nil || e.Path == nil {
			continue
		}
		existingByPath[*e.Path] = e
		extRecord := dnsRecordComparable(*e)
		existingComp = append(existingComp, &extRecord)
	}

	desiredComp := make([]common.Comparable, 0, len(desired))
	for _, d := range desired {
		if d == nil || d.Path == nil {
			continue
		}
		desiredRecord := dnsRecordComparable(*d)
		desiredComp = append(desiredComp, &desiredRecord)
	}

	changed, stale := common.CompareResources(existingComp, desiredComp)

	toUpsert = make([]*model.DnsRecord, 0, len(changed))
	for _, ch := range changed {
		d := comparableToDnsRecord(ch)
		if d == nil || d.Path == nil {
			continue
		}
		if ex, ok := existingByPath[*d.Path]; ok {
			toUpsert = append(toUpsert, mergeDNSRecordForUpdate(d, ex))
		} else {
			toUpsert = append(toUpsert, d)
		}
	}

	toRemove = make([]*model.DnsRecord, 0, len(stale))
	for _, st := range stale {
		if rec := comparableToDnsRecord(st); rec != nil {
			toRemove = append(toRemove, rec)
		}
	}
	return toUpsert, toRemove
}
