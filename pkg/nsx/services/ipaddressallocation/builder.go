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

func (service *IPAddressAllocationService) getVPCInfo(ns string, isLB bool) ([]common.VPCResourceInfo, error) {
	var VPCInfo []common.VPCResourceInfo
	if isLB {
		nc, err := service.VPCService.GetVPCNetworkConfigByNamespace(ns)
		if err != nil {
			log.Error(err, "Failed to Get NetworkConfig by Namespace", "Namespace", ns)
			return nil, err
		}
		if nc != nil && nc.Spec.LoadBalancerVPC != "" {
			vpcResourceInfo, err := common.ParseVPCResourcePath(nc.Spec.LoadBalancerVPC)
			if err != nil {
				log.Error(err, "Failed to parse LoadBalancerVPC from VPC path", "VPCPath", nc.Spec.LoadBalancerVPC)
				return nil, err
			}
			VPCInfo = append(VPCInfo, vpcResourceInfo)
		} else {
			err := fmt.Errorf("LoadBalancerVPC is not configured on VPCNetworkConfiguration for namespace %s, but IPAddressAllocation is marked for LB", ns)
			log.Error(err, "Cannot allocate IP for LoadBalancer")
			return nil, err
		}
	}
	if len(VPCInfo) == 0 {
		VPCInfo = service.VPCService.ListVPCInfo(ns)
	}
	return VPCInfo, nil
}

func (service *IPAddressAllocationService) getVPCInfoForCR(o *v1alpha1.IPAddressAllocation) ([]common.VPCResourceInfo, error) {
	annos := o.GetAnnotations()
	isLB := annos != nil && annos[common.AnnotationIPAllocLB] == "true"
	return service.getVPCInfo(o.Namespace, isLB)
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
		if rawBlockName := strings.TrimSpace(o.Spec.IPBlockName); rawBlockName != "" {
			if strings.HasPrefix(rawBlockName, "/") {
				log.Error(nil, "Invalid IPBlockName format, only block ID or ':ipBlockID' is supported, full path is not supported", "IPBlockName", rawBlockName)
				return nil, nil, fmt.Errorf("invalid IPBlockName format %s, only block ID or ':ipBlockID' is supported, full path is not supported", rawBlockName)
			} else if strings.HasPrefix(rawBlockName, ":") {
				ipBlockID := strings.TrimPrefix(rawBlockName, ":")
				if ipBlockID == "" {
					log.Error(nil, "Invalid IPBlockName format, IP block ID cannot be empty", "IPBlockName", rawBlockName)
					return nil, nil, fmt.Errorf("invalid IPBlockName format %s, IP block ID cannot be empty", rawBlockName)
				}
				ipBlock = String(fmt.Sprintf("/infra/ip-blocks/%s", ipBlockID))
			} else {
				if len(vpcInfo) == 0 || vpcInfo[0].OrgID == "" || vpcInfo[0].ProjectID == "" {
					log.Error(nil, "org or project info is missing to build path for IPBlock", "IPBlockName", rawBlockName)
					return nil, nil, fmt.Errorf("org or project info is missing to build path for IPBlock %s", rawBlockName)
				}
				ipBlock = String(fmt.Sprintf("/orgs/%s/projects/%s/infra/ip-blocks/%s", vpcInfo[0].OrgID, vpcInfo[0].ProjectID, rawBlockName))
			}
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
		annos := o.GetAnnotations()
		if annos != nil && annos[common.AnnotationIPAllocLB] == "true" {
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
