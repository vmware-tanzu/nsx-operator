// Package externaldns contains code derived from Kubernetes ExternalDNS
// (https://github.com/kubernetes-sigs/external-dns, Go module sigs.k8s.io/external-dns)
// for use inside nsx-operator only. It is a **trimmed** mirror: informers, provider glue, and most
// non-Gateway sources from upstream are intentionally omitted.
//
// Upstream layout (for comparison when reading diffs):
//
//   - source/gateway.go — Gateway/HTTPRoute/GRPCRoute/TLSRoute/ListenerSet DNS source: host merging
//     (hosts / gateway-hostname-source), gwMatchingHost / gwHost, matchRouteToListener-style admission.
//   - source/gateway_hostname.go — ASCII lower-case helper for gateway hostnames.
//   - source/annotations/processors.go — SplitHostnameAnnotation and hostname list extraction.
//   - endpoint/* — Endpoint model, Targets, EndpointsForHostname, SuitableType.
//
// Subpackages in this repo:
//
//   - [github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/annotations]: hostname parsing
//     aligned with external-dns/source/annotations; **annotation keys are caller-supplied strings**
//     (nsx-operator uses pkg/nsx/services/common constants at the gateway controller), not fixed upstream key constants.
//   - [github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint]: subset of external-dns/endpoint
//     (see endpoint/doc.go for omissions vs upstream).
//   - [github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/source]: stateless helpers and types
//     factored from external-dns/source/gateway.go (see source/doc.go); no informers or template engine.
//     gateway_host_matching.go holds GwMatchingHost / GatewayCanonicalHost (from upstream gateway.go) plus
//     nsx-operator-only ClaimGwMatchingDNSName for ordered Gateway direct-DNS batch dedupe.
//   - [github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/provider]: ZoneIDName / FindZone from
//     external-dns/provider/zonefinder.go (longest suffix zone match, IDNA label normalization).
//
// Each subpackage’s doc.go lists which symbols follow upstream closely versus nsx-operator-only behavior.
package externaldns
