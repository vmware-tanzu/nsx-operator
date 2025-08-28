package subnetipreservation

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

var (
	ipr1 = &model.DynamicIpAddressReservation{
		Id:          common.String("ipr1_3yw4m"),
		DisplayName: common.String("ipr1"),
		Path:        common.String("/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-1/dynamic-ip-reservations/ipr1_3yw4m"),
		ParentPath:  common.String("/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-1"),
		Tags: []model.Tag{
			{
				Scope: common.String(common.TagScopeNamespace),
				Tag:   common.String("ns-1"),
			},
			{
				Scope: common.String(common.TagScopeSubnetIPReservationCRName),
				Tag:   common.String("ipr1"),
			},
			{
				Scope: common.String(common.TagScopeSubnetIPReservationCRUID),
				Tag:   common.String("ipr1-uuid"),
			},
		},
	}
	ipr2 = &model.DynamicIpAddressReservation{
		Id:          common.String("ipr2_3yw4m"),
		DisplayName: common.String("ipr2"),
		Path:        common.String("/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-1/dynamic-ip-reservations/ipr2_3yw4m"),
		ParentPath:  common.String("/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-1"),
		Tags: []model.Tag{
			{
				Scope: common.String(common.TagScopeNamespace),
				Tag:   common.String("ns-1"),
			},
			{
				Scope: common.String(common.TagScopeSubnetIPReservationCRName),
				Tag:   common.String("ipr2"),
			},
			{
				Scope: common.String(common.TagScopeSubnetIPReservationCRUID),
				Tag:   common.String("ipr2-uuid"),
			},
		},
	}
	ipr3 = &model.DynamicIpAddressReservation{
		Id:          common.String("ipr1_luf2j"),
		DisplayName: common.String("ipr1"),
		Path:        common.String("/orgs/default/projects/default/vpcs/ns-2/subnets/subnet-1/dynamic-ip-reservations/ipr1_luf2j"),
		ParentPath:  common.String("/orgs/default/projects/default/vpcs/ns-2/subnets/subnet-1"),
		Tags: []model.Tag{
			{
				Scope: common.String(common.TagScopeNamespace),
				Tag:   common.String("ns-2"),
			},
			{
				Scope: common.String(common.TagScopeSubnetIPReservationCRName),
				Tag:   common.String("ipr1"),
			},
			{
				Scope: common.String(common.TagScopeSubnetIPReservationCRUID),
				Tag:   common.String("ipr3-uuid"),
			},
		},
	}
)

func TestStore(t *testing.T) {
	store := SetupStore()

	store.Apply(ipr1)
	store.Apply(ipr2)
	store.Apply(ipr3)

	ipr := store.GetByKey("ipr2_3yw4m")
	require.Equal(t, ipr2, ipr)
	iprs := store.GetByIndex(ipReservationCRUIDIndexKey, "ipr1-uuid")
	require.Equal(t, 1, len(iprs))
	require.Equal(t, ipr1, iprs[0])
	iprs = store.GetByIndex(ipReservationCRNameIndexKey, types.NamespacedName{Namespace: "ns-2", Name: "ipr1"}.String())
	require.Equal(t, 1, len(iprs))
	require.Equal(t, ipr3, iprs[0])

	iprCopy := *ipr1
	iprCopy.MarkedForDelete = common.Bool(true)
	store.Apply(&iprCopy)
	store.DeleteMultipleObjects([]*model.DynamicIpAddressReservation{ipr2, ipr3})
	require.Equal(t, 0, len(store.List()))
}
