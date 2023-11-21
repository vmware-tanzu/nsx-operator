package subnetport

import (
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type (
	SubnetPort model.SegmentPort
)

type Comparable = common.Comparable

func (sp *SubnetPort) Key() string {
	return *sp.Id
}

func (sp *SubnetPort) Value() data.DataValue {
	s := &SubnetPort{
		Id:          sp.Id,
		DisplayName: sp.DisplayName,
		Tags:        sp.Tags,
		Attachment:  sp.Attachment,
	}
	dataValue, _ := ComparableToSubnetPort(s).GetDataValue__()
	return dataValue
}

func SubnetPortToComparable(sp *model.SegmentPort) Comparable {
	return (*SubnetPort)(sp)
}

func ComparableToSubnetPort(sp Comparable) *model.SegmentPort {
	return (*model.SegmentPort)(sp.(*SubnetPort))
}
