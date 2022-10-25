package ippool

import (
	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha2"
)

func (service *IPPoolService) GetUnrealizedSubnetNames(obj *v1alpha2.IPPool) []string {
	var unrealizedSubnets []string
	for _, subnetRequest := range obj.Spec.Subnets {
		realized := false
		for _, statusSubnet := range obj.Status.Subnets {
			if statusSubnet.Name == subnetRequest.Name && statusSubnet.CIDR != "" {
				realized = true
				break
			}
		}
		if !realized {
			unrealizedSubnets = append(unrealizedSubnets, subnetRequest.Name)
		}
	}
	return unrealizedSubnets
}

func (service *IPPoolService) FullyRealized(obj *v1alpha2.IPPool) bool {
	fullyRealized := true
	for _, subnetRequest := range obj.Spec.Subnets {
		realized := false
		for _, statusSubnet := range obj.Status.Subnets {
			if statusSubnet.Name == subnetRequest.Name && statusSubnet.CIDR != "" {
				realized = true
				log.V(2).Info("Subnet realized", "name", statusSubnet.Name, "cidr", statusSubnet.CIDR)
				break
			}
		}
		if !realized {
			fullyRealized = false
			break
		}
	}
	return fullyRealized
}

func getCluster(service *IPPoolService) string {
	return service.NSXConfig.Cluster
}
