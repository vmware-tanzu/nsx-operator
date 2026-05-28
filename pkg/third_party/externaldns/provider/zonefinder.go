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

Derived from sigs.k8s.io/external-dns/provider/zonefinder.go (ZoneIDName, FindZone).
*/

package provider

import (
	"strings"

	"golang.org/x/net/idna"
)

// idnaLookup matches external-dns/internal/idna.Profile (MapForLookup + Transitional + not strict domain).
var idnaLookup = idna.New(
	idna.MapForLookup(),
	idna.Transitional(true),
	idna.StrictDomainName(false),
)

// ZoneIDName maps a stable zone identifier (e.g. NSX policy path) to a DNS zone name (Unicode).
type ZoneIDName map[string]string

// Add inserts zoneID → zoneName, storing zoneName in Unicode form per IDNA when possible (same as upstream).
func (z ZoneIDName) Add(zoneID, zoneName string) {
	u, err := idnaLookup.ToUnicode(zoneName)
	if err != nil {
		z[zoneID] = zoneName
		return
	}
	z[zoneID] = u
}

// FindZone identifies the most suitable DNS zone for a given hostname (longest suffix match).
// It returns the zone ID and zone name that best match the hostname, plus normalizedName: hostname
// with per-label IDNA ToUnicode applied (underscore labels are left unchanged), joined with ".".
//
// This mirrors sigs.k8s.io/external-dns/provider.ZoneIDName.FindZone, extended with normalizedName
// so callers can trim the matched suffix using the same string used for comparisons.
func (z ZoneIDName) FindZone(hostname string) (zoneID, zoneName, normalizedName string) {
	domainLabels := strings.Split(hostname, ".")
	for i, label := range domainLabels {
		if strings.Contains(label, "_") {
			continue
		}
		convertedLabel, err := idnaLookup.ToUnicode(label)
		if err != nil {
			convertedLabel = label
		}
		domainLabels[i] = convertedLabel
	}
	normalizedName = strings.Join(domainLabels, ".")

	for zid, zname := range z {
		if normalizedName == zname || strings.HasSuffix(normalizedName, "."+zname) {
			if zoneName == "" || len(zname) > len(zoneName) {
				zoneID = zid
				zoneName = zname
			}
		}
	}
	return zoneID, zoneName, normalizedName
}
