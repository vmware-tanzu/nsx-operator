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

func TestNormalizeDNSHostname(t *testing.T) {
	assert.Nil(t, normalizeDNSHostname(nil))
	empty := ""
	assert.Nil(t, normalizeDNSHostname(&empty))
	host := "test.example.com"
	assert.Equal(t, &host, normalizeDNSHostname(&host))
}

func TestCompareResource_DNSHostname(t *testing.T) {
	id := "test-id"
	host1 := "app1.example.com"
	host2 := "app2.example.com"
	empty := ""

	existing := &model.VpcIpAddressAllocation{
		Id:          &id,
		DnsHostname: &host1,
	}

	// 1. Same hostname -> no change
	desiredSame := &model.VpcIpAddressAllocation{
		Id:          &id,
		DnsHostname: &host1,
	}
	assert.False(t, common.CompareResource(IpAddressAllocationToComparable(existing), IpAddressAllocationToComparable(desiredSame)))

	// 2. Updated hostname -> isChanged true
	desiredUpdated := &model.VpcIpAddressAllocation{
		Id:          &id,
		DnsHostname: &host2,
	}
	assert.True(t, common.CompareResource(IpAddressAllocationToComparable(existing), IpAddressAllocationToComparable(desiredUpdated)))

	// 3. Cleared hostname (empty string) -> isChanged true
	desiredCleared := &model.VpcIpAddressAllocation{
		Id:          &id,
		DnsHostname: &empty,
	}
	assert.True(t, common.CompareResource(IpAddressAllocationToComparable(existing), IpAddressAllocationToComparable(desiredCleared)))

	// 4. Cleared hostname (nil) -> isChanged true
	desiredNil := &model.VpcIpAddressAllocation{
		Id:          &id,
		DnsHostname: nil,
	}
	assert.True(t, common.CompareResource(IpAddressAllocationToComparable(existing), IpAddressAllocationToComparable(desiredNil)))

	// 5. Both cleared (one nil, one empty) -> no change (normalized both nil)
	existingNil := &model.VpcIpAddressAllocation{
		Id:          &id,
		DnsHostname: nil,
	}
	assert.False(t, common.CompareResource(IpAddressAllocationToComparable(existingNil), IpAddressAllocationToComparable(desiredCleared)))
}
