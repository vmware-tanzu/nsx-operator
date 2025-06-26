package staticroute

import (
	"fmt"
	"net"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
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

	tags := service.buildBasicTags(obj)
	sr.Tags = tags
	objForIdGeneration := &v1.ObjectMeta{
		Name: obj.GetName(),
		UID:  types.UID(common.GetNamespaceUUID(tags)),
	}
	sr.Id = String(service.buildStaticRouteId(objForIdGeneration))
	sr.DisplayName = String(util.GenerateTruncName(common.MaxNameLength, obj.Name, "", "", "", ""))
	return sr, nil
}

func (service *StaticRouteService) buildStaticRouteId(obj v1.Object) string {
	return common.BuildUniqueIDWithRandomUUID(obj, util.GenerateIDByObject, service.staticRoutesIdExists)
}

func (service *StaticRouteService) buildBasicTags(obj *v1alpha1.StaticRoute) []model.Tag {
	return util.BuildBasicTags(service.Service.NSXConfig.Cluster, obj, service.GetNamespaceUID(obj.ObjectMeta.Namespace))
}

func (service *StaticRouteService) staticRoutesIdExists(id string) bool {
	existingStaticRoutes := service.StaticRouteStore.GetByKey(id)
	return existingStaticRoutes != nil
}
