package vpc

import (
	"net/netip"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	DefaultVPCIPAddressType               = "IPV4"
	DefaultLoadBalancerVPCEndpointEnabled = true
)

// private ip block cidr is not unique, there maybe different ip blocks using same cidr, but for different vpc cr
// using cidr_vpccruid as key so that it could quickly check if ipblocks already created.
func generateIPBlockKey(block model.IpAddressBlock) string {
	cidr := block.Cidr
	nsUID := ""
	for _, tag := range block.Tags {
		if *tag.Scope == common.TagScopeNamespaceUID {
			nsUID = *tag.Tag
		}
	}
	return *cidr + "_" + nsUID
}

func generateIPBlockSearchKey(cidr string, nsUID string) string {
	return cidr + "_" + nsUID
}

func buildPrivateIpBlock(networkInfo *v1alpha1.NetworkInfo, nsObj *v1.Namespace, cidr, ip, project, cluster string) model.IpAddressBlock {
	suffix := networkInfo.GetNamespace() + "-" + ip
	addr, _ := netip.ParseAddr(ip)
	ipType := util.If(addr.Is4(), model.IpAddressBlock_IP_ADDRESS_TYPE_IPV4, model.IpAddressBlock_IP_ADDRESS_TYPE_IPV6).(string)
	blockType := model.IpAddressBlock_VISIBILITY_PRIVATE
	block := model.IpAddressBlock{
		DisplayName:   common.String(util.GenerateDisplayName("", "ipblock", suffix, "", cluster)),
		Id:            common.String(string(nsObj.UID) + "_" + ip),
		Tags:          util.BuildBasicTags(cluster, networkInfo, nsObj.UID), // ipblock and vpc can use the same tags
		Cidr:          &cidr,
		IpAddressType: &ipType,
		Visibility:    &blockType,
	}

	return block
}

func buildNSXVPC(obj *v1alpha1.NetworkInfo, nsObj *v1.Namespace, nc common.VPCNetworkConfigInfo, cluster string, pathMap map[string]string,
	nsxVPC *model.Vpc) (*model.Vpc,
	error) {
	vpc := &model.Vpc{}
	if nsxVPC != nil {
		// for upgrade case, only check public/private ip block size changing
		if !IsVPCChanged(nc, nsxVPC) {
			log.Info("no changes on current NSX VPC, skip updating", "VPC", nsxVPC.Id)
			return nil, nil
		}
		// for updating vpc case, use current vpc id, name
		vpc = nsxVPC
	} else {
		// for creating vpc case, fill in vpc properties based on networkconfig
		vpcName := util.GenerateDisplayName("", "vpc", obj.GetNamespace(), "", cluster)
		vpc.DisplayName = &vpcName
		vpc.Id = common.String(string(nsObj.GetUID()))
		vpc.DefaultGatewayPath = &nc.DefaultGatewayPath
		vpc.IpAddressType = &DefaultVPCIPAddressType

		siteInfos := []model.SiteInfo{
			{
				EdgeClusterPaths: []string{nc.EdgeClusterPath},
			},
		}
		vpc.SiteInfos = siteInfos
		vpc.LoadBalancerVpcEndpoint = &model.LoadBalancerVPCEndpoint{Enabled: &DefaultLoadBalancerVPCEndpointEnabled}
		vpc.Tags = util.BuildBasicTags(cluster, obj, nsObj.UID)
	}

	// update private/public blocks
	vpc.ExternalIpv4Blocks = nc.ExternalIPv4Blocks
	vpc.PrivateIpv4Blocks = util.GetMapValues(pathMap)
	if nc.ShortID != "" {
		vpc.ShortId = &nc.ShortID
	}

	return vpc, nil
}
