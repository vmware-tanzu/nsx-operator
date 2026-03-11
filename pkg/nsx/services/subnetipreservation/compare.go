package subnetipreservation

import (
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type (
	StaticIpAddressReservation model.StaticIpAddressReservation
)

func (m *StaticIpAddressReservation) Key() string {
	return *m.Id
}

func (m *StaticIpAddressReservation) Value() data.DataValue {
	s := &StaticIpAddressReservation{
		Id:          m.Id,
		DisplayName: m.DisplayName,
		Tags:        m.Tags,
		ReservedIps: m.ReservedIps,
	}
	dataValue, _ := ComparableToStaticIpAddressReservation(s).GetDataValue__()
	return dataValue
}

func StaticIpAddressReservationToComparable(staticIPReservation *model.StaticIpAddressReservation) common.Comparable {
	return (*StaticIpAddressReservation)(staticIPReservation)
}

func ComparableToStaticIpAddressReservation(staticIPReservation common.Comparable) *model.StaticIpAddressReservation {
	return (*model.StaticIpAddressReservation)(staticIPReservation.(*StaticIpAddressReservation))
}
