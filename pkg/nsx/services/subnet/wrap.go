package subnet

import (
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// Patch API at infra level can be used in two flavours.
// 1. Like a regular API to update Infra object.
// 2. Hierarchical API: To create/update/delete entire or part of intent hierarchy Hierarchical.
// We use infra patch API in hierarchical mode to create/update/delete entire or part of intent hierarchy,
// for this convenience we can no longer CRUD CR separately, and reduce the number of API calls to NSX-T.

// WrapHierarchySubnet Wrap the subnet for InfraClient to patch.
func (service *SubnetService) WrapHierarchySubnet(subnet *model.VpcSubnet, vpcInfo *common.VPCResourceInfo) (*model.OrgRoot, error) {
	if orgRoot, err := service.wrapOrgRoot(subnet, vpcInfo.OrgID, vpcInfo.ProjectID, vpcInfo.VPCID); err != nil {
		return nil, err
	} else {
		return orgRoot, nil
	}
}

func (service *SubnetService) wrapOrgRoot(subnet *model.VpcSubnet, orgID, projectID, vpcID string) (*model.OrgRoot, error) {
	// This is the outermost layer of the hierarchy subnet.
	// It doesn't need ID field.
	resourceType := "OrgRoot"
	children, err := service.wrapOrg(subnet, orgID, projectID, vpcID)
	if err != nil {
		return nil, err
	}
	orgRoot := model.OrgRoot{
		Children:     children,
		ResourceType: &resourceType,
	}
	return &orgRoot, nil
}

func (service *SubnetService) wrapOrg(subnet *model.VpcSubnet, orgID, projectID, vpcID string) ([]*data.StructValue, error) {
	children, err := service.wrapProject(subnet, projectID, vpcID)
	if err != nil {
		return nil, err
	}
	targetType := "Org"
	childProject := model.ChildResourceReference{
		Id:           &orgID,
		ResourceType: "ChildResourceReference",
		TargetType:   &targetType,
		Children:     children,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childProject, model.ChildResourceReferenceBindingType())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return []*data.StructValue{dataValue.(*data.StructValue)}, nil
}

func (service *SubnetService) wrapProject(subnet *model.VpcSubnet, projectID, vpcID string) ([]*data.StructValue, error) {
	children, err := service.wrapVPC(subnet, vpcID)
	if err != nil {
		return nil, err
	}
	targetType := "Project"
	childProject := model.ChildResourceReference{
		Id:           &projectID,
		ResourceType: "ChildResourceReference",
		TargetType:   &targetType,
		Children:     children,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childProject, model.ChildResourceReferenceBindingType())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return []*data.StructValue{dataValue.(*data.StructValue)}, nil
}

func (service *SubnetService) wrapVPC(subnet *model.VpcSubnet, vpcID string) ([]*data.StructValue, error) {
	children, err := service.wrapSubnet(subnet)
	if err != nil {
		return nil, err
	}
	targetType := "Vpc"
	childVPC := model.ChildResourceReference{
		Id:           &vpcID,
		ResourceType: "ChildResourceReference",
		TargetType:   &targetType,
		Children:     children,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childVPC, model.ChildResourceReferenceBindingType())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return []*data.StructValue{dataValue.(*data.StructValue)}, nil
}

func (service *SubnetService) wrapSubnet(subnet *model.VpcSubnet) ([]*data.StructValue, error) {
	subnet.ResourceType = &common.ResourceTypeSubnet
	childSubnet := model.ChildVpcSubnet{
		Id:              subnet.Id,
		MarkedForDelete: subnet.MarkedForDelete,
		ResourceType:    "ChildVpcSubnet",
		VpcSubnet:       subnet,
	}
	dataValue, errors := NewConverter().ConvertToVapi(childSubnet, model.ChildVpcSubnetBindingType())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return []*data.StructValue{dataValue.(*data.StructValue)}, nil
}

// wrapDHCPStaticBindingConfig wraps DHCPStaticBindingConfig as children of VPCSubnet.
func (service *SubnetService) wrapDHCPStaticBindingConfig(config model.DhcpStaticBindingConfig) ([]*data.StructValue, error) {
	configValue, errors := NewConverter().ConvertToVapi(config, model.ChildDhcpStaticBindingConfigBindingType())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	configChild := model.ChildDhcpStaticBindingConfig{
		ResourceType:            "ChildDhcpStaticBindingConfig",
		Id:                      config.Id,
		DhcpStaticBindingConfig: configValue.(*data.StructValue),
		MarkedForDelete:         config.MarkedForDelete,
	}
	configChildValue, errors := NewConverter().ConvertToVapi(configChild, model.ChildDhcpStaticBindingConfigBindingType())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return []*data.StructValue{configChildValue.(*data.StructValue)}, nil
}

// wrapSegmentPort wraps SegmentPort as children of VPCSubnet.
// A reserved function for creating SegmentPorts, currently not used.
func (service *SubnetService) wrapSegmentPort(port model.SegmentPort) ([]*data.StructValue, error) {
	portChild := model.ChildSegmentPort{
		ResourceType:    "ChildSegmentPort",
		Id:              port.Id,
		SegmentPort:     &port,
		MarkedForDelete: port.MarkedForDelete,
	}
	dataValue, errors := NewConverter().ConvertToVapi(portChild, model.ChildSegmentBindingType())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return []*data.StructValue{dataValue.(*data.StructValue)}, nil
}
