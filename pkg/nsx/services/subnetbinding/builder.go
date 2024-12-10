package subnetbinding

import (
	"fmt"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	String = common.String
	Int64  = common.Int64
	Bool   = common.Bool
)

func (s *BindingService) buildSubnetBindings(binding *v1alpha1.SubnetConnectionBindingMap, parentSubnets []*model.VpcSubnet) []*model.SubnetConnectionBindingMap {
	tags := util.BuildBasicTags(s.NSXConfig.Cluster, binding, "")
	bindingMaps := make([]*model.SubnetConnectionBindingMap, len(parentSubnets))
	for i := range parentSubnets {
		parent := parentSubnets[i]
		bindingMaps[i] = &model.SubnetConnectionBindingMap{
			Id:             String(buildSubnetBindingID(binding, *parent.Id)),
			DisplayName:    String(binding.Name),
			VlanTrafficTag: Int64(binding.Spec.VLANTrafficTag),
			SubnetPath:     parent.Path,
			Tags:           tags,
		}
	}
	return bindingMaps
}

// buildSubnetBindingID generates the ID of NSX SubnetConnectionBindingMap resource, its format is like this,
// ${SubnetConnectionBindingMap_CR}.name_hash(${parent_VpcSubnet}.Id)[:8], e.g., binding1_9bc22a0c
func buildSubnetBindingID(binding *v1alpha1.SubnetConnectionBindingMap, parentSubnetID string) string {
	suffix := util.Sha1(parentSubnetID)[:common.HashLength]
	return util.GenerateID(binding.Name, "", suffix, "")
}

func buildSubnetConnectionBindingMapCR(bindingMap *model.SubnetConnectionBindingMap) (*v1alpha1.SubnetConnectionBindingMap, error) {
	var crName, crNamespace, crUID string
	for _, tag := range bindingMap.Tags {
		switch *tag.Scope {
		case common.TagScopeNamespace:
			crNamespace = *tag.Tag
		case common.TagScopeSubnetBindingCRName:
			crName = *tag.Tag
		case common.TagScopeSubnetBindingCRUID:
			crUID = *tag.Tag
		default:
			continue
		}
	}
	if crName == "" || crNamespace == "" || crUID == "" {
		return nil, fmt.Errorf("missing tags to convert to CR SubnetConnectionBindingMap, Namespace %s, Name %s, UID %s", crNamespace, crName, crUID)
	}
	return &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: v1.ObjectMeta{
			Name:      crName,
			Namespace: crNamespace,
			UID:       types.UID(crUID),
		},
	}, nil
}
