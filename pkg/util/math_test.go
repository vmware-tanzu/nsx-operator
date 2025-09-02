/* Copyright © 2025 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCalculateSubnetSize(t *testing.T) {
	type args struct {
		mask int
	}
	tests := []struct {
		name string
		args args
		want int64
	}{
		{"1", args{24}, 256},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, CalculateSubnetSize(tt.args.mask), "CalculateSubnetSize(%v)", tt.args.mask)
		})
	}
}
