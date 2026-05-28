// Copyright 2026 The Kubernetes Authors.
// Copyright 2026 Broadcom, Inc.
//
// SPDX-License-Identifier: Apache-2.0
//
// Derived from sigs.k8s.io/external-dns/endpoint/utils.go
// Attribution: see package doc.go.

package endpoint

import (
	"net/netip"
)

// SuitableType returns the DNS record type for the given target:
// A for IPv4, AAAA for IPv6, CNAME for everything else.
func SuitableType(target string) string {
	ip, err := netip.ParseAddr(target)
	if err != nil {
		return RecordTypeCNAME
	}
	if ip.Is4() {
		return RecordTypeA
	}
	return RecordTypeAAAA
}

// EndpointsForHostname returns endpoint objects for each host–target combination,
// grouping targets by suitable DNS record type (A, AAAA, or CNAME).
// nsx-operator does not attach ExternalDNS registry labels here; gateway controllers may set Labels (e.g. parent Gateway) before DNS rows reach the DNS store.
func EndpointsForHostname(hostname string, targets Targets, ttl TTL) []*Endpoint {
	byType := map[string]Targets{}
	for _, t := range targets {
		rt := SuitableType(t)
		byType[rt] = append(byType[rt], t)
	}
	var endpoints []*Endpoint
	for _, rt := range []string{RecordTypeA, RecordTypeAAAA, RecordTypeCNAME} {
		if len(byType[rt]) == 0 {
			continue
		}
		ep := NewEndpointWithTTL(hostname, rt, ttl, byType[rt]...)
		if ep == nil {
			continue
		}
		endpoints = append(endpoints, ep)
	}
	return endpoints
}
