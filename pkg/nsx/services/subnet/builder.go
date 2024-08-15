package subnet

import (
	"fmt"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	util2 "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

const AccessModeProjectInNSX string = "Private_TGW"

var (
	String = common.String
	Int64  = common.Int64
	Bool   = common.Bool
)

func getCluster(service *SubnetService) string {
	return service.NSXConfig.Cluster
}

func (service *SubnetService) BuildSubnetID(subnet *v1alpha1.Subnet) string {
	return util.GenerateIDByObject(subnet)
}

func (service *SubnetService) buildSubnetSetID(subnetset *v1alpha1.SubnetSet, index string) string {
	return util.GenerateIDByObjectWithSuffix(subnetset, index)
}

// buildSubnetName uses format "subnet.Name-subnet.UUID" to ensure the Subnet's display_name is not
// conflict with others. This is because VC will use the Subnet's display_name to created folder, so
// the name string must be unique.
func (service *SubnetService) buildSubnetName(subnet *v1alpha1.Subnet) string {
	return util.GenerateIDByObjectByLimit(subnet, common.MaxSubnetNameLength)
}

// buildSubnetSetName uses format "subnetset.Name-subnetset.UUID-index" to ensure the generated Subnet's
// display_name is not conflict with others.
func (service *SubnetService) buildSubnetSetName(subnetset *v1alpha1.SubnetSet, index string) string {
	resName := util.GenerateIDByObjectByLimit(subnetset, common.MaxSubnetNameLength-(len(index)+1))
	return util.GenerateTruncName(common.MaxSubnetNameLength, resName, "", index, "", "")
}

func convertAccessMode(accessMode string) string {
	if accessMode == v1alpha1.AccessModeProject {
		return AccessModeProjectInNSX
	}
	return accessMode
}

func (service *SubnetService) buildSubnet(obj client.Object, tags []model.Tag) (*model.VpcSubnet, error) {
	tags = append(service.buildBasicTags(obj), tags...)
	var nsxSubnet *model.VpcSubnet
	var staticIpAllocation bool
	switch o := obj.(type) {
	case *v1alpha1.Subnet:
		nsxSubnet = &model.VpcSubnet{
			Id:             String(service.BuildSubnetID(o)),
			AccessMode:     String(convertAccessMode(util.Capitalize(string(o.Spec.AccessMode)))),
			Ipv4SubnetSize: Int64(int64(o.Spec.IPv4SubnetSize)),
			DhcpConfig:     service.buildDHCPConfig(o.Spec.DHCPConfig.EnableDHCP, int64(o.Spec.IPv4SubnetSize-4)),
			DisplayName:    String(service.buildSubnetName(o)),
		}
		staticIpAllocation = o.Spec.AdvancedConfig.StaticIPAllocation.Enable
		nsxSubnet.IpAddresses = o.Spec.IPAddresses
	case *v1alpha1.SubnetSet:
		// The index is a random string with the length of 8 chars. It is the first 8 chars of the hash
		// value on a random UUID string.
		index := util.GetRandomIndexString()
		nsxSubnet = &model.VpcSubnet{
			Id:             String(service.buildSubnetSetID(o, index)),
			AccessMode:     String(convertAccessMode(util.Capitalize(string(o.Spec.AccessMode)))),
			Ipv4SubnetSize: Int64(int64(o.Spec.IPv4SubnetSize)),
			DhcpConfig:     service.buildDHCPConfig(o.Spec.DHCPConfig.EnableDHCP, int64(o.Spec.IPv4SubnetSize-4)),
			DisplayName:    String(service.buildSubnetSetName(o, index)),
		}
		staticIpAllocation = o.Spec.AdvancedConfig.StaticIPAllocation.Enable
	default:
		return nil, SubnetTypeError
	}
	// tags cannot exceed maximum size 26
	if len(tags) > common.TagsCountMax {
		errorMsg := fmt.Sprintf("tags cannot exceed maximum size 26, tags length: %d", len(tags))
		return nil, util2.ExceedTagsError{Desc: errorMsg}
	}
	nsxSubnet.Tags = tags
	nsxSubnet.AdvancedConfig = &model.SubnetAdvancedConfig{
		StaticIpAllocation: &model.StaticIpAllocation{
			Enabled: &staticIpAllocation,
		},
	}
	return nsxSubnet, nil
}

func (service *SubnetService) buildDHCPConfig(enableDHCP bool, poolSize int64) *model.VpcSubnetDhcpConfig {
	// Subnet DHCP is used by AVI, not needed for now. We need to explicitly mark enableDhcp = false,
	// otherwise Subnet will use DhcpConfig inherited from VPC.
	dhcpConfig := &model.VpcSubnetDhcpConfig{
		EnableDhcp: Bool(enableDHCP),
	}
	if !enableDHCP {
		dhcpConfig.StaticPoolConfig = &model.StaticPoolConfig{
			// Number of IPs to be reserved in static ip pool.
			// By default, if dhcp is enabled then static ipv4 pool size will be zero and all available IPs will be
			// reserved in local dhcp pool. Maximum allowed value is 'subnet size - 4'.
			Ipv4PoolSize: Int64(poolSize),
		}
	}
	return dhcpConfig
}

func (service *SubnetService) buildBasicTags(obj client.Object) []model.Tag {
	return util.BuildBasicTags(getCluster(service), obj, "")
}
