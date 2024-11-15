package subnet

import (
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type (
	Subnet model.VpcSubnet
)

type Comparable = common.Comparable

func (subnet *Subnet) Key() string {
	return *subnet.Id
}

func (subnet *Subnet) Value() data.DataValue {
	// IPv4SubnetSize/AccessMode/IPAddresses are immutable field,
	// Changes of tags and subnetDHCPConfig are considered as changed.
	// TODO AccessMode may also need to be compared in future.
	s := &Subnet{
		Tags:             subnet.Tags,
		SubnetDhcpConfig: subnet.SubnetDhcpConfig,
	}
	dataValue, _ := (*model.VpcSubnet)(s).GetDataValue__()
	return dataValue
}

func SubnetToComparable(subnet *model.VpcSubnet) Comparable {
	return (*Subnet)(subnet)
}
