package ipaddressallocation

import (
	"fmt"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	util2 "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	Int64  = common.Int64
	String = common.String
)

const (
	IPADDRESSALLOCATIONPREFIX = "ipa"
)

func (service *IPAddressAllocationService) BuildIPAddressAllocation(IPAddressAllocation *v1alpha1.IPAddressAllocation) (*model.VpcIpAddressAllocation, error) {
	var IPBlock string
	var IpBlockPathList []string

	VPCInfo := service.VPCService.ListVPCInfo(IPAddressAllocation.Namespace)
	if len(VPCInfo) == 0 {
		log.Error(nil, "failed to find VPCInfo for IPAddressAllocation CR", "IPAddressAllocation", IPAddressAllocation.Name, "namespace", IPAddressAllocation.Namespace)
		return nil, fmt.Errorf("failed to find VPCInfo for IPAddressAllocation CR %s in namespace %s", IPAddressAllocation.Name, IPAddressAllocation.Namespace)
	}

	if IPAddressAllocation.Spec.IPAddressBlockVisibility == v1alpha1.IPAddressVisibilityExternal {
		IpBlockPathList = VPCInfo[0].ExternalIPv4Blocks
	} else if IPAddressAllocation.Spec.IPAddressBlockVisibility == v1alpha1.IPAddressVisibilityPrivate {
		IpBlockPathList = VPCInfo[0].PrivateIpv4Blocks
	}

	for _, ipBlockPath := range IpBlockPathList {
		if util.Contains(service.ExhaustedIPBlock, ipBlockPath) {
			continue
		}
		IPBlock = ipBlockPath
		log.V(2).Info("use ip block path", "ip block path", ipBlockPath)
	}

	if IPBlock == "" {
		log.Error(nil, "all ip blocks are exhausted", "IpBlockPathList", IpBlockPathList)
		return nil, util2.IPBlockAllExhaustedError{Desc: "all ip blocks are exhausted"}
	}

	return &model.VpcIpAddressAllocation{
		Id:             String(service.buildIPAddressAllocationID(IPAddressAllocation)),
		DisplayName:    String(service.buildIPAddressAllocationName(IPAddressAllocation)),
		Tags:           service.buildIPAddressAllocationTags(IPAddressAllocation),
		AllocationSize: Int64(int64(IPAddressAllocation.Spec.AllocationSize)),
		IpBlock:        String(IPBlock),
	}, nil
}

func (service *IPAddressAllocationService) buildIPAddressAllocationID(IPAddressAllocation *v1alpha1.IPAddressAllocation) string {
	return util.GenerateID(string(IPAddressAllocation.UID), IPADDRESSALLOCATIONPREFIX, "", "")
}

func (service *IPAddressAllocationService) buildIPAddressAllocationName(IPAddressAllocation *v1alpha1.IPAddressAllocation) string {
	return util.GenerateDisplayName(IPAddressAllocation.ObjectMeta.Name, IPADDRESSALLOCATIONPREFIX, "", "", service.NSXConfig.Cluster)
}

func (service *IPAddressAllocationService) buildIPAddressAllocationTags(IPAddressAllocation *v1alpha1.IPAddressAllocation) []model.Tag {
	return util.BuildBasicTags(service.NSXConfig.Cluster, IPAddressAllocation, "")
}
