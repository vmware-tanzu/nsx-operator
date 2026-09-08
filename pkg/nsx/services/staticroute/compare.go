package staticroute

import (
	"sort"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type (
	StaticRoute model.StaticRoutes
)

type Comparable = common.Comparable

func (sr *StaticRoute) Key() string {
	if sr != nil && sr.Id != nil {
		return *sr.Id
	}
	return ""
}

func (sr *StaticRoute) Value() data.DataValue {
	if sr == nil {
		return nil
	}
	var nextHops []model.RouterNexthop
	if len(sr.NextHops) > 0 {
		nextHops = make([]model.RouterNexthop, len(sr.NextHops))
		for i, nh := range sr.NextHops {
			nextHops[i] = model.RouterNexthop{
				IpAddress: nh.IpAddress,
			}
		}
		sort.Slice(nextHops, func(i, j int) bool {
			if nextHops[i].IpAddress == nil {
				return true
			}
			if nextHops[j].IpAddress == nil {
				return false
			}
			return *nextHops[i].IpAddress < *nextHops[j].IpAddress
		})
	}
	s := &StaticRoute{
		Network:                 sr.Network,
		NetworkIpAllocationPath: sr.NetworkIpAllocationPath,
		NextHops:                nextHops,
	}
	dataValue, _ := ComparableToStaticRoute(s).GetDataValue__()
	return dataValue
}

func StaticRouteToComparable(sr *model.StaticRoutes) Comparable {
	return (*StaticRoute)(sr)
}

func ComparableToStaticRoute(sr Comparable) *model.StaticRoutes {
	if sr == nil {
		return nil
	}
	return (*model.StaticRoutes)(sr.(*StaticRoute))
}

func (service *StaticRouteService) compareStaticRoute(oldStaticRoute *model.StaticRoutes, newStaticRoute *model.StaticRoutes) bool {
	if oldStaticRoute == nil && newStaticRoute == nil {
		return true
	}
	if oldStaticRoute == nil || newStaticRoute == nil {
		return false
	}
	return !common.CompareResource(StaticRouteToComparable(oldStaticRoute), StaticRouteToComparable(newStaticRoute))
}
