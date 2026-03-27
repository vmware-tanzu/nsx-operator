/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import "testing"

func TestSplitPathSegments(t *testing.T) {
	tests := []struct {
		name string
		path string
		want []string
	}{
		{
			name: "empty path",
			path: "",
			want: nil,
		},
		{
			name: "slash only",
			path: "/",
			want: nil,
		},
		{
			name: "normal path",
			path: "/orgs/default/projects/demo/infra/ip-blocks/block-1",
			want: []string{"orgs", "default", "projects", "demo", "infra", "ip-blocks", "block-1"},
		},
		{
			name: "repeated separators",
			path: "//orgs//default///projects/demo//",
			want: []string{"orgs", "default", "projects", "demo"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitPathSegments(tt.path)
			if len(got) != len(tt.want) {
				t.Fatalf("SplitPathSegments(%q) length = %d, want %d", tt.path, len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("SplitPathSegments(%q)[%d] = %q, want %q", tt.path, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestLastPathSegment(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "empty path",
			path: "",
			want: "",
		},
		{
			name: "slash only",
			path: "/",
			want: "",
		},
		{
			name: "path with trailing slash",
			path: "/infra/dhcp-ip-pools/pool-1/",
			want: "pool-1",
		},
		{
			name: "single segment",
			path: "pool-1",
			want: "pool-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LastPathSegment(tt.path); got != tt.want {
				t.Fatalf("LastPathSegment(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
