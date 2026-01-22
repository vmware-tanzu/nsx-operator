package subnet

import (
	"fmt"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	controllerscommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

const AccessModeProjectInNSX string = "Private_TGW"

var (
	String = common.String
	Int64  = common.Int64
)

func getCluster(service *SubnetService) string {
	return service.NSXConfig.Cluster
}

// BuildSubnetID uses the format "subnet.Name_$(hash(${namespace.UUID}))[5]" to generate the VpcSubnet's id.
func (service *SubnetService) BuildSubnetID(obj v1.Object) string {
	return common.BuildUniqueIDWithRandomUUID(obj, util.GenerateIDByObject, service.nsxSubnetIdExists)
}

// BuildSubnetName uses the format "subnet.Name_$(hash(${namespace.UUID}))[5]" to ensure the Subnet's display_name is not
// conflict with others. This is because VC will use the Subnet's display_name to created folder, so
// the name string must be unique.
func (service *SubnetService) BuildSubnetName(obj v1.Object) string {
	return common.BuildUniqueIDWithSuffix(obj, "", common.MaxSubnetNameLength, util.GenerateIDByObject, service.nsxSubnetNameExists)
}

// buildSubnetSetID uses the format "${subnetset.Name}-index_$(hash(${namespace.UUID}))[5]" to ensure the generated Subnet's
// // display_name is not conflict with others.
func (service *SubnetService) buildSubnetSetID(obj v1.Object, index string) string {
	return common.BuildUniqueIDWithSuffix(obj, index, common.MaxIdLength, util.GenerateIDByObject, service.nsxSubnetIdExists)
}

// buildSubnetSetName uses the format "${subnetset.Name}-index_$(hash(${namespace.UUID}))[5]" to generate the VpcSubnet's name.
func (service *SubnetService) buildSubnetSetName(obj v1.Object, index string) string {
	return common.BuildUniqueIDWithSuffix(obj, index, common.MaxSubnetNameLength, util.GenerateIDByObject, service.nsxSubnetNameExists)
}

func (service *SubnetService) nsxSubnetIdExists(id string) bool {
	existingSubnet := service.SubnetStore.GetByKey(id)
	return existingSubnet != nil
}

func (service *SubnetService) nsxSubnetNameExists(subnetName string) bool {
	existingSubnets := service.SubnetStore.GetByIndex(nsxSubnetNameIndexKey, subnetName)
	return len(existingSubnets) > 0
}

func convertAccessMode(accessMode string) string {
	if accessMode == v1alpha1.AccessModeProject {
		return AccessModeProjectInNSX
	}
	return accessMode
}

// buildSubnetTags builds the basic tags and appends tepless tags for the Subnet.
// it also check the tags count
func (service *SubnetService) buildSubnetTags(obj client.Object, tags []model.Tag) ([]model.Tag, error) {
	tags = append(service.buildBasicTags(obj), tags...)
	tepLess, err := controllerscommon.IsNamespaceInTepLessMode(service.Service.Client, obj.GetNamespace())
	if err != nil {
		log.Error(err, "Failed to check TEP-less mode for subnet tags", "namespace", obj.GetNamespace())
		return nil, err
	}
	if tepLess {
		tags = append(tags, model.Tag{
			Scope: common.String(common.TagScopeEnable),
			Tag:   common.String(common.TagValueL3InVlanBackedVPCMode),
		})
	}
	// tags cannot exceed maximum size 26
	if len(tags) > common.MaxTagsCount {
		errorMsg := fmt.Sprintf("tags cannot exceed maximum size 26, tags length: %d", len(tags))
		return nil, nsxutil.ExceedTagsError{Desc: errorMsg}
	}
	return tags, nil
}

func (service *SubnetService) buildSubnet(obj client.Object, tags []model.Tag, ipAddresses []string) (*model.VpcSubnet, error) {
	tags, err := service.buildSubnetTags(obj, tags)
	if err != nil {
		return nil, err
	}
	nsUID := getNamespaceUUID(tags)
	objForIdGeneration := &v1.ObjectMeta{
		Name: obj.GetName(),
		UID:  types.UID(nsUID),
	}
	staticIpAllocation := !util.CRSubnetDHCPEnabled(obj)
	var nsxSubnet *model.VpcSubnet
	switch o := obj.(type) {
	case *v1alpha1.Subnet:
		if o.Spec.AdvancedConfig.StaticIPAllocation.Enabled != nil {
			staticIpAllocation = *o.Spec.AdvancedConfig.StaticIPAllocation.Enabled
		}
		nsxSubnet = &model.VpcSubnet{
			Id:          String(service.BuildSubnetID(objForIdGeneration)),
			AccessMode:  String(convertAccessMode(util.Capitalize(string(o.Spec.AccessMode)))),
			DisplayName: String(service.BuildSubnetName(objForIdGeneration)),
			Tags:        tags,
			AdvancedConfig: &model.SubnetAdvancedConfig{
				StaticIpAllocation: &model.StaticIpAllocation{
					Enabled: &staticIpAllocation,
				},
			},
		}
		// Support connectivity state configuration
		if o.Spec.AdvancedConfig.ConnectivityState != "" {
			switch o.Spec.AdvancedConfig.ConnectivityState {
			case v1alpha1.ConnectivityStateConnected:
				nsxSubnet.AdvancedConfig.ConnectivityState = String("CONNECTED")
			case v1alpha1.ConnectivityStateDisconnected:
				nsxSubnet.AdvancedConfig.ConnectivityState = String("DISCONNECTED")
			}
		}
		dhcpMode := string(o.Spec.SubnetDHCPConfig.Mode)
		if dhcpMode == "" {
			dhcpMode = v1alpha1.DHCPConfigModeDeactivated
		}
		var dhcpServerAdditionalConfig *model.DhcpServerAdditionalConfig
		if len(o.Spec.SubnetDHCPConfig.DHCPServerAdditionalConfig.ReservedIPRanges) > 0 {
			dhcpServerAdditionalConfig = &model.DhcpServerAdditionalConfig{}
			dhcpServerAdditionalConfig.ReservedIpRanges = o.Spec.SubnetDHCPConfig.DHCPServerAdditionalConfig.ReservedIPRanges
		}
		nsxSubnet.SubnetDhcpConfig = service.buildSubnetDHCPConfig(dhcpMode, dhcpServerAdditionalConfig)
		if len(o.Spec.IPAddresses) > 0 {
			nsxSubnet.IpAddresses = o.Spec.IPAddresses
		} else if len(o.Status.NetworkAddresses) > 0 {
			nsxSubnet.IpAddresses = o.Status.NetworkAddresses
		}
		if o.Spec.IPv4SubnetSize > 0 {
			nsxSubnet.Ipv4SubnetSize = Int64(int64(o.Spec.IPv4SubnetSize))
		}
		// Support custom gateway addresses when provided
		if len(o.Spec.AdvancedConfig.GatewayAddresses) > 0 {
			nsxSubnet.AdvancedConfig.GatewayAddresses = o.Spec.AdvancedConfig.GatewayAddresses
		}
		// Support custom DHCP server addresses only when static IP allocation is disabled and dhcp mode is v1alpha1.DHCPConfigModeServer
		if !staticIpAllocation && string(o.Spec.SubnetDHCPConfig.Mode) == v1alpha1.DHCPConfigModeServer && len(o.Spec.AdvancedConfig.DHCPServerAddresses) > 0 {
			nsxSubnet.AdvancedConfig.DhcpServerAddresses = o.Spec.AdvancedConfig.DHCPServerAddresses
		}
	case *v1alpha1.SubnetSet:
		// The index is a random string with the length of 8 chars. It is the first 8 chars of the hash
		// value on a random UUID string.
		index := util.GetRandomIndexString()
		nsxSubnet = &model.VpcSubnet{
			Id:             String(service.buildSubnetSetID(objForIdGeneration, index)),
			AccessMode:     String(convertAccessMode(util.Capitalize(string(o.Spec.AccessMode)))),
			Ipv4SubnetSize: Int64(int64(o.Spec.IPv4SubnetSize)),
			DisplayName:    String(service.buildSubnetSetName(objForIdGeneration, index)),
			Tags:           tags,
			AdvancedConfig: &model.SubnetAdvancedConfig{
				StaticIpAllocation: &model.StaticIpAllocation{
					Enabled: &staticIpAllocation,
				},
			},
		}
		dhcpMode := string(o.Spec.SubnetDHCPConfig.Mode)
		if dhcpMode == "" {
			dhcpMode = v1alpha1.DHCPConfigModeDeactivated
		}
		nsxSubnet.SubnetDhcpConfig = service.buildSubnetDHCPConfig(dhcpMode, nil)
		if len(ipAddresses) > 0 {
			nsxSubnet.IpAddresses = ipAddresses
		}
	default:
		return nil, SubnetTypeError
	}
	return nsxSubnet, nil
}

func (service *SubnetService) buildSubnetDHCPConfig(mode string, dhcpServerAdditionalConfig *model.DhcpServerAdditionalConfig) *model.SubnetDhcpConfig {
	nsxMode := nsxutil.ParseDHCPMode(mode)
	subnetDhcpConfig := &model.SubnetDhcpConfig{
		DhcpServerAdditionalConfig: dhcpServerAdditionalConfig,
		Mode:                       &nsxMode,
	}
	return subnetDhcpConfig
}

func (service *SubnetService) buildBasicTags(obj client.Object) []model.Tag {
	return util.BuildBasicTags(getCluster(service), obj, "")
}

func getNamespaceUUID(tags []model.Tag) string {
	tagValues := filterTag(tags, common.TagScopeVMNamespaceUID)
	if len(tagValues) > 0 {
		return tagValues[0]
	}
	tagValues = filterTag(tags, common.TagScopeNamespaceUID)
	if len(tagValues) > 0 {
		return tagValues[0]
	}
	return ""
}
