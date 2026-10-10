// Copyright 2025 The Kubernetes Authors.
// Copyright 2026 Broadcom, Inc.
//
// SPDX-License-Identifier: Apache-2.0
//
// Derived from sigs.k8s.io/external-dns/source/annotations/annotations.go.
// Attribution: see package doc.go.

package annotations

import (
	"strings"

	log "github.com/sirupsen/logrus"
)

const (
	// DefaultAnnotationPrefix is the standard ExternalDNS annotation prefix.
	DefaultAnnotationPrefix = "external-dns.kubernetes.io/"

	// LegacyAnnotationPrefix is the backward-compatible alpha annotation prefix.
	LegacyAnnotationPrefix = "external-dns.alpha.kubernetes.io/"

	// AnnotationKeyPrefix matches the active ExternalDNS annotation prefix.
	AnnotationKeyPrefix = DefaultAnnotationPrefix

	// HostnameKey defines the desired DNS hostnames.
	HostnameKey = AnnotationKeyPrefix + "hostname"

	// GatewayHostnameSourceKey defines the hostname origin policy for Gateways and Routes.
	GatewayHostnameSourceKey = AnnotationKeyPrefix + "gateway-hostname-source"

	// LegacyHostnameKey is the legacy alpha counterpart of HostnameKey.
	LegacyHostnameKey = LegacyAnnotationPrefix + "hostname"

	// LegacyGatewayHostnameSourceKey is the legacy alpha counterpart of GatewayHostnameSourceKey.
	LegacyGatewayHostnameSourceKey = LegacyAnnotationPrefix + "gateway-hostname-source"
)

// ResolveAnnotations returns a copy of anns with legacy annotations resolved to the standard prefix.
// If anns is nil or contains no legacy annotations, it returns anns unchanged.
func ResolveAnnotations(anns map[string]string) map[string]string {
	if len(anns) == 0 {
		return anns
	}
	hasLegacy := false
	for k := range anns {
		if strings.HasPrefix(k, LegacyAnnotationPrefix) {
			hasLegacy = true
			break
		}
	}
	if !hasLegacy {
		return anns
	}
	resolved := make(map[string]string, len(anns)+2)
	for k, v := range anns {
		resolved[k] = v
	}
	resolveLegacyAnnotations(resolved)
	return resolved
}

// resolveLegacyAnnotations copies legacy-prefixed annotations to the standard prefix in place.
// When both forms exist, the standard prefix wins and a warning is logged.
func resolveLegacyAnnotations(anns map[string]string) bool {
	if len(anns) == 0 {
		return false
	}
	var legacyKeys []string
	for key := range anns {
		if strings.HasPrefix(key, LegacyAnnotationPrefix) {
			legacyKeys = append(legacyKeys, key)
		}
	}
	if len(legacyKeys) == 0 {
		return false
	}
	modified := false
	for _, key := range legacyKeys {
		value := anns[key]
		resolved := AnnotationKeyPrefix + strings.TrimPrefix(key, LegacyAnnotationPrefix)
		if existing, ok := anns[resolved]; !ok {
			anns[resolved] = value
			modified = true
		} else if existing != value {
			log.Warnf("ignoring annotation %s=%q because %s=%q is set", key, value, resolved, existing)
		}
	}
	return modified
}

// hasAnnotationChanged reports whether any baseKey (or its legacy alpha counterpart) changed between oldAnnos and newAnnos.
// It distinguishes key existence from an empty value, ensuring that adding or removing an empty annotation (e.g. to override
// a legacy alpha annotation or clear a setting) is detected.
func hasAnnotationChanged(oldAnnos, newAnnos map[string]string, baseKeys ...string) bool {
	for _, key := range baseKeys {
		if annotationEntryChanged(oldAnnos, newAnnos, key) {
			return true
		}
		if strings.HasPrefix(key, AnnotationKeyPrefix) {
			legacyKey := LegacyAnnotationPrefix + strings.TrimPrefix(key, AnnotationKeyPrefix)
			if annotationEntryChanged(oldAnnos, newAnnos, legacyKey) {
				return true
			}
		}
	}
	return false
}

func annotationEntryChanged(oldAnnos, newAnnos map[string]string, key string) bool {
	oldVal, oldOK := oldAnnos[key]
	newVal, newOK := newAnnos[key]
	return oldOK != newOK || oldVal != newVal
}

// HasHostnameAnnotationChanged reports whether the hostname annotation (stable or legacy alpha) changed.
func HasHostnameAnnotationChanged(oldAnnos, newAnnos map[string]string) bool {
	return hasAnnotationChanged(oldAnnos, newAnnos, HostnameKey)
}

// HasRouteDNSAnnotationsChanged reports whether route DNS annotations (hostname or gateway-hostname-source, stable or alpha) changed.
func HasRouteDNSAnnotationsChanged(oldAnnos, newAnnos map[string]string) bool {
	return hasAnnotationChanged(oldAnnos, newAnnos, HostnameKey, GatewayHostnameSourceKey)
}
