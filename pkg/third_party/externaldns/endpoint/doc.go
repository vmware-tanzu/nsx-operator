// Package endpoint mirrors parts of ExternalDNS’s provider-agnostic DNS record model used by the
// Gateway source and providers. Upstream: sigs.k8s.io/external-dns/endpoint (see endpoint.go, utils.go).
//
// # Direct copy from external-dns (same behavior; exported names preserved unless noted)
//
//	(TTL).IsConfigured
//	NewEndpoint
//	NewEndpointWithTTL
//	(Endpoint).Key — upstream Key(); here returns EndpointKey struct (subset of fields).
//	(EndpointKey).String
//	(Endpoint).WithSetIdentifier
//	(Endpoint).WithProviderSpecific
//	(Endpoint).GetProviderSpecificProperty
//	(Endpoint).SetProviderSpecificProperty
//	(Endpoint).WithLabel
//	(Endpoint).GetBoolProviderSpecificProperty
//	(Endpoint).CheckEndpoint
//	(Endpoint).RetainProviderProperties
//	supportsAlias, isAlias — unexported; same as upstream.
//	EndpointsForHostname
//
// # Modified from external-dns
//
//	SuitableType — same A/AAAA/CNAME rules; implementation uses net/netip.ParseAddr instead of upstream net.ParseIP.
//	NewTargets — dedupe + sort uses slices.Sort (stdlib) instead of sort.Strings; semantics unchanged (sorted unique targets).
//	NewEndpointWithTTL — omits upstream logging when DNS label length > 63 (still returns nil).
//	endpoint.go — many registry/TXT/serialization helpers from upstream endpoint package are omitted (trimmed subset).
//
package endpoint
