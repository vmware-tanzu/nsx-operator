package subnetport

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestSubnetPortToComparable(t *testing.T) {
	id1 := "fakeSubnetPortID1"
	id2 := "fakeSubnetPortID2"
	tagScope1 := "fakeTagScope1"
	tagValue1 := "fakeTagValue1"
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

	testCases := []struct {
		name               string
		existingSubnetPort *model.VpcSubnetPort
		nsxSubnetPort      *model.VpcSubnetPort
		expectChanged      bool
	}{
		{
			name:               "SubnetPort with diff Tags",
			expectChanged:      true,
			nsxSubnetPort:      &model.VpcSubnetPort{Tags: []model.Tag{tag2}},
			existingSubnetPort: &model.VpcSubnetPort{Tags: []model.Tag{tag1}},
		},
		{
			name: "SubnetPort with diff Attachment",
			nsxSubnetPort: &model.VpcSubnetPort{
				Id: &id1,
				Attachment: &model.PortAttachment{
					Id: common.String("attachment1"),
				},
			},
			existingSubnetPort: &model.VpcSubnetPort{
				Id: &id2,
				Attachment: &model.PortAttachment{
					Id: common.String("attachment2"),
				},
			},
			expectChanged: true,
		},
		{
			name: "SubnetPort with diff AddressBindings",
			nsxSubnetPort: &model.VpcSubnetPort{
				Id: &id1,
				AddressBindings: []model.PortAddressBindingEntry{
					{IpAddress: common.String("1.1.1.1"), MacAddress: common.String("aa:bb:cc:dd:ee:ff")},
				},
			},
			existingSubnetPort: &model.VpcSubnetPort{
				Id: &id1,
				AddressBindings: []model.PortAddressBindingEntry{
					{IpAddress: common.String("2.2.2.2"), MacAddress: common.String("aa:bb:cc:dd:ee:ff")},
				},
			},
			expectChanged: true,
		},
		{
			name: "SubnetPort with diff ExternalAddressBinding",
			nsxSubnetPort: &model.VpcSubnetPort{
				Id: &id1,
				ExternalAddressBinding: &model.ExternalAddressBinding{
					AllocatedExternalIpPath: common.String("path1"),
				},
			},
			existingSubnetPort: &model.VpcSubnetPort{
				Id: &id1,
				ExternalAddressBinding: &model.ExternalAddressBinding{
					AllocatedExternalIpPath: common.String("path2"),
				},
			},
			expectChanged: true,
		},
		{
			name: "SubnetPort with diff StaticIpAllocationType",
			nsxSubnetPort: &model.VpcSubnetPort{
				Id:                     &id1,
				StaticIpAllocationType: common.String("IPV4_IPV6"),
			},
			existingSubnetPort: &model.VpcSubnetPort{
				Id:                     &id1,
				StaticIpAllocationType: common.String("IPV4"),
			},
			expectChanged: true,
		},
		{
			name: "SubnetPort with same fields",
			nsxSubnetPort: &model.VpcSubnetPort{
				Id:          &id1,
				DisplayName: common.String("fakeDisplayName"),
				Tags:        []model.Tag{tag1},
				Attachment: &model.PortAttachment{
					AllocateAddresses: common.String("fakeAllocateAddresses"),
					AppId:             common.String("fakeAppId"),
					ContextId:         common.String("fakeContextId"),
					Id:                common.String("fakeAttachmentId"),
					TrafficTag:        common.Int64(100),
					Type_:             common.String("fakeType"),
				},
				StaticIpAllocationType: common.String("IPV4"),
				AddressBindings: []model.PortAddressBindingEntry{
					{IpAddress: common.String("1.1.1.1"), MacAddress: common.String("aa:bb:cc:dd:ee:ff")},
				},
				ExternalAddressBinding: &model.ExternalAddressBinding{
					AllocatedExternalIpPath: common.String("fakePath"),
				},
			},
			existingSubnetPort: &model.VpcSubnetPort{
				Id:          &id1,
				DisplayName: common.String("fakeDisplayName"),
				Tags:        []model.Tag{tag1},
				Attachment: &model.PortAttachment{
					AllocateAddresses: common.String("fakeAllocateAddresses"),
					AppId:             common.String("fakeAppId"),
					ContextId:         common.String("fakeContextId"),
					Id:                common.String("fakeAttachmentId"),
					TrafficTag:        common.Int64(100),
					Type_:             common.String("fakeType"),
				},
				StaticIpAllocationType: common.String("IPV4"),
				AddressBindings: []model.PortAddressBindingEntry{
					{IpAddress: common.String("1.1.1.1"), MacAddress: common.String("aa:bb:cc:dd:ee:ff")},
				},
				ExternalAddressBinding: &model.ExternalAddressBinding{
					AllocatedExternalIpPath: common.String("fakePath"),
				},
			},
			expectChanged: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			changed := common.CompareResource(SubnetPortToComparable(tc.existingSubnetPort), SubnetPortToComparable(tc.nsxSubnetPort))
			assert.Equal(t, tc.expectChanged, changed)
		})
	}
}

func TestSubnetPort_Key(t *testing.T) {
	id := "fakeSubnetPortID"
	sp := &SubnetPort{Id: &id}
	assert.Equal(t, id, sp.Key())
}

func TestComparableToSubnetPort(t *testing.T) {
	id := "fakeSubnetPortID"
	sp := &model.VpcSubnetPort{Id: &id}
	comparable := SubnetPortToComparable(sp)
	converted := ComparableToSubnetPort(comparable)
	assert.Equal(t, sp, converted)
}
