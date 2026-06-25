package vpc

import (
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// WrapHierarchyVPC assembles the HAPI OrgRoot payload for creating/updating a VPC and its children.
// lbServiceIPAllocs, when non-empty, are added as the first VPC children (before LBS) and are used in
// tepless restore mode to pin the LB service IPs (IPv4 and/or IPv6) to their previously allocated addresses.
func (s *VPCService) WrapHierarchyVPC(org, nsxtProject string, vpc *model.Vpc, lbServiceIPAllocs []*model.VpcIpAddressAllocation, lbs *model.LBService, attachment *model.VpcAttachment) (*model.OrgRoot, error) {
	var vpcChildren []*data.StructValue
	for _, alloc := range lbServiceIPAllocs {
		log.Debug("Wrapping LB Service IP Allocation", "LB Service IP Allocation", alloc.Id, "Allocation IP", alloc.AllocationIp)
		childIPAlloc, err := common.WrapVpcIpAddressAllocation(alloc)
		if err != nil {
			return nil, err
		}
		vpcChildren = append(vpcChildren, childIPAlloc)
	}
	if len(lbServiceIPAllocs) > 0 {
		vpc.Children = vpcChildren
	}
	if lbs != nil {
		childrenLBS, err := s.WrapLBS(lbs)
		if err != nil {
			return nil, err
		}
		vpcChildren = append(vpcChildren, childrenLBS...)
		vpc.Children = vpcChildren
	}
	if attachment != nil {
		childrenAttachment, err := s.WrapAttachment(attachment)
		if err != nil {
			return nil, err
		}
		vpcChildren = append(vpcChildren, childrenAttachment...)
		vpc.Children = vpcChildren
	}
	var projectChildren []*data.StructValue
	childrenVPC, err := s.WrapVPC(vpc)
	if err != nil {
		return nil, err
	}
	projectChildren = append(projectChildren, childrenVPC...)

	var orgChildren []*data.StructValue
	childrenProject, err := s.WrapProject(nsxtProject, projectChildren)
	if err != nil {
		return nil, err
	}
	orgChildren = append(orgChildren, childrenProject...)

	var orgRootChildren []*data.StructValue
	childrenOrg, err := s.WrapOrg(org, orgChildren)
	if err != nil {
		return nil, err
	}
	orgRootChildren = append(orgRootChildren, childrenOrg...)

	orgRoot, _ := s.WrapOrgRoot(orgRootChildren)
	return orgRoot, nil
}
