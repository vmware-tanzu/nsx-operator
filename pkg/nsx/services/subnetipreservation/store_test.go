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

	// Static IP reservation fixtures for StaticIPReservationStore tests
	staticIpr1 = &model.StaticIpAddressReservation{
		Id:          common.String("sipr1_abc"),
		DisplayName: common.String("sipr1"),
		Path:        common.String("/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-1/static-ip-reservations/sipr1_abc"),
		ReservedIps: []string{"192.168.1.1", "192.168.1.2"},
		Tags: []model.Tag{
			{Scope: common.String(common.TagScopeNamespace), Tag: common.String("ns-1")},
			{Scope: common.String(common.TagScopeSubnetIPReservationCRName), Tag: common.String("sipr1")},
			{Scope: common.String(common.TagScopeSubnetIPReservationCRUID), Tag: common.String("sipr1-uuid")},
		},
	}
	staticIpr2 = &model.StaticIpAddressReservation{
		Id:          common.String("sipr2_def"),
		DisplayName: common.String("sipr2"),
		Path:        common.String("/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-1/static-ip-reservations/sipr2_def"),
		ReservedIps: []string{"192.168.2.0/28"},
		Tags: []model.Tag{
			{Scope: common.String(common.TagScopeNamespace), Tag: common.String("ns-1")},
			{Scope: common.String(common.TagScopeSubnetIPReservationCRName), Tag: common.String("sipr2")},
			{Scope: common.String(common.TagScopeSubnetIPReservationCRUID), Tag: common.String("sipr2-uuid")},
		},
	}
)

func TestStaticIPReservationStore(t *testing.T) {
	store := SetupStaticIPReservationStore()

	store.Apply(staticIpr1)
	store.Apply(staticIpr2)

	ipr := store.GetByKey("sipr2_def")
	require.Equal(t, staticIpr2, ipr)
	iprs := store.GetByIndex(common.TagScopeSubnetIPReservationCRUID, "sipr1-uuid")
	require.Equal(t, 1, len(iprs))
	require.Equal(t, staticIpr1, iprs[0])
	iprs = store.GetByIndex(common.TagScopeSubnetIPReservationCRName, types.NamespacedName{Namespace: "ns-1", Name: "sipr2"}.String())
	require.Equal(t, 1, len(iprs))
	require.Equal(t, staticIpr2, iprs[0])

	err := store.Apply(nil)
	require.NoError(t, err)
	require.Equal(t, 2, len(store.List()))

	iprCopy := *staticIpr1
	iprCopy.MarkedForDelete = common.Bool(true)
	store.Apply(&iprCopy)
	store.DeleteMultipleObjects([]*model.StaticIpAddressReservation{staticIpr2})
	require.Equal(t, 0, len(store.List()))
}

func TestDynamicIPReservationStore(t *testing.T) {
	store := SetupDynamicIPReservationStore()

	store.Apply(ipr1)
	store.Apply(ipr2)
	store.Apply(ipr3)

	ipr := store.GetByKey("ipr2_3yw4m")
	require.Equal(t, ipr2, ipr)
	iprs := store.GetByIndex(common.TagScopeSubnetIPReservationCRUID, "ipr1-uuid")
	require.Equal(t, 1, len(iprs))
	require.Equal(t, ipr1, iprs[0])
	iprs = store.GetByIndex(common.TagScopeSubnetIPReservationCRName, types.NamespacedName{Namespace: "ns-2", Name: "ipr1"}.String())
	require.Equal(t, 1, len(iprs))
	require.Equal(t, ipr3, iprs[0])

	iprCopy := *ipr1
	iprCopy.MarkedForDelete = common.Bool(true)
	store.Apply(&iprCopy)
	store.DeleteMultipleObjects([]*model.DynamicIpAddressReservation{ipr2, ipr3})
	require.Equal(t, 0, len(store.List()))
}
