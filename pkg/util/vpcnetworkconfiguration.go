/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

// DefaultIPv6SubnetPrefixLength is the default IPv6 prefix length when VPCNetworkConfiguration does not set one.
const DefaultIPv6SubnetPrefixLength = 64

// EffectiveDefaultIPv6PrefixLength returns the IPv6 subnet prefix length from VPCNetworkConfigurationSpec
// for Subnet/SubnetSet defaults. Values outside the valid API range [2,127] are treated as unset and yield /64.
func EffectiveDefaultIPv6PrefixLength(spec v1alpha1.VPCNetworkConfigurationSpec) int {
	pl := spec.DefaultIPv6PrefixLength
	if pl >= 2 && pl <= 127 {
		return pl
	}
	return DefaultIPv6SubnetPrefixLength
}

// SubnetFamilyUsesIPv6 reports whether the Subnet(Set) allocates IPv6 addresses.
func SubnetFamilyUsesIPv6(ipType v1alpha1.IPAddressType) bool {
	return ipType == v1alpha1.IPAddressTypeIPv6 || ipType == v1alpha1.IPAddressTypeIPv4IPv6
}
