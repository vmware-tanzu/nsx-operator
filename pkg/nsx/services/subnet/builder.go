package subnet

import (
	"github.com/google/uuid"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	String = common.String
	Int64  = common.Int64
	Bool   = common.Bool
)

const (
	SUBNETPREFIX = "sub"
)

func getCluster(service *SubnetService) string {
	return service.NSXConfig.Cluster
}

func (service *SubnetService) buildSubnetID(subnet *v1alpha1.Subnet) string {
	return util.GenerateID(string(subnet.UID), SUBNETPREFIX, "", "")
}

func (service *SubnetService) buildSubnetSetID(subnetset *v1alpha1.SubnetSet, index string) string {
	return util.GenerateID(string(subnetset.UID), SUBNETPREFIX, "", index)
}

func (service *SubnetService) buildSubnetName(subnet *v1alpha1.Subnet) string {
	return util.GenerateDisplayName(subnet.ObjectMeta.Name, SUBNETPREFIX, "", "", getCluster(service))
}

func (service *SubnetService) buildSubnetSetName(subnetset *v1alpha1.SubnetSet, index string) string {
	return util.GenerateDisplayName(subnetset.ObjectMeta.Name, SUBNETPREFIX, index, "", getCluster(service))
}

func (service *SubnetService) buildSubnet(obj client.Object, tags []model.Tag) (*model.VpcSubnet, error) {
	tags = util.AppendTags(service.buildBasicTags(obj), tags)
	var nsxSubnet *model.VpcSubnet
	var staticIpAllocation bool
	switch o := obj.(type) {
	case *v1alpha1.Subnet:
		nsxSubnet = &model.VpcSubnet{
			Id:          String(service.buildSubnetID(o)),
			AccessMode:  String(util.Capitalize(string(o.Spec.AccessMode))),
			DhcpConfig:  service.buildDHCPConfig(int64(o.Spec.IPv4SubnetSize - 4)),
			DisplayName: String(service.buildSubnetName(o)),
		}
		staticIpAllocation = o.Spec.AdvancedConfig.StaticIPAllocation.Enable
	case *v1alpha1.SubnetSet:
		index := uuid.NewString()
		nsxSubnet = &model.VpcSubnet{
			Id:          String(service.buildSubnetSetID(o, index)),
			AccessMode:  String(util.Capitalize(string(o.Spec.AccessMode))),
			DhcpConfig:  service.buildDHCPConfig(int64(o.Spec.IPv4SubnetSize - 4)),
			DisplayName: String(service.buildSubnetSetName(o, index)),
		}
		staticIpAllocation = o.Spec.AdvancedConfig.StaticIPAllocation.Enable
	default:
		return nil, SubnetTypeError
	}
	nsxSubnet.Tags = tags
	nsxSubnet.AdvancedConfig = &model.SubnetAdvancedConfig{
		StaticIpAllocation: &model.StaticIpAllocation{
			Enabled: &staticIpAllocation,
		},
	}
	return nsxSubnet, nil
}

func (service *SubnetService) buildDHCPConfig(poolSize int64) *model.VpcSubnetDhcpConfig {
	// Subnet DHCP is used by AVI, not needed for now. We need to explicitly mark enableDhcp = false,
	// otherwise Subnet will use DhcpConfig inherited from VPC.
	dhcpConfig := &model.VpcSubnetDhcpConfig{
		EnableDhcp: Bool(false),
		StaticPoolConfig: &model.StaticPoolConfig{
			// Number of IPs to be reserved in static ip pool.
			// By default, if dhcp is enabled then static ipv4 pool size will be zero and all available IPs will be
			// reserved in local dhcp pool. Maximum allowed value is 'subnet size - 4'.
			Ipv4PoolSize: Int64(poolSize),
		},
	}
	return dhcpConfig
}

func (service *SubnetService) buildDNSClientConfig(obj *v1alpha1.DNSClientConfig) *model.DnsClientConfig {
	dnsClientConfig := &model.DnsClientConfig{}
	dnsClientConfig.DnsServerIps = append(dnsClientConfig.DnsServerIps, obj.DNSServersIPs...)
	return dnsClientConfig
}

func (service *SubnetService) buildBasicTags(obj client.Object) []model.Tag {
	return util.BuildBasicTags(getCluster(service), obj, "")
}
