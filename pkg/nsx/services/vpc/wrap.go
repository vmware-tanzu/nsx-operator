package vpc

import (
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

func (s *VPCService) WrapHierarchyVPC(vpc *model.Vpc, lbs *model.LBService) (*model.Infra, error) {
	if lbs != nil {
		var vpcChildren []*data.StructValue
		lbsChildren, err := s.WrapLBS(lbs)
		if err != nil {
			return nil, err
		}
		vpcChildren = append(vpcChildren, lbsChildren...)
		vpc.Children = vpcChildren
	}
	var infraChildren []*data.StructValue
	vpcChildren, err := s.WrapVPC(vpc)
	if err != nil {
		return nil, err
	}
	infraChildren = append(infraChildren, vpcChildren...)

	infra, err := s.WrapInfra(infraChildren)
	if err != nil {
		return nil, err
	}
	return infra, nil
}
