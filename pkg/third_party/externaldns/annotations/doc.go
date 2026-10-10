// Package annotations provides ExternalDNS source annotation helpers and constants.
// Upstream: sigs.k8s.io/external-dns/source/annotations (annotations.go for prefix management and
// legacy annotation resolution; processors.go for hostname splitting).
//
// # Exported symbols
//
//	DefaultAnnotationPrefix — standard ExternalDNS prefix ("external-dns.kubernetes.io/").
//	LegacyAnnotationPrefix — legacy alpha prefix ("external-dns.alpha.kubernetes.io/").
//	HostnameKey — stable hostname annotation key ("external-dns.kubernetes.io/hostname").
//	GatewayHostnameSourceKey — stable gateway hostname source key ("external-dns.kubernetes.io/gateway-hostname-source").
//	ResolveAnnotations — copies legacy annotations to the standard prefix; standard wins on conflict.
//	SplitHostnameAnnotation — tokenizes comma-separated hostname lists.
//	HostnamesFromAnnotations — extracts hostnames preferring the stable annotation with alpha fallback.
//	HasHostnameAnnotationChanged — checks for hostname annotation changes in watch events.
//	HasRouteDNSAnnotationsChanged — checks for route DNS annotation changes in watch events.
package annotations
