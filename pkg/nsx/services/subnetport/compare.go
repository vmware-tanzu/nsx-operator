package subnetport

import (
	"github.com/zhengxiexie/vsphere-automation-sdk-go/runtime/data"
	"github.com/zhengxiexie/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type (
	SubnetPort model.VpcSubnetPort
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
	if sp.Attachment != nil {
		// Ignoring the fields BmsInterfaceConfig, ContextType, EvpnVlans, HyperbusMode
		// because operator doesn't set them.
		s.Attachment = &model.PortAttachment{
			AllocateAddresses: sp.Attachment.AllocateAddresses,
			AppId:             sp.Attachment.AppId,
			ContextId:         sp.Attachment.ContextId,
			Id:                sp.Attachment.Id,
			TrafficTag:        sp.Attachment.TrafficTag,
			Type_:             sp.Attachment.AllocateAddresses,
		}
	}
	dataValue, _ := ComparableToSubnetPort(s).GetDataValue__()
	return dataValue
}

func SubnetPortToComparable(sp *model.VpcSubnetPort) Comparable {
	return (*SubnetPort)(sp)
}

func ComparableToSubnetPort(sp Comparable) *model.VpcSubnetPort {
	return (*model.VpcSubnetPort)(sp.(*SubnetPort))
}
