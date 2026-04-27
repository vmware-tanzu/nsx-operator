package dns

import (
	"slices"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

// buildDNSRecord builds one *Record for row using batchOwner or row.effectiveOwner.
func (s *DNSRecordService) buildDNSRecord(batchOwner *ResourceRef, row EndpointRow) *Record {
	owner := batchOwner
	if row.effectiveOwner != nil {
		owner = row.effectiveOwner
	}
	tags := s.tagsForOwner(owner)
	return row.buildDNSRecord(tags)
}

// tagsForOwner returns NSX model tags (dns_for + owner ns/name + cluster tags).
func (s *DNSRecordService) tagsForOwner(owner *ResourceRef) []model.Tag {
	basicTags := util.BuildBasicTags(getCluster(s), &gatewayv1.Gateway{}, s.Service.GetNamespaceUID(owner.GetNamespace()))
	createdFor := resourceKindToCreatedFor(owner.Kind)
	tags := append(basicTags, modelTag(common.TagScopeDNSRecordFor, createdFor))
	tags = append(tags,
		modelTag(common.TagScopeDNSRecordOwnerNamespace, owner.GetNamespace()),
		modelTag(common.TagScopeDNSRecordOwnerName, owner.GetName()),
	)
	return tags
}

func (r *EndpointRow) buildDNSRecord(basicTags []model.Tag) *Record {
	tags := r.appendEndpointTags(basicTags)
	rt := r.RecordType
	recID := strings.ReplaceAll(r.recordName, ".", "_")
	if strings.TrimSpace(rt) != "" {
		recID = recID + "_" + strings.ToLower(strings.TrimSpace(rt))
	}
	path := strings.Join([]string{r.projectPath, recID}, "/")
	display := r.recordName
	return &Record{
		Id:           common.String(recID),
		Path:         common.String(path),
		DisplayName:  common.String(display),
		Tags:         tags,
		Fqdn:         common.String(r.DNSName),
		RecordType:   &rt,
		RecordValues: append([]string(nil), r.Targets...),
		DnsZonePath:  common.String(r.zonePath),
	}
}

func (r *EndpointRow) appendEndpointTags(basicTags []model.Tag) []model.Tag {
	gwKey := ""
	if r.Endpoint != nil && r.Endpoint.Labels != nil {
		gwKey = strings.TrimSpace(r.Endpoint.Labels[EndpointLabelParentGateway])
	}
	tags := append([]model.Tag{}, basicTags...)
	if gwKey != "" {
		tags = append(tags, modelTag(common.TagScopeDNSRecordGatewayIndexList, gwKey))
	}
	ctag := formatContributingOwnersTag(r.contributingOwnerKeys)
	if ctag != "" {
		tags = append(tags, modelTag(common.TagScopeDNSRecordContributingOwners, ctag))
	}
	return tags
}

func modelTag(scope, value string) model.Tag {
	s, v := scope, value
	return model.Tag{Scope: &s, Tag: &v}
}

// compareRecords returns (toUpsert, toRemove) for reconcile; caller marks toRemove copies deleted.
func compareRecords(desired, existing []*Record) (toUpsert []*Record, toRemove []*Record) {
	desiredByID := make(map[string]*Record)
	existingByID := make(map[string]*Record)
	for _, e := range existing {
		if e == nil || e.Id == nil {
			continue
		}
		existingByID[*e.Id] = e
	}
	for _, d := range desired {
		if d == nil || d.Id == nil {
			continue
		}
		desiredByID[*d.Id] = d
		id := *d.Id
		ex, ok := existingByID[id]
		if !ok {
			toUpsert = append(toUpsert, d)
			continue
		}
		if dnsRecordSpecEqual(d, ex) {
			continue
		}
		toUpsert = append(toUpsert, mergeDNSRecordForUpdate(d, ex))
	}
	for _, ex := range existing {
		if ex == nil || ex.Id == nil {
			continue
		}
		if _, ok := desiredByID[*ex.Id]; !ok {
			toRemove = append(toRemove, ex)
		}
	}
	return toUpsert, toRemove
}

func mergeDNSRecordForUpdate(desired, existing *Record) *Record {
	out := *desired
	out.Id = existing.Id
	out.DisplayName = existing.DisplayName
	out.Path = existing.Path
	return &out
}

func dnsRecordSpecEqual(a, b *Record) bool {
	if a == nil || b == nil {
		return a == b
	}
	if !ptrStringEqual(a.Fqdn, b.Fqdn) || !ptrStringEqual(a.RecordType, b.RecordType) ||
		!ptrStringEqual(a.DnsZonePath, b.DnsZonePath) {
		return false
	}
	if !slices.Equal(sortedCopyStrings(a.RecordValues), sortedCopyStrings(b.RecordValues)) {
		return false
	}
	return dnsRecordTagsEqual(a.Tags, b.Tags)
}

func ptrStringEqual(pa, pb *string) bool {
	if pa == nil && pb == nil {
		return true
	}
	if pa == nil || pb == nil {
		return false
	}
	return *pa == *pb
}

func sortedCopyStrings(in []string) []string {
	out := append([]string(nil), in...)
	slices.Sort(out)
	return out
}

func dnsRecordTagsEqual(a, b []model.Tag) bool {
	akeys := tagCompareKeys(a)
	bkeys := tagCompareKeys(b)
	return slices.Equal(akeys, bkeys)
}

func tagCompareKeys(tags []model.Tag) []string {
	keys := make([]string, 0, len(tags))
	for _, t := range tags {
		s, v := "", ""
		if t.Scope != nil {
			s = *t.Scope
		}
		if t.Tag != nil {
			v = *t.Tag
		}
		if s == common.TagScopeDNSRecordContributingOwners {
			v = contributingTagNormalized(v)
		}
		keys = append(keys, s+"\x00"+v)
	}
	slices.Sort(keys)
	return keys
}
