// Package annotations provides a small subset of ExternalDNS source annotation helpers.
// Upstream: sigs.k8s.io/external-dns/source/annotations (notably processors.go for hostname splitting/list parsing;
// upstream also defines fixed annotation key constants used by the full Gateway/Ingress sources).
//
// # Direct copy from external-dns (same logic; same exported names where applicable)
//
//	SplitHostnameAnnotation — same comma-separated hostname tokenization as upstream processors.
//	HostnamesFromAnnotations(input, hostnameKey) — same parsing as upstream after resolving the key; upstream
//	overload reads a package-level hostname key constant.
//
// # Modified from external-dns
//
//	HostnamesFromAnnotations — returns nil if hostnameKey is "" or input is nil (defensive); upstream resolves
//	from a fixed key and may not short-circuit the same way.
//
// # nsx-operator / subset
//
//	This package does **not** vendor upstream annotation **key** constants. Callers (e.g. gateway controller +
//	source.RouteHostnames) pass key strings—typically from pkg/nsx/services/common—or any compatible key for tests.
package annotations
