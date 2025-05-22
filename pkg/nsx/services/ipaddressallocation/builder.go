package ipaddressallocation

import (
	"fmt"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
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
	if visibility == v1alpha1.IPAddressVisibilityPrivateTGW {
		return "PRIVATE_TGW"
	}
	return visibility
}

func (service *IPAddressAllocationService) BuildIPAddressAllocation(obj metav1.Object, subnetPortCR *v1alpha1.SubnetPort, restoreMode bool) (*model.VpcIpAddressAllocation, error) {
	ipAddressBlockVisibility := v1alpha1.IPAddressVisibilityExternal
	var allocationIps *string
	var allocationSize *int64
	switch o := obj.(type) {
	case *v1alpha1.IPAddressAllocation:
		VPCInfo := service.VPCService.ListVPCInfo(o.Namespace)
		if len(VPCInfo) == 0 {
			log.Error(nil, "Failed to find VPCInfo for IPAddressAllocation CR", "IPAddressAllocation", o.Name, "Namespace", o.Namespace)
			return nil, fmt.Errorf("failed to find VPCInfo for IPAddressAllocation CR %s in Namespace %s", o.Name, o.Namespace)
		}
		ipAddressBlockVisibility = convertIpAddressBlockVisibility(o.Spec.IPAddressBlockVisibility)
		if len(o.Spec.AllocationIPs) > 0 {
			allocationIps = String(o.Spec.AllocationIPs)
		} else if restoreMode && len(o.Status.AllocationIPs) > 0 {
			allocationIps = String(o.Status.AllocationIPs)
		} else {
			// Field AllocationIPs and AllocationSize cannot be provided together for VPC IP allocation.
			allocationSize = Int64(int64(o.Spec.AllocationSize))
		}
	case *v1alpha1.AddressBinding:
		if !restoreMode || subnetPortCR == nil || o.Spec.IPAddressAllocationName != "" {
			return nil, nil
		}
		allocationIps = &o.Status.IPAddress
	}
	tags := service.buildIPAddressAllocationTags(obj)
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
	ipAddressAllocationid := service.BuildIPAddressAllocationID(obj)
	vpcIpAddressAllocation := &model.VpcIpAddressAllocation{
		Id:                       String(ipAddressAllocationid),
		DisplayName:              String(service.buildIPAddressAllocationName(obj)),
		Tags:                     tags,
		IpAddressBlockVisibility: &ipAddressBlockVisibilityStr,
		AllocationIps:            allocationIps,
		AllocationSize:           allocationSize,
	}

	return vpcIpAddressAllocation, nil
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
