package vpc

import (
	"fmt"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	DefaultVPCIPAddressType = "IPV4"
	defaultLBSName          = "default"
)

func generateLBSKey(lbs model.LBService) (string, error) {
	if lbs.ConnectivityPath == nil || *lbs.ConnectivityPath == "" {
		return "", fmt.Errorf("ConnectivityPath is nil or empty")
	}
	pathParts := strings.Split(*lbs.ConnectivityPath, "/")
	vpcID := pathParts[len(pathParts)-1]
	if vpcID == "" {
		return "", fmt.Errorf("invalid VPC ID extracted from ConnectivityPath")
	}
	if lbs.Id == nil || *lbs.Id == "" {
		return "", fmt.Errorf("the LBS ID is nil or empty")
	}
	return combineVPCIDAndLBSID(vpcID, *lbs.Id), nil
}

func generateVirtualServerKey(vs model.LBVirtualServer) (string, error) {
	if vs.Path == nil || *vs.Path == "" {
		return "", fmt.Errorf("LBVirtualServer path is nil or empty")
	}
	return *vs.Path, nil
}

func generatePoolKey(pool model.LBPool) (string, error) {
	if pool.Path == nil || *pool.Path == "" {
		return "", fmt.Errorf("LBPool path is nil or empty")
	}
	return *pool.Path, nil
}

func combineVPCIDAndLBSID(vpcID, lbsID string) string {
	return fmt.Sprintf("%s_%s", vpcID, lbsID)
}

func (s *VPCService) buildNSXVPC(obj *v1alpha1.NetworkInfo, nsObj *v1.Namespace, nc v1alpha1.VPCNetworkConfiguration, cluster string,
	nsxVPC *model.Vpc, useAVILB bool, lbProviderChanged bool, serviceClusterReady bool) (*model.Vpc, error) {
	vpc := &model.Vpc{}
	// enable LB Endpoint for either AVI LB or day2 DTGW
	enableLBEndpoint := useAVILB || serviceClusterReady
	if nsxVPC != nil {
		oldEnableLBEndpoint := (nsxVPC.LoadBalancerVpcEndpoint != nil) && (nsxVPC.LoadBalancerVpcEndpoint.Enabled != nil) && *nsxVPC.LoadBalancerVpcEndpoint.Enabled
		// for upgrade case, check changes for public/private ip block size, LBProvider and if lb endpoint enable
		if !IsVPCChanged(nc, nsxVPC) && !lbProviderChanged && (enableLBEndpoint == oldEnableLBEndpoint) {
			log.Info("no changes on current NSX VPC, skip updating", "VPC", nsxVPC.Id)
			return nil, nil
		}
		*vpc = *nsxVPC
		// for updating vpc case, use current vpc id, name
		if enableLBEndpoint {
			loadBalancerVPCEndpointEnabled := true
			vpc.LoadBalancerVpcEndpoint = &model.LoadBalancerVPCEndpoint{Enabled: &loadBalancerVPCEndpointEnabled}
		}
	} else {
		// for creating vpc case, fill in vpc properties based on networkconfig
		vpc.DisplayName = common.String(s.buildVpcName(obj))
		vpc.Id = common.String(common.BuildUniqueIDWithRandomUUID(obj, util.GenerateIDByObject, s.nsxVpcIdExists))
		vpc.IpAddressType = &DefaultVPCIPAddressType

		if enableLBEndpoint {
			loadBalancerVPCEndpointEnabled := true
			vpc.LoadBalancerVpcEndpoint = &model.LoadBalancerVPCEndpoint{Enabled: &loadBalancerVPCEndpointEnabled}
		}
		vpc.Tags = util.BuildBasicTags(cluster, obj, nsObj.UID)
		vpc.Tags = append(vpc.Tags, model.Tag{
			Scope: common.String(common.TagScopeManagedBy), Tag: common.String(common.AutoCreatedTagValue)})
	}

	vpc.PrivateIps = nc.Spec.PrivateIPs
	return vpc, nil
}

func (s *VPCService) buildVpcName(obj metav1.Object) string {
	return common.BuildUniqueIDWithSuffix(obj, "", common.MaxSubnetNameLength, util.GenerateIDByObject, s.nsxVpcNameExists)
}

func (s *VPCService) nsxVpcIdExists(vpcId string) bool {
	vpc := s.VpcStore.GetByKey(vpcId)
	return vpc != nil
}

func (s *VPCService) nsxVpcNameExists(vpcName string) bool {
	vpcs := s.VpcStore.GetByIndex(nsxVpcNameIndexKey, vpcName)
	return len(vpcs) > 0
}

func buildNSXLBS(obj *v1alpha1.NetworkInfo, nsObj *v1.Namespace, cluster, lbsSize, vpcPath string, relaxScaleValidation *bool) (*model.LBService, error) {
	lbs := &model.LBService{}
	lbsName := defaultLBSName
	lbs.Id = common.String(defaultLBSName)
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

func buildVpcAttachment(obj *v1alpha1.NetworkInfo, nsObj *v1.Namespace, cluster string, vpcconnectiveprofile string, restoreMode bool) (*model.VpcAttachment, error) {
	attachment := &model.VpcAttachment{}
	attachment.VpcConnectivityProfile = &vpcconnectiveprofile
	attachment.DisplayName = common.String(common.DefaultVpcAttachmentId)
	attachment.Id = common.String(common.DefaultVpcAttachmentId)
	attachment.Tags = util.BuildBasicTags(cluster, obj, nsObj.GetUID())
	if restoreMode && len(obj.VPCs) > 0 && len(obj.VPCs[0].DefaultSNATIP) > 0 {
		log.V(1).Info("Restoring DefaultSNATIP", "DefaultSNATIP", obj.VPCs[0].DefaultSNATIP, "NetworkInfo", obj.Name)
		attachment.PreferredDefaultSnatIp = &obj.VPCs[0].DefaultSNATIP
	}
	return attachment, nil
}
