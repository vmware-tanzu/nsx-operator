/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import "strings"

// SplitPathSegments returns the non-empty path segments from a slash-delimited path.
func SplitPathSegments(path string) []string {
	var parts []string
	for _, part := range strings.Split(path, "/") {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

// LastPathSegment returns the last non-empty segment from a slash-delimited path.
func LastPathSegment(path string) string {
	parts := SplitPathSegments(path)
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}
