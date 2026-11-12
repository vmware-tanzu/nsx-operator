/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

func TestDNSForwarderZonePathsRejected(t *testing.T) {
	for _, zonePath := range []string{
		"/infra/dns-forwarder-zones/z",
		"/orgs/o/projects/p/infra/dns-forwarder-zones/z",
	} {
		t.Run(zonePath, func(t *testing.T) {
			// No NSX client is configured: unsupported paths must fail before any API request.
			svc := &DNSRecordService{DNSZoneMap: newDNSZoneCache()}
			zone, err := svc.getDNSZoneFromNSX(zonePath)
			require.Nil(t, zone)
			require.ErrorContains(t, err, "DNS forwarder zones are not supported")
			require.ErrorContains(t, err, zonePath)

			config := &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{DNSZones: []string{zonePath}}}
			zones, err := svc.SyncDNSZonesByVpcNetworkConfig(config)
			require.Nil(t, zones)
			require.ErrorContains(t, err, "DNS forwarder zones are not supported")
			_, cached := svc.DNSZoneMap.get(zonePath)
			require.False(t, cached)

			// A cache entry left by older code must not bypass path validation.
			svc.DNSZoneMap.set(zonePath, "example.com")
			zones, err = svc.SyncDNSZonesByVpcNetworkConfig(config)
			require.Nil(t, zones)
			require.ErrorContains(t, err, "DNS forwarder zones are not supported")
		})
	}
}
