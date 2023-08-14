package subnet

import (
	"fmt"

	"github.com/google/uuid"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

var (
	String = common.String
	Int64  = common.Int64
	Bool   = common.Bool
)

func getCluster(service *SubnetService) string {
	return service.NSXConfig.Cluster
}

func (service *SubnetService) buildSubnet(obj client.Object, tags []model.Tag) (*model.VpcSubnet, error) {
	tags = append(tags, service.buildBasicTags(obj)...)
	var nsxSubnet *model.VpcSubnet
	var staticIpAllocation bool
	switch o := obj.(type) {
	case *v1alpha1.Subnet:
		nsxSubnet = &model.VpcSubnet{
			Id:          String(string(o.GetUID())),
			AccessMode:  String(string(o.Spec.AccessMode)),
			DhcpConfig:  service.buildDHCPConfig(int64(o.Spec.IPv4SubnetSize - 4)),
			DisplayName: String(fmt.Sprintf("%s-%s", obj.GetNamespace(), obj.GetName())),
		}
		staticIpAllocation = o.Spec.AdvancedConfig.StaticIPAllocation.Enable
	case *v1alpha1.SubnetSet:
		index := uuid.NewString()
		nsxSubnet = &model.VpcSubnet{
			Id:          String(fmt.Sprintf("%s-%s", string(o.GetUID()), index)),
			AccessMode:  String(string(o.Spec.AccessMode)),
			DhcpConfig:  service.buildDHCPConfig(int64(o.Spec.IPv4SubnetSize - 4)),
			DisplayName: String(fmt.Sprintf("%s-%s-%s", obj.GetNamespace(), obj.GetName(), index)),
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
	tags := []model.Tag{
		{
			Scope: String(common.TagScopeCluster),
			Tag:   String(getCluster(service)),
		},
		{
			Scope: String(common.TagScopeSubnetCRUID),
			Tag:   String(string(obj.GetUID())),
		},
		{
			Scope: String(common.TagScopeNamespace),
			Tag:   String(obj.GetNamespace()),
		},
	}
	switch obj.(type) {
	case *v1alpha1.Subnet:
		tags = append(tags, model.Tag{
			Scope: String(common.TagScopeSubnetCRType),
			Tag:   String("subnet"),
		}, model.Tag{
			Scope: String(common.TagScopeSubnetCRName),
			Tag:   String(obj.GetName()),
		})
	case *v1alpha1.SubnetSet:
		tags = append(tags, model.Tag{
			Scope: String(common.TagScopeSubnetCRType),
			Tag:   String("subnetset"),
		}, model.Tag{
			Scope: String(common.TagScopeSubnetSetCRName),
			Tag:   String(obj.GetName()),
		})
	default:
		log.Error(SubnetTypeError, "unsupported type when building NSX Subnet tags")
		return nil
	}
	return tags
}
