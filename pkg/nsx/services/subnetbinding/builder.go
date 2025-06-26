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

func (s *BindingService) buildSubnetBindings(binding *v1alpha1.SubnetConnectionBindingMap, parentSubnetPaths []string) []*model.SubnetConnectionBindingMap {
	tags := util.BuildBasicTags(s.NSXConfig.Cluster, binding, "")
	bindingMaps := make([]*model.SubnetConnectionBindingMap, len(parentSubnetPaths))
	for i := range parentSubnetPaths {
		path := parentSubnetPaths[i]
		vpcSubnetInfo, err := common.ParseVPCResourcePath(path)
		if err != nil {
			log.Error(err, "failed to parse parent Subnet path, ignore it")
			continue
		}
		bindingMaps[i] = &model.SubnetConnectionBindingMap{
			Id:             String(s.buildSubnetBindingID(binding, vpcSubnetInfo.ID)),
			DisplayName:    String(binding.Name),
			VlanTrafficTag: Int64(binding.Spec.VLANTrafficTag),
			SubnetPath:     &path,
			Tags:           tags,
		}
	}
	return bindingMaps
}

// buildSubnetBindingID generates the ID of NSX SubnetConnectionBindingMap resource, its format is like this,
// ${SubnetConnectionBindingMap_CR}.name_hash(${parent_VpcSubnet}.Path)[:5], e.g., binding1_9bc22. Note, if
// the generated id has collision with the existing NSX SubnetConnectionBindingMap.id, a random UUID is used as
// an alternative of the parent path to generate the hash suffix.
func (s *BindingService) buildSubnetBindingID(binding *v1alpha1.SubnetConnectionBindingMap, parentSubnetPath string) string {
	idCR := &v1.ObjectMeta{
		Name: binding.GetName(),
		UID:  types.UID(parentSubnetPath),
	}
	return common.BuildUniqueIDWithRandomUUID(idCR, util.GenerateIDByObject, func(id string) bool {
		return s.BindingStore.GetByKey(id) != nil
	})
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
