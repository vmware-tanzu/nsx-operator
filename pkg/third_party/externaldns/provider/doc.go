// Package provider mirrors small pieces of Kubernetes ExternalDNS
// (https://github.com/kubernetes-sigs/external-dns, Go module sigs.k8s.io/external-dns)
// that are not tied to a specific cloud provider implementation.
//
// zonefinder.go follows external-dns/provider/zonefinder.go (ZoneIDName, FindZone): longest
// suffix-matching zone selection and per-label IDNA (Unicode) normalization, including skipping
// IDNA conversion for labels that contain underscores (SRV/TXT-style names).
package provider
