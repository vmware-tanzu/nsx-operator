package vpc

import (
	"net/netip"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

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
	vpc_uid := ""
	for _, tag := range block.Tags {
		if *tag.Scope == common.TagScopeNetworkInfoCRUID {
			vpc_uid = *tag.Tag
		}
	}
	return *cidr + "_" + vpc_uid
}

func generateIPBlockSearchKey(cidr string, vpcCRUID string) string {
	return cidr + "_" + vpcCRUID
}

func buildPrivateIpBlock(networkInfo *v1alpha1.NetworkInfo, cidr, ip, project, cluster string) model.IpAddressBlock {
	suffix := networkInfo.GetNamespace() + "-" + networkInfo.Name + "-" + ip
	addr, _ := netip.ParseAddr(ip)
	ipType := util.If(addr.Is4(), model.IpAddressBlock_IP_ADDRESS_TYPE_IPV4, model.IpAddressBlock_IP_ADDRESS_TYPE_IPV6).(string)
	blockType := model.IpAddressBlock_VISIBILITY_PRIVATE
	block := model.IpAddressBlock{
		DisplayName:   common.String(util.GenerateDisplayName("ipblock", "", suffix, "", cluster)),
		Id:            common.String(string(networkInfo.UID) + "_" + ip),
		Tags:          util.BuildBasicTags(cluster, networkInfo, ""), // ipblock and vpc can use the same tags
		Cidr:          &cidr,
		IpAddressType: &ipType,
		Visibility:    &blockType,
	}

	return block
}

func buildNSXVPC(obj *v1alpha1.NetworkInfo, nc common.VPCNetworkConfigInfo, cluster string, pathMap map[string]string, nsxVPC *model.Vpc,
	sharedVPCName string) (*model.Vpc,
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
		vpcName := sharedVPCName
		if sharedVPCName == "" {
			vpcName = util.GenerateDisplayName(obj.Name, obj.GetNamespace(), cluster, "", "")
		}
		vpc.DisplayName = &vpcName
		vpc.Id = common.String(string(obj.GetUID()))
		vpc.DefaultGatewayPath = &nc.DefaultGatewayPath
		vpc.IpAddressType = &DefaultVPCIPAddressType

		siteInfos := []model.SiteInfo{
			{
				EdgeClusterPaths: []string{nc.EdgeClusterPath},
			},
		}
		vpc.SiteInfos = siteInfos
		vpc.LoadBalancerVpcEndpoint = &model.LoadBalancerVPCEndpoint{Enabled: &DefaultLoadBalancerVPCEndpointEnabled}
		vpc.Tags = util.BuildBasicTags(cluster, obj, "")
	}

	// update private/public blocks
	vpc.ExternalIpv4Blocks = nc.ExternalIPv4Blocks
	vpc.PrivateIpv4Blocks = util.GetMapValues(pathMap)
	if nc.ShortID != "" {
		vpc.ShortId = &nc.ShortID
	}

	return vpc, nil
}
