// Package source implements Gateway API hostname and status checks aligned with ExternalDNS’s
// Gateway route source. Primary upstream reference: sigs.k8s.io/external-dns/source/gateway.go
// (gatewayRoute interface, (*gatewayRouteResolver).hosts, matchRouteToListener, gwMatchingHost, gwHost,
// isIPAddr, listener / ListenerSet handling) and source/gateway_hostname.go (ASCII lower-case).
// This package is **not** a port of the full Gateway source: there are no informers, no
// templateEngine.ExecFQDN implementation here—callers pass fqdn-template outputs into
// RouteHostnamesForRoute when needed.
//
// # Direct copy or same logic as upstream (names may differ)
//
//	ToLowerCaseASCII — upstream toLowerCaseASCII in source/gateway_hostname.go (exported here).
//	GwMatchingHost — upstream gwMatchingHost (gateway_host_matching.go).
//	GatewayCanonicalHost — upstream gwHost (exported); private DNS1123 / isAlphaNum helpers same idea as gateway.go (gateway_host_matching.go).
//	isGatewayHostIP — upstream isIPAddr; uses this repo’s endpoint.SuitableType instead of upstream endpoint.
//	conditionTrue — same idea as upstream conditionStatusIsTrue on Route parent conditions (renamed).
//
// # Factored from upstream gateway.go (same rules, different packaging)
//
//	ParentRefMatchesGateway — group/kind/name/namespace checks for a ParentReference to a Gateway
//	(namespaced ref); equivalent to the ref checks embedded in the upstream resolver, not one named upstream func.
//	HTTPRouteParentReadyForGateway, GRPCRouteParentReadyForGateway, TLSRouteParentReadyForGateway —
//	return true when the route’s status.parents entry for the given Gateway has RouteConditionAccepted=True
//	(same parent gate style as ExternalDNS for the HTTP path; see upstream gateway route readiness checks).
//	CollectAdmissionHostnameFilters — listener hostname allow-list from Gateway + ListenerSet spec listeners
//	(nil Hostname → ""); deduped; mirrors how upstream builds listener-side host filters for matchRouteToListener.
//	RouteHostnames, RouteHostnamesForRoute, mergeGatewayRouteHostnameAnnotations, appendAnnotationHostsDefault —
//	same gateway-hostname-source modes and hostname annotation merge as (*gatewayRouteResolver).hosts(); callers pass
//	gatewayHostnameSourceAnnoKey and hostnameAnnoKey strings (upstream hard-codes annotation keys in annotations package).
//	Invalid gateway-hostname-source values: upstream logs with logrus; here log/slog Default logger Warn.
//	RouteHostnames* return []string only (no error); upstream hosts() returns ([]string, error) mainly for FQDN template errors.
//	normalizeHostnamesList, NormalizeHostnameStrings — local helpers.
//	RouteSpecHostnames — HTTPRoute/GRPCRoute/TLSRoute Spec.Hostnames to trimmed strings.
//
// # nsx-operator admission (Gateway API DNS scope; upstream matchRouteToListener + gwMatchingHost)
//
//	RouteHostnamesMatchingAdmission — maps raw route hostname tokens through GwMatchingHost against allowed filters;
//	skips empty+empty filter/route pairs; empty route host with multiple listener filters yields multiple names;
//	wildcard DNS names require a non-empty hostname annotation at hostnameAnnoKey. Signature includes error for API
//	symmetry; current implementation always returns a nil error.
//	admissionMatchesForRouteHost, hostnameMoreSpecific — local helpers.
//
// # nsx-operator direct Gateway / ListenerSet DNS (not in upstream as named APIs)
//
//	ClaimGwMatchingDNSName (in gateway_host_matching.go) — ordered hostname precedence using GwMatchingHost when
//	merging annotation-derived DNS batches (Gateway before ListenerSet). Upstream builds endpoints in one Gateway
//	source pass and does not expose this batch-dedupe step.
package source
