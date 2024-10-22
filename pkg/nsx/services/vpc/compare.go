package vpc

import (
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// IsVPCChanged checks if the VPCNetworkConfig is changed, currently we only support appending public/private cidrs
// so only comparing list size is enough to identify if VPC changed
func IsVPCChanged(nc common.VPCNetworkConfigInfo, vpc *model.Vpc) bool {
	if len(nc.PrivateIPs) != len(vpc.PrivateIps) {
		return true
	}
	return false
}
