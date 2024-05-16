package ipaddressallocation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
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
