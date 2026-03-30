package subnetipreservation

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestStaticIpAddressReservation_Key(t *testing.T) {
	m := &StaticIpAddressReservation{
		Id: common.String("res-id-1"),
	}
	require.Equal(t, "res-id-1", m.Key())
}

func TestStaticIpAddressReservationToComparable(t *testing.T) {
	nsxObj := &model.StaticIpAddressReservation{
		Id:          common.String("id1"),
		DisplayName: common.String("dn1"),
		ReservedIps: []string{"192.168.1.1"},
	}
	c := StaticIpAddressReservationToComparable(nsxObj)
	require.NotNil(t, c)
	require.Equal(t, "id1", c.(*StaticIpAddressReservation).Key())
}

func TestComparableToStaticIpAddressReservation(t *testing.T) {
	nsxObj := &model.StaticIpAddressReservation{
		Id:          common.String("id2"),
		DisplayName: common.String("dn2"),
	}
	c := StaticIpAddressReservationToComparable(nsxObj)
	back := ComparableToStaticIpAddressReservation(c)
	require.Equal(t, nsxObj.Id, back.Id)
	require.Equal(t, nsxObj.DisplayName, back.DisplayName)
}

func TestStaticIpAddressReservation_Value(t *testing.T) {
	m := &StaticIpAddressReservation{
		Id:          common.String("val-id"),
		DisplayName: common.String("display"),
		ReservedIps: []string{"10.0.0.1"},
	}
	dv := m.Value()
	require.NotNil(t, dv)
}
