// Copyright 2011 The Go Authors. All rights reserved.
// Copyright 2026 Broadcom, Inc.
//
// Use of this source code is governed by a BSD-style license that can be found in the LICENSE file.
// See: https://golang.org/LICENSE — derived from sigs.k8s.io/external-dns/source/gateway_hostname.go
//
// SPDX-License-Identifier: BSD-3-Clause
// Attribution: see package doc.go.

package source

import "unicode/utf8"

// ToLowerCaseASCII returns a lower-case version of s. See RFC 6125 6.4.1. This is an explicitly ASCII
// function to avoid sharp corners from performing Unicode operations on DNS labels (ExternalDNS gateway path).
func ToLowerCaseASCII(s string) string {
	isAlreadyLowerCase := true
	for _, c := range s {
		if c == utf8.RuneError {
			isAlreadyLowerCase = false
			break
		}
		if 'A' <= c && c <= 'Z' {
			isAlreadyLowerCase = false
			break
		}
	}
	if isAlreadyLowerCase {
		return s
	}
	out := []byte(s)
	for i, c := range out {
		if 'A' <= c && c <= 'Z' {
			out[i] += 'a' - 'A'
		}
	}
	return string(out)
}
