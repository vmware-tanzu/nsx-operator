package ipaddressallocation

import (
	"fmt"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	controllerscommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
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
	if visibility == "" {
		return v1alpha1.IPAddressVisibilityPrivate
	}
	if visibility == v1alpha1.IPAddressVisibilityPrivateTGW {
		return "PRIVATE_TGW"
	}
	return visibility
}

func ipAddressTypeToNSX(ipAddressType v1alpha1.IPAllocationAddressType) string {
	switch ipAddressType {
	case v1alpha1.IPAllocationIPAddressTypeIPv6:
		return model.VpcIpAddressAllocation_IP_ADDRESS_TYPE_IPV6
	case v1alpha1.IPAllocationIPAddressTypeIPv4:
		fallthrough
	default:
		return model.VpcIpAddressAllocation_IP_ADDRESS_TYPE_IPV4
	}
}

func (service *IPAddressAllocationService) isLBAllocation(o *v1alpha1.IPAddressAllocation) bool {
	if o == nil {
		return false
	}
	return o.Spec.UsedFor == v1alpha1.UsedForLBFrontend
}

func (service *IPAddressAllocationService) getVPCInfo(ns string, isLB bool) ([]common.VPCResourceInfo, error) {
	if isLB {
		nc, err := service.VPCService.GetVPCNetworkConfigByNamespace(ns)
		if err != nil {
			log.Error(err, "Failed to Get NetworkConfig by Namespace", "Namespace", ns)
			return nil, err
		}
		if nc == nil || nc.Spec.LoadBalancerVPC == "" {
			errNotFound := fmt.Errorf("LoadBalancerVPC is not configured on VPCNetworkConfiguration for namespace %s while usedFor is %s", ns, v1alpha1.UsedForLBFrontend)
			log.Error(errNotFound, "Failed to get LoadBalancerVPC for LB IPAddressAllocation", "Namespace", ns)
			return nil, errNotFound
		}
		vpcResourceInfo, err := common.ParseVPCResourcePath(nc.Spec.LoadBalancerVPC)
		if err != nil {
			log.Error(err, "Failed to parse LoadBalancerVPC from VPC path", "VPCPath", nc.Spec.LoadBalancerVPC)
			return nil, err
		}
		return []common.VPCResourceInfo{vpcResourceInfo}, nil
	}
	VPCInfo := service.VPCService.ListVPCInfo(ns)
	return VPCInfo, nil
}

func (service *IPAddressAllocationService) getVPCInfoForCR(o *v1alpha1.IPAddressAllocation) ([]common.VPCResourceInfo, error) {
	return service.getVPCInfo(o.Namespace, service.isLBAllocation(o))
}

func (service *IPAddressAllocationService) buildIPBlockPath(rawBlock string, visibility v1alpha1.IPAddressVisibility, vpcInfo []common.VPCResourceInfo) (*string, error) {
	rawBlock = strings.TrimSpace(rawBlock)
	if rawBlock == "" {
		return nil, nil
	}

	// 1. Direct path starting with /
	if strings.HasPrefix(rawBlock, "/") {
		if strings.HasPrefix(rawBlock, "/infra/ip-blocks/") || (strings.HasPrefix(rawBlock, "/orgs/") && strings.Contains(rawBlock, "/infra/ip-blocks/")) {
			return String(rawBlock), nil
		}
		log.Error(nil, "Invalid IPBlock format, full path must be /infra/ip-blocks/<id> or /orgs/<org>/projects/<proj>/infra/ip-blocks/<id>", "IPBlock", rawBlock)
		return nil, fmt.Errorf("invalid IPBlock format %s, full path must be /infra/ip-blocks/<id> or /orgs/<org>/projects/<proj>/infra/ip-blocks/<id>", rawBlock)
	}

	// 2. Contains colon ':'
	if strings.Contains(rawBlock, ":") {
		if strings.HasPrefix(rawBlock, ":") {
			ipBlockID := strings.TrimPrefix(rawBlock, ":")
			if ipBlockID == "" {
				log.Error(nil, "Invalid IPBlock format, IP block ID cannot be empty", "IPBlock", rawBlock)
				return nil, fmt.Errorf("invalid IPBlock format %s, IP block ID cannot be empty", rawBlock)
			}
			return String(fmt.Sprintf("/infra/ip-blocks/%s", ipBlockID)), nil
		}
		// Project format: <project ID>:<ipBlockID>
		parts := strings.SplitN(rawBlock, ":", 2)
		projectID := strings.TrimSpace(parts[0])
		ipBlockID := strings.TrimSpace(parts[1])
		if projectID == "" || ipBlockID == "" {
			log.Error(nil, "Invalid IPBlock format, expected '<project ID>:<ipBlockID>'", "IPBlock", rawBlock)
			return nil, fmt.Errorf("invalid IPBlock format %s, expected '<project ID>:<ipBlockID>'", rawBlock)
		}
		orgID := "default"
		if len(vpcInfo) > 0 && vpcInfo[0].OrgID != "" {
			orgID = vpcInfo[0].OrgID
		}
		return String(fmt.Sprintf("/orgs/%s/projects/%s/infra/ip-blocks/%s", orgID, projectID, ipBlockID)), nil
	}

	// 3. No colon and no slash:
	// If visibility is External or block name starts with "ipblock-", it's an infra IPBlock (e.g. legacy VIP ranges under loadBalancerVPC).
	if visibility == v1alpha1.IPAddressVisibilityExternal || strings.HasPrefix(rawBlock, "ipblock-") || len(vpcInfo) == 0 || vpcInfo[0].ProjectID == "" {
		return String(fmt.Sprintf("/infra/ip-blocks/%s", rawBlock)), nil
	}

	// Otherwise, it is a project-scoped IPBlock under the current VPC's project.
	return String(fmt.Sprintf("/orgs/%s/projects/%s/infra/ip-blocks/%s", vpcInfo[0].OrgID, vpcInfo[0].ProjectID, rawBlock)), nil
}

func (service *IPAddressAllocationService) BuildIPAddressAllocation(obj metav1.Object, subnetPortCR *v1alpha1.SubnetPort, restoreMode bool) (*model.VpcIpAddressAllocation, []common.VPCResourceInfo, error) {
	ipAddressBlockVisibility := v1alpha1.IPAddressVisibilityPrivate
	var allocationIps *string
	var allocationSize *int64
	var ipAddressType string
	var ipv6AllocationPrefixLength *int64
	var ipBlock *string
	var vpcInfo []common.VPCResourceInfo

	switch o := obj.(type) {
	case *v1alpha1.IPAddressAllocation:
		ipAddressType = ipAddressTypeToNSX(o.Spec.IPAddressType)
		var err error
		vpcInfo, err = service.getVPCInfoForCR(o)
		if err != nil {
			return nil, nil, err
		}
		if len(vpcInfo) == 0 {
			log.Error(nil, "Failed to find VPCInfo for IPAddressAllocation CR", "IPAddressAllocation", o.Name, "Namespace", o.Namespace)
			return nil, nil, fmt.Errorf("failed to find VPCInfo for IPAddressAllocation CR %s in Namespace %s", o.Name, o.Namespace)
		}
		ipAddressBlockVisibility = convertIpAddressBlockVisibility(o.Spec.IPAddressBlockVisibility)
		if len(o.Spec.AllocationIPs) > 0 {
			allocationIps = String(o.Spec.AllocationIPs)
		} else if restoreMode && len(o.Status.AllocationIPs) > 0 {
			allocationIps = String(o.Status.AllocationIPs)
		} else {
			// Field AllocationIPs and AllocationSize/Ipv6AllocationPrefixLength cannot be provided together for VPC IP allocation.
			if ipAddressType == model.VpcIpAddressAllocation_IP_ADDRESS_TYPE_IPV6 {
				prefixLen := o.Spec.IPv6AllocationPrefixLength
				if prefixLen == 0 {
					prefixLen = 64
				}
				ipv6AllocationPrefixLength = Int64(int64(prefixLen))
			} else {
				allocationSize = Int64(int64(o.Spec.AllocationSize))
			}
		}
		var errBlock error
		ipBlock, errBlock = service.buildIPBlockPath(o.Spec.IPBlock, ipAddressBlockVisibility, vpcInfo)
		if errBlock != nil {
			return nil, nil, errBlock
		}
	case *v1alpha1.AddressBinding:
		if !restoreMode || subnetPortCR == nil || o.Spec.IPAddressAllocationName != "" {
			return nil, nil, nil
		}
		ipAddressBlockVisibility = v1alpha1.IPAddressVisibilityExternal
		allocationIps = &o.Status.IPAddress
		ipAddressType = controllerscommon.ConvertCRIPAddressTypeToNSX(v1alpha1.IPAddressTypeIPv4)
		if util.IsIPv6(o.Status.IPAddress) {
			ipAddressType = controllerscommon.ConvertCRIPAddressTypeToNSX(v1alpha1.IPAddressTypeIPv6)
		}
	}
	tags := service.buildIPAddressAllocationTags(obj)
	if o, ok := obj.(*v1alpha1.IPAddressAllocation); ok {
		if service.isLBAllocation(o) {
			tags = append(tags, model.Tag{
				Scope: String(common.TagScopeIPAllocLB),
				Tag:   String("true"),
			})
		}
	}
	if restoreMode && subnetPortCR != nil {
		subnetPortTags := []model.Tag{
			{
				Scope: String(common.TagScopeSubnetPortCRName),
				Tag:   &subnetPortCR.Name,
			},
			{
				Scope: String(common.TagScopeSubnetPortCRUID),
				Tag:   (*string)(&subnetPortCR.UID),
			},
		}
		tags = append(tags, subnetPortTags...)
	}
	ipAddressBlockVisibilityStr := util.ToUpper(string(ipAddressBlockVisibility))
	// objForIdGeneration is an object to use the Namespace's UID, which is used to generate the NSX IpAddressAllocation ID.
	objForIdGeneration := &metav1.ObjectMeta{
		Name: obj.GetName(),
		UID:  types.UID(common.GetNamespaceUIDFromTag(tags)),
	}
	ipAddressAllocationId := service.BuildIPAddressAllocationID(objForIdGeneration)
	vpcIpAddressAllocation := &model.VpcIpAddressAllocation{
		Id:                         String(ipAddressAllocationId),
		DisplayName:                String(service.buildIPAddressAllocationName(obj)),
		Tags:                       tags,
		IpAddressType:              &ipAddressType,
		AllocationIps:              allocationIps,
		AllocationSize:             allocationSize,
		Ipv6AllocationPrefixLength: ipv6AllocationPrefixLength,
		IpBlock:                    ipBlock,
	}
	if ipAddressType != model.VpcIpAddressAllocation_IP_ADDRESS_TYPE_IPV6 {
		vpcIpAddressAllocation.IpAddressBlockVisibility = &ipAddressBlockVisibilityStr
	}

	return vpcIpAddressAllocation, vpcInfo, nil
}

func (service *IPAddressAllocationService) BuildIPAddressAllocationID(obj metav1.Object) string {
	return common.BuildUniqueIDWithRandomUUID(obj, util.GenerateIDByObject, service.allocationIdExists)
}

func (service *IPAddressAllocationService) buildIPAddressAllocationName(obj metav1.Object) string {
	return util.GenerateTruncName(common.MaxNameLength, obj.GetName(), "", "", "", "")
}

func (service *IPAddressAllocationService) buildIPAddressAllocationTags(obj metav1.Object) []model.Tag {
	return util.BuildBasicTags(service.NSXConfig.Cluster, obj, service.GetNamespaceUID(obj.GetNamespace()))
}
