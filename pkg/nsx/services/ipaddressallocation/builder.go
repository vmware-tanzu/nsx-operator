package ipaddressallocation

import (
	"fmt"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	Int64  = common.Int64
	String = common.String
)

const (
	IPADDRESSALLOCATIONPREFIX = "ipa"
)

func convertIpAddressBlockVisibility(visibility v1alpha1.IPAddressVisibility) v1alpha1.IPAddressVisibility {
	if visibility == v1alpha1.IPAddressVisibilityPrivateTGW {
		return "PRIVATE_TGW"
	}
	return visibility
}

func (service *IPAddressAllocationService) BuildIPAddressAllocation(IPAddressAllocation *v1alpha1.IPAddressAllocation) (*model.VpcIpAddressAllocation, error) {
	VPCInfo := service.VPCService.ListVPCInfo(IPAddressAllocation.Namespace)
	if len(VPCInfo) == 0 {
		log.Error(nil, "failed to find VPCInfo for IPAddressAllocation CR", "IPAddressAllocation", IPAddressAllocation.Name, "namespace", IPAddressAllocation.Namespace)
		return nil, fmt.Errorf("failed to find VPCInfo for IPAddressAllocation CR %s in namespace %s", IPAddressAllocation.Name, IPAddressAllocation.Namespace)
	}

	ipAddressBlockVisibility := convertIpAddressBlockVisibility(IPAddressAllocation.Spec.IPAddressBlockVisibility)
	ipAddressBlockVisibilityStr := util.ToUpper(string(ipAddressBlockVisibility))
	return &model.VpcIpAddressAllocation{
		Id:                       String(service.buildIPAddressAllocationID(IPAddressAllocation)),
		DisplayName:              String(service.buildIPAddressAllocationName(IPAddressAllocation)),
		Tags:                     service.buildIPAddressAllocationTags(IPAddressAllocation),
		IpAddressBlockVisibility: &ipAddressBlockVisibilityStr,
		AllocationSize:           Int64(int64(IPAddressAllocation.Spec.AllocationSize)),
	}, nil
}

func (service *IPAddressAllocationService) buildIPAddressAllocationID(IPAddressAllocation *v1alpha1.IPAddressAllocation) string {
	return util.GenerateIDByObject(IPAddressAllocation)
}

func (service *IPAddressAllocationService) buildIPAddressAllocationName(IPAddressAllocation *v1alpha1.IPAddressAllocation) string {
	return util.GenerateTruncName(common.MaxNameLength, IPAddressAllocation.ObjectMeta.Name, "", "", "", "")
}

func (service *IPAddressAllocationService) buildIPAddressAllocationTags(IPAddressAllocation *v1alpha1.IPAddressAllocation) []model.Tag {
	return util.BuildBasicTags(service.NSXConfig.Cluster, IPAddressAllocation, "")
}
