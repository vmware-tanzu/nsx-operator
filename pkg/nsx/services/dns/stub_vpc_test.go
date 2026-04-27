/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	pkgmock "github.com/vmware-tanzu/nsx-operator/pkg/mock"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

var _ servicecommon.VPCServiceProvider = (*pkgmock.MockVPCServiceProvider)(nil)

func testVPCNetworkConfiguration() *v1alpha1.VPCNetworkConfiguration {
	return &v1alpha1.VPCNetworkConfiguration{
		Spec: v1alpha1.VPCNetworkConfigurationSpec{
			DNSZones: []string{"/zones/t"},
		},
		Status: v1alpha1.VPCNetworkConfigurationStatus{
			VPCs: []v1alpha1.VPCInfo{{
				VPCPath: "/orgs/org1/projects/proj1/vpcs/vpc1",
			}},
		},
	}
}

// testDNSZoneMapForVPCFixture maps testVPCNetworkConfiguration Spec.DNSZones paths to delegated DNS
// names so ValidateEndpointsByDNSZone does not call getDNSZoneFromNSX (stub returns nil today).
func testDNSZoneMapForVPCFixture() map[string]string {
	return map[string]string{"/zones/t": "example.com"}
}
