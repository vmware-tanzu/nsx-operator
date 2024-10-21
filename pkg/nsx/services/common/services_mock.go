package common

import (
	"github.com/stretchr/testify/mock"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type MockVPCServiceProvider struct {
	mock.Mock
}

func (m *MockVPCServiceProvider) GetNamespacesByNetworkconfigName(nc string) []string {
	return nil
}

func (m *MockVPCServiceProvider) RegisterVPCNetworkConfig(ncCRName string, info VPCNetworkConfigInfo) {
}

func (m *MockVPCServiceProvider) RegisterNamespaceNetworkconfigBinding(ns string, ncCRName string) {
	m.Called(ns, ncCRName)
}

func (m *MockVPCServiceProvider) UnRegisterNamespaceNetworkconfigBinding(ns string) {
	m.Called(ns)
}

func (m *MockVPCServiceProvider) GetVPCNetworkConfig(ncCRName string) (VPCNetworkConfigInfo, bool) {
	m.Called(ncCRName)
	return VPCNetworkConfigInfo{}, false
}

func (m *MockVPCServiceProvider) ValidateNetworkConfig(nc VPCNetworkConfigInfo) bool {
	m.Called(nc)
	return true
}

func (m *MockVPCServiceProvider) GetVPCNetworkConfigByNamespace(ns string) *VPCNetworkConfigInfo {
	m.Called()
	return nil
}

func (m *MockVPCServiceProvider) GetDefaultNetworkConfig() (bool, *VPCNetworkConfigInfo) {
	m.Called()
	return false, nil
}

func (m *MockVPCServiceProvider) ListVPCInfo(ns string) []VPCResourceInfo {
	arg := m.Called(ns)
	return arg.Get(0).([]VPCResourceInfo)
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

func (m *MockSubnetServiceProvider) CreateOrUpdateSubnet(obj client.Object, vpcInfo VPCResourceInfo, tags []model.Tag) (string, error) {
	arg := m.Called(obj, vpcInfo, tags)
	return arg.Get(0).(string), arg.Error(1)
}

func (m *MockSubnetServiceProvider) GenerateSubnetNSTags(obj client.Object) []model.Tag {
	m.Called()
	return []model.Tag{}
}

func (m *MockSubnetServiceProvider) LockSubnet(path *string) {
	return
}

func (m *MockSubnetServiceProvider) UnlockSubnet(path *string) {
	return
}

type MockSubnetPortServiceProvider struct {
	mock.Mock
}

func (m *MockSubnetPortServiceProvider) GetPortsOfSubnet(nsxSubnetID string) (ports []*model.VpcSubnetPort) {
	return
}
