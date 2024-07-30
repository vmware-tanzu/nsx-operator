package vpc

import (
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

func (s *VPCService) WrapHierarchyVPC(org, nsxtProject string, vpc *model.Vpc, lbs *model.LBService) (*model.OrgRoot, error) {
	if lbs != nil {
		var vpcChildren []*data.StructValue
		childrenLBS, err := s.WrapLBS(lbs)
		if err != nil {
			return nil, err
		}
		vpcChildren = append(vpcChildren, childrenLBS...)
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
