package subnetipreservation

import (
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func (s *IPReservationService) buildIPReservation(ipReservation *v1alpha1.SubnetIPReservation, subnetPath string) *model.DynamicIpAddressReservation {
	tags := util.BuildBasicTags(getCluster(s), ipReservation, "")
	nsxIPReservation := &model.DynamicIpAddressReservation{
		NumberOfIps: common.Int64(int64(ipReservation.Spec.NumberOfIPs)),
		Tags:        tags,
		Id:          common.String(s.buildIPReservationID(ipReservation, subnetPath)),
		DisplayName: common.String(ipReservation.Name),
	}
	return nsxIPReservation
}

func getCluster(service *IPReservationService) string {
	return service.NSXConfig.Cluster
}

// buildIPReservationID generates the ID of NSX SubnetIPReservation resource, its format is like this,
// ${SubnetIPReservation_CR}.name_hash(${parent_VpcSubnet}.Path)[:5], e.g., ipreservation1_823ca. Note, if
// the generated id has collision with the existing NSX SubnetIPReservation.id, a random UUID is used as
// an alternative of the parent path to generate the hash suffix.
func (s *IPReservationService) buildIPReservationID(ipReservation *v1alpha1.SubnetIPReservation, subnetPath string) string {
	idCR := &v1.ObjectMeta{
		Name: ipReservation.GetName(),
		UID:  types.UID(subnetPath),
	}
	return common.BuildUniqueIDWithRandomUUID(idCR, util.GenerateIDByObject, func(id string) bool {
		return s.IPReservationStore.GetByKey(id) != nil
	})
}
