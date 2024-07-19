package staticroute

import (
	"fmt"
	"net"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/crd.nsx.vmware.com/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func validateStaticRoute(obj *v1alpha1.StaticRoute) error {
	ipDict := make(map[string]bool)
	for index := range obj.Spec.NextHops {
		ip := obj.Spec.NextHops[index].IPAddress
		if _, exist := ipDict[ip]; exist {
			err := fmt.Errorf("duplicate ip address %s", ip)
			log.Error(err, "buildStaticRoute")
			return err
		}
		if value := net.ParseIP(ip); value == nil {
			err := fmt.Errorf("invalid IP address: %s", ip)
			log.Error(err, "buildStaticRoute")
			return err
		}
		ipDict[ip] = true
	}
	return nil
}

func (service *StaticRouteService) buildStaticRoute(obj *v1alpha1.StaticRoute) (*model.StaticRoutes, error) {
	if err := validateStaticRoute(obj); err != nil {
		return nil, err
	}
	sr := &model.StaticRoutes{}
	sr.Network = &obj.Spec.Network
	dis := int64(1)
	for index := range obj.Spec.NextHops {
		nexthop := model.RouterNexthop{AdminDistance: &dis}
		nexthop.IpAddress = &obj.Spec.NextHops[index].IPAddress
		sr.NextHops = append(sr.NextHops, nexthop)
	}
	sr.Id = String(util.GenerateIDByObject(obj))
	sr.DisplayName = String(util.GenerateTruncName(common.MaxNameLength, obj.Name, "", "", "", ""))
	sr.Tags = service.buildBasicTags(obj)
	return sr, nil
}

func (service *StaticRouteService) buildBasicTags(obj *v1alpha1.StaticRoute) []model.Tag {
	return util.BuildBasicTags(service.Service.NSXConfig.Cluster, obj, "")
}
