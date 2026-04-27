/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
)

const (
	ResourceKindGateway     = "Gateway"
	ResourceKindListenerSet = "ListenerSet"
	ResourceKindHTTPRoute   = "HTTPRoute"
	ResourceKindGRPCRoute   = "GRPCRoute"
	ResourceKindTLSRoute    = "TLSRoute"
	ResourceKindService     = "Service"
)

// EndpointLabelParentGateway is the ExternalDNS Endpoint label for parent Gateway ns/name (comma-separated if merged).
const EndpointLabelParentGateway = "nsx.vmware.com/parent-gateway"

// ResourceRef is a DNS owner: Kind plus embedded ObjectMeta (namespace, name, uid).
type ResourceRef struct {
	metav1.Object
	Kind string
}

// AggregatedDNSEndponts is one reconcile batch: optional Owner plus Endpoint rows (Owner required when Rows non-empty).
type AggregatedDNSEndponts struct {
	Namespace string
	Rows      []EndpointRow
	Owner     *ResourceRef
}

type EndpointRow struct {
	*extdns.Endpoint
	zonePath              string
	recordName            string
	projectPath           string
	effectiveOwner        *ResourceRef // primary for shared FQDN row when adopting
	contributingOwnerKeys []string     // other owners (index keys), excluding primary
}

func NewEndpointRow(ep *extdns.Endpoint, zonePath string, projectPath string, recordName string) *EndpointRow {
	return &EndpointRow{
		Endpoint:    ep,
		zonePath:    zonePath,
		projectPath: projectPath,
		recordName:  recordName,
	}
}

// NewOwnerScopedAggregatedRouteDNS returns a batch for scopeOwner and rows, or nil if scopeOwner is nil.
func NewOwnerScopedAggregatedRouteDNS(scopeOwner *ResourceRef, rows []EndpointRow) *AggregatedDNSEndponts {
	if scopeOwner == nil {
		return nil
	}
	return &AggregatedDNSEndponts{
		Namespace: scopeOwner.GetNamespace(),
		Owner:     scopeOwner,
		Rows:      rows,
	}
}

// RouteDNSWrite is the DNS write API for Gateway Route and LoadBalancer Service DNS; *DNSRecordService implements it.
type RouteDNSWrite interface {
	CreateOrUpdateDNSRecords(ctx context.Context, batch *AggregatedDNSEndponts) (bool, error)
	DeleteDNSRecordByOwnerNN(ctx context.Context, kind, namespace, name string) (bool, error)
	ValidateEndpointsByDNSZone(namespace string, owner *ResourceRef, eps []*extdns.Endpoint) ([]EndpointRow, error)
}
