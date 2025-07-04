package subnet

import (
	"fmt"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
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

// BuildSubnetID uses format "subnet.Name_$(hash(${namespace.UUID}))[5]" to generate the VpcSubnet's id.
func (service *SubnetService) BuildSubnetID(obj v1.Object) string {
	return common.BuildUniqueIDWithRandomUUID(obj, util.GenerateIDByObject, service.nsxSubnetIdExists)
}

// BuildSubnetName uses format "subnet.Name_$(hash(${namespace.UUID}))[5]" to ensure the Subnet's display_name is not
// conflict with others. This is because VC will use the Subnet's display_name to created folder, so
// the name string must be unique.
func (service *SubnetService) BuildSubnetName(obj v1.Object) string {
	return common.BuildUniqueIDWithSuffix(obj, "", common.MaxSubnetNameLength, util.GenerateIDByObject, service.nsxSubnetNameExists)
}

// buildSubnetSetID uses format "${subnetset.Name}-index_$(hash(${namespace.UUID}))[5]" to ensure the generated Subnet's
// // display_name is not conflict with others.
func (service *SubnetService) buildSubnetSetID(obj v1.Object, index string) string {
	return common.BuildUniqueIDWithSuffix(obj, index, common.MaxIdLength, util.GenerateIDByObject, service.nsxSubnetIdExists)
}

// buildSubnetSetName uses format "${subnetset.Name}-index_$(hash(${namespace.UUID}))[5]" to generate the VpcSubnet's name.
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

func (service *SubnetService) buildSubnet(obj client.Object, tags []model.Tag, ipAddresses []string) (*model.VpcSubnet, error) {
	tags = append(service.buildBasicTags(obj), tags...)

	nsUUID := getNamespaceUUID(tags)
	objForIdGeneration := &v1.ObjectMeta{
		Name: obj.GetName(),
		UID:  types.UID(nsUUID),
	}
	var nsxSubnet *model.VpcSubnet
	var staticIpAllocation bool
	switch o := obj.(type) {
	case *v1alpha1.Subnet:
		staticIpAllocation = o.Spec.SubnetDHCPConfig.Mode == "" || o.Spec.SubnetDHCPConfig.Mode == v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeDeactivated)
		nsxSubnet = &model.VpcSubnet{
			Id:             String(service.BuildSubnetID(objForIdGeneration)),
			AccessMode:     String(convertAccessMode(util.Capitalize(string(o.Spec.AccessMode)))),
			Ipv4SubnetSize: Int64(int64(o.Spec.IPv4SubnetSize)),
			DisplayName:    String(service.BuildSubnetName(objForIdGeneration)),
		}
		dhcpMode := string(o.Spec.SubnetDHCPConfig.Mode)
		if dhcpMode == "" {
			dhcpMode = v1alpha1.DHCPConfigModeDeactivated
		}
		nsxSubnet.SubnetDhcpConfig = service.buildSubnetDHCPConfig(dhcpMode, nil)
		if len(o.Spec.IPAddresses) > 0 {
			nsxSubnet.IpAddresses = o.Spec.IPAddresses
		} else if len(o.Status.NetworkAddresses) > 0 {
			nsxSubnet.IpAddresses = o.Status.NetworkAddresses
		}
	case *v1alpha1.SubnetSet:
		// The index is a random string with the length of 8 chars. It is the first 8 chars of the hash
		// value on a random UUID string.
		staticIpAllocation = o.Spec.SubnetDHCPConfig.Mode == "" || o.Spec.SubnetDHCPConfig.Mode == v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeDeactivated)
		index := util.GetRandomIndexString()
		nsxSubnet = &model.VpcSubnet{
			Id:             String(service.buildSubnetSetID(objForIdGeneration, index)),
			AccessMode:     String(convertAccessMode(util.Capitalize(string(o.Spec.AccessMode)))),
			Ipv4SubnetSize: Int64(int64(o.Spec.IPv4SubnetSize)),
			DisplayName:    String(service.buildSubnetSetName(objForIdGeneration, index)),
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
	// tags cannot exceed maximum size 26
	if len(tags) > common.MaxTagsCount {
		errorMsg := fmt.Sprintf("tags cannot exceed maximum size 26, tags length: %d", len(tags))
		return nil, nsxutil.ExceedTagsError{Desc: errorMsg}
	}
	nsxSubnet.Tags = tags
	nsxSubnet.AdvancedConfig = &model.SubnetAdvancedConfig{
		StaticIpAllocation: &model.StaticIpAllocation{
			Enabled: &staticIpAllocation,
		},
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
