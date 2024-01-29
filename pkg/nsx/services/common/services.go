package common

// The method in this interface can be provided to other controllers.
// Using interface instead vpc service instance can prevent other service
// calling method that should not be exposed.
type VPCServiceProvider interface {
	RegisterNamespaceNetworkconfigBinding(ns string, ncCRName string)
	UnRegisterNamespaceNetworkconfigBinding(ns string)
	GetVPCNetworkConfig(ncCRName string) (VPCNetworkConfigInfo, bool)
	ValidateNetworkConfig(nc VPCNetworkConfigInfo) bool
	GetVPCNetworkConfigByNamespace(ns string) *VPCNetworkConfigInfo
	GetDefaultNetworkConfig() (bool, *VPCNetworkConfigInfo)
	ListVPCInfo(ns string) []VPCResourceInfo
}
