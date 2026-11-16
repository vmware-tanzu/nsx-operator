/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import "fmt"

// DNSZoneValidationError is returned when DNS zone policy validation fails (e.g. no allowed zones,
// FQDN conflict in a zone). Use errors.As on the returned error with the wrapped Cause when present.
type DNSZoneValidationError struct {
	Msg   string
	Cause error
}

func (e *DNSZoneValidationError) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Cause)
	}
	return e.Msg
}

// Unwrap implements errors.Unwrap.
func (e *DNSZoneValidationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// DNSZoneMismatchError identifies zones that require a forced refresh.
type DNSZoneMismatchError struct{ ZonePaths []string }

func (e *DNSZoneMismatchError) Error() string {
	return fmt.Sprintf("NSX DNS record FQDN does not match the desired FQDN; refresh zones %v", e.ZonePaths)
}

// DNSZoneNotFoundError reports a zone that NSX explicitly says does not exist.
type DNSZoneNotFoundError struct {
	Path string
	Err  error
}

func (e *DNSZoneNotFoundError) Error() string {
	return fmt.Sprintf("NSX DNS zone %s not found: %v", e.Path, e.Err)
}

func (e *DNSZoneNotFoundError) Unwrap() error { return e.Err }
