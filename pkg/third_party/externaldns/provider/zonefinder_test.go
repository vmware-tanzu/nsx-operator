/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package provider

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestZoneIDName_FindZone_table(t *testing.T) {
	tests := []struct {
		name         string
		setup        func(z ZoneIDName)
		hostname     string
		wantID       string
		wantName     string
		wantNormHost string
	}{
		{
			name: "longest_suffix_wins",
			setup: func(z ZoneIDName) {
				z.Add("z1", "qux.baz")
				z.Add("z2", "foo.qux.baz")
			},
			hostname:     "name.foo.qux.baz",
			wantID:       "z2",
			wantName:     "foo.qux.baz",
			wantNormHost: "name.foo.qux.baz",
		},
		{
			name: "shorter_zone_when_deeper_zone_missing",
			setup: func(z ZoneIDName) {
				z.Add("z1", "qux.baz")
				z.Add("z2", "foo.qux.baz")
			},
			hostname:     "name.qux.baz",
			wantID:       "z1",
			wantName:     "qux.baz",
			wantNormHost: "name.qux.baz",
		},
		{
			name: "punycode_zone_matches_unicode_hostname",
			setup: func(z ZoneIDName) {
				z.Add("zone1", "xn--testcass-e1ae.fr")
			},
			hostname:     "example.xn--testcass-e1ae.fr",
			wantID:       "zone1",
			wantName:     "testécassé.fr",
			wantNormHost: "example.testécassé.fr",
		},
		{
			name: "underscore_metadata_label_zone",
			setup: func(z ZoneIDName) {
				z.Add("zmeta", "_foo._metadata.example.com")
			},
			hostname:     "_foo._metadata.example.com",
			wantID:       "zmeta",
			wantName:     "_foo._metadata.example.com",
			wantNormHost: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			z := make(ZoneIDName)
			tt.setup(z)
			id, name, norm := z.FindZone(tt.hostname)
			require.Equal(t, tt.wantID, id)
			require.Equal(t, tt.wantName, name)
			if tt.wantNormHost != "" {
				require.Equal(t, tt.wantNormHost, norm)
			}
		})
	}
}
