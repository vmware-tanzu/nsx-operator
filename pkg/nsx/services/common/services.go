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
	GetNamespacesByNetworkconfigName(nc string) ([]string, error)
	GetVPCNetworkConfig(ncCRName string) (*v1alpha1.VPCNetworkConfiguration, bool, error)
	ValidateNetworkConfig(nc *v1alpha1.VPCNetworkConfiguration) error
	GetVPCNetworkConfigByNamespace(ns string) (*v1alpha1.VPCNetworkConfiguration, error)
	GetDefaultNetworkConfig() (*v1alpha1.VPCNetworkConfiguration, error)
	ListVPCInfo(ns string) []VPCResourceInfo
	GetNetworkconfigNameFromNS(ctx context.Context, ns string) (string, error)
}

type SubnetServiceProvider interface {
	GetSubnetByKey(key string) (*model.VpcSubnet, error)
	GetSubnetByPath(path string) (*model.VpcSubnet, error)
	GetSubnetsByIndex(key, value string) []*model.VpcSubnet
	CreateOrUpdateSubnet(obj client.Object, vpcInfo VPCResourceInfo, tags []model.Tag) (*model.VpcSubnet, error)
	GenerateSubnetNSTags(obj client.Object) []model.Tag
}

type SubnetPortServiceProvider interface {
	GetPortsOfSubnet(nsxSubnetID string) (ports []*model.VpcSubnetPort)
	AllocatePortFromSubnet(subnet *model.VpcSubnet) bool
	ReleasePortInSubnet(path string)
	IsEmptySubnet(id string, path string) bool
	DeletePortCount(path string)
}

type NodeServiceReader interface {
	GetNodeByName(nodeName string) []*model.HostTransportNode
}

type IPBlocksInfoServiceProvider interface {
	SyncIPBlocksInfo(ctx context.Context) error
	UpdateIPBlocksInfo(ctx context.Context, vpcConfigCR *v1alpha1.VPCNetworkConfiguration) error
	ResetPeriodicSync()
}
