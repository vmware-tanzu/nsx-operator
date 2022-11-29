package vpc

import (
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

var (
	vpcName1          = "ns1-vpc-1"
	vpcName2          = "ns1-vpc-2"
	vpcID1            = "ns-vpc-uid-1"
	vpcID2            = "ns-vpc-uid-2"
	IPv4Type          = "IPv4"
	cluster           = "k8scl-one"
	tagValueNS        = "ns1"
	tagScopeVPCCRName = common.TagScopeVPCCRName
	tagScopeVPCCRUID  = common.TagScopeVPCCRUID
	tagValueVPCCRName = "vpcA"
	tagValueVPCCRUID  = "uidA"
	tagScopeCluster   = common.TagScopeCluster
	tagScopeNamespace = common.TagScopeNamespace

	basicTags = []model.Tag{
		{
			Scope: &tagScopeCluster,
			Tag:   &cluster,
		},
		{
			Scope: &tagScopeNamespace,
			Tag:   &tagValueNS,
		},
		{
			Scope: &tagScopeVPCCRName,
			Tag:   &tagValueVPCCRName,
		},
		{
			Scope: &tagScopeVPCCRUID,
			Tag:   &tagValueVPCCRUID,
		},
	}
)
