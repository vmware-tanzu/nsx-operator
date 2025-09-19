/* Copyright © 2025 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNormalizeId(t *testing.T) {
	type args struct {
		name string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "1",
			args: args{
				name: "k8scl-one-test",
			},
			want: "k8scl-one-test",
		},
		{
			name: "2",
			args: args{
				name: "k8scl-one:test",
			},
			want: "k8scl-one_test",
		},
		{
			name: "3",
			args: args{
				name: "k8scl-one:1234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890",
			},
			want: "k8scl-one_12345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456-e8ad9afc",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeId(tt.args.name); got != tt.want {
				t.Errorf("NormalizeId() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGenerateID(t *testing.T) {
	type args struct {
		resId  string
		prefix string
		suffix string
		index  string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "test-1",
			args: args{
				resId:  "1234-456",
				prefix: "sp",
				suffix: "",
				index:  "",
			},
			want: "sp_1234-456",
		},
		{
			name: "test-subfix",
			args: args{
				resId:  "1234-456",
				prefix: "sp",
				suffix: "scope",
				index:  "",
			},
			want: "sp_1234-456_scope",
		},
		{
			name: "test-index",
			args: args{
				resId:  "1234-456",
				prefix: "sp",
				suffix: "scope",
				index:  "4",
			},
			want: "sp_1234-456_4_scope",
		},
		{
			name: "test-scope",
			args: args{
				resId:  "1234-456",
				prefix: "",
				suffix: "scope",
				index:  "",
			},
			want: "1234-456_scope",
		},
		{
			name: "test-complex-index",
			args: args{
				resId:  "1234-456",
				prefix: "",
				suffix: "scope",
				index:  "6_7",
			},
			want: "1234-456_6_7_scope",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GenerateID(tt.args.resId, tt.args.prefix, tt.args.suffix, tt.args.index); got != tt.want {
				t.Errorf("GenerateID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGenerateDisplayName(t *testing.T) {
	type args struct {
		resName string
		prefix  string
		suffix  string
		project string
		cluster string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "test-1",
			args: args{
				resName: "1234-456",
				prefix:  "sp",
				suffix:  "",
				project: "",
			},
			want: "sp-1234-456",
		},
		{
			name: "test-suffix",
			args: args{
				resName: "1234-456",
				prefix:  "sp",
				suffix:  "scope",
				project: "",
			},
			want: "sp-1234-456-scope",
		},
		{
			name: "test-index",
			args: args{
				resName: "1234-456",
				prefix:  "sp",
				suffix:  "scope",
				project: "test",
			},
			want: "sp-1234-456-test-scope",
		},
		{
			name: "test-cluster",
			args: args{
				resName: "1234-456",
				prefix:  "",
				suffix:  "scope",
				project: "",
				cluster: "k8scl-one",
			},
			want: "k8scl-one-1234-456-scope",
		},
		{
			name: "test-project-cluster",
			args: args{
				resName: "1234-456",
				prefix:  "",
				suffix:  "scope",
				project: "test",
				cluster: "k8scl-one",
			},
			want: "k8scl-one-1234-456-test-scope",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := generateDisplayName("-", tt.args.resName, tt.args.prefix, tt.args.suffix, tt.args.project, tt.args.cluster); got != tt.want {
				t.Errorf("generateDisplayName() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGenerateTruncName(t *testing.T) {
	type args struct {
		limit   int
		resName string
		prefix  string
		suffix  string
		project string
		cluster string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "test-1",
			args: args{
				limit:   255,
				resName: "1234-456",
				prefix:  "sp",
				suffix:  "",
				project: "",
			},
			want: "sp_1234-456",
		},
		{
			name: "test-only-name",
			args: args{
				limit:   255,
				resName: "1234-456",
				prefix:  "",
				suffix:  "",
				project: "",
			},
			want: "1234-456",
		},
		{
			name: "test-suffix",
			args: args{
				limit:   255,
				resName: "1234-456",
				prefix:  "sp",
				suffix:  "scope",
				project: "",
			},
			want: "sp_1234-456_scope",
		},
		{
			name: "test-index",
			args: args{
				limit:   255,
				resName: "1234-456",
				prefix:  "sp",
				suffix:  "scope",
				project: "test",
			},
			want: "sp_1234-456_test_scope",
		},
		{
			name: "test-cluster",
			args: args{
				limit:   255,
				resName: "1234-456",
				prefix:  "",
				suffix:  "scope",
				project: "",
				cluster: "k8scl-one",
			},
			want: "k8scl-one_1234-456_scope",
		},
		{
			name: "test-project-cluster",
			args: args{
				limit:   255,
				resName: "1234-456",
				prefix:  "sr",
				suffix:  "scope",
				project: strings.Repeat("s", 300),
				cluster: "k8scl-one",
			},
			want: "sr_k8scl-one_1234-456_sssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssss_r65nx_scope",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GenerateTruncName(tt.args.limit, tt.args.resName, tt.args.prefix, tt.args.suffix, tt.args.project, tt.args.cluster); got != tt.want {
				t.Errorf("GenerateTruncName() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGenerateIDByObject(t *testing.T) {
	for _, tc := range []struct {
		name  string
		obj   metav1.Object
		expID string
	}{
		{
			name:  "no limit set",
			obj:   &metav1.ObjectMeta{Name: "abcdefg", UID: "b720ee2c-5788-4680-9796-0f93db33d8a9"},
			expID: "abcdefg_q3qpx",
		},
		{
			name:  "longer name with truncate",
			obj:   &metav1.ObjectMeta{Name: strings.Repeat("a", 256), UID: "b720ee2c-5788-4680-9796-0f93db33d8a9"},
			expID: fmt.Sprintf("%s_q3qpx", strings.Repeat("a", 249)),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var id string
			id = GenerateIDByObject(tc.obj)
			assert.Equal(t, tc.expID, id)
		})
	}
}

func TestGenerateIDByObjectWithSuffix(t *testing.T) {
	for _, tc := range []struct {
		name   string
		obj    metav1.Object
		limit  int
		suffix string
		expID  string
	}{
		{
			name:   "no limit set",
			obj:    &metav1.ObjectMeta{Name: "abcdefg", UID: "b720ee2c-5788-4680-9796-0f93db33d8a9"},
			limit:  0,
			suffix: "2",
			expID:  "abcdefg_b720ee2c-5788-4680-9796-0f93db33d8a9_2",
		},
		{
			name:   "longer name with truncate",
			obj:    &metav1.ObjectMeta{Name: strings.Repeat("a", 256), UID: "b720ee2c-5788-4680-9796-0f93db33d8a9"},
			limit:  0,
			suffix: "28e85c0b-21e4-4cab-b1c3-597639dfe752",
			expID:  fmt.Sprintf("%s_q3qpx2_28e85c0b-21e4-4cab-b1c3-597639dfe752", strings.Repeat("a", 211)),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := GenerateIDByObjectWithSuffix(tc.obj, tc.suffix)
			assert.Equal(t, tc.expID, id)
		})
	}
}

func TestGetClusterUUID(t *testing.T) {
	// Save the original clusterUUID and restore after test to avoid side effects
	origClusterUUID := clusterUUID
	defer func() { clusterUUID = origClusterUUID }()

	// Test that a new UUID is generated for a given clusterID
	clusterID := "test-cluster"
	clusterUUID = uuid.Nil
	uuid1 := GetClusterUUID(clusterID)
	assert.NotEqual(t, uuid.Nil, uuid1, "UUID should not be nil after generation")

	// Test that calling again returns the same UUID (cached)
	uuid2 := GetClusterUUID("another-cluster")
	assert.Equal(t, uuid1, uuid2, "UUID should be cached and not change for different input")

	// Test that the UUID is deterministic for the same clusterID if the cache is reset
	clusterUUID = uuid.Nil
	uuid3 := GetClusterUUID(clusterID)
	assert.Equal(t, uuid1, uuid3, "UUID should be deterministic for the same clusterID")
}

func TestNormalizeName(t *testing.T) {
	shortName := strings.Repeat("a", 256)
	assert.Equal(t, NormalizeLabelValue(shortName, truncateLabelHash), shortName)
	longName := strings.Repeat("a", 257)
	assert.Equal(t, NormalizeLabelValue(longName, truncateLabelHash), fmt.Sprintf("%s_%s", strings.Repeat("a", 256-HashLength-1), "0c103888"))
}

func TestNormalizeLabelKey(t *testing.T) {
	shortKey := strings.Repeat("a", 128)
	assert.Equal(t, NormalizeLabelKey(shortKey, truncateLabelHash), shortKey)
	longKey := strings.Repeat("a", 129) + "/def"
	assert.Equal(t, NormalizeLabelKey(longKey, truncateLabelHash), "def")
}

func TestNormalizeLabels(t *testing.T) {
	shortKey := strings.Repeat("a", 128)
	longKey := strings.Repeat("a", 129) + "/def"
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
				"def": NormalizeLabelValue(longValue, truncateLabelHash),
			},
		},
		{
			name: "labels-with-short-key",
			inputLabels: &map[string]string{
				shortKey: longValue,
			},
			expectedLabels: &map[string]string{
				shortKey: NormalizeLabelValue(longValue, truncateLabelHash),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedLabels, NormalizeLabels(tt.inputLabels))
		})
	}
}
