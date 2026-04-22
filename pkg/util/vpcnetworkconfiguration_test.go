/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

func TestIPv4SubnetSizeApplicable(t *testing.T) {
	assert.True(t, IPv4SubnetSizeApplicable(""))                             // unset → IPv4-only
	assert.True(t, IPv4SubnetSizeApplicable(v1alpha1.IPAddressTypeIPv4))     // explicit IPv4
	assert.True(t, IPv4SubnetSizeApplicable(v1alpha1.IPAddressTypeIPv4IPv6)) // dual-stack
	assert.False(t, IPv4SubnetSizeApplicable(v1alpha1.IPAddressTypeIPv6))    // IPv6-only
}

func TestIPv6PrefixLengthApplicable(t *testing.T) {
	assert.False(t, IPv6PrefixLengthApplicable(""))                            // unset → IPv4-only
	assert.False(t, IPv6PrefixLengthApplicable(v1alpha1.IPAddressTypeIPv4))    // IPv4-only
	assert.True(t, IPv6PrefixLengthApplicable(v1alpha1.IPAddressTypeIPv4IPv6)) // dual-stack
	assert.True(t, IPv6PrefixLengthApplicable(v1alpha1.IPAddressTypeIPv6))     // IPv6-only
}

func TestSubnetFamilyUsesIPv6(t *testing.T) {
	assert.False(t, SubnetFamilyUsesIPv6(""))
	assert.False(t, SubnetFamilyUsesIPv6(v1alpha1.IPAddressTypeIPv4))
	assert.True(t, SubnetFamilyUsesIPv6(v1alpha1.IPAddressTypeIPv6))
	assert.True(t, SubnetFamilyUsesIPv6(v1alpha1.IPAddressTypeIPv4IPv6))
}
