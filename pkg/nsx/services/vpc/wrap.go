package vpc

import (
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// WrapHierarchyVPC assembles the HAPI OrgRoot payload for creating/updating a VPC and its children.
// lbServiceIPAlloc, when non-nil, is added as the first VPC child (before LBS) and is used in tepless
// restore mode to pin the LB service IP to the previously allocated address.
func (s *VPCService) WrapHierarchyVPC(org, nsxtProject string, vpc *model.Vpc, lbServiceIPAlloc *model.VpcIpAddressAllocation, lbs *model.LBService, attachment *model.VpcAttachment) (*model.OrgRoot, error) {
	var vpcChildren []*data.StructValue
	if lbServiceIPAlloc != nil {
		log.Debug("Wrapping LB Service IP Allocation", "LB Service IP Allocation", lbServiceIPAlloc.Id, "Allocation IP", lbServiceIPAlloc.AllocationIp)
		childIPAlloc, err := common.WrapVpcIpAddressAllocation(lbServiceIPAlloc)
		if err != nil {
			return nil, err
		}
		vpcChildren = append(vpcChildren, childIPAlloc)
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
