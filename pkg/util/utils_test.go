/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSha1(t *testing.T) {
	assert.Equal(t, Sha1("name"), "6ae999552a0d2dca14d62e2bc8b764d377b1dd6c")
}

func TestNormalizeName(t *testing.T) {
	shortName := strings.Repeat("a", 256)
	assert.Equal(t, NormalizeName(shortName), shortName)
	longName := strings.Repeat("a", 257)
	assert.Equal(t, NormalizeName(longName), fmt.Sprintf("%s-%s", strings.Repeat("a", 256-HashLength-1), "0c103888"))
}

func TestNormalizeLabelKey(t *testing.T) {
	shortKey := strings.Repeat("a", 256)
	assert.Equal(t, NormalizeLabelKey(shortKey), shortKey)
	longKey := strings.Repeat("a", 257) + "/def"
	assert.Equal(t, NormalizeLabelKey(longKey), "def")
}

func TestNormalizeLabels(t *testing.T) {
	shortKey := strings.Repeat("a", 256)
	longKey := strings.Repeat("a", 257) + "/def"
	longValue := strings.Repeat("v", 257)
	tests := []struct {
		name           string
		inputLabels    *map[string]string
		expectedLabels *map[string]string
	}{
		{
			name: "labels-with-long-key-including-multiple-layers",
			inputLabels: &map[string]string{
				longKey: longValue,
			},
			expectedLabels: &map[string]string{
				"def": NormalizeName(longValue),
			},
		},
		{
			name: "labels-with-short-key",
			inputLabels: &map[string]string{
				shortKey: longValue,
			},
			expectedLabels: &map[string]string{
				shortKey: NormalizeName(longValue),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedLabels, NormalizeLabels(tt.inputLabels))
		})
	}
}

func TestRemoveDuplicateStr(t *testing.T) {
	type args struct {
		strSlice []string
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{"1", args{[]string{"test", "test", "test"}}, []string{"test"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, RemoveDuplicateStr(tt.args.strSlice), "RemoveDuplicateStr(%v)", tt.args.strSlice)
		})
	}
}

func TestToUpper(t *testing.T) {
	type args struct {
		obj interface{}
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{"1", args{"test"}, "TEST"},
		{"2", args{"Tes1"}, "TES1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, ToUpper(tt.args.obj), "ToUpper(%v)", tt.args.obj)
		})
	}
}
