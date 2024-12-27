package mock

import (
	"github.com/stretchr/testify/mock"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type MockVPCServiceProvider struct {
	mock.Mock
}

func (m *MockVPCServiceProvider) GetNamespacesByNetworkconfigName(nc string) []string {
	return nil
}

func (m *MockVPCServiceProvider) RegisterVPCNetworkConfig(ncCRName string, info common.VPCNetworkConfigInfo) {
}

func (m *MockVPCServiceProvider) RegisterNamespaceNetworkconfigBinding(ns string, ncCRName string) {
	m.Called(ns, ncCRName)
}

func (m *MockVPCServiceProvider) UnRegisterNamespaceNetworkconfigBinding(ns string) {
	m.Called(ns)
}

func (m *MockVPCServiceProvider) GetVPCNetworkConfig(ncCRName string) (common.VPCNetworkConfigInfo, bool) {
	m.Called(ncCRName)
	return common.VPCNetworkConfigInfo{}, false
}

func (m *MockVPCServiceProvider) ValidateNetworkConfig(nc common.VPCNetworkConfigInfo) bool {
	m.Called(nc)
	return true
}

func (m *MockVPCServiceProvider) GetVPCNetworkConfigByNamespace(ns string) *common.VPCNetworkConfigInfo {
	m.Called()
	return nil
}

func (m *MockVPCServiceProvider) GetDefaultNetworkConfig() (bool, *common.VPCNetworkConfigInfo) {
	m.Called()
	return false, nil
}

func (m *MockVPCServiceProvider) ListVPCInfo(ns string) []common.VPCResourceInfo {
	arg := m.Called(ns)
	return arg.Get(0).([]common.VPCResourceInfo)
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
