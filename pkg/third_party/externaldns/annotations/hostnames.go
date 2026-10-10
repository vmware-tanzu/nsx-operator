// Copyright 2025 The Kubernetes Authors.
// Copyright 2026 Broadcom, Inc.
//
// SPDX-License-Identifier: Apache-2.0
//
// Derived from sigs.k8s.io/external-dns/source/annotations/processors.go (hostname helpers).
// Attribution: see package doc.go.

package annotations

import (
	"strings"
)

// SplitHostnameAnnotation tokenizes a comma-separated hostname annotation string, stripping whitespace.
func SplitHostnameAnnotation(input string) []string {
	return strings.Split(strings.TrimSpace(strings.ReplaceAll(input, " ", "")), ",")
}

// HostnamesFromAnnotations extracts hostnames from annotations, preferring the stable
// hostname annotation with fallback to the legacy alpha annotation.
func HostnamesFromAnnotations(input map[string]string, hostnameKeys ...string) []string {
	if len(input) == 0 {
		return nil
	}
	key := HostnameKey
	if len(hostnameKeys) > 0 {
		key = hostnameKeys[0]
		if key == "" {
			return nil
		}
	}

	if annotation, ok := input[key]; ok {
		return SplitHostnameAnnotation(annotation)
	}

	// Fall back to legacy alpha prefix if standard prefix key is absent.
	if strings.HasPrefix(key, AnnotationKeyPrefix) {
		legacyKey := LegacyAnnotationPrefix + strings.TrimPrefix(key, AnnotationKeyPrefix)
		if annotation, ok := input[legacyKey]; ok {
			return SplitHostnameAnnotation(annotation)
		}
	}
	return nil
}
