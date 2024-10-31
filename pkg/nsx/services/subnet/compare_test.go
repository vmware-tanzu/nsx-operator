package subnet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// Test SubnetToComparable function
func TestSubnetToComparable(t *testing.T) {
	tagScope1 := "fakeTagScope1"
	tagValue1 := "fakeTagvalue1"
	tag1 := model.Tag{
		Scope: &tagScope1,
		Tag:   &tagValue1,
	}
	tagScope2 := "fakeTagScope2"
	tagValue2 := "fakeTagValue2"
	tag2 := model.Tag{
		Scope: &tagScope2,
		Tag:   &tagValue2,
	}
	id1 := "fakeSubnetID1"
	id2 := "fakeSubnetID2"
	testCases := []struct {
		name           string
		existingSubnet *model.VpcSubnet
		nsxSubnet      *model.VpcSubnet
		expectChanged  bool
	}{
		{
			name:           "Subnet without Tags",
			nsxSubnet:      &model.VpcSubnet{Id: &id1},
			existingSubnet: &model.VpcSubnet{Id: &id2},
			expectChanged:  false,
		},
		{
			name:           "Subnet with the same Tags",
			nsxSubnet:      &model.VpcSubnet{Id: &id1, Tags: []model.Tag{tag1}},
			existingSubnet: &model.VpcSubnet{Id: &id2, Tags: []model.Tag{tag1}},
			expectChanged:  false,
		},
		{
			name:           "Subnet with diff Tags",
			expectChanged:  true,
			nsxSubnet:      &model.VpcSubnet{Tags: []model.Tag{tag2}},
			existingSubnet: &model.VpcSubnet{Tags: []model.Tag{tag1}},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			changed := common.CompareResource(SubnetToComparable(tc.existingSubnet), SubnetToComparable(tc.nsxSubnet))
			assert.Equal(t, tc.expectChanged, changed)
		})
	}
}
