package staticroute

import (
	"testing"

	"github.com/openlyinc/pointy"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

func TestCompareStaticRoute(t *testing.T) {
	service := &StaticRouteService{}

	oldStaticRoute := &model.StaticRoutes{
		Network: pointy.String("192.168.1.0/24"),
		NextHops: []model.RouterNexthop{
			{IpAddress: pointy.String("192.168.1.1")},
			{IpAddress: pointy.String("192.168.1.2")},
		},
	}

	newStaticRouteSame := &model.StaticRoutes{
		Network: pointy.String("192.168.1.0/24"),
		NextHops: []model.RouterNexthop{
			{IpAddress: pointy.String("192.168.1.1")},
			{IpAddress: pointy.String("192.168.1.2")},
		},
	}

	newStaticRouteDifferent := &model.StaticRoutes{
		Network: pointy.String("192.168.1.0/24"),
		NextHops: []model.RouterNexthop{
			{IpAddress: pointy.String("192.168.1.4")},
		},
	}

	newStaticRouteDifferentNetwork := &model.StaticRoutes{
		Network: pointy.String("192.168.2.0/24"),
		NextHops: []model.RouterNexthop{
			{IpAddress: pointy.String("192.168.1.1")},
			{IpAddress: pointy.String("192.168.1.2")},
		},
	}
	assert.True(t, service.compareStaticRoute(oldStaticRoute, newStaticRouteSame))
	assert.False(t, service.compareStaticRoute(oldStaticRoute, newStaticRouteDifferent))
	assert.False(t, service.compareStaticRoute(oldStaticRoute, newStaticRouteDifferentNetwork))
}
