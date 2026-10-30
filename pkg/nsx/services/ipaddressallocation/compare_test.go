package ipaddressallocation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestKey(t *testing.T) {
	id := "test-id"
	iap := &IpAddressAllocation{Id: &id}
	assert.Equal(t, "test-id", iap.Key())

	iapNil := &IpAddressAllocation{Id: nil}
	assert.Panics(t, func() { iapNil.Key() })
}

func TestValue(t *testing.T) {
	id := "test-id"
	displayName := "test-display-name"
	tags := []model.Tag{{Scope: String("scope"), Tag: String("tag")}}
	iap := &IpAddressAllocation{Id: &id, DisplayName: &displayName, Tags: tags}

	dataValue := iap.Value()
	expectedDataValue, _ := ComparableToIpAddressAllocation(iap).GetDataValue__()

	assert.Equal(t, expectedDataValue, dataValue)
}

func TestCompareIPAddressAllocationUsedFor(t *testing.T) {
	lbFrontend := []string{model.VpcIpAddressAllocation_USED_FOR_FRONTEND}
	tests := []struct {
		name     string
		existing []string
		expected []string
		changed  bool
	}{
		{name: "ordinary allocation unchanged"},
		{name: "LB allocation unchanged", existing: lbFrontend, expected: lbFrontend},
		{name: "LB intent missing on NSX", expected: lbFrontend, changed: true},
		{name: "LB intent absent from desired allocation", existing: lbFrontend, changed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			existing := &model.VpcIpAddressAllocation{Id: String("allocation"), UsedFor: tt.existing}
			expected := &model.VpcIpAddressAllocation{Id: String("allocation"), UsedFor: tt.expected}
			changed := common.CompareResource(IpAddressAllocationToComparable(existing), IpAddressAllocationToComparable(expected))
			assert.Equal(t, tt.changed, changed)
		})
	}
}

func TestIpAddressAllocationToComparable(t *testing.T) {
	id := "test-id"
	displayName := "test-display-name"
	tags := []model.Tag{{Scope: String("scope"), Tag: String("tag")}}
	vpcIap := &model.VpcIpAddressAllocation{Id: &id, DisplayName: &displayName, Tags: tags}

	comparable := IpAddressAllocationToComparable(vpcIap)
	assert.IsType(t, &IpAddressAllocation{}, comparable)
}

func TestComparableToIpAddressAllocation(t *testing.T) {
	id := "test-id"
	displayName := "test-display-name"
	tags := []model.Tag{{Scope: String("scope"), Tag: String("tag")}}
	iap := &IpAddressAllocation{Id: &id, DisplayName: &displayName, Tags: tags}

	vpcIap := ComparableToIpAddressAllocation(iap)
	assert.IsType(t, &model.VpcIpAddressAllocation{}, vpcIap)
	assert.Equal(t, id, *vpcIap.Id)
	assert.Equal(t, displayName, *vpcIap.DisplayName)
	assert.Equal(t, tags, vpcIap.Tags)
}
