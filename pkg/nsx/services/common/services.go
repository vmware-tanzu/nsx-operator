package common

import (
	"context"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

// VPCServiceProvider provides to methods other controllers and services.
// Using interface instead vpc service instance can prevent other service
// calling method that should not be exposed.
type VPCServiceProvider interface {
	RegisterNamespaceNetworkconfigBinding(ns string, ncCRName string)
	GetNamespacesByNetworkconfigName(nc string) []string
	RegisterVPCNetworkConfig(ncCRName string, info VPCNetworkConfigInfo)
	UnRegisterNamespaceNetworkconfigBinding(ns string)
	GetVPCNetworkConfig(ncCRName string) (VPCNetworkConfigInfo, bool)
	ValidateNetworkConfig(nc VPCNetworkConfigInfo) bool
	GetVPCNetworkConfigByNamespace(ns string) *VPCNetworkConfigInfo
	GetDefaultNetworkConfig() (bool, *VPCNetworkConfigInfo)
	ListVPCInfo(ns string) []VPCResourceInfo
}

type SubnetServiceProvider interface {
	GetSubnetByKey(key string) (*model.VpcSubnet, error)
	GetSubnetByPath(path string) (*model.VpcSubnet, error)
	GetSubnetsByIndex(key, value string) []*model.VpcSubnet
	CreateOrUpdateSubnet(obj client.Object, vpcInfo VPCResourceInfo, tags []model.Tag) (string, error)
	GenerateSubnetNSTags(obj client.Object) []model.Tag
	LockSubnet(path *string)
	UnlockSubnet(path *string)
}

type SubnetPortServiceProvider interface {
	GetPortsOfSubnet(nsxSubnetID string) (ports []*model.VpcSubnetPort)
}

type NodeServiceReader interface {
	GetNodeByName(nodeName string) []*model.HostTransportNode
}

type IPBlocksInfoServiceProvider interface {
	SyncIPBlocksInfo(ctx context.Context) error
	UpdateIPBlocksInfo(ctx context.Context, vpcConfigCR *v1alpha1.VPCNetworkConfiguration) error
	ResetPeriodicSync()
}
