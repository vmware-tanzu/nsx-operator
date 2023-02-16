package vpc

import (
	"github.com/google/uuid"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	DefaultVPCIPAddressType = "IPV4"
	VPCLBEndpointEnabled    = true
)

// private ip block cidr is not unique, there maybe different ip blocks using same cidr, but for different vpc cr
// using cidr_vpccruid as key so that it could quickly check if ipblocks already created.
func generateIPBlockKey(block model.IpAddressBlock) string {
	cidr := block.Cidr
	vpc_uid := ""
	for _, tag := range block.Tags {
		if *tag.Scope == common.TagScopeVPCCRUID {
			vpc_uid = *tag.Tag
		}
	}
	return *cidr + "_" + vpc_uid
}

func generateIPBlockSearchKey(cidr string, vpcCRUID string) string {
	return cidr + "_" + vpcCRUID
}

func TransferIpblockIDstoPaths(ids []string) []string {
	paths := []string{}
	if ids == nil {
		return paths
	}

	for _, id := range ids {
		path := VPCIPBlockPathPrefix + id
		paths = append(paths, path)
	}

	return paths
}

func buildNSXVPC(obj *v1alpha1.VPC, nc VPCNetworkConfigInfo, cluster string, pathMap map[string]string, nsxVPC *model.Vpc) (*model.Vpc, error) {
	vpc := &model.Vpc{}
	if nsxVPC != nil {
		// for upgrade case, only check public/private ip block size changing
		if !IsVPCChanged(nc, nsxVPC) {
			log.Info("no changes on current nsx vpc, skip updating", "VPC", nsxVPC.Id)
			return nil, nil
		}
		// for updating vpc case, use current vpc id, name
		vpc = nsxVPC
	} else {
		// for creating vpc case, fill in vpc properties based on networkconfig
		vpcName := "VPC_" + obj.GetNamespace() + "_" + uuid.NewString()
		vpc.DisplayName = &vpcName
		vpc.Id = (*string)(&obj.UID)
		vpc.DefaultGatewayPath = &nc.DefaultGatewayPath
		vpc.IpAddressType = &DefaultVPCIPAddressType

		siteInfos := []model.SiteInfo{
			{
				EdgeClusterPaths: []string{nc.EdgeClusterPath},
			},
		}
		vpc.SiteInfos = siteInfos
		vpc.LoadBalancerVpcEndpoint = &model.LoadBalancerVPCEndpoint{Enabled: &VPCLBEndpointEnabled}
		vpc.Tags = buildVPCTags(obj, cluster)
	}

	// update private/public blocks
	vpc.ExternalIpv4Blocks = TransferIpblockIDstoPaths(nc.ExternalIPv4Blocks)
	vpc.PrivateIpv4Blocks = util.GetMapValues(pathMap)

	return vpc, nil
}

func buildPrivateIPBlockTags(cluster string, project string, ns string, vpcUid string) []model.Tag {
	tags := []model.Tag{
		{
			Scope: common.String(common.TagScopeCluster),
			Tag:   common.String(cluster),
		},
		{
			Scope: common.String(common.TagScopeNamespace),
			Tag:   common.String(ns),
		},
		{
			Scope: common.String(common.TagScopeVPCCRUID),
			Tag:   common.String(vpcUid),
		},
	}
	return tags
}

func buildVPCTags(obj *v1alpha1.VPC, cluster string) []model.Tag {
	tags := []model.Tag{
		{
			Scope: common.String(common.TagScopeCluster),
			Tag:   common.String(cluster),
		},
		{
			Scope: common.String(common.TagScopeNamespace),
			Tag:   common.String(obj.GetNamespace()),
		},
		{
			Scope: common.String(common.TagScopeVPCCRName),
			Tag:   common.String(obj.GetName()),
		},
		{
			Scope: common.String(common.TagScopeVPCCRUID),
			Tag:   common.String(string(obj.UID)),
		},
	}
	return tags
}
