package common

import (
	"github.com/stretchr/testify/mock"
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
	m.Called()
	return []VPCResourceInfo{}
}
