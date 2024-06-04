package vpc

import (
	"github.com/zhengxiexie/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// currently we only support appending public/private cidrs
// so only comparing list size is enough to identify if vcp changed
func IsVPCChanged(nc common.VPCNetworkConfigInfo, vpc *model.Vpc) bool {
	if len(nc.ExternalIPv4Blocks) != len(vpc.ExternalIpv4Blocks) {
		return true
	}

	if len(nc.PrivateIPv4CIDRs) != len(vpc.PrivateIpv4Blocks) {
		return true
	}

	return false
}
