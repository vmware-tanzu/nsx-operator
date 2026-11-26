/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package topology

import "testing"

func TestBatchSnapshotValidate(t *testing.T) {
	tests := []struct {
		name     string
		snapshot BatchSnapshot
		wantErr  bool
	}{
		{name: "complete snapshot", snapshot: BatchSnapshot{Complete: true}},
		{name: "incomplete discovery", snapshot: BatchSnapshot{}, wantErr: true},
		{name: "resource type missing", snapshot: BatchSnapshot{Complete: true, Resources: []ResourceIdentity{{Key: "stable-id"}}}, wantErr: true},
		{name: "resource key missing", snapshot: BatchSnapshot{Complete: true, Resources: []ResourceIdentity{{ResourceType: ResourceDVPG}}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.snapshot.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}
