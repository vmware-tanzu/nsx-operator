package common

import (
	"context"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	IsDefaultNSXProject(orgID, projectID string) (bool, error)
}

type SubnetServiceProvider interface {
	GetSubnetByKey(key string) (*model.VpcSubnet, error)
	GetSubnetByPath(path string, sharedSubnet bool) (*model.VpcSubnet, error)
	GetSubnetsByIndex(key, value string) []*model.VpcSubnet
	CreateOrUpdateSubnet(obj client.Object, vpcInfo VPCResourceInfo, tags []model.Tag) (*model.VpcSubnet, error)
	GenerateSubnetNSTags(obj client.Object) []model.Tag
	ListSubnetByName(ns, name string) []*model.VpcSubnet
	ListSubnetBySubnetSetName(ns, subnetSetName string) []*model.VpcSubnet
	GetSubnetByCR(subnet *v1alpha1.Subnet) (*model.VpcSubnet, error)
	GetNSXSubnetFromCacheOrAPI(associatedResource string) (*model.VpcSubnet, error)
}

type SubnetPortServiceProvider interface {
	GetPortsOfSubnet(nsxSubnetID string) (ports []*model.VpcSubnetPort)
	AllocatePortFromSubnet(subnet *model.VpcSubnet) (bool, error)
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

type IPAddressAllocationServiceProvider interface {
	GetIPAddressAllocationByOwner(owner metav1.Object) (*model.VpcIpAddressAllocation, error)
	CreateIPAddressAllocationForAddressBinding(addressBinding *v1alpha1.AddressBinding, subnetPort *v1alpha1.SubnetPort, restoreMode bool) error
	DeleteIPAddressAllocationForAddressBinding(obj metav1.Object) error
	BuildIPAddressAllocationID(obj metav1.Object) string
	DeleteIPAddressAllocationByNSXResource(nsxIPAddressAllocation *model.VpcIpAddressAllocation) error
	ListIPAddressAllocationWithAddressBinding() []*model.VpcIpAddressAllocation
}
