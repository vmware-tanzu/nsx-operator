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
