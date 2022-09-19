package staticroute

import (
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/util/sets"
)

// assume that staticroute doesn't have the same ipaddress, return true if equal
func (service *StaticRouteService) compareStaticRoute(oldStaticRoute *model.StaticRoutes, newStaticRoute *model.StaticRoutes) bool {
	if *oldStaticRoute.Network != *newStaticRoute.Network {
		return false
	}
	oldNextHops := oldStaticRoute.NextHops
	newNextHops := newStaticRoute.NextHops
	if len(oldNextHops) != len(newNextHops) {
		return false
	}
	oldHopsSet := sets.NewString()
	for _, addr := range oldNextHops {
		oldHopsSet.Insert(*addr.IpAddress)
	}
	for _, addr := range newNextHops {
		if !oldHopsSet.Has(*addr.IpAddress) {
			return false
		}
	}
	return true
}
