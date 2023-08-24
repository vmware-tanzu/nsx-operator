package common

import "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"

type IVPCService interface {
	RegisterNamespaceNetworkconfigBinding(ns string, ncCRName string)
	UnRegisterNamespaceNetworkconfigBinding(ns string)
	GetVPCNetworkConfig(ncCRName string) (common.VPCNetworkConfigInfo, bool)
	ValidateNetworkConfig(nc common.VPCNetworkConfigInfo) bool
	GetVPCNetworkConfigByNamespace(ns string) *common.VPCNetworkConfigInfo
}
