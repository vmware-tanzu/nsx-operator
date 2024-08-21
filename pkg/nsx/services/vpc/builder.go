package vpc

import (
	"net/netip"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
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
	resName := networkInfo.GetNamespace() + "-" + ip
	addr, _ := netip.ParseAddr(ip)
	ipType := util.If(addr.Is4(), model.IpAddressBlock_IP_ADDRESS_TYPE_IPV4, model.IpAddressBlock_IP_ADDRESS_TYPE_IPV6).(string)
	blockType := model.IpAddressBlock_VISIBILITY_PRIVATE
	block := model.IpAddressBlock{
		DisplayName:   common.String(util.GenerateTruncName(common.MaxNameLength, resName, "", "", "", cluster)),
		Id:            common.String(util.GenerateIDByObjectWithSuffix(nsObj, ip)),
		Tags:          util.BuildBasicTags(cluster, networkInfo, nsObj.UID), // ipblock and vpc can use the same tags
		Cidr:          &cidr,
		IpAddressType: &ipType,
		Visibility:    &blockType,
	}

	return block
}

func buildNSXVPC(obj *v1alpha1.NetworkInfo, nsObj *v1.Namespace, nc common.VPCNetworkConfigInfo, cluster string, pathMap map[string]string,
	nsxVPC *model.Vpc, useAVILB bool) (*model.Vpc,
	error) {
	vpc := &model.Vpc{}
	if nsxVPC != nil {
		// for upgrade case, only check public/private ip block size changing
		if !IsVPCChanged(nc, nsxVPC) {
			log.Info("no changes on current NSX VPC, skip updating", "VPC", nsxVPC.Id)
			return nil, nil
		}
		// for updating vpc case, use current vpc id, name
		*vpc = *nsxVPC
	} else {
		// for creating vpc case, fill in vpc properties based on networkconfig
		vpcName := util.GenerateIDByObjectByLimit(obj, common.MaxNameLength)
		vpc.DisplayName = &vpcName
		vpc.Id = common.String(util.GenerateIDByObject(obj))
		vpc.IpAddressType = &DefaultVPCIPAddressType

		if useAVILB {
			loadBalancerVPCEndpointEnabled := true
			vpc.LoadBalancerVpcEndpoint = &model.LoadBalancerVPCEndpoint{Enabled: &loadBalancerVPCEndpointEnabled}
		}
		vpc.Tags = util.BuildBasicTags(cluster, obj, nsObj.UID)
		vpc.Tags = append(vpc.Tags, model.Tag{
			Scope: common.String(common.TagScopeVPCManagedBy), Tag: common.String(common.AutoCreatedVPCTagValue)})
	}

	if nc.VPCConnectivityProfile != "" {
		vpc.VpcConnectivityProfile = &nc.VPCConnectivityProfile
	}

	// TODO: add PrivateIps and remove PrivateIpv4Blocks once the NSX VPC API support private_ips field.
	// vpc.PrivateIps = nc.PrivateIPs
	vpc.PrivateIpv4Blocks = util.GetMapValues(pathMap)
	if nc.ShortID != "" {
		vpc.ShortId = &nc.ShortID
	}

	return vpc, nil
}

func buildNSXLBS(obj *v1alpha1.NetworkInfo, nsObj *v1.Namespace, cluster, lbsSize, vpcPath string, relaxScaleValidation *bool) (*model.LBService, error) {
	lbs := &model.LBService{}
	lbsName := util.GenerateIDByObjectByLimit(obj, common.MaxNameLength)
	// Use VPC id for auto-created LBS id
	lbs.Id = common.String(util.GenerateIDByObject(obj))
	lbs.DisplayName = &lbsName
	lbs.Tags = util.BuildBasicTags(cluster, obj, nsObj.GetUID())
	// "created_for" is required by NCP, and "lb_t1_link_ip" is not needed for VPC
	lbs.Tags = append(lbs.Tags, model.Tag{
		Scope: common.String(common.TagScopeCreatedFor),
		Tag:   common.String(common.TagValueSLB),
	})
	lbs.Size = &lbsSize
	lbs.ConnectivityPath = &vpcPath
	lbs.RelaxScaleValidation = relaxScaleValidation
	return lbs, nil
}
