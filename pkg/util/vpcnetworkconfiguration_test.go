/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

func TestEffectiveDefaultIPv6PrefixLength(t *testing.T) {
	assert.Equal(t, 64, EffectiveDefaultIPv6PrefixLength(v1alpha1.VPCNetworkConfigurationSpec{}))
	assert.Equal(t, 64, EffectiveDefaultIPv6PrefixLength(v1alpha1.VPCNetworkConfigurationSpec{DefaultIPv6PrefixLength: 0}))
	assert.Equal(t, 64, EffectiveDefaultIPv6PrefixLength(v1alpha1.VPCNetworkConfigurationSpec{DefaultIPv6PrefixLength: 1}))
	assert.Equal(t, 72, EffectiveDefaultIPv6PrefixLength(v1alpha1.VPCNetworkConfigurationSpec{DefaultIPv6PrefixLength: 72}))
	assert.Equal(t, 127, EffectiveDefaultIPv6PrefixLength(v1alpha1.VPCNetworkConfigurationSpec{DefaultIPv6PrefixLength: 127}))
	assert.Equal(t, 64, EffectiveDefaultIPv6PrefixLength(v1alpha1.VPCNetworkConfigurationSpec{DefaultIPv6PrefixLength: 200}))
}

func TestSubnetFamilyUsesIPv6(t *testing.T) {
	assert.False(t, SubnetFamilyUsesIPv6(""))
	assert.False(t, SubnetFamilyUsesIPv6(v1alpha1.IPAddressTypeIPv4))
	assert.True(t, SubnetFamilyUsesIPv6(v1alpha1.IPAddressTypeIPv6))
	assert.True(t, SubnetFamilyUsesIPv6(v1alpha1.IPAddressTypeIPv4IPv6))
}
