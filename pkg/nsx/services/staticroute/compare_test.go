package staticroute

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func TestKey(t *testing.T) {
	id := "test-sr-id"
	sr := &StaticRoute{Id: &id}
	assert.Equal(t, "test-sr-id", sr.Key())

	srNilId := &StaticRoute{Id: nil}
	assert.Equal(t, "", srNilId.Key())

	var srNil *StaticRoute
	assert.Equal(t, "", srNil.Key())
}

func TestValue(t *testing.T) {
	id := "test-sr-id"
	network := "192.168.1.0/24"
	sr := &StaticRoute{
		Id:      &id,
		Network: &network,
		NextHops: []model.RouterNexthop{
			{IpAddress: util.Ptr("192.168.1.2")},
			{IpAddress: util.Ptr("192.168.1.1")},
		},
	}

	dataValue := sr.Value()
	assert.NotNil(t, dataValue)

	// Nil receiver should return nil
	var nilSr *StaticRoute
	assert.Nil(t, nilSr.Value())
}

func TestStaticRouteToComparable(t *testing.T) {
	id := "test-sr-id"
	network := "192.168.1.0/24"
	nsxSR := &model.StaticRoutes{
		Id:      &id,
		Network: &network,
	}

	comparable := StaticRouteToComparable(nsxSR)
	assert.NotNil(t, comparable)
	assert.IsType(t, &StaticRoute{}, comparable)
}

func TestComparableToStaticRoute(t *testing.T) {
	id := "test-sr-id"
	network := "192.168.1.0/24"
	sr := &StaticRoute{
		Id:      &id,
		Network: &network,
	}

	nsxSR := ComparableToStaticRoute(sr)
	assert.NotNil(t, nsxSR)
	assert.IsType(t, &model.StaticRoutes{}, nsxSR)
	assert.Equal(t, id, *nsxSR.Id)
	assert.Equal(t, network, *nsxSR.Network)

	assert.Nil(t, ComparableToStaticRoute(nil))
}

func TestCompareStaticRoute(t *testing.T) {
	service := &StaticRouteService{}

	oldStaticRoute := &model.StaticRoutes{
		Network: util.Ptr("192.168.1.0/24"),
		NextHops: []model.RouterNexthop{
			{IpAddress: util.Ptr("192.168.1.1")},
			{IpAddress: util.Ptr("192.168.1.2")},
		},
	}

	newStaticRouteSame := &model.StaticRoutes{
		Network: util.Ptr("192.168.1.0/24"),
		NextHops: []model.RouterNexthop{
			{IpAddress: util.Ptr("192.168.1.1")},
			{IpAddress: util.Ptr("192.168.1.2")},
		},
	}

	newStaticRouteReorderedHops := &model.StaticRoutes{
		Network: util.Ptr("192.168.1.0/24"),
		NextHops: []model.RouterNexthop{
			{IpAddress: util.Ptr("192.168.1.2")},
			{IpAddress: util.Ptr("192.168.1.1")},
		},
	}

	newStaticRouteDifferent := &model.StaticRoutes{
		Network: util.Ptr("192.168.1.0/24"),
		NextHops: []model.RouterNexthop{
			{IpAddress: util.Ptr("192.168.1.4")},
		},
	}

	newStaticRouteDifferentNetwork := &model.StaticRoutes{
		Network: util.Ptr("192.168.2.0/24"),
		NextHops: []model.RouterNexthop{
			{IpAddress: util.Ptr("192.168.1.1")},
			{IpAddress: util.Ptr("192.168.1.2")},
		},
	}

	oldStaticRouteWithAllocation := &model.StaticRoutes{
		NetworkIpAllocationPath: util.Ptr("/infra/ip-pools/pool1"),
		NextHops: []model.RouterNexthop{
			{IpAddress: util.Ptr("192.168.1.1")},
		},
	}

	newStaticRouteWithAllocationSame := &model.StaticRoutes{
		NetworkIpAllocationPath: util.Ptr("/infra/ip-pools/pool1"),
		NextHops: []model.RouterNexthop{
			{IpAddress: util.Ptr("192.168.1.1")},
		},
	}

	newStaticRouteWithAllocationDifferent := &model.StaticRoutes{
		NetworkIpAllocationPath: util.Ptr("/infra/ip-pools/pool2"),
		NextHops: []model.RouterNexthop{
			{IpAddress: util.Ptr("192.168.1.1")},
		},
	}

	// Same static routes
	assert.True(t, service.compareStaticRoute(oldStaticRoute, newStaticRouteSame))
	// Reordered next hops should still be considered equal
	assert.True(t, service.compareStaticRoute(oldStaticRoute, newStaticRouteReorderedHops))
	// Different next hops
	assert.False(t, service.compareStaticRoute(oldStaticRoute, newStaticRouteDifferent))
	// Different network
	assert.False(t, service.compareStaticRoute(oldStaticRoute, newStaticRouteDifferentNetwork))

	// Test allocation path comparisons
	assert.True(t, service.compareStaticRoute(oldStaticRouteWithAllocation, newStaticRouteWithAllocationSame))
	assert.False(t, service.compareStaticRoute(oldStaticRouteWithAllocation, newStaticRouteWithAllocationDifferent))

	// Test cross-type comparisons (Network vs NetworkIpAllocationPath)
	assert.False(t, service.compareStaticRoute(oldStaticRoute, oldStaticRouteWithAllocation))
	assert.False(t, service.compareStaticRoute(oldStaticRouteWithAllocation, oldStaticRoute))

	// Nil comparisons
	assert.True(t, service.compareStaticRoute(nil, nil))
	assert.False(t, service.compareStaticRoute(oldStaticRoute, nil))
	assert.False(t, service.compareStaticRoute(nil, newStaticRouteSame))
}
