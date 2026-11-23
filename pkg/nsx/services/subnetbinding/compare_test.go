package subnetbinding

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestCompareSubnetConnectionBindingMapDefaultAssociation(t *testing.T) {
	newBM := func(association *string) *model.SubnetConnectionBindingMap {
		return &model.SubnetConnectionBindingMap{
			Id:                String("bm1"),
			DisplayName:       String("binding1"),
			SubnetPath:        String(parentSubnetPath1),
			VlanTrafficTag:    Int64(201),
			SubnetAssociation: association,
			Tags: []model.Tag{
				{Scope: String(common.TagScopeCluster), Tag: String("fake_cluster")},
			},
		}
	}
	trunk := model.SubnetConnectionBindingMap_SUBNET_ASSOCIATION_TRUNK
	branch := model.SubnetConnectionBindingMap_SUBNET_ASSOCIATION_BRANCH

	tests := []struct {
		name     string
		existing *string
		desired  *string
		changed  bool
	}{
		// NSX defaults an omitted subnet_association to TRUNK, so an unset desired
		// association must be treated as equal to the realized TRUNK value.
		{name: "unset desired equals realized TRUNK", existing: &trunk, desired: nil, changed: false},
		{name: "unset existing equals desired TRUNK", existing: nil, desired: &trunk, changed: false},
		{name: "unset equals unset", existing: nil, desired: nil, changed: false},
		{name: "TRUNK equals TRUNK", existing: &trunk, desired: &trunk, changed: false},
		{name: "BRANCH equals BRANCH", existing: &branch, desired: &branch, changed: false},
		{name: "TRUNK differs from BRANCH", existing: &trunk, desired: &branch, changed: true},
		{name: "unset differs from BRANCH", existing: nil, desired: &branch, changed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changed := common.CompareResource(
				SubnetConnectionBindingMapToComparable(newBM(tt.existing)),
				SubnetConnectionBindingMapToComparable(newBM(tt.desired)),
			)
			assert.Equal(t, tt.changed, changed)
		})
	}
}
