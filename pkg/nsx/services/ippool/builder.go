package ippool

import (
	"fmt"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/crd.nsx.vmware.com/v1alpha2"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	Int64  = common.Int64
	String = common.String
)

const (
	IPPOOLPREFIX       = "ipc"
	IPPOOLSUBNETPREFIX = "ibs"
)

func (service *IPPoolService) BuildIPPool(IPPool *v1alpha2.IPPool) (*model.IpAddressPool, []*model.IpAddressPoolBlockSubnet) {
	return &model.IpAddressPool{
		Id:          String(service.buildIPPoolID(IPPool)),
		DisplayName: String(service.buildIPPoolName(IPPool)),
		Tags:        service.buildIPPoolTags(IPPool),
	}, service.buildIPSubnets(IPPool)
}

func (service *IPPoolService) buildIPPoolID(IPPool *v1alpha2.IPPool) string {
	return util.GenerateID(string(IPPool.UID), IPPOOLPREFIX, "", "")
}

func (service *IPPoolService) buildIPPoolName(IPPool *v1alpha2.IPPool) string {
	return util.GenerateDisplayName(IPPool.ObjectMeta.Name, IPPOOLPREFIX, "", "", getCluster(service))
}

func (service *IPPoolService) buildIPPoolTags(IPPool *v1alpha2.IPPool) []model.Tag {
	basicTags := util.BuildBasicTags(getCluster(service), IPPool, "")
	tags := util.AppendTags(basicTags, []model.Tag{
		{Scope: String(common.TagScopeIPPoolCRType), Tag: String(IPPool.Spec.Type)}},
	)
	return tags
}

func (service *IPPoolService) buildIPSubnets(IPPool *v1alpha2.IPPool) []*model.IpAddressPoolBlockSubnet {
	var IPSubnets []*model.IpAddressPoolBlockSubnet
	for _, subnetRequest := range IPPool.Spec.Subnets {
		IPSubnet := service.buildIPSubnet(IPPool, subnetRequest)
		if IPSubnet != nil {
			IPSubnets = append(IPSubnets, IPSubnet)
		}
	}
	return IPSubnets
}

func (service *IPPoolService) buildIPSubnetID(IPPool *v1alpha2.IPPool, subnetRequest *v1alpha2.SubnetRequest) string {
	return util.GenerateID(string(IPPool.UID), IPPOOLSUBNETPREFIX, subnetRequest.Name, "")
}

func (service *IPPoolService) buildIPSubnetName(IPPool *v1alpha2.IPPool, subnetRequest *v1alpha2.SubnetRequest) string {
	return util.GenerateDisplayName(IPPool.ObjectMeta.Name, IPPOOLSUBNETPREFIX, subnetRequest.Name, "", getCluster(service))
}

func (service *IPPoolService) buildIPSubnetTags(IPPool *v1alpha2.IPPool, subnetRequest *v1alpha2.SubnetRequest) []model.Tag {
	basicTags := util.BuildBasicTags(getCluster(service), IPPool, "")
	tags := util.AppendTags(basicTags, []model.Tag{
		{Scope: String(common.TagScopeIPSubnetName), Tag: String(subnetRequest.Name)}},
	)
	return tags
}

func (service *IPPoolService) buildIPSubnetIntentPath(IPPool *v1alpha2.IPPool, subnetRequest *v1alpha2.SubnetRequest) string {
	if IPPool.Spec.Type == common.IPPoolTypePrivate {
		VPCInfo := service.VPCService.ListVPCInfo(IPPool.Namespace)
		if len(VPCInfo) == 0 {
			return ""
		}
		return strings.Join([]string{fmt.Sprintf("/orgs/%s/projects/%s/infra/ip-pools", VPCInfo[0].OrgID, VPCInfo[0].ProjectID),
			service.buildIPPoolID(IPPool),
			"ip-subnets", service.buildIPSubnetID(IPPool, subnetRequest)}, "/")
	} else {
		return strings.Join([]string{"/infra/ip-pools", service.buildIPPoolID(IPPool),
			"ip-subnets", service.buildIPSubnetID(IPPool, subnetRequest)}, "/")
	}
}

func (service *IPPoolService) buildIPSubnet(IPPool *v1alpha2.IPPool, subnetRequest v1alpha2.SubnetRequest) *model.IpAddressPoolBlockSubnet {
	IpBlockPath := String("")
	VPCInfo := service.VPCService.ListVPCInfo(IPPool.Namespace)
	if len(VPCInfo) == 0 {
		log.Error(nil, "failed to find VPCInfo for IPPool CR", "IPPool", IPPool.Name, "namespace", IPPool.Namespace)
		return nil
	}
	var IpBlockPathList []string
	if IPPool.Spec.Type == common.IPPoolTypePrivate {
		IpBlockPathList = VPCInfo[0].PrivateIpv4Blocks
	}
	for _, ipBlockPath := range IpBlockPathList {
		if util.Contains(service.ExhaustedIPBlock, ipBlockPath) {
			continue
		}
		IpBlockPath = String(ipBlockPath)
		log.V(2).Info("use ip block path", "ip block path", ipBlockPath)
	}

	return &model.IpAddressPoolBlockSubnet{
		Id:          String(service.buildIPSubnetID(IPPool, &subnetRequest)),
		DisplayName: String(service.buildIPSubnetName(IPPool, &subnetRequest)),
		Tags:        service.buildIPSubnetTags(IPPool, &subnetRequest),
		Size:        Int64(util.CalculateSubnetSize(subnetRequest.PrefixLength)),
		IpBlockPath: IpBlockPath,
	}
}
