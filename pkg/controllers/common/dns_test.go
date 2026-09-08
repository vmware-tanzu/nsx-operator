/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

func TestIPAddressAllocationDNSRecordCondition(t *testing.T) {
	ipa := &v1alpha1.IPAddressAllocation{}

	// Initially removing returns false because condition does not exist
	assert.False(t, RemoveIPAddressAllocationDNSRecordCondition(ipa))

	// Set condition to True
	updated := SetIPAddressAllocationDNSRecordCondition(ipa, v1.ConditionTrue, nsxutil.ConditionReasonDNSSuccess, "Created")
	assert.True(t, updated)
	assert.Len(t, ipa.Status.Conditions, 1)
	assert.Equal(t, v1alpha1.ConditionType(nsxutil.ConditionTypeDNSRecordReady), ipa.Status.Conditions[0].Type)
	assert.Equal(t, v1.ConditionTrue, ipa.Status.Conditions[0].Status)

	// Setting identical condition returns false
	updated = SetIPAddressAllocationDNSRecordCondition(ipa, v1.ConditionTrue, nsxutil.ConditionReasonDNSSuccess, "Created")
	assert.False(t, updated)

	// Remove condition returns true
	removed := RemoveIPAddressAllocationDNSRecordCondition(ipa)
	assert.True(t, removed)
	assert.Empty(t, ipa.Status.Conditions)

	// Removing again returns false
	assert.False(t, RemoveIPAddressAllocationDNSRecordCondition(ipa))
}

func TestSubnetPortDNSRecordCondition(t *testing.T) {
	sp := &v1alpha1.SubnetPort{}

	// Initially removing returns false
	assert.False(t, RemoveSubnetPortDNSRecordCondition(sp))

	// Set condition to True
	updated := SetSubnetPortDNSRecordCondition(sp, v1.ConditionTrue, nsxutil.ConditionReasonDNSSuccess, "Created")
	assert.True(t, updated)
	assert.Len(t, sp.Status.Conditions, 1)
	assert.Equal(t, v1alpha1.ConditionType(nsxutil.ConditionTypeDNSRecordReady), sp.Status.Conditions[0].Type)

	// Remove condition
	removed := RemoveSubnetPortDNSRecordCondition(sp)
	assert.True(t, removed)
	assert.Empty(t, sp.Status.Conditions)
}
