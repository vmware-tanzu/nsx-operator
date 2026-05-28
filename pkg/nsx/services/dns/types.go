/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
)

const (
	ResourceKindGateway   = "Gateway"
	ResourceKindHTTPRoute = "HTTPRoute"
	ResourceKindGRPCRoute = "GRPCRoute"
	ResourceKindTLSRoute  = "TLSRoute"
	ResourceKindService   = "Service"
	// DNSRecordPathSegment is the NSX Policy path segment for project-scoped ProjectDnsRecord (same as common.PathSegmentProjectDnsRecords).
	DNSRecordPathSegment = common.PathSegmentProjectDnsRecords
)

// EndpointLabelParentGateway is the ExternalDNS Endpoint label for parent Gateway ns/name (comma-separated if merged).
const EndpointLabelParentGateway = "nsx.vmware.com/parent-gateway"

// ResourceRef is a DNS owner: Kind plus embedded ObjectMeta (namespace, name, uid).
type ResourceRef struct {
	metav1.Object
	Kind string
}

// AggregatedDNSEndpoints is one reconcile batch: optional Owner plus Endpoint rows (Owner required when Rows non-empty).
type AggregatedDNSEndpoints struct {
	Namespace string
	Rows      []EndpointRow
	Owner     *ResourceRef
}

type EndpointRow struct {
	*extdns.Endpoint
	zonePath string
	// nsxRecordName is the host prefix of the FQDN, i.e. Endpoint.DNSName with the DNS zone's domain
	// suffix (and the separating dot) stripped. For example, if DNSName is "foo.example.com" and
	// the zone domain is "example.com", nsxRecordName is "foo".
	nsxRecordName         string
	effectiveOwner        *ResourceRef // primary for shared FQDN row when adopting
	contributingOwnerKeys string       // sorted, comma-separated contributing owner index keys; matches TagScopeDNSRecordContributingOwners tag value
}

func NewEndpointRow(ep *extdns.Endpoint, zonePath string, recordName string) *EndpointRow {
	return &EndpointRow{
		Endpoint:      ep,
		zonePath:      zonePath,
		nsxRecordName: recordName,
	}
}

// NewOwnerScopedAggregatedRouteDNS returns a batch for scopeOwner and rows, or nil if scopeOwner is nil.
func NewOwnerScopedAggregatedRouteDNS(scopeOwner *ResourceRef, rows []EndpointRow) *AggregatedDNSEndpoints {
	return &AggregatedDNSEndpoints{
		Namespace: scopeOwner.GetNamespace(),
		Owner:     scopeOwner,
		Rows:      rows,
	}
}

// DNSRecordProvider is the DNS record API for Gateway Route and LoadBalancer Service DNS; *DNSRecordService implements it.
type DNSRecordProvider interface {
	CreateOrUpdateRecords(ctx context.Context, batch *AggregatedDNSEndpoints) (bool, error)
	DeleteRecordByOwnerNN(ctx context.Context, kind, namespace, name string) (bool, error)
	ValidateEndpointsByZone(namespace string, owner *ResourceRef, eps []*extdns.Endpoint) ([]EndpointRow, map[string]string, error)
	// DeleteRecordsForOwnerOutsideAllowedZones deletes the DNS records whose zone_path is not in allowedZonePaths (NSX + store).
	DeleteRecordsForOwnerOutsideAllowedZones(ctx context.Context, kind, namespace, name string, allowedZonePaths sets.Set[string]) (bool, error)
	ListReferredGatewayNN() sets.Set[types.NamespacedName]
	ListRecordOwnerResource() map[string]sets.Set[types.NamespacedName]
}
