// Package source provides stateless helpers and types factored from external-dns/source/gateway.go.
//
// # Modified from external-dns
//
// This package contains modified, stripped-down implementations of functions found in upstream
// external-dns for Gateway API route matching, admission hostnames processing, and ParentReference
// resolution. It avoids all Kubernetes informers, caches, and template engines to provide
// strictly pure functional helpers.
//
// Notably, wrappers for checking HTTPRoute/GRPCRoute/TLSRoute "Ready" status against specific Gateways
// have been removed in favor of using standard ParentRef matching directly.
//
// # nsx-operator / subset
//
//	GatewayNamespacedNameFromParentRef - resolves a ParentReference to a Gateway NamespacedName
//	ListenerSetNamespacedNameFromParentRef - resolves a ParentReference to a ListenerSet NamespacedName
//	RouteHostnames - processes Route hostnames and annotations based on GatewayHostnameSource policies
//	BestMatchingAdmissionFilter - logic from upstream gateway.go to pick the most specific admission filter
//	BuildAdmissionHostCacheRows - builds admission cache rows for Gateway and ListenerSet
package source
