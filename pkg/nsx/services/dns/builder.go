package dns

import (
	"fmt"
	"slices"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

const (
	DefaultRecordTtL = 300
)

// BuildDnsRecord builds one *model.DnsRecord for row using batchOwner or row.effectiveOwner.
func (s *DNSRecordService) BuildDnsRecord(batchOwner *ResourceRef, row EndpointRow) *model.DnsRecord {
	owner := batchOwner
	if row.effectiveOwner != nil {
		owner = row.effectiveOwner
	}
	tags := s.tagsForOwner(owner)
	return row.buildDNSRecord(tags)
}

// tagsForOwner returns NSX model tags (dns_for + owner ns/name + cluster tags).
func (s *DNSRecordService) tagsForOwner(owner *ResourceRef) []model.Tag {
	clusterTags := append(util.BuildClusterTags(getCluster(s)),
		modelTag(common.TagScopeNamespaceUID, string(s.Service.GetNamespaceUID(owner.GetNamespace()))))
	createdFor := resourceKindToCreatedFor(owner.Kind)
	tags := append(clusterTags, modelTag(common.TagScopeDNSRecordFor, createdFor))
	tags = append(tags,
		modelTag(common.TagScopeDNSRecordOwnerNamespace, owner.GetNamespace()),
		modelTag(common.TagScopeDNSRecordOwnerName, owner.GetName()),
	)
	return tags
}

// getRecordIDAndPathAndType returns the desired DnsRecord's Id, Path, and RecordType
func getRecordIDAndPathAndType(recordName, endpointRecordType, zonePath string) (string, string, string) {
	nsxRecordType := getNSXDnsRecordType(endpointRecordType)
	recID := strings.ReplaceAll(recordName, ".", "_")
	// Ignore the errors returned in `parseDnsZonePath`, as it was validated in previous steps when
	// preparing the DNS zone maps in the service.
	orgID, projectID, _, zoneID, _ := parseDnsZonePath(zonePath)
	recID = recID + "_" + zoneID
	if strings.TrimSpace(nsxRecordType) != "" {
		recID = recID + "_" + strings.ToLower(strings.TrimSpace(nsxRecordType))
	}
	recordPath := fmt.Sprintf("/orgs/%s/projects/%s/%s/%s", orgID, projectID, DNSRecordPathSegment, recID)
	return recID, recordPath, nsxRecordType
}

func (r *EndpointRow) buildDNSRecord(basicTags []model.Tag) *model.DnsRecord {
	// Append the tags according to the Endpoint labels, e.g., the parent gateway settings for a Route.
	tags := r.appendRowOwnershipTags(basicTags)
	recID, path, rt := getRecordIDAndPathAndType(r.nsxRecordName, r.RecordType, r.zonePath)
	ttl := int64(DefaultRecordTtL)
	if r.Endpoint.RecordTTL.IsConfigured() {
		ttl = int64(r.Endpoint.RecordTTL)
	}
	display := r.nsxRecordName
	rec := &model.DnsRecord{
		Id:           common.String(recID),
		Path:         common.String(path),
		RecordName:   common.String(r.nsxRecordName),
		DisplayName:  common.String(display),
		Tags:         tags,
		RecordType:   common.String(rt),
		RecordValues: append([]string(nil), r.Targets...),
		ZonePath:     common.String(r.zonePath),
		Ttl:          common.Int64(ttl),
		// Mirror logical FQDN for store indexing / conflict detection; stripped before Policy PATCH (see WrapDnsRecord).
		Fqdn: common.String(strings.ToLower(r.Endpoint.DNSName)),
	}
	return rec
}

func getNSXDnsRecordType(recType string) string {
	switch recType {
	case extdns.RecordTypeAAAA:
		return model.DnsRecord_RECORD_TYPE_AAAA
	case extdns.RecordTypeCNAME:
		return model.DnsRecord_RECORD_TYPE_CNAME
	case extdns.RecordTypeNS:
		return model.DnsRecord_RECORD_TYPE_NS
	case extdns.RecordTypePTR:
		return model.DnsRecord_RECORD_TYPE_PTR
	case extdns.RecordTypeA:
		return model.DnsRecord_RECORD_TYPE_A
	default:
		log.Info("Unknown record type is detected, using type A instead", "type", recType)
		return model.DnsRecord_RECORD_TYPE_A
	}
}

func (r *EndpointRow) appendRowOwnershipTags(ownerTags []model.Tag) []model.Tag {
	tags := append([]model.Tag{}, ownerTags...)
	gwKey := ""
	if r.Endpoint != nil && r.Endpoint.Labels != nil {
		gwKeys := strings.TrimSpace(r.Endpoint.Labels[EndpointLabelParentGateway])
		if len(gwKeys) > 0 {
			gwKey = compressString(gwKeys)
		}
	}
	return appendGatewayAndContributionTags(tags, gwKey, r.contributingOwnerKeys)
}

func modelTag(scope, value string) model.Tag {
	return model.Tag{Scope: common.String(scope), Tag: common.String(value)}
}

func mergeDNSRecordForUpdate(desired, existing *model.DnsRecord) *model.DnsRecord {
	out := *desired
	out.Id = existing.Id
	out.DisplayName = existing.DisplayName
	out.Path = existing.Path
	return &out
}

func sortedCopyStrings(in []string) []string {
	out := append([]string(nil), in...)
	slices.Sort(out)
	return out
}
