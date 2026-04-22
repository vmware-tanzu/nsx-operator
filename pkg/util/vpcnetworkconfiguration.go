/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

// IPv4SubnetSizeApplicable reports whether IPv4SubnetSize should be set for the
// given ip_family. It is applicable for IPv4, dual-stack, or unset ip_family.
func IPv4SubnetSizeApplicable(ipFamily v1alpha1.IPAddressType) bool {
	return ipFamily != v1alpha1.IPAddressTypeIPv6
}

// IPv6PrefixLengthApplicable reports whether IPv6PrefixLength should be set for
// the given ip_family. It is applicable only for dual-stack or IPv6-only clusters.
func IPv6PrefixLengthApplicable(ipFamily v1alpha1.IPAddressType) bool {
	return ipFamily == v1alpha1.IPAddressTypeIPv4IPv6 || ipFamily == v1alpha1.IPAddressTypeIPv6
}

// SubnetFamilyUsesIPv6 reports whether the Subnet(Set) allocates IPv6 addresses.
func SubnetFamilyUsesIPv6(ipType v1alpha1.IPAddressType) bool {
	return ipType == v1alpha1.IPAddressTypeIPv6 || ipType == v1alpha1.IPAddressTypeIPv4IPv6
}

// SubnetFamilyUsesIPv4 reports whether the Subnet(Set) allocates IPv4 addresses.
func SubnetFamilyUsesIPv4(ipType v1alpha1.IPAddressType) bool {
	return ipType == v1alpha1.IPAddressTypeIPv4 || ipType == v1alpha1.IPAddressTypeIPv4IPv6
}
