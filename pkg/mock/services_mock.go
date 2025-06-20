package mock

import (
	"context"

	"github.com/stretchr/testify/mock"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type MockVPCServiceProvider struct {
	mock.Mock
}

func (m *MockVPCServiceProvider) GetNamespacesByNetworkconfigName(nc string) ([]string, error) {
	return nil, nil
}

func (m *MockVPCServiceProvider) UpdateDefaultNetworkConfig(vpcNetworkConfig *v1alpha1.VPCNetworkConfiguration) error {
	m.Called()
	return nil
}

func (m *MockVPCServiceProvider) GetVPCNetworkConfig(ncCRName string) (*v1alpha1.VPCNetworkConfiguration, bool, error) {
	m.Called(ncCRName)
	return &v1alpha1.VPCNetworkConfiguration{}, false, nil
}

func (m *MockVPCServiceProvider) ValidateNetworkConfig(nc *v1alpha1.VPCNetworkConfiguration) error {
	m.Called(nc)
	return nil
}

func (m *MockVPCServiceProvider) GetVPCNetworkConfigByNamespace(ns string) (*v1alpha1.VPCNetworkConfiguration, error) {
	m.Called()
	return nil, nil
}

func (m *MockVPCServiceProvider) GetDefaultNetworkConfig() (*v1alpha1.VPCNetworkConfiguration, error) {
	m.Called()
	return nil, nil
}

func (m *MockVPCServiceProvider) ListVPCInfo(ns string) []common.VPCResourceInfo {
	arg := m.Called(ns)
	return arg.Get(0).([]common.VPCResourceInfo)
}

func (m *MockVPCServiceProvider) GetNetworkconfigNameFromNS(ctx context.Context, ns string) (string, error) {
	m.Called()
	return "", nil
}

func (m *MockVPCServiceProvider) GetProjectName(orgID, projectID string) (string, error) {
	args := m.Called(orgID, projectID)
	return args.String(0), args.Error(1)
}

func (m *MockVPCServiceProvider) GetVPCName(orgID, projectID, vpcID string) (string, error) {
	args := m.Called(orgID, projectID, vpcID)
	return args.String(0), args.Error(1)
}

func (m *MockVPCServiceProvider) IsDefaultNSXProject(orgID, projectID string) (bool, error) {
	args := m.Called(orgID, projectID)
	return args.Bool(0), args.Error(1)
}

type MockSubnetServiceProvider struct {
	mock.Mock
}

func (m *MockSubnetServiceProvider) GetSubnetByKey(key string) (*model.VpcSubnet, error) {
	return nil, nil
}

func (m *MockSubnetServiceProvider) GetSubnetByPath(path string) (*model.VpcSubnet, error) {
	return nil, nil
}

func (m *MockSubnetServiceProvider) GetSubnetsByIndex(key, value string) []*model.VpcSubnet {
	arg := m.Called(key, value)
	return arg.Get(0).([]*model.VpcSubnet)
}

func (m *MockSubnetServiceProvider) CreateOrUpdateSubnet(obj client.Object, vpcInfo common.VPCResourceInfo, tags []model.Tag) (*model.VpcSubnet, error) {
	arg := m.Called(obj, vpcInfo, tags)
	return arg.Get(0).(*model.VpcSubnet), arg.Error(1)
}

func (m *MockSubnetServiceProvider) GenerateSubnetNSTags(obj client.Object) []model.Tag {
	m.Called()
	return []model.Tag{}
}

func (m *MockSubnetServiceProvider) ListSubnetByName(ns, name string) []*model.VpcSubnet {
	return []*model.VpcSubnet{}
}

func (m *MockSubnetServiceProvider) ListSubnetBySubnetSetName(ns, subnetSetName string) []*model.VpcSubnet {
	return []*model.VpcSubnet{}
}

type MockSubnetPortServiceProvider struct {
	mock.Mock
}

func (m *MockSubnetPortServiceProvider) GetPortsOfSubnet(nsxSubnetID string) (ports []*model.VpcSubnetPort) {
	return
}

func (m *MockSubnetPortServiceProvider) AllocatePortFromSubnet(subnet *model.VpcSubnet) bool {
	return true
}

func (m *MockSubnetPortServiceProvider) ReleasePortInSubnet(path string) {
	return
}

func (m *MockSubnetPortServiceProvider) IsEmptySubnet(id string, path string) bool {
	return true
}

func (m *MockSubnetPortServiceProvider) DeletePortCount(path string) {
	return
}

type MockIPAddressAllocationProvider struct {
	mock.Mock
}

func (m *MockIPAddressAllocationProvider) BuildIPAddressAllocationID(obj metav1.Object) string {
	return ""
}

func (m *MockIPAddressAllocationProvider) GetIPAddressAllocationByOwner(owner metav1.Object) (*model.VpcIpAddressAllocation, error) {
	return nil, nil
}

func (m *MockIPAddressAllocationProvider) CreateIPAddressAllocationForAddressBinding(addressBinding *v1alpha1.AddressBinding, subnetPort *v1alpha1.SubnetPort, restoreMode bool) error {
	return nil
}

func (m *MockIPAddressAllocationProvider) DeleteIPAddressAllocationForAddressBinding(obj metav1.Object) error {
	return nil
}

func (m *MockIPAddressAllocationProvider) DeleteIPAddressAllocationByNSXResource(nsxIPAddressAllocation *model.VpcIpAddressAllocation) error {
	return nil
}

func (m *MockIPAddressAllocationProvider) ListIPAddressAllocationWithAddressBinding() []*model.VpcIpAddressAllocation {
	return []*model.VpcIpAddressAllocation{}
}
