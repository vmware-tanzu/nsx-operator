/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDNSZoneValidationError_Unwrap(t *testing.T) {
	inner := fmt.Errorf("inner")
	err := &DNSZoneValidationError{Msg: "outer", Cause: inner}
	var d *DNSZoneValidationError
	require.ErrorAs(t, err, &d)
	require.ErrorIs(t, err, inner)
}

func TestDNSZoneValidationError_nilReceiver(t *testing.T) {
	var e *DNSZoneValidationError
	require.Equal(t, "", e.Error())
	require.Nil(t, e.Unwrap())
}

func TestDNSZoneValidationError_Error_table(t *testing.T) {
	tests := []struct {
		name string
		err  *DNSZoneValidationError
		want string
	}{
		{
			name: "message only (no cause)",
			err:  &DNSZoneValidationError{Msg: "zone not found"},
			want: "zone not found",
		},
		{
			name: "message with cause",
			err:  &DNSZoneValidationError{Msg: "outer", Cause: fmt.Errorf("inner cause")},
			want: "outer: inner cause",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.err.Error())
		})
	}
}
